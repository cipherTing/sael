ALTER TABLE audit_events
    ADD COLUMN IF NOT EXISTS risk_protocol text GENERATED ALWAYS AS (coalesce(body->>'protocol', '')) STORED,
    ADD COLUMN IF NOT EXISTS risk_model text GENERATED ALWAYS AS (coalesce(body->>'model', '')) STORED,
    ADD COLUMN IF NOT EXISTS risk_credential_id text GENERATED ALWAYS AS (coalesce(body->>'credential_id', '')) STORED,
    ADD COLUMN IF NOT EXISTS risk_client_ip text GENERATED ALWAYS AS (coalesce(body->>'client_ip', '')) STORED;

CREATE INDEX IF NOT EXISTS audit_events_risk_sources_idx ON audit_events(time DESC)
    INCLUDE (risk_protocol, risk_model, risk_credential_id, risk_client_ip)
    WHERE kind = 'hit';
