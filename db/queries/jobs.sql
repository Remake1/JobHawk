-- name: InsertJob :one
INSERT INTO jobs (
    source_type,
    source_key,
    external_id,
    title,
    company,
    location,
    url,
    posted_at
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
ON CONFLICT (source_type, source_key, external_id) DO NOTHING
RETURNING *;

-- name: UpdateJob :one
UPDATE jobs
SET title = $4,
    company = $5,
    location = $6,
    url = $7,
    posted_at = $8,
    last_seen_at = now()
WHERE source_type = $1
  AND source_key = $2
  AND external_id = $3
RETURNING *;
