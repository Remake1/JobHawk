package daily

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"
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

type fakeQueryStore struct {
	queries []searchqueries.Query
	err     error
}

func (s fakeQueryStore) List(context.Context) ([]searchqueries.Query, error) {
	return s.queries, s.err
}

type fakeRunState struct {
	run      Run
	pending  []ClaimedQuery
	newJobs  []jobs.Job
	failures []QueryFailure
	deferred int
}

type fakeRunStore struct {
	nextID  int64
	seen    map[string]struct{}
	states  map[int64]*fakeRunState
	reports []Report
}

func newFakeRunStore() *fakeRunStore {
	return &fakeRunStore{seen: make(map[string]struct{}), states: make(map[int64]*fakeRunState)}
}

func (s *fakeRunStore) start(queries []searchqueries.Query, runType string) Run {
	s.nextID++
	run := Run{ID: s.nextID, RunType: runType, Status: "running", TargetChatID: 1, QueryCount: len(queries)}
	state := &fakeRunState{run: run}
	for i, query := range queries {
		state.pending = append(state.pending, ClaimedQuery{ID: int64(i + 1), DailyRunID: run.ID, LeaseToken: "lease", Query: query})
	}
	s.states[run.ID] = state
	return run
}

func (s *fakeRunStore) StartScheduled(_ context.Context, _ time.Time, _ int64, queries []searchqueries.Query) (Run, error) {
	return s.start(queries, "scheduled"), nil
}

func (s *fakeRunStore) StartManual(_ context.Context, _ time.Time, _ int64, queries []searchqueries.Query) (Run, error) {
	return s.start(queries, "manual"), nil
}

func (s *fakeRunStore) ListIncomplete(context.Context) ([]Run, error) {
	var runs []Run
	for _, state := range s.states {
		if state.run.Status == "running" {
			runs = append(runs, state.run)
		}
	}
	return runs, nil
}

func (s *fakeRunStore) ClaimNext(_ context.Context, runID int64) (ClaimedQuery, bool, error) {
	state := s.states[runID]
	if state == nil || len(state.pending) == 0 {
		return ClaimedQuery{}, false, nil
	}
	claimed := state.pending[0]
	state.pending = state.pending[1:]
	return claimed, true, nil
}

func (*fakeRunStore) RenewLease(context.Context, ClaimedQuery) error { return nil }

func (s *fakeRunStore) CompleteQuery(_ context.Context, claimed ClaimedQuery, discovered []jobstore.Discovered) error {
	state := s.states[claimed.DailyRunID]
	for _, candidate := range discovered {
		key := candidate.SourceType + "|" + candidate.SourceKey + "|" + candidate.Job.ID
		if _, exists := s.seen[key]; !exists {
			s.seen[key] = struct{}{}
			state.newJobs = append(state.newJobs, candidate.Job)
		}
	}
	return nil
}

func (s *fakeRunStore) FailQuery(_ context.Context, claimed ClaimedQuery, err error) error {
	s.states[claimed.DailyRunID].failures = append(s.states[claimed.DailyRunID].failures, QueryFailure{QueryName: claimed.Query.Name, Error: err})
	return nil
}

func (s *fakeRunStore) RetryRateLimitedQuery(_ context.Context, claimed ClaimedQuery, _ time.Time, _ error) error {
	s.states[claimed.DailyRunID].deferred++
	return nil
}

func (s *fakeRunStore) Finalize(_ context.Context, run Run) (bool, error) {
	state := s.states[run.ID]
	if len(state.pending) != 0 || state.deferred != 0 {
		return false, nil
	}
	state.run.Status = "completed"
	s.reports = append(s.reports, Report{QueryCount: run.QueryCount, NewJobs: state.newJobs, Failures: state.failures})
	return true, nil
}

type fakeAshby struct{ found []jobs.Job }

func (s fakeAshby) Search(context.Context, ashby.Filters) ([]jobs.Job, error) {
	return s.found, nil
}

type fakeGreenhouse struct{ found []jobs.Job }

func (s fakeGreenhouse) Search(context.Context, greenhouse.Filters) ([]jobs.Job, error) {
	return s.found, nil
}

type fakeWorkday struct{ found []jobs.Job }

func (s fakeWorkday) Search(context.Context, workday.Filters) ([]jobs.Job, error) {
	return s.found, nil
}

type fakeText struct{ found []jobs.Job }

func (s fakeText) Search(context.Context, textsearch.Filters) ([]jobs.Job, error) {
	return s.found, nil
}

func TestRunnerStoresAllJobsAndSendsOneDigest(t *testing.T) {
	ashbyFilters := ashby.Filters{JobBoard: "acme"}
	greenhouseFilters := greenhouse.Filters{BoardToken: "acme"}
	workdayFilters := workday.Filters{Host: "acme.wd1.myworkdayjobs.com", Tenant: "acme", Site: "Jobs"}
	queryStore := fakeQueryStore{queries: []searchqueries.Query{
		{ID: 1, Name: "Ashby", SourceType: searchqueries.SourceAshby, Ashby: &ashbyFilters},
		{ID: 2, Name: "Greenhouse", SourceType: searchqueries.SourceGreenhouse, Greenhouse: &greenhouseFilters},
		{ID: 3, Name: "Workday", SourceType: searchqueries.SourceWorkday, Workday: &workdayFilters},
	}}
	store := newFakeRunStore()
	runner := NewRunner(
		queryStore,
		store,
		1,
		time.UTC,
		fakeAshby{found: []jobs.Job{{ID: "a1", Title: "Ashby Engineer"}}},
		fakeGreenhouse{found: []jobs.Job{{ID: "g1", Title: "Greenhouse Engineer"}}},
		fakeWorkday{found: []jobs.Job{{ID: "w1", Title: "Workday Engineer"}}},
	)

	if err := runner.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}
	if len(store.reports) != 1 {
		t.Fatalf("outbox reports = %d, want 1", len(store.reports))
	}
	if got := store.reports[0]; got.QueryCount != 3 || len(got.NewJobs) != 3 || len(got.Failures) != 0 {
		t.Fatalf("first report = %+v", got)
	}

	if err := runner.RunOnce(context.Background()); err != nil {
		t.Fatalf("second RunOnce() error = %v", err)
	}
	if len(store.reports) != 2 || len(store.reports[1].NewJobs) != 0 {
		t.Fatalf("second report = %+v", store.reports)
	}
}

type failingGreenhouse struct{}

func (failingGreenhouse) Search(context.Context, greenhouse.Filters) ([]jobs.Job, error) {
	return nil, errors.New("provider unavailable")
}

func TestRunnerStillSendsDigestWhenAQueryFails(t *testing.T) {
	filters := greenhouse.Filters{BoardToken: "acme"}
	store := newFakeRunStore()
	runner := NewRunner(
		fakeQueryStore{queries: []searchqueries.Query{{ID: 1, Name: "Broken", SourceType: searchqueries.SourceGreenhouse, Greenhouse: &filters}}},
		store, 1, time.UTC,
		fakeAshby{}, failingGreenhouse{}, fakeWorkday{},
	)

	if err := runner.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}
	if len(store.reports) != 1 || len(store.reports[0].Failures) != 1 {
		t.Fatalf("reports = %+v", store.reports)
	}
	if store.states[1].run.Status != "completed" {
		t.Fatalf("run status = %q", store.states[1].run.Status)
	}
}

type rateLimitedGreenhouse struct{ calls int }

func (s *rateLimitedGreenhouse) Search(context.Context, greenhouse.Filters) ([]jobs.Job, error) {
	s.calls++
	return nil, &searcherrors.RateLimitError{Provider: "Greenhouse", Status: "429 Too Many Requests"}
}

func TestRunnerDefersOnlyRateLimitedQuery(t *testing.T) {
	filters := greenhouse.Filters{BoardToken: "acme"}
	store := newFakeRunStore()
	provider := &rateLimitedGreenhouse{}
	runner := NewRunner(
		fakeQueryStore{queries: []searchqueries.Query{{ID: 1, Name: "Limited", SourceType: searchqueries.SourceGreenhouse, Greenhouse: &filters}}},
		store, 1, time.UTC, fakeAshby{}, provider, fakeWorkday{},
	)
	if err := runner.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}
	if provider.calls != 1 || store.states[1].deferred != 1 || len(store.reports) != 0 {
		t.Fatalf("calls=%d deferred=%d reports=%d", provider.calls, store.states[1].deferred, len(store.reports))
	}
}

func TestRateLimitRetryDelay(t *testing.T) {
	if got := rateLimitRetryDelay(0, 0); got != 5*time.Minute {
		t.Fatalf("first fallback = %v", got)
	}
	if got := rateLimitRetryDelay(2, 0); got != time.Hour {
		t.Fatalf("third fallback = %v", got)
	}
	if got := rateLimitRetryDelay(0, 20*time.Second); got != time.Minute {
		t.Fatalf("short Retry-After = %v", got)
	}
	if got := rateLimitRetryDelay(0, 10*time.Minute); got != 10*time.Minute {
		t.Fatalf("Retry-After = %v", got)
	}
}

func TestRunnerStoresTextAvailabilityResult(t *testing.T) {
	filters := textsearch.Filters{URL: "https://example.com/jobs?location=Poland", NoJobsText: "No jobs"}
	store := newFakeRunStore()
	runner := NewRunner(
		fakeQueryStore{queries: []searchqueries.Query{{ID: 1, Name: "Text", SourceType: searchqueries.SourceText, Text: &filters}}},
		store, 1, time.UTC, fakeAshby{}, fakeGreenhouse{}, fakeWorkday{},
		fakeText{found: []jobs.Job{{ID: "availability", Title: "Matching jobs available", URL: filters.URL}}},
	)

	if err := runner.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}
	if len(store.reports) != 1 || len(store.reports[0].NewJobs) != 1 {
		t.Fatalf("reports = %+v", store.reports)
	}
	if _, ok := store.seen["text|"+filters.URL+"|availability"]; !ok {
		t.Fatalf("stored keys = %+v", store.seen)
	}
}

func TestRunnerResumesFromDurableQuerySnapshot(t *testing.T) {
	filters := greenhouse.Filters{BoardToken: "acme", TitleWords: []string{"Engineer"}}
	store := newFakeRunStore()
	store.start([]searchqueries.Query{{
		ID: 11, Name: "Recovered", SourceType: searchqueries.SourceGreenhouse, Greenhouse: &filters,
	}}, "scheduled")
	runner := NewRunner(
		fakeQueryStore{err: errors.New("live query store should not be read")},
		store, 1, time.UTC, fakeAshby{},
		fakeGreenhouse{found: []jobs.Job{{ID: "g1", Title: "Engineer"}}},
		fakeWorkday{},
	)

	if err := runner.ResumeIncomplete(context.Background()); err != nil {
		t.Fatalf("ResumeIncomplete() error = %v", err)
	}
	if len(store.reports) != 1 || len(store.reports[0].NewJobs) != 1 {
		t.Fatalf("recovered reports = %+v", store.reports)
	}
}

func TestReportPayloadRoundTripPreservesFailures(t *testing.T) {
	want := Report{
		QueryCount: 2,
		NewJobs:    []jobs.Job{{ID: "1", Title: "Engineer", URL: "https://example.com/1"}},
		Failures:   []QueryFailure{{QueryName: "Broken", Error: errors.New("provider unavailable")}},
	}
	payload, err := marshalReport(want)
	if err != nil {
		t.Fatal(err)
	}
	got, err := unmarshalReport(payload)
	if err != nil {
		t.Fatal(err)
	}
	if got.QueryCount != want.QueryCount || len(got.NewJobs) != 1 || got.NewJobs[0].ID != "1" ||
		len(got.Failures) != 1 || got.Failures[0].Error.Error() != "provider unavailable" {
		t.Fatalf("round trip = %+v", got)
	}
}

func TestWorkdaySourceKeyUsesProviderCoordinates(t *testing.T) {
	got := workdaySourceKey(workday.Filters{Host: "host", Tenant: "tenant", Site: "Global"})
	if got != "host=host&site=Global&tenant=tenant" {
		t.Fatalf("workdaySourceKey() = %q", got)
	}
}

func TestNextDailyRun(t *testing.T) {
	location, err := time.LoadLocation("America/Chicago")
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		now  time.Time
		want string
	}{
		{now: time.Date(2026, 8, 25, 8, 30, 0, 0, location), want: "2026-08-25 09:00 CDT"},
		{now: time.Date(2026, 8, 25, 9, 0, 0, 0, location), want: "2026-08-26 09:00 CDT"},
		{now: time.Date(2026, 10, 31, 10, 0, 0, 0, location), want: "2026-11-01 09:00 CST"},
	}
	for _, tt := range tests {
		t.Run(fmt.Sprint(tt.now.Unix()), func(t *testing.T) {
			got := nextDailyRun(tt.now, 9, location).Format("2006-01-02 15:04 MST")
			if got != tt.want {
				t.Fatalf("nextDailyRun() = %q, want %q", got, tt.want)
			}
		})
	}
}

type fakeScheduledRunner struct {
	dates []time.Time
	err   error
}

func (*fakeScheduledRunner) ResumeIncomplete(context.Context) error { return nil }

func (r *fakeScheduledRunner) RunScheduled(_ context.Context, date time.Time) error {
	r.dates = append(r.dates, date)
	err := r.err
	r.err = nil
	return err
}

func TestSchedulerCatchUpIsOncePerSuccessfulLocalDate(t *testing.T) {
	location, err := time.LoadLocation("America/Chicago")
	if err != nil {
		t.Fatal(err)
	}
	runner := &fakeScheduledRunner{}
	scheduler := NewScheduler(runner, 9, location, nil)
	now := time.Date(2026, time.August, 30, 10, 0, 0, 0, location)
	scheduler.now = func() time.Time { return now }
	lastDate := ""

	scheduler.runDueToday(context.Background(), &lastDate)
	scheduler.runDueToday(context.Background(), &lastDate)
	if len(runner.dates) != 1 || lastDate != "2026-08-30" {
		t.Fatalf("dates=%v lastDate=%q", runner.dates, lastDate)
	}
	now = now.Add(24 * time.Hour)
	scheduler.runDueToday(context.Background(), &lastDate)
	if len(runner.dates) != 2 || lastDate != "2026-08-31" {
		t.Fatalf("dates=%v lastDate=%q", runner.dates, lastDate)
	}
}

func TestSchedulerRetriesFailedCatchUp(t *testing.T) {
	runner := &fakeScheduledRunner{err: errors.New("database unavailable")}
	scheduler := NewScheduler(runner, 9, time.UTC, nil)
	scheduler.now = func() time.Time { return time.Date(2026, time.August, 30, 10, 0, 0, 0, time.UTC) }
	lastDate := ""
	scheduler.runDueToday(context.Background(), &lastDate)
	scheduler.runDueToday(context.Background(), &lastDate)
	if len(runner.dates) != 2 || lastDate != "2026-08-30" {
		t.Fatalf("dates=%v lastDate=%q", runner.dates, lastDate)
	}
}

func TestSchedulerDoesNotRelogNextRunOnRecoveryPolls(t *testing.T) {
	runner := &fakeScheduledRunner{}
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, nil))
	scheduler := NewScheduler(runner, 9, time.UTC, logger)
	scheduler.now = func() time.Time { return time.Date(2026, time.August, 30, 10, 0, 0, 0, time.UTC) }
	scheduler.recoveryInterval = 5 * time.Millisecond
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Millisecond)
	defer cancel()
	if err := scheduler.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(logs.String(), "daily job run scheduled"); got != 1 {
		t.Fatalf("schedule log count = %d\n%s", got, logs.String())
	}
	if len(runner.dates) != 1 {
		t.Fatalf("scheduled runs = %d", len(runner.dates))
	}
}
