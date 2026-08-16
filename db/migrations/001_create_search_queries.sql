CREATE TABLE search_queries (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    name text NOT NULL UNIQUE,
    source_type text NOT NULL,
    filters jsonb NOT NULL CHECK (jsonb_typeof(filters) = 'object'),
    enabled boolean NOT NULL DEFAULT true,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX search_queries_source_type_idx ON search_queries (source_type);
