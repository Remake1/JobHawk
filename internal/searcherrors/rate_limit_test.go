package searcherrors

import (
	"errors"
	"net/http"
	"testing"
	"time"
)

func TestRetryDelayRecognizesOnlyRateLimitErrors(t *testing.T) {
	response := &http.Response{Status: "429 Too Many Requests", Header: http.Header{"Retry-After": []string{"120"}}}
	err := NewRateLimit("Workday", response, "slow down")
	delay, ok := RetryDelay(err)
	if !ok || delay != 2*time.Minute {
		t.Fatalf("RetryDelay() = %v, %t", delay, ok)
	}
	if _, ok := RetryDelay(errors.New("400 Bad Request")); ok {
		t.Fatal("RetryDelay() classified a regular error as rate limited")
	}
}

func TestParseRetryAfterHTTPDate(t *testing.T) {
	now := time.Date(2026, time.August, 30, 12, 0, 0, 0, time.UTC)
	got := parseRetryAfter(now.Add(3*time.Minute).Format(http.TimeFormat), now)
	if got != 3*time.Minute {
		t.Fatalf("parseRetryAfter() = %v", got)
	}
}
