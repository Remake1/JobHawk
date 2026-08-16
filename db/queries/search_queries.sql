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

-- name: ListSearchQueries :many
SELECT *
FROM search_queries
ORDER BY name;
