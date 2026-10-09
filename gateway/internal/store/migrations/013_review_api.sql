CREATE TABLE IF NOT EXISTS review_api_keys (
    id text PRIMARY KEY,
    name text NOT NULL,
    note text NOT NULL DEFAULT '',
    secret_hash bytea NOT NULL UNIQUE,
    prefix text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    last_used_at timestamptz,
    revoked_at timestamptz
);

CREATE INDEX IF NOT EXISTS review_api_keys_active_idx ON review_api_keys(created_at DESC) WHERE revoked_at IS NULL;

CREATE TABLE IF NOT EXISTS review_api_counts_minute (
    bucket timestamptz NOT NULL,
    key_id text NOT NULL,
    outcome text NOT NULL,
    count bigint NOT NULL DEFAULT 0,
    duration_sum_ms bigint NOT NULL DEFAULT 0,
    duration_samples bigint NOT NULL DEFAULT 0,
    cache_hits bigint NOT NULL DEFAULT 0,
    PRIMARY KEY (bucket, key_id, outcome)
);
CREATE INDEX IF NOT EXISTS review_api_counts_time_idx ON review_api_counts_minute(bucket DESC);

CREATE TABLE IF NOT EXISTS review_api_scene_counts_minute (
    bucket timestamptz NOT NULL,
    key_id text NOT NULL,
    scene_id text NOT NULL,
    scene_name text NOT NULL,
    count bigint NOT NULL DEFAULT 0,
    PRIMARY KEY (bucket, key_id, scene_id)
);

CREATE TABLE IF NOT EXISTS review_api_latency_minute (
    bucket timestamptz NOT NULL,
    upper_ms bigint NOT NULL,
    count bigint NOT NULL DEFAULT 0,
    PRIMARY KEY (bucket, upper_ms)
);

-- Second counts provide a rolling minute RPM without storing request content.
CREATE TABLE IF NOT EXISTS review_api_requests_second (
    second timestamptz PRIMARY KEY,
    count bigint NOT NULL DEFAULT 0
);
ALTER TABLE audit_events ADD COLUMN IF NOT EXISTS request_source text
    GENERATED ALWAYS AS (coalesce(nullif(body->>'request_source',''),'gateway')) STORED;
CREATE INDEX IF NOT EXISTS audit_events_request_source_idx ON audit_events(request_source,time DESC);
