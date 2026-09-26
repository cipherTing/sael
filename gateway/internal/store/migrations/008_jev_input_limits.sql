ALTER TABLE gateway_jev ADD COLUMN IF NOT EXISTS max_input_tokens integer CHECK (max_input_tokens > 0);
