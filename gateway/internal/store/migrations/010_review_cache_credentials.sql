CREATE TABLE IF NOT EXISTS gateway_review_cache (
 id integer PRIMARY KEY CHECK(id=1), ttl_days integer NOT NULL DEFAULT 7, max_bytes bigint NOT NULL DEFAULT 1073741824
);
INSERT INTO gateway_review_cache(id) VALUES(1) ON CONFLICT DO NOTHING;
CREATE TABLE IF NOT EXISTS gateway_credentials (
 id text PRIMARY KEY, upstream text NOT NULL, nonce bytea NOT NULL, ciphertext bytea NOT NULL, first_seen_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS gateway_credential_key (
 id integer PRIMARY KEY CHECK(id=1), nonce bytea NOT NULL, ciphertext bytea NOT NULL
);
CREATE INDEX IF NOT EXISTS audit_events_credential_idx ON audit_events ((body->>'credential_id'));

ALTER TABLE gateway_credentials ADD COLUMN IF NOT EXISTS retained_at timestamptz NOT NULL DEFAULT now();
