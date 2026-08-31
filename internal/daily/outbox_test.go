package daily

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"
)

type fakeOutboxStore struct {
	message   OutboxMessage
	available bool
	sent      int
	retried   int
	retryAt   time.Time
	retryErr  error
}

func (s *fakeOutboxStore) ClaimOutbox(context.Context, time.Duration) (OutboxMessage, bool, error) {
	if !s.available {
		return OutboxMessage{}, false, nil
	}
	s.available = false
	return s.message, true, nil
}

func (s *fakeOutboxStore) MarkOutboxSent(context.Context, OutboxMessage) error {
	s.sent++
	return nil
}

func (s *fakeOutboxStore) RetryOutbox(_ context.Context, _ OutboxMessage, next time.Time, err error) error {
	s.retried++
	s.retryAt = next
	s.retryErr = err
	return nil
}

type fakeDigestSender struct {
	calls int
	err   error
}

func (s *fakeDigestSender) SendDailyDigest(context.Context, int64, Report) error {
	s.calls++
	return s.err
}

func TestDispatcherMarksSuccessfulDeliverySent(t *testing.T) {
	store := &fakeOutboxStore{available: true, message: OutboxMessage{ID: 1, ChatID: 7, LeaseToken: "lease"}}
	sender := &fakeDigestSender{}
	dispatcher := NewDispatcher(store, sender, slog.New(slog.NewTextHandler(io.Discard, nil)))
	dispatcher.drain(context.Background())
	if sender.calls != 1 || store.sent != 1 || store.retried != 0 {
		t.Fatalf("calls=%d sent=%d retried=%d", sender.calls, store.sent, store.retried)
	}
}

func TestDispatcherSchedulesTelegramFailureForRetry(t *testing.T) {
	now := time.Date(2026, time.August, 30, 12, 0, 0, 0, time.UTC)
	store := &fakeOutboxStore{available: true, message: OutboxMessage{ID: 1, ChatID: 7, Attempts: 2, LeaseToken: "lease"}}
	sendErr := errors.New("telegram unavailable")
	sender := &fakeDigestSender{err: sendErr}
	dispatcher := NewDispatcher(store, sender, slog.New(slog.NewTextHandler(io.Discard, nil)))
	dispatcher.now = func() time.Time { return now }
	dispatcher.drain(context.Background())
	if store.sent != 0 || store.retried != 1 || !errors.Is(store.retryErr, sendErr) {
		t.Fatalf("sent=%d retried=%d retryErr=%v", store.sent, store.retried, store.retryErr)
	}
	want := now.Add(5 * time.Minute)
	if !store.retryAt.Equal(want) {
		t.Fatalf("retryAt=%v, want %v", store.retryAt, want)
	}
}

func TestNotificationRetryDelayCapsAtSixHours(t *testing.T) {
	if got := notificationRetryDelay(20); got != 6*time.Hour {
		t.Fatalf("notificationRetryDelay(20)=%v", got)
	}
}
