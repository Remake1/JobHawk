CREATE TABLE daily_runs (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    scheduled_date date NOT NULL,
    run_type text NOT NULL CHECK (run_type IN ('scheduled', 'manual')),
    schedule_key text UNIQUE,
    target_chat_id bigint NOT NULL,
    status text NOT NULL DEFAULT 'running' CHECK (status IN ('running', 'completed')),
    query_count integer NOT NULL DEFAULT 0 CHECK (query_count >= 0),
    failure_count integer NOT NULL DEFAULT 0 CHECK (failure_count >= 0),
    started_at timestamptz NOT NULL DEFAULT now(),
    completed_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CHECK (
        (run_type = 'scheduled' AND schedule_key IS NOT NULL)
        OR (run_type = 'manual' AND schedule_key IS NULL)
    )
);

CREATE INDEX daily_runs_incomplete_idx
    ON daily_runs (started_at, id)
    WHERE status = 'running';

CREATE TABLE query_runs (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    daily_run_id bigint NOT NULL REFERENCES daily_runs (id) ON DELETE CASCADE,
    search_query_id bigint NOT NULL,
    query_name text NOT NULL,
    source_type text NOT NULL,
    filters jsonb NOT NULL CHECK (jsonb_typeof(filters) = 'object'),
    status text NOT NULL DEFAULT 'pending'
        CHECK (status IN ('pending', 'running', 'succeeded', 'failed')),
    jobs_found integer NOT NULL DEFAULT 0 CHECK (jobs_found >= 0),
    new_jobs integer NOT NULL DEFAULT 0 CHECK (new_jobs >= 0),
    error_text text NOT NULL DEFAULT '',
    lease_token text,
    locked_until timestamptz,
    started_at timestamptz,
    completed_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (daily_run_id, search_query_id)
);

CREATE INDEX query_runs_claim_idx
    ON query_runs (daily_run_id, status, locked_until, id);

-- A job is linked only when the jobs row was inserted for the first time.
-- This preserves the digest contents if the process crashes before delivery.
CREATE TABLE daily_run_jobs (
    daily_run_id bigint NOT NULL REFERENCES daily_runs (id) ON DELETE CASCADE,
    job_id bigint NOT NULL REFERENCES jobs (id) ON DELETE CASCADE,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (daily_run_id, job_id)
);

CREATE TABLE notification_outbox (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    daily_run_id bigint NOT NULL UNIQUE REFERENCES daily_runs (id) ON DELETE CASCADE,
    kind text NOT NULL CHECK (kind IN ('daily_digest')),
    chat_id bigint NOT NULL,
    payload jsonb NOT NULL CHECK (jsonb_typeof(payload) = 'object'),
    status text NOT NULL DEFAULT 'pending'
        CHECK (status IN ('pending', 'processing', 'sent', 'failed')),
    attempts integer NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    next_attempt_at timestamptz NOT NULL DEFAULT now(),
    lease_token text,
    locked_until timestamptz,
    last_error text NOT NULL DEFAULT '',
    sent_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX notification_outbox_due_idx
    ON notification_outbox (next_attempt_at, id)
    WHERE status IN ('pending', 'processing');
