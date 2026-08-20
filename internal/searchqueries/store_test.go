package searchqueries

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"jobhawk/internal/database/db"
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
	if query.SourceType != SourceWorkday || query.Workday == nil || query.Greenhouse != nil {
		t.Fatalf("decodeQuery() = %+v", query)
	}
}
