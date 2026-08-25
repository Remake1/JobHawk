package jobstore

import (
	"testing"
	"time"

	"jobhawk/internal/jobs"
)

func TestNormalize(t *testing.T) {
	postedAt := time.Date(2026, time.August, 25, 9, 0, 0, 0, time.FixedZone("test", -5*60*60))
	normalized, params, err := normalize(Discovered{
		SourceType: " GreenHouse ",
		SourceKey:  " point72 ",
		Job: jobs.Job{
			ID: " 123 ", Title: " Engineer ", Company: " Acme ",
			Location: " Remote ", URL: " https://example.com/123 ", PostedAt: postedAt,
		},
	})
	if err != nil {
		t.Fatalf("normalize() error = %v", err)
	}
	if normalized.SourceType != "greenhouse" || normalized.SourceKey != "point72" || normalized.Job.ID != "123" {
		t.Fatalf("normalize() = %+v", normalized)
	}
	if params.Title != "Engineer" || params.Location != "Remote" || !params.PostedAt.Valid || params.PostedAt.Time.Location() != time.UTC {
		t.Fatalf("params = %+v", params)
	}
}

func TestNormalizeRejectsMissingIdentity(t *testing.T) {
	_, _, err := normalize(Discovered{SourceType: "greenhouse", Job: jobs.Job{Title: "Engineer"}})
	if err == nil {
		t.Fatal("normalize() accepted a job without a source key and external ID")
	}
}
