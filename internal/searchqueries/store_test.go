package searchqueries

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"jobhawk/internal/ashby"
	"jobhawk/internal/database/db"
	"jobhawk/internal/greenhouse"
	"jobhawk/internal/workday"
)

func TestDecodeGreenhouse(t *testing.T) {
	now := time.Date(2026, time.August, 16, 12, 0, 0, 0, time.UTC)
	query, err := decodeGreenhouse(db.SearchQuery{
		ID:         7,
		Name:       "Point72 SWE Internship 2027",
		SourceType: "greenhouse",
		Filters:    json.RawMessage(`{"board_token":"point72","location":"Warsaw, Poland","title_words":["2027","Internship","Software"]}`),
		Enabled:    true,
		CreatedAt:  pgtype.Timestamptz{Time: now, Valid: true},
		UpdatedAt:  pgtype.Timestamptz{Time: now, Valid: true},
	})
	if err != nil {
		t.Fatalf("decodeGreenhouse() error = %v", err)
	}
	if query.ID != 7 || query.Filters.BoardToken != "point72" || len(query.Filters.TitleWords) != 3 {
		t.Fatalf("decodeGreenhouse() = %+v", query)
	}
}

func TestDecodeAshby(t *testing.T) {
	now := time.Date(2026, time.August, 25, 12, 0, 0, 0, time.UTC)
	query, err := decodeAshby(db.SearchQuery{
		ID:         9,
		Name:       "Snowflake Software",
		SourceType: "ashby",
		Filters:    json.RawMessage(`{"job_board":"snowflake","location":"Warsaw, Poland","title_words":["Software","Engineer"]}`),
		Enabled:    true,
		CreatedAt:  pgtype.Timestamptz{Time: now, Valid: true},
		UpdatedAt:  pgtype.Timestamptz{Time: now, Valid: true},
	})
	if err != nil {
		t.Fatalf("decodeAshby() error = %v", err)
	}
	if query.ID != 9 || query.Filters.JobBoard != "snowflake" || len(query.Filters.TitleWords) != 2 {
		t.Fatalf("decodeAshby() = %+v", query)
	}
}

func TestDecodeGreenhouseRejectsAnotherSource(t *testing.T) {
	_, err := decodeGreenhouse(db.SearchQuery{Name: "other", SourceType: "workday"})
	if err == nil {
		t.Fatal("decodeGreenhouse() expected a source type error")
	}
}

func TestDecodeWorkday(t *testing.T) {
	now := time.Date(2026, time.August, 17, 12, 0, 0, 0, time.UTC)
	query, err := decodeWorkday(db.SearchQuery{
		ID:         8,
		Name:       "State Street Poland",
		SourceType: "workday",
		Filters:    json.RawMessage(`{"host":"statestreet.wd1.myworkdayjobs.com","tenant":"statestreet","site":"Global","location":"Poland","title_words":["Working","Student"]}`),
		Enabled:    true,
		CreatedAt:  pgtype.Timestamptz{Time: now, Valid: true},
		UpdatedAt:  pgtype.Timestamptz{Time: now, Valid: true},
	})
	if err != nil {
		t.Fatalf("decodeWorkday() error = %v", err)
	}
	if query.ID != 8 || query.Filters.Site != "Global" || query.Filters.Location != "Poland" {
		t.Fatalf("decodeWorkday() = %+v", query)
	}
}

func TestDecodeQuerySelectsWorkday(t *testing.T) {
	query, err := decodeQuery(db.SearchQuery{
		Name:       "Workday",
		SourceType: "workday",
		Filters:    json.RawMessage(`{"host":"tenant.wd1.myworkdayjobs.com","tenant":"tenant","site":"Global","location":"Poland"}`),
	})
	if err != nil {
		t.Fatalf("decodeQuery() error = %v", err)
	}
	if query.SourceType != SourceWorkday || query.Workday == nil || query.Greenhouse != nil || query.Ashby != nil {
		t.Fatalf("decodeQuery() = %+v", query)
	}
}

func TestDecodeQuerySelectsAshby(t *testing.T) {
	query, err := decodeQuery(db.SearchQuery{
		Name:       "Ashby",
		SourceType: "ashby",
		Filters:    json.RawMessage(`{"job_board":"snowflake","location":"Warsaw, Poland"}`),
	})
	if err != nil {
		t.Fatalf("decodeQuery() error = %v", err)
	}
	if query.SourceType != SourceAshby || query.Ashby == nil || query.Greenhouse != nil || query.Workday != nil {
		t.Fatalf("decodeQuery() = %+v", query)
	}
}

func TestDecodeQuerySelectsTextSearch(t *testing.T) {
	query, err := decodeQuery(db.SearchQuery{
		Name:       "Google internships",
		SourceType: "text",
		Filters:    json.RawMessage(`{"url":"https://example.com/jobs?location=Poland","no_jobs_text":"No matching jobs"}`),
	})
	if err != nil {
		t.Fatalf("decodeQuery() error = %v", err)
	}
	if query.SourceType != SourceText || query.Text == nil || query.Text.NoJobsText != "No matching jobs" || query.Text.ClientSideRender || query.Ashby != nil || query.Greenhouse != nil || query.Workday != nil {
		t.Fatalf("decodeQuery() = %+v", query)
	}
}

func TestDecodeQueryPreservesTextSearchCSRMode(t *testing.T) {
	query, err := decodeQuery(db.SearchQuery{
		Name:       "Dynamic jobs",
		SourceType: "text",
		Filters:    json.RawMessage(`{"url":"https://example.com/jobs","no_jobs_text":"No matching jobs","client_side_render":true}`),
	})
	if err != nil {
		t.Fatalf("decodeQuery() error = %v", err)
	}
	if query.Text == nil || !query.Text.ClientSideRender {
		t.Fatalf("decodeQuery() = %+v", query)
	}
}

func TestApplyEditableFiltersPreservesImmutableFields(t *testing.T) {
	tests := []struct {
		name  string
		query Query
		check func(*testing.T, Query)
	}{
		{
			name: "Ashby board",
			query: Query{ID: 1, Name: "Ashby query", SourceType: SourceAshby, Ashby: &ashby.Filters{
				JobBoard: "snowflake", Location: "Old", TitleWords: []string{"Old"},
			}},
			check: func(t *testing.T, got Query) {
				if got.Name != "Ashby query" || got.Ashby.JobBoard != "snowflake" {
					t.Fatalf("immutable fields changed: %+v", got)
				}
			},
		},
		{
			name: "Greenhouse board",
			query: Query{ID: 2, Name: "Greenhouse query", SourceType: SourceGreenhouse, Greenhouse: &greenhouse.Filters{
				BoardToken: "point72", Location: "Old", TitleWords: []string{"Old"},
			}},
			check: func(t *testing.T, got Query) {
				if got.Name != "Greenhouse query" || got.Greenhouse.BoardToken != "point72" {
					t.Fatalf("immutable fields changed: %+v", got)
				}
			},
		},
		{
			name: "Workday coordinates",
			query: Query{ID: 3, Name: "Workday query", SourceType: SourceWorkday, Workday: &workday.Filters{
				Host: "tenant.wd1.myworkdayjobs.com", Tenant: "tenant", Site: "Global", Location: "Old", TitleWords: []string{"Old"},
			}},
			check: func(t *testing.T, got Query) {
				if got.Name != "Workday query" || got.Workday.Host != "tenant.wd1.myworkdayjobs.com" || got.Workday.Tenant != "tenant" || got.Workday.Site != "Global" {
					t.Fatalf("immutable fields changed: %+v", got)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := applyEditableFilters(tt.query, EditableFilters{
				Location: " New location ",
				Tags:     []string{" Engineer ", "Go", "go"},
			})
			if err != nil {
				t.Fatalf("applyEditableFilters() error = %v", err)
			}
			tt.check(t, got)
			editableLocation, editableTags := "", []string(nil)
			switch got.SourceType {
			case SourceAshby:
				editableLocation, editableTags = got.Ashby.Location, got.Ashby.TitleWords
			case SourceGreenhouse:
				editableLocation, editableTags = got.Greenhouse.Location, got.Greenhouse.TitleWords
			case SourceWorkday:
				editableLocation, editableTags = got.Workday.Location, got.Workday.TitleWords
			}
			if editableLocation != "New location" || len(editableTags) != 2 || editableTags[0] != "Engineer" {
				t.Fatalf("editable fields = %q, %v", editableLocation, editableTags)
			}
		})
	}
}

func TestApplyEditableFiltersRejectsUnfilteredQuery(t *testing.T) {
	_, err := applyEditableFilters(Query{
		Name:       "Point72",
		SourceType: SourceGreenhouse,
		Greenhouse: &greenhouse.Filters{BoardToken: "point72", Location: "Warsaw"},
	}, EditableFilters{})
	if err == nil {
		t.Fatal("applyEditableFilters() accepted an empty location and tags")
	}
}
