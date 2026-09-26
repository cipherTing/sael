CREATE TABLE IF NOT EXISTS gateway_measurements_minute (
 bucket timestamptz NOT NULL, protocol text NOT NULL, model text NOT NULL,
 metric text NOT NULL, upper_bound double precision NOT NULL, count bigint NOT NULL,
 PRIMARY KEY (bucket,protocol,model,metric,upper_bound)
);
CREATE TABLE IF NOT EXISTS gateway_scene_matches_minute (
 bucket timestamptz NOT NULL, protocol text NOT NULL, model text NOT NULL,
 scene_id text NOT NULL, scene_name text NOT NULL, action text NOT NULL,
 winner_id text NOT NULL, winner_name text NOT NULL, count bigint NOT NULL,
 PRIMARY KEY (bucket,protocol,model,scene_id,action,winner_id)
);
CREATE TABLE IF NOT EXISTS gateway_jev_errors_minute (
 bucket timestamptz NOT NULL, protocol text NOT NULL, model text NOT NULL,
 kind text NOT NULL, count bigint NOT NULL,
 PRIMARY KEY (bucket,protocol,model,kind)
);
CREATE INDEX IF NOT EXISTS audit_events_endpoint_time_idx ON audit_events ((body->>'protocol'),time DESC);
CREATE INDEX IF NOT EXISTS audit_events_scene_time_idx ON audit_events ((body->'decision'->>'scene_id'),time DESC);
