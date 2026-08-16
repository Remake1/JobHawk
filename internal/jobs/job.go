package jobs

import (
	"context"
	"time"
)

// Job is the provider-independent representation of an opening.
type Job struct {
	ID       string
	Title    string
	Company  string
	Location string
	URL      string
	PostedAt time.Time
}

// Source is implemented by each job board or company feed integration.
type Source interface {
	Name() string
	Fetch(ctx context.Context) ([]Job, error)
}
