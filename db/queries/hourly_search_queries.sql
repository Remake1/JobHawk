-- name: UpsertHourlySearchQuery :one
INSERT INTO hourly_search_queries (search_query_id, search_date, interval_minutes, next_run_at)
VALUES ($1, $2, $3, $4)
ON CONFLICT (search_query_id) DO UPDATE SET
    search_date = EXCLUDED.search_date,
    interval_minutes = EXCLUDED.interval_minutes,
    next_run_at = EXCLUDED.next_run_at,
    updated_at = now()
RETURNING *;

-- name: GetHourlySearchQueryBySearchQueryID :one
SELECT *
FROM hourly_search_queries
WHERE search_query_id = $1;

-- name: ListDueHourlySearchQueries :many
SELECT *
FROM hourly_search_queries
WHERE search_date = $1
  AND next_run_at <= $2
ORDER BY next_run_at, id;

-- name: UpdateHourlySearchQueryNextRun :exec
UPDATE hourly_search_queries
SET next_run_at = $2,
    updated_at = now()
WHERE id = $1;

-- name: DeleteHourlySearchQueryBySearchQueryID :execrows
DELETE FROM hourly_search_queries
WHERE search_query_id = $1;

-- name: DeleteExpiredHourlySearchQueries :execrows
DELETE FROM hourly_search_queries
WHERE search_date < $1;
