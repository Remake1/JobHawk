package greenhouse

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"jobhawk/internal/jobs"
)

const defaultBaseURL = "https://boards-api.greenhouse.io/v1/boards"

var boardTokenPattern = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// Filters is the JSONB payload stored for a Greenhouse search query. Every
// title word must occur in the title; matching is case-insensitive.
type Filters struct {
	BoardToken string   `json:"board_token"`
	Location   string   `json:"location,omitempty"`
	TitleWords []string `json:"title_words,omitempty"`
}

func (f Filters) Normalize() (Filters, error) {
	f.BoardToken = strings.TrimSpace(f.BoardToken)
	f.Location = strings.TrimSpace(f.Location)

	words := make([]string, 0, len(f.TitleWords))
	seen := make(map[string]struct{}, len(f.TitleWords))
	for _, word := range f.TitleWords {
		word = strings.TrimSpace(word)
		if word == "" {
			continue
		}
		key := strings.ToLower(word)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		words = append(words, word)
	}
	f.TitleWords = words

	if f.BoardToken == "" {
		return Filters{}, errors.New("Greenhouse board token is required")
	}
	if !boardTokenPattern.MatchString(f.BoardToken) {
		return Filters{}, errors.New("Greenhouse board token may contain only letters, digits, underscores, and hyphens")
	}
	if f.Location == "" && len(f.TitleWords) == 0 {
		return Filters{}, errors.New("at least one location or title word filter is required")
	}
	return f, nil
}

type Client struct {
	httpClient *http.Client
	baseURL    string
}

func NewClient(httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 15 * time.Second}
	}
	return &Client{httpClient: httpClient, baseURL: defaultBaseURL}
}

// NewClientWithBaseURL is primarily useful for tests and private Greenhouse
// proxies. Production callers should use NewClient.
func NewClientWithBaseURL(httpClient *http.Client, baseURL string) *Client {
	client := NewClient(httpClient)
	client.baseURL = strings.TrimRight(baseURL, "/")
	return client
}

// Search executes one Greenhouse API request and returns only matching jobs.
// It has no polling, persistence, or deduplication behavior.
func (c *Client) Search(ctx context.Context, filters Filters) ([]jobs.Job, error) {
	filters, err := filters.Normalize()
	if err != nil {
		return nil, err
	}

	endpoint := c.baseURL + "/" + url.PathEscape(filters.BoardToken) + "/jobs"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("create Greenhouse request: %w", err)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch Greenhouse jobs: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("Greenhouse returned %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}

	var payload apiResponse
	decoder := json.NewDecoder(io.LimitReader(resp.Body, 10<<20))
	if err := decoder.Decode(&payload); err != nil {
		return nil, fmt.Errorf("decode Greenhouse response: %w", err)
	}

	matches := make([]jobs.Job, 0)
	for _, candidate := range payload.Jobs {
		if !matchesFilters(candidate, filters) {
			continue
		}
		matches = append(matches, jobs.Job{
			ID:       strconv.FormatInt(candidate.ID, 10),
			Title:    strings.TrimSpace(candidate.Title),
			Company:  strings.TrimSpace(candidate.CompanyName),
			Location: strings.TrimSpace(candidate.Location.Name),
			URL:      candidate.AbsoluteURL,
			PostedAt: candidate.FirstPublished,
		})
	}
	return matches, nil
}

type apiResponse struct {
	Jobs []apiJob `json:"jobs"`
}

type apiJob struct {
	ID             int64     `json:"id"`
	AbsoluteURL    string    `json:"absolute_url"`
	Title          string    `json:"title"`
	CompanyName    string    `json:"company_name"`
	FirstPublished time.Time `json:"first_published"`
	Location       struct {
		Name string `json:"name"`
	} `json:"location"`
}

func matchesFilters(candidate apiJob, filters Filters) bool {
	if filters.Location != "" && !strings.EqualFold(strings.TrimSpace(candidate.Location.Name), filters.Location) {
		return false
	}
	title := strings.ToLower(candidate.Title)
	for _, word := range filters.TitleWords {
		if !strings.Contains(title, strings.ToLower(word)) {
			return false
		}
	}
	return true
}
