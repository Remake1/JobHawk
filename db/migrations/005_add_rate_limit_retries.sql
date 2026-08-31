ALTER TABLE query_runs
    ADD COLUMN rate_limit_attempts integer NOT NULL DEFAULT 0 CHECK (rate_limit_attempts >= 0),
    ADD COLUMN next_attempt_at timestamptz NOT NULL DEFAULT now();

DROP INDEX query_runs_claim_idx;

CREATE INDEX query_runs_claim_idx
    ON query_runs (daily_run_id, status, next_attempt_at, locked_until, id);
