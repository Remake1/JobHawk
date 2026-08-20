package workday

import (
	"bytes"
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
)

const (
	pageSize        = 20
	maxResponseSize = 10 << 20
)

var identifierPattern = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// Filters is the JSONB payload stored for a Workday search query. Title words
// must all occur in the title; location is a case-insensitive partial match.
type Filters struct {
	Host       string   `json:"host"`
	Tenant     string   `json:"tenant"`
	Site       string   `json:"site"`
	Location   string   `json:"location,omitempty"`
	TitleWords []string `json:"title_words,omitempty"`
}

// FiltersFromJobURL extracts the CXS API coordinates from a public Workday job
// URL and adds the user-facing search filters.
func FiltersFromJobURL(jobURL, location string, titleWords []string) (Filters, error) {
	host, tenant, site, err := ParseJobURL(jobURL)
	if err != nil {
		return Filters{}, err
	}
	return (Filters{
		Host:       host,
		Tenant:     tenant,
		Site:       site,
		Location:   location,
		TitleWords: titleWords,
	}).Normalize()
}

// ParseJobURL accepts a public myworkdayjobs.com job URL and returns the host,
// tenant, and recruiting site required by the Workday CXS endpoint.
func ParseJobURL(value string) (host, tenant, site string, err error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", "", "", errors.New("Workday job URL is required")
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme != "https" || parsed.Hostname() == "" || parsed.Port() != "" {
		return "", "", "", errors.New("enter a valid HTTPS Workday job URL")
	}
	host = strings.ToLower(parsed.Hostname())
	labels := strings.Split(host, ".")
	if len(labels) < 4 || labels[len(labels)-2] != "myworkdayjobs" || labels[len(labels)-1] != "com" {
		return "", "", "", errors.New("job URL must use a myworkdayjobs.com host")
	}
	tenant = labels[0]
	if !identifierPattern.MatchString(tenant) {
		return "", "", "", errors.New("Workday tenant in job URL is invalid")
	}

	segments := strings.Split(strings.Trim(parsed.EscapedPath(), "/"), "/")
	jobIndex := -1
	for i, segment := range segments {
		if segment == "job" {
			jobIndex = i
			break
		}
	}
	if jobIndex < 1 {
		return "", "", "", errors.New("enter a Workday job URL whose path contains /<site>/job/")
	}
	site, err = url.PathUnescape(segments[jobIndex-1])
	if err != nil || !identifierPattern.MatchString(site) {
		return "", "", "", errors.New("Workday recruiting site in job URL is invalid")
	}
	return host, tenant, site, nil
}

func (f Filters) Normalize() (Filters, error) {
	f.Host = strings.ToLower(strings.TrimSpace(f.Host))
	f.Tenant = strings.ToLower(strings.TrimSpace(f.Tenant))
	f.Site = strings.TrimSpace(f.Site)
	if f.Host == "" || f.Tenant == "" || f.Site == "" {
		return Filters{}, errors.New("Workday host, tenant, and site are required")
	}
	if !identifierPattern.MatchString(f.Tenant) || !identifierPattern.MatchString(f.Site) {
		return Filters{}, errors.New("Workday tenant and site may contain only letters, digits, underscores, and hyphens")
	}
	labels := strings.Split(f.Host, ".")
	if len(labels) < 4 || labels[0] != strings.ToLower(f.Tenant) || labels[len(labels)-2] != "myworkdayjobs" || labels[len(labels)-1] != "com" {
		return Filters{}, errors.New("Workday host must match the tenant and use myworkdayjobs.com")
	}

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

type Client struct {
	httpClient *http.Client
	baseURL    string
}

func NewClient(httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 15 * time.Second}
	}
	return &Client{httpClient: httpClient}
}

// NewClientWithBaseURL is primarily useful for tests and private Workday
// proxies. Production callers should use NewClient.
func NewClientWithBaseURL(httpClient *http.Client, baseURL string) *Client {
	client := NewClient(httpClient)
	client.baseURL = strings.TrimRight(baseURL, "/")
	return client
}

// Search fetches every page from a Workday CXS jobs endpoint and returns only
// jobs matching all configured filters.
func (c *Client) Search(ctx context.Context, filters Filters) ([]jobs.Job, error) {
	filters, err := filters.Normalize()
	if err != nil {
		return nil, err
	}

	matches := make([]jobs.Job, 0)
	total := 0
	for offset := 0; ; offset += pageSize {
		payload, err := c.fetchPage(ctx, filters, offset, strings.Join(filters.TitleWords, " "))
		if err != nil {
			return nil, err
		}
		if total == 0 && payload.Total > 0 {
			total = payload.Total
		}
		for _, candidate := range payload.JobPostings {
			if !matchesFilters(candidate, filters) {
				continue
			}
			matches = append(matches, jobs.Job{
				ID:       strings.TrimSpace(candidate.ExternalPath),
				Title:    strings.TrimSpace(candidate.Title),
				Company:  filters.Tenant,
				Location: strings.TrimSpace(candidate.LocationsText),
				URL:      "https://" + filters.Host + "/" + url.PathEscape(filters.Site) + ensureLeadingSlash(candidate.ExternalPath),
			})
		}

		if len(payload.JobPostings) == 0 || total > 0 && offset+len(payload.JobPostings) >= total {
			break
		}
	}
	return matches, nil
}

func (c *Client) fetchPage(ctx context.Context, filters Filters, offset int, searchText string) (apiResponse, error) {
	body, err := json.Marshal(apiRequest{
		AppliedFacets: map[string][]string{},
		Limit:         pageSize,
		Offset:        offset,
		SearchText:    searchText,
	})
	if err != nil {
		return apiResponse{}, fmt.Errorf("encode Workday request: %w", err)
	}
	baseURL := c.baseURL
	if baseURL == "" {
		baseURL = "https://" + filters.Host
	}
	endpoint := baseURL + "/wday/cxs/" + url.PathEscape(filters.Tenant) + "/" + url.PathEscape(filters.Site) + "/jobs"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return apiResponse{}, fmt.Errorf("create Workday request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return apiResponse{}, fmt.Errorf("fetch Workday jobs: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		responseBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return apiResponse{}, fmt.Errorf("Workday returned %s: %s", resp.Status, strings.TrimSpace(string(responseBody)))
	}

	var payload apiResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxResponseSize)).Decode(&payload); err != nil {
		return apiResponse{}, fmt.Errorf("decode Workday response: %w", err)
	}
	if payload.Total < 0 {
		return apiResponse{}, errors.New("decode Workday response: total cannot be negative")
	}
	return payload, nil
}

type apiRequest struct {
	AppliedFacets map[string][]string `json:"appliedFacets"`
	Limit         int                 `json:"limit"`
	Offset        int                 `json:"offset"`
	SearchText    string              `json:"searchText"`
}

type apiResponse struct {
	Total       int      `json:"total"`
	JobPostings []apiJob `json:"jobPostings"`
}

type apiJob struct {
	Title         string `json:"title"`
	ExternalPath  string `json:"externalPath"`
	LocationsText string `json:"locationsText"`
	PostedOn      string `json:"postedOn"`
}

func matchesFilters(candidate apiJob, filters Filters) bool {
	if filters.Location != "" && !strings.Contains(strings.ToLower(candidate.LocationsText), strings.ToLower(filters.Location)) {
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

func ensureLeadingSlash(value string) string {
	value = strings.TrimSpace(value)
	if strings.HasPrefix(value, "/") {
		return value
	}
	return "/" + value
}
