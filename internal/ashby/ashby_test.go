package ashby

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestParseJobURL(t *testing.T) {
	got, err := ParseJobURL("https://jobs.ashbyhq.com/snowflake/fc1923c1-b151-4458-a792-40d58331a5be")
	if err != nil {
		t.Fatalf("ParseJobURL() error = %v", err)
	}
	if got != "snowflake" {
		t.Fatalf("ParseJobURL() = %q", got)
	}
}

func TestParseJobURLRejectsAnotherHost(t *testing.T) {
	if _, err := ParseJobURL("https://example.com/snowflake/job-id"); err == nil {
		t.Fatal("ParseJobURL() expected an error")
	}
}

func TestSearchFiltersByPartialLocationAndAllTitleWords(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/snowflake" {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Accept") != "application/json" {
			t.Fatalf("Accept = %q", r.Header.Get("Accept"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"jobs":[
            {"id":"fc1923c1-b151-4458-a792-40d58331a5be","title":"Senior Software Engineer, Query Processing","location":"PL-Warsaw-Lixa C","address":{"postalAddress":{"addressLocality":"Warsaw","addressCountry":"Poland"}},"publishedAt":"2026-08-03T20:06:28+00:00","jobUrl":"https://jobs.ashbyhq.com/snowflake/fc1923c1-b151-4458-a792-40d58331a5be","isListed":true},
            {"id":"2","title":"Senior Software Engineer","location":"Warsaw, Poland","publishedAt":"2026-08-03T20:06:28+00:00","jobUrl":"https://example.com/2","isListed":true},
            {"id":"3","title":"Senior Software Engineer, Query Processing","location":"London, United Kingdom","publishedAt":"2026-08-03T20:06:28+00:00","jobUrl":"https://example.com/3","isListed":true},
            {"id":"4","title":"Senior Software Engineer, Query Processing","location":"Warsaw, Poland","publishedAt":"2026-08-03T20:06:28+00:00","jobUrl":"https://example.com/4","isListed":false}
        ]}`))
	}))
	defer server.Close()

	client := NewClientWithBaseURL(server.Client(), server.URL)
	got, err := client.Search(context.Background(), Filters{
		JobBoard:   "snowflake",
		Location:   " warsaw ",
		TitleWords: []string{"Software", "Query"},
	})
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if len(got) != 1 || got[0].ID != "fc1923c1-b151-4458-a792-40d58331a5be" || got[0].Company != "snowflake" || got[0].Location != "Warsaw, Poland" {
		t.Fatalf("Search() = %+v", got)
	}
}

func TestMatchesLocationIncludesSecondaryLocations(t *testing.T) {
	candidate := apiJob{Location: "US-CA-Menlo Park"}
	candidate.SecondaryLocations = []apiSecondaryLocation{{
		Location: "PL-Warsaw-Lixa C",
		Address:  apiPostalAddress{Locality: "Warsaw", Country: "Poland"},
	}}
	if !matchesLocation(candidate, "warsaw") {
		t.Fatal("matchesLocation() did not match the structured secondary address")
	}
}

func TestFiltersNormalize(t *testing.T) {
	got, err := (Filters{
		JobBoard:   " snowflake ",
		TitleWords: []string{" Software ", "software", "", "Query"},
	}).Normalize()
	if err != nil {
		t.Fatalf("Normalize() error = %v", err)
	}
	if got.JobBoard != "snowflake" || len(got.TitleWords) != 2 || got.TitleWords[0] != "Software" {
		t.Fatalf("Normalize() = %+v", got)
	}
}

func TestSearchRejectsUnfilteredQuery(t *testing.T) {
	client := NewClient(nil)
	if _, err := client.Search(context.Background(), Filters{JobBoard: "snowflake"}); err == nil {
		t.Fatal("Search() expected validation error")
	}
}
