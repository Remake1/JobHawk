// Package textsearch checks a filtered job-board page for its empty-state text.
package textsearch

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/chromedp/chromedp"

	"jobhawk/internal/jobs"
	"jobhawk/internal/searcherrors"
)

const (
	maxResponseSize = 10 << 20
	renderTimeout   = 30 * time.Second
	renderSettle    = 2 * time.Second
	// Some job boards return different or unfiltered HTML to bot-identifying
	// user agents. Use the same shape as a regular desktop Chrome request so
	// server-rendered filters are honored without requiring Chromium.
	browserUserAgent = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/140.0.0.0 Safari/537.36"
)

// Filters is the JSONB payload stored for a text search query. When
// NoJobsText occurs verbatim in the fetched HTML or rendered DOM, the query has
// no results. ClientSideRender is false by default for backward compatibility.
type Filters struct {
	URL              string `json:"url"`
	NoJobsText       string `json:"no_jobs_text"`
	ClientSideRender bool   `json:"client_side_render,omitempty"`
}

func (f Filters) Normalize() (Filters, error) {
	requestURL, err := NormalizeURL(f.URL)
	if err != nil {
		return Filters{}, err
	}
	f.URL = requestURL
	f.NoJobsText = strings.TrimSpace(f.NoJobsText)
	if f.NoJobsText == "" {
		return Filters{}, errors.New("text shown when no jobs are found is required")
	}
	if len([]rune(f.NoJobsText)) > 2_000 {
		return Filters{}, errors.New("no-jobs text must be 2000 characters or fewer")
	}
	return f, nil
}

// NormalizeURL validates an absolute HTTP(S) job-board URL while preserving
// its query string, which normally contains the user's selected filters.
func NormalizeURL(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", errors.New("job board URL is required")
	}
	if len(value) > 8_192 {
		return "", errors.New("job board URL must be 8192 characters or fewer")
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Hostname() == "" || parsed.User != nil || parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", errors.New("enter a valid HTTP or HTTPS job board URL")
	}
	normalizedQuery, err := normalizeQuery(parsed.RawQuery)
	if err != nil {
		return "", err
	}
	parsed.RawQuery = normalizedQuery
	parsed.Fragment = ""
	return parsed.String(), nil
}

// normalizeQuery canonicalizes each key and value without sorting parameters
// or collapsing duplicates. QueryUnescape handles the normal encoding layer;
// decodeExtraLayer repairs one common accidental second layer such as %2520.
func normalizeQuery(rawQuery string) (string, error) {
	if rawQuery == "" {
		return "", nil
	}
	parts := strings.Split(rawQuery, "&")
	for i, part := range parts {
		if part == "" {
			continue
		}
		rawKey, rawValue, hasValue := strings.Cut(part, "=")
		key, err := url.QueryUnescape(rawKey)
		if err != nil {
			return "", errors.New("job board URL contains an invalid query parameter name")
		}
		value := ""
		if hasValue {
			value, err = url.QueryUnescape(rawValue)
			if err != nil {
				return "", fmt.Errorf("job board URL contains an invalid value for query parameter %q", key)
			}
		}
		key = decodeExtraLayer(key)
		value = decodeExtraLayer(value)
		parts[i] = url.QueryEscape(key)
		if hasValue {
			parts[i] += "=" + url.QueryEscape(value)
		}
	}
	return strings.Join(parts, "&"), nil
}

// decodeExtraLayer decodes at most one additional percent-encoding layer.
// PathUnescape is intentional: unlike QueryUnescape it leaves literal plus
// signs alone after the first, normal query decoding pass.
func decodeExtraLayer(value string) string {
	if !hasPercentEscape(value) {
		return value
	}
	decoded, err := url.PathUnescape(value)
	if err != nil {
		return value
	}
	return decoded
}

func hasPercentEscape(value string) bool {
	for i := 0; i+2 < len(value); i++ {
		if value[i] == '%' && isHex(value[i+1]) && isHex(value[i+2]) {
			return true
		}
	}
	return false
}

func isHex(value byte) bool {
	return value >= '0' && value <= '9' || value >= 'a' && value <= 'f' || value >= 'A' && value <= 'F'
}

type Client struct {
	httpClient *http.Client
	renderer   pageRenderer
}

func NewClient(httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 20 * time.Second}
	}
	return &Client{httpClient: httpClient, renderer: newChromeRenderer()}
}

// Close releases the shared Chrome process if client-side rendering was used.
// It is safe to call Close when no rendered search has run.
func (c *Client) Close() {
	if c != nil && c.renderer != nil {
		c.renderer.Close()
	}
}

// Search fetches the configured page and searches its HTML for the exact
// no-results fragment. Since this strategy cannot extract individual job
// details, an absent fragment produces one availability result linked to the
// original filtered page.
func (c *Client) Search(ctx context.Context, filters Filters) ([]jobs.Job, error) {
	filters, err := filters.Normalize()
	if err != nil {
		return nil, err
	}

	var body string
	if filters.ClientSideRender {
		body, err = c.renderer.Render(ctx, filters.URL)
		if err != nil {
			return nil, err
		}
	} else {
		body, err = c.fetchHTML(ctx, filters.URL)
		if err != nil {
			return nil, err
		}
	}
	if len(body) > maxResponseSize {
		return nil, fmt.Errorf("job board page exceeds %d bytes", maxResponseSize)
	}
	if strings.Contains(body, filters.NoJobsText) {
		return nil, nil
	}

	parsed, _ := url.Parse(filters.URL)
	return []jobs.Job{{
		ID:      "availability",
		Title:   "Matching jobs available",
		Company: parsed.Hostname(),
		URL:     filters.URL,
	}}, nil
}

func (c *Client) fetchHTML(ctx context.Context, requestURL string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL, nil)
	if err != nil {
		return "", fmt.Errorf("create text search request: %w", err)
	}
	req.Header.Set("Accept", "text/html,application/xhtml+xml")
	req.Header.Set("User-Agent", browserUserAgent)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("fetch job board page: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4_096))
		if resp.StatusCode == http.StatusTooManyRequests {
			return "", searcherrors.NewRateLimit("job board", resp, string(body))
		}
		return "", fmt.Errorf("job board returned %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseSize+1))
	if err != nil {
		return "", fmt.Errorf("read job board page: %w", err)
	}
	if len(body) > maxResponseSize {
		return "", fmt.Errorf("job board page exceeds %d bytes", maxResponseSize)
	}
	return string(body), nil
}

type pageRenderer interface {
	Render(context.Context, string) (string, error)
	Close()
}

// chromeRenderer owns one lazily started Chrome process. browserCtx represents
// its long-lived first tab; every Render call creates a child context, which
// chromedp maps to a new tab in that same browser.
type chromeRenderer struct {
	mu              sync.Mutex
	browserCtx      context.Context
	browserCancel   context.CancelFunc
	allocatorCancel context.CancelFunc
	closed          bool
}

func newChromeRenderer() *chromeRenderer {
	return &chromeRenderer{}
}

func (r *chromeRenderer) Render(ctx context.Context, requestURL string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", fmt.Errorf("render job board page: %w", err)
	}
	browserCtx, err := r.context()
	if err != nil {
		return "", err
	}

	tabCtx, cancelTab := chromedp.NewContext(browserCtx)
	stopCallerCancellation := context.AfterFunc(ctx, cancelTab)
	tabCtx, cancelTimeout := context.WithTimeout(tabCtx, renderTimeout)
	defer func() {
		stopCallerCancellation()
		cancelTimeout()
		cancelTab()
	}()

	var html string
	if err := chromedp.Run(tabCtx,
		chromedp.Navigate(requestURL),
		chromedp.WaitReady("html", chromedp.ByQuery),
		// Navigate waits for the load event. Give asynchronous framework/API
		// work a short settling window before capturing the live DOM.
		chromedp.Sleep(renderSettle),
		chromedp.OuterHTML("html", &html, chromedp.ByQuery),
	); err != nil {
		return "", fmt.Errorf("render job board page: %w", err)
	}
	return html, nil
}

func (r *chromeRenderer) context() (context.Context, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return nil, errors.New("client-side renderer is closed")
	}
	if r.browserCtx != nil {
		return r.browserCtx, nil
	}

	options := append([]chromedp.ExecAllocatorOption{}, chromedp.DefaultExecAllocatorOptions[:]...)
	options = append(options, chromedp.Flag("no-sandbox", true))
	allocatorCtx, allocatorCancel := chromedp.NewExecAllocator(context.Background(), options...)
	browserCtx, browserCancel := chromedp.NewContext(allocatorCtx)
	if err := chromedp.Run(browserCtx); err != nil {
		browserCancel()
		allocatorCancel()
		return nil, fmt.Errorf("start Chrome for client-side rendering: %w", err)
	}
	r.browserCtx = browserCtx
	r.browserCancel = browserCancel
	r.allocatorCancel = allocatorCancel
	return browserCtx, nil
}

func (r *chromeRenderer) Close() {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return
	}
	r.closed = true
	browserCancel := r.browserCancel
	allocatorCancel := r.allocatorCancel
	r.browserCtx = nil
	r.browserCancel = nil
	r.allocatorCancel = nil
	r.mu.Unlock()

	if browserCancel != nil {
		browserCancel()
	}
	if allocatorCancel != nil {
		allocatorCancel()
	}
}
