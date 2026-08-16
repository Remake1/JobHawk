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
