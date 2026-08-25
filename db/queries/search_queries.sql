-- name: UpsertSearchQuery :one
INSERT INTO search_queries (name, source_type, filters)
VALUES ($1, $2, $3)
ON CONFLICT (name) DO UPDATE SET
    source_type = EXCLUDED.source_type,
    filters = EXCLUDED.filters,
    updated_at = now()
RETURNING *;

-- name: GetSearchQueryByName :one
SELECT *
FROM search_queries
WHERE name = $1;

-- name: GetSearchQueryByID :one
SELECT *
FROM search_queries
WHERE id = $1 AND source_type = $2;

-- name: GetSearchQueryByAnyID :one
SELECT *
FROM search_queries
WHERE id = $1;

-- name: ListSearchQueries :many
SELECT *
FROM search_queries
ORDER BY name;

-- name: UpdateSearchQueryFilters :one
UPDATE search_queries
SET filters = $3,
    updated_at = now()
WHERE id = $1 AND source_type = $2
RETURNING *;

-- name: DeleteSearchQueryByID :execrows
DELETE FROM search_queries
WHERE id = $1 AND source_type = $2;

-- name: DeleteSearchQueryByAnyID :execrows
DELETE FROM search_queries
WHERE id = $1;
