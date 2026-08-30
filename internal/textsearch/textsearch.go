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
	"time"

	"jobhawk/internal/jobs"
)

const maxResponseSize = 10 << 20

// Filters is the JSONB payload stored for a text search query. When
// NoJobsText occurs verbatim in the fetched HTML, the query has no results.
type Filters struct {
	URL        string `json:"url"`
	NoJobsText string `json:"no_jobs_text"`
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
	parsed.Fragment = ""
	return parsed.String(), nil
}

type Client struct {
	httpClient *http.Client
}

func NewClient(httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 20 * time.Second}
	}
	return &Client{httpClient: httpClient}
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

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, filters.URL, nil)
	if err != nil {
		return nil, fmt.Errorf("create text search request: %w", err)
	}
	req.Header.Set("Accept", "text/html,application/xhtml+xml")
	req.Header.Set("User-Agent", "JobHawk/1.0")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch job board page: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4_096))
		return nil, fmt.Errorf("job board returned %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseSize+1))
	if err != nil {
		return nil, fmt.Errorf("read job board page: %w", err)
	}
	if len(body) > maxResponseSize {
		return nil, fmt.Errorf("job board page exceeds %d bytes", maxResponseSize)
	}
	if strings.Contains(string(body), filters.NoJobsText) {
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
