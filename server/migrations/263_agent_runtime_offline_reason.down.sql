BEGIN;

ALTER TABLE agent_runtime DROP COLUMN offline_reason;

COMMIT;
