CREATE TABLE IF NOT EXISTS gateway_jev (
    id integer PRIMARY KEY CHECK (id = 1),
    base_url text NOT NULL DEFAULT '',
    model text NOT NULL DEFAULT '',
    api_key text NOT NULL DEFAULT '',
    updated_at timestamptz NOT NULL DEFAULT now()
);
INSERT INTO gateway_jev(id) VALUES (1) ON CONFLICT (id) DO NOTHING;
