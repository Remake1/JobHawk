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
	"jobhawk/internal/searcherrors"
	"jobhawk/internal/searchqueries"
	"jobhawk/internal/textsearch"
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

type runStore interface {
	StartScheduled(context.Context, time.Time, int64, []searchqueries.Query) (Run, error)
	StartManual(context.Context, time.Time, int64, []searchqueries.Query) (Run, error)
	ListIncomplete(context.Context) ([]Run, error)
	ClaimNext(context.Context, int64) (ClaimedQuery, bool, error)
	RenewLease(context.Context, ClaimedQuery) error
	CompleteQuery(context.Context, ClaimedQuery, []jobstore.Discovered) error
	FailQuery(context.Context, ClaimedQuery, error) error
	RetryRateLimitedQuery(context.Context, ClaimedQuery, time.Time, error) error
	Finalize(context.Context, Run) (bool, error)
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

type textSearcher interface {
	Search(context.Context, textsearch.Filters) ([]jobs.Job, error)
}

// Runner executes every saved search, records every returned job, and emits a
// single aggregate digest. A query is automatically part of the daily run as
// soon as it is saved.
type Runner struct {
	runMu      sync.Mutex
	queries    queryStore
	runs       runStore
	chatID     int64
	location   *time.Location
	ashby      ashbySearcher
	greenhouse greenhouseSearcher
	text       textSearcher
	workday    workdaySearcher
}

func NewRunner(
	queries queryStore,
	runs runStore,
	chatID int64,
	location *time.Location,
	ashby ashbySearcher,
	greenhouse greenhouseSearcher,
	workday workdaySearcher,
	textSearchers ...textSearcher,
) *Runner {
	if location == nil {
		location = time.Local
	}
	text := textSearcher(textsearch.NewClient(nil))
	if len(textSearchers) > 0 && textSearchers[0] != nil {
		text = textSearchers[0]
	}
	return &Runner{
		queries: queries, runs: runs, chatID: chatID, location: location,
		ashby: ashby, greenhouse: greenhouse, text: text, workday: workday,
	}
}

// RunOnce starts a fresh durable manual run. Scheduled executions use
// RunScheduled so only one run can be claimed for a local calendar date.
func (r *Runner) RunOnce(ctx context.Context) error {
	r.runMu.Lock()
	defer r.runMu.Unlock()

	queries, err := r.queries.List(ctx)
	if err != nil {
		return fmt.Errorf("load daily search queries: %w", err)
	}
	runDate := localDate(time.Now(), r.location)
	run, err := r.runs.StartManual(ctx, runDate, r.chatID, queries)
	if err != nil {
		return err
	}
	return r.execute(ctx, run)
}

func (r *Runner) RunScheduled(ctx context.Context, date time.Time) error {
	r.runMu.Lock()
	defer r.runMu.Unlock()
	queries, err := r.queries.List(ctx)
	if err != nil {
		return fmt.Errorf("load daily search queries: %w", err)
	}
	run, err := r.runs.StartScheduled(ctx, localDate(date, r.location), r.chatID, queries)
	if err != nil {
		return err
	}
	if run.Status == "completed" {
		return nil
	}
	return r.execute(ctx, run)
}

// ResumeIncomplete finishes manual or scheduled runs whose process stopped
// before all query checkpoints or the outbox transaction were committed.
func (r *Runner) ResumeIncomplete(ctx context.Context) error {
	r.runMu.Lock()
	defer r.runMu.Unlock()
	runs, err := r.runs.ListIncomplete(ctx)
	if err != nil {
		return err
	}
	var runErrors []error
	for _, run := range runs {
		if err := r.execute(ctx, run); err != nil {
			runErrors = append(runErrors, fmt.Errorf("resume daily run %d: %w", run.ID, err))
		}
	}
	return errors.Join(runErrors...)
}

func (r *Runner) execute(ctx context.Context, run Run) error {
	var runErrors []error
	for {
		claimed, ok, err := r.runs.ClaimNext(ctx, run.ID)
		if err != nil {
			return errors.Join(append(runErrors, err)...)
		}
		if !ok {
			break
		}
		if err := r.executeQuery(ctx, claimed); err != nil {
			runErrors = append(runErrors, fmt.Errorf("run query %q: %w", claimed.Query.Name, err))
		}
	}
	if _, err := r.runs.Finalize(ctx, run); err != nil {
		runErrors = append(runErrors, err)
	}
	return errors.Join(runErrors...)
}

func (r *Runner) executeQuery(ctx context.Context, claimed ClaimedQuery) error {
	queryCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	stopHeartbeat := make(chan struct{})
	heartbeatErr := make(chan error, 1)
	go func() {
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-stopHeartbeat:
				return
			case <-queryCtx.Done():
				return
			case <-ticker.C:
				if err := r.runs.RenewLease(queryCtx, claimed); err != nil {
					heartbeatErr <- err
					cancel()
					return
				}
			}
		}
	}()

	found, sourceKey, searchErr := r.search(queryCtx, claimed.Query)
	close(stopHeartbeat)
	select {
	case err := <-heartbeatErr:
		return err
	default:
	}
	if searchErr != nil {
		if retryAfter, rateLimited := searcherrors.RetryDelay(searchErr); rateLimited {
			next := time.Now().UTC().Add(rateLimitRetryDelay(claimed.RateLimitAttempts, retryAfter))
			return r.runs.RetryRateLimitedQuery(ctx, claimed, next, searchErr)
		}
		if err := r.runs.FailQuery(ctx, claimed, searchErr); err != nil {
			return errors.Join(searchErr, err)
		}
		return nil
	}
	discovered := make([]jobstore.Discovered, 0, len(found))
	for _, foundJob := range found {
		discovered = append(discovered, jobstore.Discovered{
			SourceType: string(claimed.Query.SourceType), SourceKey: sourceKey, Job: foundJob,
		})
	}
	return r.runs.CompleteQuery(ctx, claimed, discovered)
}

func rateLimitRetryDelay(previousAttempts int, retryAfter time.Duration) time.Duration {
	if retryAfter > 0 {
		if retryAfter < time.Minute {
			return time.Minute
		}
		return retryAfter
	}
	switch previousAttempts {
	case 0:
		return 5 * time.Minute
	case 1:
		return 15 * time.Minute
	case 2:
		return time.Hour
	default:
		return 6 * time.Hour
	}
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
	case searchqueries.SourceText:
		if query.Text == nil {
			return nil, "", errors.New("text search filters are missing")
		}
		found, err := r.text.Search(ctx, *query.Text)
		return found, strings.TrimSpace(query.Text.URL), err
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

type scheduledRunner interface {
	ResumeIncomplete(context.Context) error
	RunScheduled(context.Context, time.Time) error
}

// Scheduler waits until the configured local hour, runs once, then schedules
// the next calendar day. Recomputing the time each day handles DST changes.
type Scheduler struct {
	runner           scheduledRunner
	hour             int
	location         *time.Location
	logger           *slog.Logger
	now              func() time.Time
	recoveryInterval time.Duration
}

func NewScheduler(runner scheduledRunner, hour int, location *time.Location, logger *slog.Logger) *Scheduler {
	if logger == nil {
		logger = slog.Default()
	}
	if location == nil {
		location = time.Local
	}
	return &Scheduler{
		runner: runner, hour: hour, location: location, logger: logger,
		now: time.Now, recoveryInterval: time.Minute,
	}
}

func (s *Scheduler) Run(ctx context.Context) error {
	if err := s.runner.ResumeIncomplete(ctx); err != nil && ctx.Err() == nil {
		s.logger.Error("resume incomplete daily runs", "error", err)
	}
	lastSuccessfulDate := ""
	s.runDueToday(ctx, &lastSuccessfulDate)
	recoveryTicker := time.NewTicker(s.recoveryInterval)
	defer recoveryTicker.Stop()
	next := nextDailyRun(s.now(), s.hour, s.location)
	s.logger.Info("daily job run scheduled", "at", next)
	timer := time.NewTimer(time.Until(next))
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-recoveryTicker.C:
			if err := s.runner.ResumeIncomplete(ctx); err != nil && ctx.Err() == nil {
				s.logger.Error("resume incomplete daily runs", "error", err)
			}
			s.runDueToday(ctx, &lastSuccessfulDate)
		case <-timer.C:
			s.runDueToday(ctx, &lastSuccessfulDate)
			next = nextDailyRun(s.now(), s.hour, s.location)
			s.logger.Info("daily job run scheduled", "at", next)
			timer.Reset(time.Until(next))
		}
	}
}

func (s *Scheduler) runDueToday(ctx context.Context, lastSuccessfulDate *string) {
	now := s.now()
	localNow := now.In(s.location)
	scheduled := time.Date(localNow.Year(), localNow.Month(), localNow.Day(), s.hour, 0, 0, 0, s.location)
	dateKey := localNow.Format("2006-01-02")
	if now.Before(scheduled) || *lastSuccessfulDate == dateKey {
		return
	}
	if s.run(ctx, localNow) {
		*lastSuccessfulDate = dateKey
	}
}

func (s *Scheduler) run(ctx context.Context, date time.Time) bool {
	if err := s.runner.RunScheduled(ctx, date); err != nil && ctx.Err() == nil {
		s.logger.Error("daily job run completed with errors", "error", err)
		return false
	}
	if ctx.Err() == nil {
		s.logger.Info("daily job run completed")
		return true
	}
	return false
}

func nextDailyRun(now time.Time, hour int, location *time.Location) time.Time {
	localNow := now.In(location)
	next := time.Date(localNow.Year(), localNow.Month(), localNow.Day(), hour, 0, 0, 0, location)
	if !next.After(localNow) {
		next = time.Date(localNow.Year(), localNow.Month(), localNow.Day()+1, hour, 0, 0, 0, location)
	}
	return next
}

func localDate(value time.Time, location *time.Location) time.Time {
	local := value.In(location)
	return time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, location)
}
