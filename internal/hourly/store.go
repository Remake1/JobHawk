package hourly

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"jobhawk/internal/database/db"
)

type Subscription struct {
	ID              int64
	SearchQueryID   int64
	SearchDate      time.Time
	IntervalMinutes int
	NextRunAt       time.Time
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

type Store struct {
	queries *db.Queries
}

func NewStore(queries *db.Queries) *Store {
	return &Store{queries: queries}
}

func (s *Store) Upsert(ctx context.Context, searchQueryID int64, searchDate time.Time, intervalMinutes int, nextRunAt time.Time) (Subscription, error) {
	if searchQueryID <= 0 {
		return Subscription{}, errors.New("search query ID must be positive")
	}
	if !validInterval(intervalMinutes) {
		return Subscription{}, errors.New("interval must be 15, 30, or 60 minutes")
	}
	if nextRunAt.IsZero() {
		return Subscription{}, errors.New("next run time is required")
	}
	row, err := s.queries.UpsertHourlySearchQuery(ctx, db.UpsertHourlySearchQueryParams{
		SearchQueryID:   searchQueryID,
		SearchDate:      pgDate(searchDate),
		IntervalMinutes: int32(intervalMinutes),
		NextRunAt:       pgTimestamptz(nextRunAt),
	})
	if err != nil {
		return Subscription{}, fmt.Errorf("save hourly search query: %w", err)
	}
	return decodeSubscription(row)
}

func (s *Store) GetByQueryID(ctx context.Context, searchQueryID int64) (Subscription, bool, error) {
	row, err := s.queries.GetHourlySearchQueryBySearchQueryID(ctx, searchQueryID)
	if errors.Is(err, pgx.ErrNoRows) {
		return Subscription{}, false, nil
	}
	if err != nil {
		return Subscription{}, false, fmt.Errorf("get hourly search query: %w", err)
	}
	subscription, err := decodeSubscription(row)
	return subscription, err == nil, err
}

func (s *Store) ListDue(ctx context.Context, searchDate, now time.Time) ([]Subscription, error) {
	rows, err := s.queries.ListDueHourlySearchQueries(ctx, db.ListDueHourlySearchQueriesParams{
		SearchDate: pgDate(searchDate),
		NextRunAt:  pgTimestamptz(now),
	})
	if err != nil {
		return nil, fmt.Errorf("list due hourly search queries: %w", err)
	}
	result := make([]Subscription, 0, len(rows))
	for _, row := range rows {
		subscription, err := decodeSubscription(row)
		if err != nil {
			return nil, err
		}
		result = append(result, subscription)
	}
	return result, nil
}

func (s *Store) Advance(ctx context.Context, id int64, nextRunAt time.Time) error {
	if err := s.queries.UpdateHourlySearchQueryNextRun(ctx, db.UpdateHourlySearchQueryNextRunParams{
		ID: id, NextRunAt: pgTimestamptz(nextRunAt),
	}); err != nil {
		return fmt.Errorf("advance hourly search query: %w", err)
	}
	return nil
}

func (s *Store) DeleteByQueryID(ctx context.Context, searchQueryID int64) (bool, error) {
	deleted, err := s.queries.DeleteHourlySearchQueryBySearchQueryID(ctx, searchQueryID)
	if err != nil {
		return false, fmt.Errorf("delete hourly search query: %w", err)
	}
	return deleted == 1, nil
}

func (s *Store) DeleteExpired(ctx context.Context, before time.Time) error {
	if _, err := s.queries.DeleteExpiredHourlySearchQueries(ctx, pgDate(before)); err != nil {
		return fmt.Errorf("delete expired hourly search queries: %w", err)
	}
	return nil
}

func validInterval(minutes int) bool {
	return minutes == 15 || minutes == 30 || minutes == 60
}

func pgDate(value time.Time) pgtype.Date {
	date := time.Date(value.Year(), value.Month(), value.Day(), 0, 0, 0, 0, time.UTC)
	return pgtype.Date{Time: date, Valid: true}
}

func pgTimestamptz(value time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: value.UTC(), Valid: true}
}

func decodeSubscription(row db.HourlySearchQuery) (Subscription, error) {
	if !row.SearchDate.Valid || !row.NextRunAt.Valid {
		return Subscription{}, fmt.Errorf("hourly search query %d has an invalid schedule", row.ID)
	}
	if !validInterval(int(row.IntervalMinutes)) {
		return Subscription{}, fmt.Errorf("hourly search query %d has invalid interval %d", row.ID, row.IntervalMinutes)
	}
	return Subscription{
		ID: row.ID, SearchQueryID: row.SearchQueryID,
		SearchDate: row.SearchDate.Time, IntervalMinutes: int(row.IntervalMinutes),
		NextRunAt: row.NextRunAt.Time, CreatedAt: row.CreatedAt.Time, UpdatedAt: row.UpdatedAt.Time,
	}, nil
}
