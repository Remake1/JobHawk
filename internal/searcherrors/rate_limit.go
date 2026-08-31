package searcherrors

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// RateLimitError is returned only for an HTTP 429 response. Callers can use
// RetryDelay to distinguish it from terminal provider and parsing failures.
type RateLimitError struct {
	Provider   string
	Status     string
	Body       string
	RetryAfter time.Duration
}

func (e *RateLimitError) Error() string {
	if e.Body == "" {
		return fmt.Sprintf("%s returned %s", e.Provider, e.Status)
	}
	return fmt.Sprintf("%s returned %s: %s", e.Provider, e.Status, e.Body)
}

func NewRateLimit(provider string, response *http.Response, body string) error {
	return &RateLimitError{
		Provider: provider, Status: response.Status, Body: strings.TrimSpace(body),
		RetryAfter: parseRetryAfter(response.Header.Get("Retry-After"), time.Now()),
	}
}

func RetryDelay(err error) (time.Duration, bool) {
	var rateLimit *RateLimitError
	if !errors.As(err, &rateLimit) {
		return 0, false
	}
	return rateLimit.RetryAfter, true
}

func parseRetryAfter(value string, now time.Time) time.Duration {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	if seconds, err := strconv.ParseInt(value, 10, 64); err == nil {
		if seconds <= 0 {
			return 0
		}
		return time.Duration(seconds) * time.Second
	}
	when, err := http.ParseTime(value)
	if err != nil || !when.After(now) {
		return 0
	}
	return when.Sub(now)
}
