UPDATE gateway_policy SET body=body-'version' WHERE body ? 'version';
UPDATE policy_changes SET before=before-'version', after=after-'version'
WHERE before ? 'version' OR after ? 'version';
UPDATE audit_events SET body=body-'policy_version' WHERE body ? 'policy_version';
ALTER TABLE gateway_policy DROP COLUMN IF EXISTS version;
ALTER TABLE policy_changes DROP COLUMN IF EXISTS version;
