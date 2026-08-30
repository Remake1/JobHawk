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

func TestNormalizeURLRepairsDoubleEncodedQueryLiterals(t *testing.T) {
	input := "https://www.metacareers.com/jobsearch/?teams[0]=Internship%2520-%2520Engineering%252C%2520Tech%2520%2526%2520Design&roles[0]=Internship"
	want := "https://www.metacareers.com/jobsearch/?teams%5B0%5D=Internship+-+Engineering%2C+Tech+%26+Design&roles%5B0%5D=Internship"

	got, err := NormalizeURL(input)
	if err != nil {
		t.Fatalf("NormalizeURL() error = %v", err)
	}
	if got != want {
		t.Fatalf("NormalizeURL() = %q, want %q", got, want)
	}
}

func TestNormalizeURLPreservesOrderDuplicatesAndLiteralPlus(t *testing.T) {
	input := "https://example.com/jobs?tag=C%2B%2B&location=New%2520York&tag=Go"
	want := "https://example.com/jobs?tag=C%2B%2B&location=New+York&tag=Go"

	got, err := NormalizeURL(input)
	if err != nil {
		t.Fatalf("NormalizeURL() error = %v", err)
	}
	if got != want {
		t.Fatalf("NormalizeURL() = %q, want %q", got, want)
	}
}

func TestNormalizeURLRejectsMalformedQueryEscape(t *testing.T) {
	if _, err := NormalizeURL("https://example.com/jobs?location=%zz"); err == nil {
		t.Fatal("NormalizeURL() expected a malformed query escape error")
	}
}
