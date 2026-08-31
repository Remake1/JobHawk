package ashby

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"jobhawk/internal/jobs"
	"jobhawk/internal/searcherrors"
)

const defaultBaseURL = "https://api.ashbyhq.com/posting-api/job-board"

var jobBoardPattern = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// Filters is the JSONB payload stored for an Ashby search query. The location
// must occur in one of the job's location values, and every title word must
// occur in the title; matching is case-insensitive.
type Filters struct {
	JobBoard   string   `json:"job_board"`
	Location   string   `json:"location,omitempty"`
	TitleWords []string `json:"title_words,omitempty"`
}

func (f Filters) Normalize() (Filters, error) {
	jobBoard, err := NormalizeJobBoard(f.JobBoard)
	if err != nil {
		return Filters{}, err
	}
	f.JobBoard = jobBoard
	f.Location = strings.TrimSpace(f.Location)
	if len([]rune(f.Location)) > 200 {
		return Filters{}, errors.New("location must be 200 characters or fewer")
	}

	words := make([]string, 0, len(f.TitleWords))
	seen := make(map[string]struct{}, len(f.TitleWords))
	for _, word := range f.TitleWords {
		word = strings.TrimSpace(word)
		if word == "" {
			continue
		}
		if len([]rune(word)) > 100 {
			return Filters{}, errors.New("each title word must be 100 characters or fewer")
		}
		key := strings.ToLower(word)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		words = append(words, word)
	}
	f.TitleWords = words
	if len(f.TitleWords) > 20 {
		return Filters{}, errors.New("no more than 20 title words are allowed")
	}
	if f.Location == "" && len(f.TitleWords) == 0 {
		return Filters{}, errors.New("at least one location or title word filter is required")
	}
	return f, nil
}

// NormalizeJobBoard validates the final path segment used by Ashby's public
// job-board API.
func NormalizeJobBoard(jobBoard string) (string, error) {
	jobBoard = strings.TrimSpace(jobBoard)
	if jobBoard == "" {
		return "", errors.New("Ashby job board name is required")
	}
	if len(jobBoard) > 100 {
		return "", errors.New("Ashby job board name must be 100 characters or fewer")
	}
	if !jobBoardPattern.MatchString(jobBoard) {
		return "", errors.New("Ashby job board name may contain only letters, digits, underscores, and hyphens")
	}
	return jobBoard, nil
}

// ParseJobURL extracts the job board name from a public Ashby job URL.
func ParseJobURL(value string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil || parsed.Scheme != "https" || parsed.Hostname() != "jobs.ashbyhq.com" || parsed.Port() != "" {
		return "", errors.New("enter a valid HTTPS Ashby job URL")
	}
	segments := strings.Split(strings.Trim(parsed.EscapedPath(), "/"), "/")
	if len(segments) < 2 {
		return "", errors.New("enter an Ashby URL for a specific job")
	}
	jobBoard, err := url.PathUnescape(segments[0])
	if err != nil {
		return "", errors.New("Ashby job board name in URL is invalid")
	}
	return NormalizeJobBoard(jobBoard)
}

// NormalizeJobBoardInput accepts either a job board name or a public Ashby job
// URL, making the command and guided creation flow convenient to use.
func NormalizeJobBoardInput(value string) (string, error) {
	value = strings.TrimSpace(value)
	if strings.Contains(value, "://") {
		return ParseJobURL(value)
	}
	return NormalizeJobBoard(value)
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

// NewClientWithBaseURL is primarily useful for tests and private Ashby
// proxies. Production callers should use NewClient.
func NewClientWithBaseURL(httpClient *http.Client, baseURL string) *Client {
	client := NewClient(httpClient)
	client.baseURL = strings.TrimRight(baseURL, "/")
	return client
}

// Search executes one Ashby public job-board request and returns only matching
// listed jobs. It has no polling, persistence, or deduplication behavior.
func (c *Client) Search(ctx context.Context, filters Filters) ([]jobs.Job, error) {
	filters, err := filters.Normalize()
	if err != nil {
		return nil, err
	}

	endpoint := c.baseURL + "/" + url.PathEscape(filters.JobBoard)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("create Ashby request: %w", err)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch Ashby jobs: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		if resp.StatusCode == http.StatusTooManyRequests {
			return nil, searcherrors.NewRateLimit("Ashby", resp, string(body))
		}
		return nil, fmt.Errorf("Ashby returned %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}

	var payload apiResponse
	decoder := json.NewDecoder(io.LimitReader(resp.Body, 10<<20))
	if err := decoder.Decode(&payload); err != nil {
		return nil, fmt.Errorf("decode Ashby response: %w", err)
	}

	matches := make([]jobs.Job, 0)
	for _, candidate := range payload.Jobs {
		if candidate.IsListed != nil && !*candidate.IsListed || !matchesFilters(candidate, filters) {
			continue
		}
		matches = append(matches, jobs.Job{
			ID:       strings.TrimSpace(candidate.ID),
			Title:    strings.TrimSpace(candidate.Title),
			Company:  filters.JobBoard,
			Location: displayLocation(candidate),
			URL:      strings.TrimSpace(candidate.JobURL),
			PostedAt: candidate.PublishedAt,
		})
	}
	return matches, nil
}

type apiResponse struct {
	Jobs []apiJob `json:"jobs"`
}

type apiJob struct {
	ID                 string                 `json:"id"`
	Title              string                 `json:"title"`
	Location           string                 `json:"location"`
	SecondaryLocations []apiSecondaryLocation `json:"secondaryLocations"`
	PublishedAt        time.Time              `json:"publishedAt"`
	JobURL             string                 `json:"jobUrl"`
	IsListed           *bool                  `json:"isListed"`
	Address            struct {
		PostalAddress apiPostalAddress `json:"postalAddress"`
	} `json:"address"`
}

type apiSecondaryLocation struct {
	Location string           `json:"location"`
	Address  apiPostalAddress `json:"address"`
}

type apiPostalAddress struct {
	Locality string `json:"addressLocality"`
	Region   string `json:"addressRegion"`
	Country  string `json:"addressCountry"`
}

func matchesFilters(candidate apiJob, filters Filters) bool {
	if filters.Location != "" && !matchesLocation(candidate, filters.Location) {
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

func matchesLocation(candidate apiJob, location string) bool {
	location = strings.ToLower(strings.TrimSpace(location))
	for _, candidateLocation := range candidateLocations(candidate) {
		if strings.Contains(strings.ToLower(candidateLocation), location) {
			return true
		}
	}
	return false
}

func candidateLocations(candidate apiJob) []string {
	locations := []string{
		strings.TrimSpace(candidate.Location),
		formatPostalAddress(candidate.Address.PostalAddress),
	}
	for _, secondary := range candidate.SecondaryLocations {
		locations = append(locations,
			strings.TrimSpace(secondary.Location),
			formatPostalAddress(secondary.Address),
		)
	}
	return locations
}

func displayLocation(candidate apiJob) string {
	if location := formatPostalAddress(candidate.Address.PostalAddress); location != "" {
		return location
	}
	return strings.TrimSpace(candidate.Location)
}

func formatPostalAddress(address apiPostalAddress) string {
	parts := make([]string, 0, 3)
	for _, part := range []string{address.Locality, address.Region, address.Country} {
		part = strings.TrimSpace(part)
		if part == "" || containsFold(parts, part) {
			continue
		}
		parts = append(parts, part)
	}
	return strings.Join(parts, ", ")
}

func containsFold(values []string, candidate string) bool {
	for _, value := range values {
		if strings.EqualFold(value, candidate) {
			return true
		}
	}
	return false
}
