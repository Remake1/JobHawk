package greenhouse

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSearchFiltersByExactLocationAndAllTitleWords(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/point72/jobs" {
			t.Fatalf("request path = %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"jobs":[
            {"id":8423978002,"absolute_url":"https://example.com/1","title":"2027 Software Engineer Internship","company_name":"Point72 ","first_published":"2026-08-03T20:06:28-04:00","location":{"name":"Warsaw, Poland"}},
            {"id":2,"absolute_url":"https://example.com/2","title":"2027 Software Engineer","company_name":"Point72","first_published":"2026-08-03T20:06:28-04:00","location":{"name":"Warsaw, Poland"}},
            {"id":3,"absolute_url":"https://example.com/3","title":"2027 Software Engineer Internship","company_name":"Point72","first_published":"2026-08-03T20:06:28-04:00","location":{"name":"London, United Kingdom"}}
        ]}`))
	}))
	defer server.Close()

	client := NewClientWithBaseURL(server.Client(), server.URL)
	got, err := client.Search(context.Background(), Filters{
		BoardToken: "point72",
		Location:   "Warsaw, Poland",
		TitleWords: []string{"2027", "Internship", "Software"},
	})
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if len(got) != 1 || got[0].ID != "8423978002" || got[0].Company != "Point72" {
		t.Fatalf("Search() = %+v", got)
	}
}

func TestFiltersNormalize(t *testing.T) {
	got, err := (Filters{
		BoardToken: " point72 ",
		TitleWords: []string{" Software ", "software", "", "2027"},
	}).Normalize()
	if err != nil {
		t.Fatalf("Normalize() error = %v", err)
	}
	if got.BoardToken != "point72" || len(got.TitleWords) != 2 || got.TitleWords[0] != "Software" {
		t.Fatalf("Normalize() = %+v", got)
	}
}

func TestSearchRejectsUnfilteredQuery(t *testing.T) {
	client := NewClient(nil)
	if _, err := client.Search(context.Background(), Filters{BoardToken: "point72"}); err == nil {
		t.Fatal("Search() expected validation error")
	}
}
