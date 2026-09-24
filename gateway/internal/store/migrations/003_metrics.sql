ALTER TABLE gateway_counts_minute ADD COLUMN IF NOT EXISTS classifier_sum_ms bigint NOT NULL DEFAULT 0;
ALTER TABLE gateway_counts_minute ADD COLUMN IF NOT EXISTS classifier_samples bigint NOT NULL DEFAULT 0;
