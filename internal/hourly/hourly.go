package hourly

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"jobhawk/internal/ashby"
	"jobhawk/internal/greenhouse"
	"jobhawk/internal/jobs"
	"jobhawk/internal/searchqueries"
	"jobhawk/internal/workday"
)

type subscriptionStore interface {
	ListDue(context.Context, time.Time, time.Time) ([]Subscription, error)
	Advance(context.Context, int64, time.Time) error
	DeleteExpired(context.Context, time.Time) error
}

type queryStore interface {
	GetByID(context.Context, int64) (searchqueries.Query, error)
}

type notifier interface {
	NotifyHourlyResults(context.Context, searchqueries.Query, []jobs.Job) error
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

type Runner struct {
	runMu         sync.Mutex
	subscriptions subscriptionStore
	queries       queryStore
	notifier      notifier
	ashby         ashbySearcher
	greenhouse    greenhouseSearcher
	workday       workdaySearcher
	location      *time.Location
}

func NewRunner(subscriptions subscriptionStore, queries queryStore, notifier notifier, ashby ashbySearcher, greenhouse greenhouseSearcher, workday workdaySearcher, location *time.Location) *Runner {
	if location == nil {
		location = time.Local
	}
	return &Runner{
		subscriptions: subscriptions, queries: queries, notifier: notifier,
		ashby: ashby, greenhouse: greenhouse, workday: workday, location: location,
	}
}

// RunDue executes every schedule due at now. The next run is persisted before
// contacting a provider so a transient failure cannot make the scheduler spin.
func (r *Runner) RunDue(ctx context.Context, now time.Time) error {
	r.runMu.Lock()
	defer r.runMu.Unlock()

	today := dateInLocation(now, r.location)
	var runErrors []error
	if err := r.subscriptions.DeleteExpired(ctx, today); err != nil {
		runErrors = append(runErrors, err)
	}
	subscriptions, err := r.subscriptions.ListDue(ctx, today, now)
	if err != nil {
		return errors.Join(append(runErrors, err)...)
	}
	for _, subscription := range subscriptions {
		next := nextRunAfter(subscription, now)
		if err := r.subscriptions.Advance(ctx, subscription.ID, next); err != nil {
			runErrors = append(runErrors, err)
			continue
		}
		query, err := r.queries.GetByID(ctx, subscription.SearchQueryID)
		if err != nil {
			runErrors = append(runErrors, fmt.Errorf("load hourly query %d: %w", subscription.SearchQueryID, err))
			continue
		}
		found, err := r.search(ctx, query)
		if err != nil {
			runErrors = append(runErrors, fmt.Errorf("run hourly query %q: %w", query.Name, err))
			continue
		}
		if len(found) == 0 {
			continue
		}
		if err := r.notifier.NotifyHourlyResults(ctx, query, found); err != nil {
			runErrors = append(runErrors, fmt.Errorf("notify hourly query %q: %w", query.Name, err))
		}
	}
	return errors.Join(runErrors...)
}

func (r *Runner) search(ctx context.Context, query searchqueries.Query) ([]jobs.Job, error) {
	switch query.SourceType {
	case searchqueries.SourceAshby:
		if query.Ashby == nil {
			return nil, errors.New("Ashby filters are missing")
		}
		return r.ashby.Search(ctx, *query.Ashby)
	case searchqueries.SourceGreenhouse:
		if query.Greenhouse == nil {
			return nil, errors.New("Greenhouse filters are missing")
		}
		return r.greenhouse.Search(ctx, *query.Greenhouse)
	case searchqueries.SourceWorkday:
		if query.Workday == nil {
			return nil, errors.New("Workday filters are missing")
		}
		return r.workday.Search(ctx, *query.Workday)
	default:
		return nil, fmt.Errorf("unsupported source %q", query.SourceType)
	}
}

func nextRunAfter(subscription Subscription, now time.Time) time.Time {
	next := subscription.NextRunAt.Add(time.Duration(subscription.IntervalMinutes) * time.Minute)
	for !next.After(now) {
		next = next.Add(time.Duration(subscription.IntervalMinutes) * time.Minute)
	}
	return next
}

func dateInLocation(value time.Time, location *time.Location) time.Time {
	local := value.In(location)
	return time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, location)
}

type dueRunner interface {
	RunDue(context.Context, time.Time) error
}

type Scheduler struct {
	runner dueRunner
	logger *slog.Logger
	now    func() time.Time
	tick   time.Duration
}

func NewScheduler(runner dueRunner, logger *slog.Logger) *Scheduler {
	if logger == nil {
		logger = slog.Default()
	}
	return &Scheduler{runner: runner, logger: logger, now: time.Now, tick: time.Minute}
}

func (s *Scheduler) Run(ctx context.Context) error {
	s.run(ctx)
	ticker := time.NewTicker(s.tick)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			s.run(ctx)
		}
	}
}

func (s *Scheduler) run(ctx context.Context) {
	if err := s.runner.RunDue(ctx, s.now()); err != nil && ctx.Err() == nil {
		s.logger.Error("hourly search run completed with errors", "error", err)
	}
}
