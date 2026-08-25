package daily

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"sync"
	"time"

	"jobhawk/internal/ashby"
	"jobhawk/internal/greenhouse"
	"jobhawk/internal/jobs"
	"jobhawk/internal/jobstore"
	"jobhawk/internal/searchqueries"
	"jobhawk/internal/workday"
)

type QueryFailure struct {
	QueryName string
	Error     error
}

type Report struct {
	QueryCount int
	NewJobs    []jobs.Job
	Failures   []QueryFailure
}

type queryStore interface {
	List(context.Context) ([]searchqueries.Query, error)
}

type discoveredJobStore interface {
	Upsert(context.Context, jobstore.Discovered) (jobs.Job, bool, error)
}

type notifier interface {
	NotifyDailyDigest(context.Context, Report) error
}

type ashbySearcher interface {
	Search(context.Context, ashby.Filters) ([]jobs.Job, error)
}

type greenhouseSearcher interface {
	Search(context.Context, greenhouse.Filters) ([]jobs.Job, error)
}

type workdaySearcher interface {
	Search(context.Context, workday.Filters) ([]jobs.Job, error)
}

// Runner executes every saved search, records every returned job, and emits a
// single aggregate digest. A query is automatically part of the daily run as
// soon as it is saved.
type Runner struct {
	runMu      sync.Mutex
	queries    queryStore
	jobs       discoveredJobStore
	notifier   notifier
	ashby      ashbySearcher
	greenhouse greenhouseSearcher
	workday    workdaySearcher
}

func NewRunner(
	queries queryStore,
	jobs discoveredJobStore,
	notifier notifier,
	ashby ashbySearcher,
	greenhouse greenhouseSearcher,
	workday workdaySearcher,
) *Runner {
	return &Runner{
		queries: queries, jobs: jobs, notifier: notifier,
		ashby: ashby, greenhouse: greenhouse, workday: workday,
	}
}

func (r *Runner) RunOnce(ctx context.Context) error {
	// The scheduler and debug button share this runner. Serializing runs keeps
	// one run from claiming only part of another run's newly inserted jobs.
	r.runMu.Lock()
	defer r.runMu.Unlock()

	queries, err := r.queries.List(ctx)
	if err != nil {
		return fmt.Errorf("load daily search queries: %w", err)
	}

	report := Report{QueryCount: len(queries)}
	runErrors := make([]error, 0)
	for _, query := range queries {
		found, sourceKey, err := r.search(ctx, query)
		if err != nil {
			report.Failures = append(report.Failures, QueryFailure{QueryName: query.Name, Error: err})
			runErrors = append(runErrors, fmt.Errorf("run query %q: %w", query.Name, err))
			continue
		}

		queryFailed := false
		for _, foundJob := range found {
			normalized, isNew, upsertErr := r.jobs.Upsert(ctx, jobstore.Discovered{
				SourceType: string(query.SourceType),
				SourceKey:  sourceKey,
				Job:        foundJob,
			})
			if upsertErr != nil {
				queryFailed = true
				runErrors = append(runErrors, fmt.Errorf("store job %q from query %q: %w", foundJob.ID, query.Name, upsertErr))
				continue
			}
			if isNew {
				report.NewJobs = append(report.NewJobs, normalized)
			}
		}
		if queryFailed {
			report.Failures = append(report.Failures, QueryFailure{
				QueryName: query.Name,
				Error:     errors.New("one or more returned jobs could not be stored"),
			})
		}
	}

	if err := r.notifier.NotifyDailyDigest(ctx, report); err != nil {
		runErrors = append(runErrors, fmt.Errorf("send daily digest: %w", err))
	}
	return errors.Join(runErrors...)
}

func (r *Runner) search(ctx context.Context, query searchqueries.Query) ([]jobs.Job, string, error) {
	switch query.SourceType {
	case searchqueries.SourceAshby:
		if query.Ashby == nil {
			return nil, "", errors.New("Ashby filters are missing")
		}
		found, err := r.ashby.Search(ctx, *query.Ashby)
		return found, strings.TrimSpace(query.Ashby.JobBoard), err
	case searchqueries.SourceGreenhouse:
		if query.Greenhouse == nil {
			return nil, "", errors.New("Greenhouse filters are missing")
		}
		found, err := r.greenhouse.Search(ctx, *query.Greenhouse)
		return found, strings.TrimSpace(query.Greenhouse.BoardToken), err
	case searchqueries.SourceWorkday:
		if query.Workday == nil {
			return nil, "", errors.New("Workday filters are missing")
		}
		found, err := r.workday.Search(ctx, *query.Workday)
		return found, workdaySourceKey(*query.Workday), err
	default:
		return nil, "", fmt.Errorf("unsupported source %q", query.SourceType)
	}
}

func workdaySourceKey(filters workday.Filters) string {
	values := url.Values{}
	values.Set("host", strings.TrimSpace(filters.Host))
	values.Set("site", strings.TrimSpace(filters.Site))
	values.Set("tenant", strings.TrimSpace(filters.Tenant))
	return values.Encode()
}

type onceRunner interface {
	RunOnce(context.Context) error
}

// Scheduler waits until the configured local hour, runs once, then schedules
// the next calendar day. Recomputing the time each day handles DST changes.
type Scheduler struct {
	runner   onceRunner
	hour     int
	location *time.Location
	logger   *slog.Logger
}

func NewScheduler(runner onceRunner, hour int, location *time.Location, logger *slog.Logger) *Scheduler {
	if logger == nil {
		logger = slog.Default()
	}
	if location == nil {
		location = time.Local
	}
	return &Scheduler{runner: runner, hour: hour, location: location, logger: logger}
}

func (s *Scheduler) Run(ctx context.Context) error {
	for {
		next := nextDailyRun(time.Now(), s.hour, s.location)
		s.logger.Info("daily job run scheduled", "at", next)
		timer := time.NewTimer(time.Until(next))
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return nil
		case <-timer.C:
		}

		if err := s.runner.RunOnce(ctx); err != nil && ctx.Err() == nil {
			s.logger.Error("daily job run completed with errors", "error", err)
		} else if ctx.Err() == nil {
			s.logger.Info("daily job run completed")
		}
	}
}

func nextDailyRun(now time.Time, hour int, location *time.Location) time.Time {
	localNow := now.In(location)
	next := time.Date(localNow.Year(), localNow.Month(), localNow.Day(), hour, 0, 0, 0, location)
	if !next.After(localNow) {
		next = time.Date(localNow.Year(), localNow.Month(), localNow.Day()+1, hour, 0, 0, 0, location)
	}
	return next
}
