CREATE TABLE IF NOT EXISTS gateway_upstream (
    id integer PRIMARY KEY CHECK (id = 1),
    base_url text NOT NULL DEFAULT '',
    updated_at timestamptz NOT NULL DEFAULT now()
);
INSERT INTO gateway_upstream(id) VALUES (1) ON CONFLICT (id) DO NOTHING;

ALTER TABLE gateway_jev ADD COLUMN IF NOT EXISTS timeout_ms integer NOT NULL DEFAULT 5000;
