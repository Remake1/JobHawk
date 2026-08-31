package jobstore

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"jobhawk/internal/database/db"
	"jobhawk/internal/jobs"
)

// Discovered identifies a provider job independently of the search query that
// happened to match it. SourceKey is the provider board/site identity.
type Discovered struct {
	SourceType string
	SourceKey  string
	Job        jobs.Job
}

type Store struct {
	queries *db.Queries
}

type Stored struct {
	Job        jobs.Job
	DatabaseID int64
	IsNew      bool
}

func NewStore(queries *db.Queries) *Store {
	return &Store{queries: queries}
}

// Upsert stores the latest normalized job data and reports whether this is the
// first time the provider job has ever been observed.
func (s *Store) Upsert(ctx context.Context, discovered Discovered) (jobs.Job, bool, error) {
	stored, err := s.UpsertWithID(ctx, discovered)
	if err != nil {
		return jobs.Job{}, false, err
	}
	return stored.Job, stored.IsNew, nil
}

// UpsertWithID is the transactional form used by the daily-run store. In
// addition to the normalized provider job, it returns the database identity so
// a newly inserted row can be attributed to the run that discovered it.
func (s *Store) UpsertWithID(ctx context.Context, discovered Discovered) (Stored, error) {
	normalized, params, err := normalize(discovered)
	if err != nil {
		return Stored{}, err
	}

	row, err := s.queries.InsertJob(ctx, params)
	if err == nil {
		return Stored{Job: normalized.Job, DatabaseID: row.ID, IsNew: true}, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return Stored{}, fmt.Errorf("insert discovered job: %w", err)
	}

	row, err = s.queries.UpdateJob(ctx, db.UpdateJobParams(params))
	if err != nil {
		return Stored{}, fmt.Errorf("update discovered job: %w", err)
	}
	return Stored{Job: normalized.Job, DatabaseID: row.ID}, nil
}

func normalize(discovered Discovered) (Discovered, db.InsertJobParams, error) {
	discovered.SourceType = strings.ToLower(strings.TrimSpace(discovered.SourceType))
	discovered.SourceKey = strings.TrimSpace(discovered.SourceKey)
	discovered.Job.ID = strings.TrimSpace(discovered.Job.ID)
	discovered.Job.Title = strings.TrimSpace(discovered.Job.Title)
	discovered.Job.Company = strings.TrimSpace(discovered.Job.Company)
	discovered.Job.Location = strings.TrimSpace(discovered.Job.Location)
	discovered.Job.URL = strings.TrimSpace(discovered.Job.URL)
	if !discovered.Job.PostedAt.IsZero() {
		discovered.Job.PostedAt = discovered.Job.PostedAt.UTC()
	}

	switch {
	case discovered.SourceType == "":
		return Discovered{}, db.InsertJobParams{}, errors.New("job source type is required")
	case discovered.SourceKey == "":
		return Discovered{}, db.InsertJobParams{}, errors.New("job source key is required")
	case discovered.Job.ID == "":
		return Discovered{}, db.InsertJobParams{}, errors.New("job external ID is required")
	case discovered.Job.Title == "":
		return Discovered{}, db.InsertJobParams{}, errors.New("job title is required")
	}

	postedAt := pgtype.Timestamptz{}
	if !discovered.Job.PostedAt.IsZero() {
		postedAt = pgtype.Timestamptz{Time: discovered.Job.PostedAt, Valid: true}
	}
	return discovered, db.InsertJobParams{
		SourceType: discovered.SourceType,
		SourceKey:  discovered.SourceKey,
		ExternalID: discovered.Job.ID,
		Title:      discovered.Job.Title,
		Company:    discovered.Job.Company,
		Location:   discovered.Job.Location,
		Url:        discovered.Job.URL,
		PostedAt:   postedAt,
	}, nil
}
