CREATE TABLE hourly_search_queries (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    search_query_id bigint NOT NULL UNIQUE REFERENCES search_queries (id) ON DELETE CASCADE,
    search_date date NOT NULL,
    interval_minutes integer NOT NULL CHECK (interval_minutes IN (15, 30, 60)),
    next_run_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX hourly_search_queries_due_idx
    ON hourly_search_queries (search_date, next_run_at);
