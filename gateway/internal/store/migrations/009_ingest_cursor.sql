CREATE TABLE IF NOT EXISTS gateway_ingest_cursor (
 name text PRIMARY KEY,
 last_id text NOT NULL
);
