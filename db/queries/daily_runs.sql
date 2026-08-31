-- name: InsertScheduledDailyRun :one
INSERT INTO daily_runs (scheduled_date, run_type, schedule_key, target_chat_id, query_count)
VALUES ($1, 'scheduled', $2, $3, $4)
ON CONFLICT (schedule_key) DO NOTHING
RETURNING *;

-- name: GetDailyRunByScheduleKey :one
SELECT *
FROM daily_runs
WHERE schedule_key = $1;

-- name: InsertManualDailyRun :one
INSERT INTO daily_runs (scheduled_date, run_type, target_chat_id, query_count)
VALUES ($1, 'manual', $2, $3)
RETURNING *;

-- name: InsertQueryRun :exec
INSERT INTO query_runs (
    daily_run_id,
    search_query_id,
    query_name,
    source_type,
    filters
)
VALUES ($1, $2, $3, $4, $5);

-- name: ListIncompleteDailyRuns :many
SELECT *
FROM daily_runs
WHERE status = 'running'
ORDER BY started_at, id;

-- name: ClaimNextQueryRun :one
WITH candidate AS (
    SELECT id
    FROM query_runs
    WHERE query_runs.daily_run_id = sqlc.arg(run_id)
      AND (
          (status = 'pending' AND next_attempt_at <= now())
          OR (status = 'running' AND locked_until <= now())
      )
    ORDER BY id
    FOR UPDATE SKIP LOCKED
    LIMIT 1
)
UPDATE query_runs
SET status = 'running',
    lease_token = @lease_token,
    locked_until = now() + sqlc.arg(lease_seconds)::bigint * interval '1 second',
    started_at = COALESCE(started_at, now()),
    updated_at = now()
WHERE id = (SELECT id FROM candidate)
RETURNING *;

-- name: RenewQueryRunLease :execrows
UPDATE query_runs
SET locked_until = now() + sqlc.arg(lease_seconds)::bigint * interval '1 second',
    updated_at = now()
WHERE id = $1
  AND status = 'running'
  AND lease_token = $2;

-- name: MarkQueryRunSucceeded :execrows
UPDATE query_runs
SET status = 'succeeded',
    jobs_found = $3,
    new_jobs = $4,
    error_text = '',
    lease_token = NULL,
    locked_until = NULL,
    completed_at = now(),
    updated_at = now()
WHERE id = $1
  AND status = 'running'
  AND lease_token = $2;

-- name: MarkQueryRunFailed :execrows
UPDATE query_runs
SET status = 'failed',
    error_text = $3,
    lease_token = NULL,
    locked_until = NULL,
    completed_at = now(),
    updated_at = now()
WHERE id = $1
  AND status = 'running'
  AND lease_token = $2;

-- name: RetryRateLimitedQueryRun :execrows
UPDATE query_runs
SET status = 'pending',
    rate_limit_attempts = rate_limit_attempts + 1,
    next_attempt_at = $3,
    error_text = $4,
    lease_token = NULL,
    locked_until = NULL,
    updated_at = now()
WHERE id = $1
  AND status = 'running'
  AND lease_token = $2;

-- name: CountUnfinishedQueryRuns :one
SELECT count(*)
FROM query_runs
WHERE daily_run_id = $1
  AND status IN ('pending', 'running');

-- name: ListFailedQueryRuns :many
SELECT *
FROM query_runs
WHERE daily_run_id = $1
  AND status = 'failed'
ORDER BY id;

-- name: InsertDailyRunJob :exec
INSERT INTO daily_run_jobs (daily_run_id, job_id)
VALUES ($1, $2)
ON CONFLICT DO NOTHING;

-- name: ListDailyRunJobs :many
SELECT jobs.*
FROM daily_run_jobs
JOIN jobs ON jobs.id = daily_run_jobs.job_id
WHERE daily_run_jobs.daily_run_id = $1
ORDER BY daily_run_jobs.created_at, daily_run_jobs.job_id;

-- name: MarkDailyRunCompleted :execrows
UPDATE daily_runs
SET status = 'completed',
    failure_count = $2,
    completed_at = now(),
    updated_at = now()
WHERE id = $1
  AND status = 'running';

-- name: InsertNotificationOutbox :exec
INSERT INTO notification_outbox (daily_run_id, kind, chat_id, payload)
VALUES ($1, 'daily_digest', $2, $3)
ON CONFLICT (daily_run_id) DO NOTHING;

-- name: ClaimNotificationOutbox :one
WITH candidate AS (
    SELECT id
    FROM notification_outbox
    WHERE (
            status = 'pending'
            AND next_attempt_at <= now()
          )
       OR (
            status = 'processing'
            AND locked_until <= now()
          )
    ORDER BY next_attempt_at, id
    FOR UPDATE SKIP LOCKED
    LIMIT 1
)
UPDATE notification_outbox
SET status = 'processing',
    attempts = attempts + 1,
    lease_token = @lease_token,
    locked_until = now() + sqlc.arg(lease_seconds)::bigint * interval '1 second',
    updated_at = now()
WHERE id = (SELECT id FROM candidate)
RETURNING *;

-- name: MarkNotificationOutboxSent :execrows
UPDATE notification_outbox
SET status = 'sent',
    lease_token = NULL,
    locked_until = NULL,
    last_error = '',
    sent_at = now(),
    updated_at = now()
WHERE id = $1
  AND status = 'processing'
  AND lease_token = $2;

-- name: RetryNotificationOutbox :execrows
UPDATE notification_outbox
SET status = 'pending',
    next_attempt_at = $3,
    lease_token = NULL,
    locked_until = NULL,
    last_error = $4,
    updated_at = now()
WHERE id = $1
  AND status = 'processing'
  AND lease_token = $2;

-- name: FailNotificationOutbox :execrows
UPDATE notification_outbox
SET status = 'failed',
    lease_token = NULL,
    locked_until = NULL,
    last_error = $3,
    updated_at = now()
WHERE id = $1
  AND status = 'processing'
  AND lease_token = $2;
