package workday

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
)

func TestParseJobURL(t *testing.T) {
	host, tenant, site, err := ParseJobURL("https://statestreet.wd1.myworkdayjobs.com/Global/job/Munich-Germany/Working-Student_R-795614-1/apply")
	if err != nil {
		t.Fatalf("ParseJobURL() error = %v", err)
	}
	if host != "statestreet.wd1.myworkdayjobs.com" || tenant != "statestreet" || site != "Global" {
		t.Fatalf("ParseJobURL() = (%q, %q, %q)", host, tenant, site)
	}
}

func TestParseJobURLRejectsNonWorkdayURL(t *testing.T) {
	if _, _, _, err := ParseJobURL("https://example.com/Global/job/Engineer/1"); err == nil {
		t.Fatal("ParseJobURL() expected an error")
	}
}

func TestParseJobURLAcceptsLocalePrefix(t *testing.T) {
	_, _, site, err := ParseJobURL("https://example.wd5.myworkdayjobs.com/en-US/External_Careers/job/Chicago/Engineer_R-1")
	if err != nil || site != "External_Careers" {
		t.Fatalf("ParseJobURL() = site %q, error %v", site, err)
	}
}

func TestSearchPaginatesAndFiltersTitleAndPartialLocation(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Method != http.MethodPost || r.URL.Path != "/wday/cxs/tenant/Global/jobs" {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Content-Type") != "application/json" || r.Header.Get("Accept") != "application/json" {
			t.Fatalf("headers = %+v", r.Header)
		}
		var request apiRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		if request.SearchText != "Working Student" {
			t.Fatalf("searchText = %q", request.SearchText)
		}
		if request.Offset == 0 {
			jobs := make([]apiJob, 20)
			for i := range jobs {
				jobs[i] = apiJob{Title: "Other role " + strconv.Itoa(i), ExternalPath: "/job/other/" + strconv.Itoa(i), LocationsText: "London, UK"}
			}
			_ = json.NewEncoder(w).Encode(apiResponse{Total: 41, JobPostings: jobs})
			return
		}
		if request.Offset == 20 {
			jobs := make([]apiJob, 20)
			for i := range jobs {
				jobs[i] = apiJob{Title: "Other role " + strconv.Itoa(i+20), ExternalPath: "/job/other/" + strconv.Itoa(i+20), LocationsText: "London, UK"}
			}
			// Workday commonly reports total only on the first page. A zero
			// total here must not prevent the third page from being fetched.
			_ = json.NewEncoder(w).Encode(apiResponse{Total: 0, JobPostings: jobs})
			return
		}
		if request.Offset != 40 || request.Limit != 20 {
			t.Fatalf("third request = %+v", request)
		}
		_ = json.NewEncoder(w).Encode(apiResponse{Total: 0, JobPostings: []apiJob{{Title: "Working Student - Chief Administration Office", ExternalPath: "/job/Munich-Germany/Working-Student_R-1", LocationsText: "Krakow, Poland"}}})
	}))
	defer server.Close()

	client := NewClientWithBaseURL(server.Client(), server.URL)
	filters := Filters{Host: "tenant.wd1.myworkdayjobs.com", Tenant: "tenant", Site: "Global", Location: "Poland", TitleWords: []string{"Working", "Student"}}

	got, err := client.Search(context.Background(), filters)
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if requests != 3 || len(got) != 1 {
		t.Fatalf("requests = %d, Search() = %+v", requests, got)
	}
	if got[0].Location != "Krakow, Poland" || got[0].URL != "https://tenant.wd1.myworkdayjobs.com/Global/job/Munich-Germany/Working-Student_R-1" {
		t.Fatalf("Search() = %+v", got)
	}
}

func TestFiltersNormalizeRequiresAJobFilter(t *testing.T) {
	_, err := (Filters{Host: "tenant.wd1.myworkdayjobs.com", Tenant: "tenant", Site: "Global"}).Normalize()
	if err == nil {
		t.Fatal("Normalize() expected an error")
	}
}
