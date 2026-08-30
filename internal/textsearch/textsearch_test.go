package textsearch

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSearchReturnsNoJobsWhenEmptyTextIsPresent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`<html><body>Search again or try updating your filters</body></html>`))
	}))
	defer server.Close()

	found, err := NewClient(server.Client()).Search(context.Background(), Filters{
		URL:        server.URL + "/jobs?location=Poland",
		NoJobsText: "Search again or try updating your filters",
	})
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if len(found) != 0 {
		t.Fatalf("Search() returned %d jobs, want 0", len(found))
	}
}

func TestSearchReturnsAvailabilityWhenEmptyTextIsAbsent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.RawQuery != "location=Poland" {
			t.Errorf("query string = %q", r.URL.RawQuery)
		}
		_, _ = w.Write([]byte(`<html><body><a href="/job/1">Software Intern</a></body></html>`))
	}))
	defer server.Close()

	requestURL := server.URL + "/jobs?location=Poland"
	found, err := NewClient(server.Client()).Search(context.Background(), Filters{
		URL: requestURL, NoJobsText: "No matching jobs",
	})
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if len(found) != 1 || found[0].ID != "availability" || found[0].URL != requestURL || found[0].Title != "Matching jobs available" {
		t.Fatalf("Search() = %+v", found)
	}
}

func TestSearchRejectsErrorResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	}))
	defer server.Close()

	_, err := NewClient(server.Client()).Search(context.Background(), Filters{
		URL: server.URL, NoJobsText: "No jobs",
	})
	if err == nil {
		t.Fatal("Search() expected an HTTP status error")
	}
}

func TestFiltersRequireURLAndNoJobsText(t *testing.T) {
	for _, filters := range []Filters{
		{NoJobsText: "No jobs"},
		{URL: "https://example.com/jobs"},
		{URL: "javascript:alert(1)", NoJobsText: "No jobs"},
	} {
		if _, err := filters.Normalize(); err == nil {
			t.Errorf("Normalize(%+v) expected an error", filters)
		}
	}
}
