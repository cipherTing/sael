CREATE TABLE IF NOT EXISTS gateway_key_sampling (
    fingerprint text PRIMARY KEY,
    upstream text NOT NULL,
    masked_key text NOT NULL,
    sample_rate smallint NOT NULL DEFAULT 100 CHECK (sample_rate BETWEEN 0 AND 100),
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS gateway_key_sampling_created_idx
    ON gateway_key_sampling(created_at DESC);
