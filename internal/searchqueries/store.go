package searchqueries

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"jobhawk/internal/ashby"
	"jobhawk/internal/database/db"
	"jobhawk/internal/greenhouse"
	"jobhawk/internal/workday"
)

type SourceType string

const (
	SourceAshby      SourceType = "ashby"
	SourceGreenhouse SourceType = "greenhouse"
	SourceWorkday    SourceType = "workday"
)

type Query struct {
	ID         int64
	Name       string
	SourceType SourceType
	Ashby      *ashby.Filters
	Greenhouse *greenhouse.Filters
	Workday    *workday.Filters
	Enabled    bool
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// EditableFilters is the complete set of fields that may be changed after a
// query is created. Provider coordinates, the query name, and source type are
// deliberately absent so callers cannot overwrite them through the edit API.
type EditableFilters struct {
	Location string
	Tags     []string
}

type AshbyQuery struct {
	ID        int64
	Name      string
	Filters   ashby.Filters
	Enabled   bool
	CreatedAt time.Time
	UpdatedAt time.Time
}

type GreenhouseQuery struct {
	ID        int64
	Name      string
	Filters   greenhouse.Filters
	Enabled   bool
	CreatedAt time.Time
	UpdatedAt time.Time
}

type WorkdayQuery struct {
	ID        int64
	Name      string
	Filters   workday.Filters
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

func (s *Store) SaveAshby(ctx context.Context, name string, filters ashby.Filters) (AshbyQuery, error) {
	name, err := normalizeName(name)
	if err != nil {
		return AshbyQuery{}, err
	}
	filters, err = filters.Normalize()
	if err != nil {
		return AshbyQuery{}, err
	}
	payload, err := json.Marshal(filters)
	if err != nil {
		return AshbyQuery{}, fmt.Errorf("encode Ashby filters: %w", err)
	}

	row, err := s.queries.UpsertSearchQuery(ctx, db.UpsertSearchQueryParams{
		Name:       name,
		SourceType: string(SourceAshby),
		Filters:    payload,
	})
	if err != nil {
		return AshbyQuery{}, fmt.Errorf("save Ashby search query: %w", err)
	}
	return decodeAshby(row)
}

func (s *Store) SaveGreenhouse(ctx context.Context, name string, filters greenhouse.Filters) (GreenhouseQuery, error) {
	name, err := normalizeName(name)
	if err != nil {
		return GreenhouseQuery{}, err
	}
	filters, err = filters.Normalize()
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

func (s *Store) SaveWorkday(ctx context.Context, name string, filters workday.Filters) (WorkdayQuery, error) {
	name, err := normalizeName(name)
	if err != nil {
		return WorkdayQuery{}, err
	}
	filters, err = filters.Normalize()
	if err != nil {
		return WorkdayQuery{}, err
	}
	payload, err := json.Marshal(filters)
	if err != nil {
		return WorkdayQuery{}, fmt.Errorf("encode Workday filters: %w", err)
	}
	row, err := s.queries.UpsertSearchQuery(ctx, db.UpsertSearchQueryParams{
		Name:       name,
		SourceType: string(SourceWorkday),
		Filters:    payload,
	})
	if err != nil {
		return WorkdayQuery{}, fmt.Errorf("save Workday search query: %w", err)
	}
	return decodeWorkday(row)
}

func normalizeName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", errors.New("query name is required")
	}
	if len([]rune(name)) > 100 {
		return "", errors.New("query name must be 100 characters or fewer")
	}
	return name, nil
}

func (s *Store) Get(ctx context.Context, name string) (Query, error) {
	row, err := s.queries.GetSearchQueryByName(ctx, strings.TrimSpace(name))
	if err != nil {
		return Query{}, fmt.Errorf("get search query: %w", err)
	}
	return decodeQuery(row)
}

func (s *Store) GetByID(ctx context.Context, id int64) (Query, error) {
	row, err := s.queries.GetSearchQueryByAnyID(ctx, id)
	if err != nil {
		return Query{}, fmt.Errorf("get search query: %w", err)
	}
	return decodeQuery(row)
}

func (s *Store) List(ctx context.Context) ([]Query, error) {
	rows, err := s.queries.ListSearchQueries(ctx)
	if err != nil {
		return nil, fmt.Errorf("list search queries: %w", err)
	}
	result := make([]Query, 0, len(rows))
	for _, row := range rows {
		query, err := decodeQuery(row)
		if err != nil {
			return nil, err
		}
		result = append(result, query)
	}
	return result, nil
}

func (s *Store) Update(ctx context.Context, id int64, editable EditableFilters) (Query, error) {
	row, err := s.queries.GetSearchQueryByAnyID(ctx, id)
	if err != nil {
		return Query{}, fmt.Errorf("get search query for update: %w", err)
	}
	query, err := decodeQuery(row)
	if err != nil {
		return Query{}, err
	}
	query, err = applyEditableFilters(query, editable)
	if err != nil {
		return Query{}, err
	}
	payload, err := encodeQueryFilters(query)
	if err != nil {
		return Query{}, err
	}

	updated, err := s.queries.UpdateSearchQueryFilters(ctx, db.UpdateSearchQueryFiltersParams{
		ID:         id,
		SourceType: string(query.SourceType),
		Filters:    payload,
	})
	if err != nil {
		return Query{}, fmt.Errorf("update search query filters: %w", err)
	}
	return decodeQuery(updated)
}

func applyEditableFilters(query Query, editable EditableFilters) (Query, error) {
	switch query.SourceType {
	case SourceAshby:
		if query.Ashby == nil {
			return Query{}, errors.New("Ashby query filters are missing")
		}
		filters := *query.Ashby
		filters.Location = editable.Location
		filters.TitleWords = editable.Tags
		normalized, err := filters.Normalize()
		if err != nil {
			return Query{}, err
		}
		query.Ashby = &normalized
	case SourceGreenhouse:
		if query.Greenhouse == nil {
			return Query{}, errors.New("Greenhouse query filters are missing")
		}
		filters := *query.Greenhouse
		filters.Location = editable.Location
		filters.TitleWords = editable.Tags
		normalized, err := filters.Normalize()
		if err != nil {
			return Query{}, err
		}
		query.Greenhouse = &normalized
	case SourceWorkday:
		if query.Workday == nil {
			return Query{}, errors.New("Workday query filters are missing")
		}
		filters := *query.Workday
		filters.Location = editable.Location
		filters.TitleWords = editable.Tags
		normalized, err := filters.Normalize()
		if err != nil {
			return Query{}, err
		}
		query.Workday = &normalized
	default:
		return Query{}, fmt.Errorf("query %q has unsupported source type %q", query.Name, query.SourceType)
	}
	return query, nil
}

func encodeQueryFilters(query Query) (json.RawMessage, error) {
	var filters any
	switch query.SourceType {
	case SourceAshby:
		filters = query.Ashby
	case SourceGreenhouse:
		filters = query.Greenhouse
	case SourceWorkday:
		filters = query.Workday
	default:
		return nil, fmt.Errorf("query %q has unsupported source type %q", query.Name, query.SourceType)
	}
	payload, err := json.Marshal(filters)
	if err != nil {
		return nil, fmt.Errorf("encode %s filters: %w", query.SourceType, err)
	}
	return payload, nil
}

func (s *Store) Delete(ctx context.Context, id int64) (bool, error) {
	deleted, err := s.queries.DeleteSearchQueryByAnyID(ctx, id)
	if err != nil {
		return false, fmt.Errorf("delete search query: %w", err)
	}
	return deleted == 1, nil
}

func (s *Store) GetGreenhouse(ctx context.Context, name string) (GreenhouseQuery, error) {
	row, err := s.queries.GetSearchQueryByName(ctx, strings.TrimSpace(name))
	if err != nil {
		return GreenhouseQuery{}, fmt.Errorf("get search query: %w", err)
	}
	return decodeGreenhouse(row)
}

func (s *Store) GetAshby(ctx context.Context, name string) (AshbyQuery, error) {
	row, err := s.queries.GetSearchQueryByName(ctx, strings.TrimSpace(name))
	if err != nil {
		return AshbyQuery{}, fmt.Errorf("get search query: %w", err)
	}
	return decodeAshby(row)
}

func (s *Store) GetAshbyByID(ctx context.Context, id int64) (AshbyQuery, error) {
	row, err := s.queries.GetSearchQueryByID(ctx, db.GetSearchQueryByIDParams{
		ID:         id,
		SourceType: string(SourceAshby),
	})
	if err != nil {
		return AshbyQuery{}, fmt.Errorf("get Ashby search query: %w", err)
	}
	return decodeAshby(row)
}

func (s *Store) DeleteAshby(ctx context.Context, id int64) (bool, error) {
	deleted, err := s.queries.DeleteSearchQueryByID(ctx, db.DeleteSearchQueryByIDParams{
		ID:         id,
		SourceType: string(SourceAshby),
	})
	if err != nil {
		return false, fmt.Errorf("delete Ashby search query: %w", err)
	}
	return deleted == 1, nil
}

func (s *Store) ListAshby(ctx context.Context) ([]AshbyQuery, error) {
	rows, err := s.queries.ListSearchQueries(ctx)
	if err != nil {
		return nil, fmt.Errorf("list search queries: %w", err)
	}

	result := make([]AshbyQuery, 0, len(rows))
	for _, row := range rows {
		if SourceType(row.SourceType) != SourceAshby {
			continue
		}
		query, err := decodeAshby(row)
		if err != nil {
			return nil, err
		}
		result = append(result, query)
	}
	return result, nil
}

func (s *Store) GetGreenhouseByID(ctx context.Context, id int64) (GreenhouseQuery, error) {
	row, err := s.queries.GetSearchQueryByID(ctx, db.GetSearchQueryByIDParams{
		ID:         id,
		SourceType: string(SourceGreenhouse),
	})
	if err != nil {
		return GreenhouseQuery{}, fmt.Errorf("get Greenhouse search query: %w", err)
	}
	return decodeGreenhouse(row)
}

func (s *Store) DeleteGreenhouse(ctx context.Context, id int64) (bool, error) {
	deleted, err := s.queries.DeleteSearchQueryByID(ctx, db.DeleteSearchQueryByIDParams{
		ID:         id,
		SourceType: string(SourceGreenhouse),
	})
	if err != nil {
		return false, fmt.Errorf("delete Greenhouse search query: %w", err)
	}
	return deleted == 1, nil
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

func decodeAshby(row db.SearchQuery) (AshbyQuery, error) {
	if SourceType(row.SourceType) != SourceAshby {
		return AshbyQuery{}, fmt.Errorf("query %q has source type %q, not %q", row.Name, row.SourceType, SourceAshby)
	}
	var filters ashby.Filters
	if err := json.Unmarshal(row.Filters, &filters); err != nil {
		return AshbyQuery{}, fmt.Errorf("decode filters for query %q: %w", row.Name, err)
	}
	filters, err := filters.Normalize()
	if err != nil {
		return AshbyQuery{}, fmt.Errorf("validate filters for query %q: %w", row.Name, err)
	}
	return AshbyQuery{
		ID:        row.ID,
		Name:      row.Name,
		Filters:   filters,
		Enabled:   row.Enabled,
		CreatedAt: row.CreatedAt.Time,
		UpdatedAt: row.UpdatedAt.Time,
	}, nil
}

func decodeWorkday(row db.SearchQuery) (WorkdayQuery, error) {
	if SourceType(row.SourceType) != SourceWorkday {
		return WorkdayQuery{}, fmt.Errorf("query %q has source type %q, not %q", row.Name, row.SourceType, SourceWorkday)
	}
	var filters workday.Filters
	if err := json.Unmarshal(row.Filters, &filters); err != nil {
		return WorkdayQuery{}, fmt.Errorf("decode filters for query %q: %w", row.Name, err)
	}
	filters, err := filters.Normalize()
	if err != nil {
		return WorkdayQuery{}, fmt.Errorf("validate filters for query %q: %w", row.Name, err)
	}
	return WorkdayQuery{
		ID:        row.ID,
		Name:      row.Name,
		Filters:   filters,
		Enabled:   row.Enabled,
		CreatedAt: row.CreatedAt.Time,
		UpdatedAt: row.UpdatedAt.Time,
	}, nil
}

func decodeQuery(row db.SearchQuery) (Query, error) {
	query := Query{
		ID:         row.ID,
		Name:       row.Name,
		SourceType: SourceType(row.SourceType),
		Enabled:    row.Enabled,
		CreatedAt:  row.CreatedAt.Time,
		UpdatedAt:  row.UpdatedAt.Time,
	}
	switch query.SourceType {
	case SourceAshby:
		decoded, err := decodeAshby(row)
		if err != nil {
			return Query{}, err
		}
		query.Ashby = &decoded.Filters
	case SourceGreenhouse:
		decoded, err := decodeGreenhouse(row)
		if err != nil {
			return Query{}, err
		}
		query.Greenhouse = &decoded.Filters
	case SourceWorkday:
		decoded, err := decodeWorkday(row)
		if err != nil {
			return Query{}, err
		}
		query.Workday = &decoded.Filters
	default:
		return Query{}, fmt.Errorf("query %q has unsupported source type %q", row.Name, row.SourceType)
	}
	return query, nil
}
