ALTER TABLE gateway_credentials ADD COLUMN IF NOT EXISTS note text NOT NULL DEFAULT '';
