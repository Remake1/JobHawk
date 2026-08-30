package hourly

import (
	"context"
	"testing"
	"time"

	"jobhawk/internal/ashby"
	"jobhawk/internal/greenhouse"
	"jobhawk/internal/jobs"
	"jobhawk/internal/searchqueries"
	"jobhawk/internal/textsearch"
	"jobhawk/internal/workday"
)

type fakeSubscriptionStore struct {
	due           []Subscription
	advanced      map[int64]time.Time
	expiredBefore time.Time
}

func (s *fakeSubscriptionStore) ListDue(context.Context, time.Time, time.Time) ([]Subscription, error) {
	return s.due, nil
}

func (s *fakeSubscriptionStore) Advance(_ context.Context, id int64, next time.Time) error {
	if s.advanced == nil {
		s.advanced = make(map[int64]time.Time)
	}
	s.advanced[id] = next
	return nil
}

func (s *fakeSubscriptionStore) DeleteExpired(_ context.Context, before time.Time) error {
	s.expiredBefore = before
	return nil
}

type fakeQueryStore struct{ query searchqueries.Query }

func (s fakeQueryStore) GetByID(context.Context, int64) (searchqueries.Query, error) {
	return s.query, nil
}

type fakeNotifier struct {
	queries []searchqueries.Query
	results [][]jobs.Job
}

func (n *fakeNotifier) NotifyHourlyResults(_ context.Context, query searchqueries.Query, found []jobs.Job) error {
	n.queries = append(n.queries, query)
	n.results = append(n.results, found)
	return nil
}

type fakeAshby struct{ found []jobs.Job }

func (s fakeAshby) Search(context.Context, ashby.Filters) ([]jobs.Job, error) { return s.found, nil }

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

func TestRunnerAdvancesSilentEmptySearch(t *testing.T) {
	location := time.FixedZone("test", -5*60*60)
	now := time.Date(2026, 8, 25, 10, 7, 0, 0, location)
	filters := greenhouse.Filters{BoardToken: "acme"}
	subscriptions := &fakeSubscriptionStore{due: []Subscription{{
		ID: 4, SearchQueryID: 9, SearchDate: dateInLocation(now, location),
		IntervalMinutes: 15, NextRunAt: now.Add(-7 * time.Minute),
	}}}
	notifier := &fakeNotifier{}
	runner := NewRunner(
		subscriptions,
		fakeQueryStore{query: searchqueries.Query{ID: 9, Name: "Acme", SourceType: searchqueries.SourceGreenhouse, Greenhouse: &filters}},
		notifier, fakeAshby{}, fakeGreenhouse{}, fakeWorkday{}, location,
	)

	if err := runner.RunDue(context.Background(), now); err != nil {
		t.Fatalf("RunDue() error = %v", err)
	}
	if len(notifier.results) != 0 {
		t.Fatalf("empty search sent %d notifications", len(notifier.results))
	}
	if got := subscriptions.advanced[4]; !got.Equal(now.Add(8 * time.Minute)) {
		t.Fatalf("next run = %v, want %v", got, now.Add(8*time.Minute))
	}
	if !subscriptions.expiredBefore.Equal(dateInLocation(now, location)) {
		t.Fatalf("expired cutoff = %v", subscriptions.expiredBefore)
	}
}

func TestRunnerNotifiesWhenSearchHasResults(t *testing.T) {
	now := time.Date(2026, 8, 25, 10, 0, 0, 0, time.UTC)
	filters := ashby.Filters{JobBoard: "acme"}
	subscriptions := &fakeSubscriptionStore{due: []Subscription{{
		ID: 4, SearchQueryID: 9, SearchDate: now, IntervalMinutes: 30, NextRunAt: now,
	}}}
	notifier := &fakeNotifier{}
	runner := NewRunner(
		subscriptions,
		fakeQueryStore{query: searchqueries.Query{ID: 9, Name: "Acme", SourceType: searchqueries.SourceAshby, Ashby: &filters}},
		notifier,
		fakeAshby{found: []jobs.Job{{ID: "1", Title: "Engineer"}}},
		fakeGreenhouse{}, fakeWorkday{}, time.UTC,
	)

	if err := runner.RunDue(context.Background(), now); err != nil {
		t.Fatalf("RunDue() error = %v", err)
	}
	if len(notifier.results) != 1 || len(notifier.results[0]) != 1 || notifier.queries[0].Name != "Acme" {
		t.Fatalf("notifications = %+v", notifier.results)
	}
}

func TestRunnerNotifiesForTextAvailability(t *testing.T) {
	now := time.Date(2026, 8, 25, 10, 0, 0, 0, time.UTC)
	filters := textsearch.Filters{URL: "https://example.com/jobs", NoJobsText: "No jobs"}
	subscriptions := &fakeSubscriptionStore{due: []Subscription{{
		ID: 5, SearchQueryID: 10, SearchDate: now, IntervalMinutes: 30, NextRunAt: now,
	}}}
	notifier := &fakeNotifier{}
	runner := NewRunner(
		subscriptions,
		fakeQueryStore{query: searchqueries.Query{ID: 10, Name: "Text", SourceType: searchqueries.SourceText, Text: &filters}},
		notifier, fakeAshby{}, fakeGreenhouse{}, fakeWorkday{}, time.UTC,
		fakeText{found: []jobs.Job{{ID: "availability", Title: "Matching jobs available"}}},
	)

	if err := runner.RunDue(context.Background(), now); err != nil {
		t.Fatalf("RunDue() error = %v", err)
	}
	if len(notifier.results) != 1 || notifier.results[0][0].ID != "availability" {
		t.Fatalf("notifications = %+v", notifier.results)
	}
}

func TestNextRunAfterSkipsMissedIntervals(t *testing.T) {
	now := time.Date(2026, 8, 25, 10, 44, 0, 0, time.UTC)
	got := nextRunAfter(Subscription{IntervalMinutes: 15, NextRunAt: time.Date(2026, 8, 25, 10, 0, 0, 0, time.UTC)}, now)
	want := time.Date(2026, 8, 25, 10, 45, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Fatalf("nextRunAfter() = %v, want %v", got, want)
	}
}
