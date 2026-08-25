package daily

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"jobhawk/internal/ashby"
	"jobhawk/internal/greenhouse"
	"jobhawk/internal/jobs"
	"jobhawk/internal/jobstore"
	"jobhawk/internal/searchqueries"
	"jobhawk/internal/workday"
)

type fakeQueryStore struct {
	queries []searchqueries.Query
}

func (s fakeQueryStore) List(context.Context) ([]searchqueries.Query, error) {
	return s.queries, nil
}

type fakeJobStore struct {
	seen map[string]struct{}
}

func (s *fakeJobStore) Upsert(_ context.Context, discovered jobstore.Discovered) (jobs.Job, bool, error) {
	key := discovered.SourceType + "|" + discovered.SourceKey + "|" + discovered.Job.ID
	_, exists := s.seen[key]
	s.seen[key] = struct{}{}
	return discovered.Job, !exists, nil
}

type fakeNotifier struct {
	reports []Report
}

func (n *fakeNotifier) NotifyDailyDigest(_ context.Context, report Report) error {
	n.reports = append(n.reports, report)
	return nil
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

func TestRunnerStoresAllJobsAndSendsOneDigest(t *testing.T) {
	ashbyFilters := ashby.Filters{JobBoard: "acme"}
	greenhouseFilters := greenhouse.Filters{BoardToken: "acme"}
	workdayFilters := workday.Filters{Host: "acme.wd1.myworkdayjobs.com", Tenant: "acme", Site: "Jobs"}
	queryStore := fakeQueryStore{queries: []searchqueries.Query{
		{Name: "Ashby", SourceType: searchqueries.SourceAshby, Ashby: &ashbyFilters},
		{Name: "Greenhouse", SourceType: searchqueries.SourceGreenhouse, Greenhouse: &greenhouseFilters},
		{Name: "Workday", SourceType: searchqueries.SourceWorkday, Workday: &workdayFilters},
	}}
	store := &fakeJobStore{seen: make(map[string]struct{})}
	notifier := &fakeNotifier{}
	runner := NewRunner(
		queryStore,
		store,
		notifier,
		fakeAshby{found: []jobs.Job{{ID: "a1", Title: "Ashby Engineer"}}},
		fakeGreenhouse{found: []jobs.Job{{ID: "g1", Title: "Greenhouse Engineer"}}},
		fakeWorkday{found: []jobs.Job{{ID: "w1", Title: "Workday Engineer"}}},
	)

	if err := runner.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}
	if len(notifier.reports) != 1 {
		t.Fatalf("digest calls = %d, want 1", len(notifier.reports))
	}
	if got := notifier.reports[0]; got.QueryCount != 3 || len(got.NewJobs) != 3 || len(got.Failures) != 0 {
		t.Fatalf("first report = %+v", got)
	}

	if err := runner.RunOnce(context.Background()); err != nil {
		t.Fatalf("second RunOnce() error = %v", err)
	}
	if len(notifier.reports) != 2 || len(notifier.reports[1].NewJobs) != 0 {
		t.Fatalf("second report = %+v", notifier.reports)
	}
}

type failingGreenhouse struct{}

func (failingGreenhouse) Search(context.Context, greenhouse.Filters) ([]jobs.Job, error) {
	return nil, errors.New("provider unavailable")
}

func TestRunnerStillSendsDigestWhenAQueryFails(t *testing.T) {
	filters := greenhouse.Filters{BoardToken: "acme"}
	notifier := &fakeNotifier{}
	runner := NewRunner(
		fakeQueryStore{queries: []searchqueries.Query{{Name: "Broken", SourceType: searchqueries.SourceGreenhouse, Greenhouse: &filters}}},
		&fakeJobStore{seen: make(map[string]struct{})},
		notifier,
		fakeAshby{}, failingGreenhouse{}, fakeWorkday{},
	)

	if err := runner.RunOnce(context.Background()); err == nil {
		t.Fatal("RunOnce() expected the query error")
	}
	if len(notifier.reports) != 1 || len(notifier.reports[0].Failures) != 1 {
		t.Fatalf("reports = %+v", notifier.reports)
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
