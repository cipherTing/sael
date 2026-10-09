ALTER TABLE gateway_jev ADD COLUMN IF NOT EXISTS max_input_chars integer CHECK (max_input_chars > 0);
-- The old token estimate was not reliable for compressed or multilingual input.
-- Reset existing installations to the explicit character based default.
UPDATE gateway_jev SET max_input_chars=5000 WHERE id=1 AND max_input_chars IS NULL AND max_input_tokens IS NOT NULL;
ALTER TABLE gateway_jev DROP COLUMN IF EXISTS max_input_tokens;
