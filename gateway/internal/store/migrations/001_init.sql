CREATE TABLE IF NOT EXISTS gateway_policy (
    id integer PRIMARY KEY CHECK (id = 1),
    version bigint NOT NULL,
    body jsonb NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now()
);
INSERT INTO gateway_policy(id, version, body) VALUES
    (1, 1, '{"enabled":false,"version":1,"thresholds":{},"scenes":[],"unmatched_action":"","preview_chars":null,"retention_days":null}')
ON CONFLICT (id) DO NOTHING;

CREATE TABLE IF NOT EXISTS audit_events (
    id text PRIMARY KEY,
    time timestamptz NOT NULL,
    kind text NOT NULL,
    action text NOT NULL,
    request_id text NOT NULL,
    body jsonb NOT NULL
);
CREATE INDEX IF NOT EXISTS audit_events_time_idx ON audit_events(time DESC);
CREATE INDEX IF NOT EXISTS audit_events_request_id_idx ON audit_events(request_id);

CREATE TABLE IF NOT EXISTS gateway_counts_minute (
    bucket timestamptz NOT NULL,
    protocol text NOT NULL,
    model text NOT NULL,
    outcome text NOT NULL,
    count bigint NOT NULL DEFAULT 0,
    last_seen timestamptz NOT NULL,
    PRIMARY KEY (bucket, protocol, model, outcome)
);
CREATE INDEX IF NOT EXISTS gateway_counts_time_idx ON gateway_counts_minute(bucket DESC);

CREATE TABLE IF NOT EXISTS replayed_counts (
    id text PRIMARY KEY
);

CREATE TABLE IF NOT EXISTS policy_changes (
    id bigserial PRIMARY KEY,
    time timestamptz NOT NULL DEFAULT now(),
    version bigint NOT NULL,
    actor text NOT NULL,
    before jsonb NOT NULL,
    after jsonb NOT NULL
);
