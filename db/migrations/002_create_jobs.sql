CREATE TABLE jobs (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    source_type text NOT NULL,
    source_key text NOT NULL,
    external_id text NOT NULL,
    title text NOT NULL,
    company text NOT NULL DEFAULT '',
    location text NOT NULL DEFAULT '',
    url text NOT NULL DEFAULT '',
    posted_at timestamptz,
    first_seen_at timestamptz NOT NULL DEFAULT now(),
    last_seen_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (source_type, source_key, external_id)
);

CREATE INDEX jobs_last_seen_at_idx ON jobs (last_seen_at DESC);
