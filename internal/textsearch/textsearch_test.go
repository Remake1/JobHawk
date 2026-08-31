package textsearch

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type fakeRenderer struct {
	html   string
	err    error
	urls   []string
	closed bool
}

func (r *fakeRenderer) Render(_ context.Context, requestURL string) (string, error) {
	r.urls = append(r.urls, requestURL)
	return r.html, r.err
}

func (r *fakeRenderer) Close() { r.closed = true }

func TestSearchReturnsNoJobsWhenEmptyTextIsPresent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.UserAgent(); got != browserUserAgent {
			t.Errorf("User-Agent = %q, want %q", got, browserUserAgent)
		}
		if got := r.Header.Get("Accept"); got != "text/html,application/xhtml+xml" {
			t.Errorf("Accept = %q", got)
		}
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

func TestSearchUsesRenderedDOMForClientSidePage(t *testing.T) {
	renderer := &fakeRenderer{html: `<html><body><div>No matching jobs</div></body></html>`}
	client := NewClient(nil)
	client.renderer = renderer

	found, err := client.Search(context.Background(), Filters{
		URL: "https://example.com/jobs", NoJobsText: "No matching jobs", ClientSideRender: true,
	})
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if len(found) != 0 {
		t.Fatalf("Search() returned %d jobs, want 0", len(found))
	}
	if len(renderer.urls) != 1 || renderer.urls[0] != "https://example.com/jobs" {
		t.Fatalf("renderer URLs = %v", renderer.urls)
	}

	client.Close()
	if !renderer.closed {
		t.Fatal("Close() did not close the renderer")
	}
}

func TestSearchRejectsOversizedRenderedPage(t *testing.T) {
	client := NewClient(nil)
	client.renderer = &fakeRenderer{html: strings.Repeat("x", maxResponseSize+1)}

	_, err := client.Search(context.Background(), Filters{
		URL: "https://example.com/jobs", NoJobsText: "No jobs", ClientSideRender: true,
	})
	if err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("Search() error = %v, want size error", err)
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

func TestFiltersDefaultToServerSideRendering(t *testing.T) {
	filters, err := (Filters{URL: "https://example.com/jobs", NoJobsText: "No jobs"}).Normalize()
	if err != nil {
		t.Fatalf("Normalize() error = %v", err)
	}
	if filters.ClientSideRender {
		t.Fatal("ClientSideRender defaults to true")
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
