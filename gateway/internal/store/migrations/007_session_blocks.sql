CREATE TABLE IF NOT EXISTS gateway_session_blocks (
    session_hash text PRIMARY KEY,
    expires_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS gateway_session_blocks_expiry_idx ON gateway_session_blocks(expires_at);
