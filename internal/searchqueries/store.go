package searchqueries

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"jobhawk/internal/database/db"
	"jobhawk/internal/greenhouse"
)

type SourceType string

const SourceGreenhouse SourceType = "greenhouse"

type GreenhouseQuery struct {
	ID        int64
	Name      string
	Filters   greenhouse.Filters
	Enabled   bool
	CreatedAt time.Time
	UpdatedAt time.Time
}

type Store struct {
	queries *db.Queries
}

func NewStore(queries *db.Queries) *Store {
	return &Store{queries: queries}
}

func (s *Store) SaveGreenhouse(ctx context.Context, name string, filters greenhouse.Filters) (GreenhouseQuery, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return GreenhouseQuery{}, errors.New("query name is required")
	}
	filters, err := filters.Normalize()
	if err != nil {
		return GreenhouseQuery{}, err
	}
	payload, err := json.Marshal(filters)
	if err != nil {
		return GreenhouseQuery{}, fmt.Errorf("encode Greenhouse filters: %w", err)
	}

	row, err := s.queries.UpsertSearchQuery(ctx, db.UpsertSearchQueryParams{
		Name:       name,
		SourceType: string(SourceGreenhouse),
		Filters:    payload,
	})
	if err != nil {
		return GreenhouseQuery{}, fmt.Errorf("save Greenhouse search query: %w", err)
	}
	return decodeGreenhouse(row)
}

func (s *Store) GetGreenhouse(ctx context.Context, name string) (GreenhouseQuery, error) {
	row, err := s.queries.GetSearchQueryByName(ctx, strings.TrimSpace(name))
	if err != nil {
		return GreenhouseQuery{}, fmt.Errorf("get search query: %w", err)
	}
	return decodeGreenhouse(row)
}

func (s *Store) ListGreenhouse(ctx context.Context) ([]GreenhouseQuery, error) {
	rows, err := s.queries.ListSearchQueries(ctx)
	if err != nil {
		return nil, fmt.Errorf("list search queries: %w", err)
	}

	result := make([]GreenhouseQuery, 0, len(rows))
	for _, row := range rows {
		if SourceType(row.SourceType) != SourceGreenhouse {
			continue
		}
		query, err := decodeGreenhouse(row)
		if err != nil {
			return nil, err
		}
		result = append(result, query)
	}
	return result, nil
}

func decodeGreenhouse(row db.SearchQuery) (GreenhouseQuery, error) {
	if SourceType(row.SourceType) != SourceGreenhouse {
		return GreenhouseQuery{}, fmt.Errorf("query %q has source type %q, not %q", row.Name, row.SourceType, SourceGreenhouse)
	}
	var filters greenhouse.Filters
	if err := json.Unmarshal(row.Filters, &filters); err != nil {
		return GreenhouseQuery{}, fmt.Errorf("decode filters for query %q: %w", row.Name, err)
	}
	filters, err := filters.Normalize()
	if err != nil {
		return GreenhouseQuery{}, fmt.Errorf("validate filters for query %q: %w", row.Name, err)
	}
	return GreenhouseQuery{
		ID:        row.ID,
		Name:      row.Name,
		Filters:   filters,
		Enabled:   row.Enabled,
		CreatedAt: row.CreatedAt.Time,
		UpdatedAt: row.UpdatedAt.Time,
	}, nil
}
