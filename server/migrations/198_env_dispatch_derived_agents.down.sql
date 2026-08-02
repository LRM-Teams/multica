-- Reverse 198: restore the original environment_agent_sandbox status check,
-- drop the derived-agent binding columns and indexes, and remove agent
-- source-agent lineage. Existing rows keep source_agent_id NULL; new-path
-- inserts always set it, so no legacy binding is silently claimed as a
-- derived workflow on rollback.

-- Task #100 (2026-08-02): this narrowing drops 6 statuses this migration's
-- up.sql introduced ("the derived provisioning states", per its authoring
-- commit e3ae0ec46 — pure feature expansion, not a bugfix; git log -S
-- confirms neither value has ever been touched by a follow-up commit or
-- design doc). Split by whether the pre-198 code had an equivalent way to
-- express the same fact:
--
-- Safe remap (the old code already collapsed these distinctions, so this
-- recovers its original vocabulary rather than inventing one):
--   credential_ready / sandbox_creating / runtime_waiting / agent_creating
--     -> 'provisioning' (all four are sub-steps within what pre-198 code
--        only ever saw as one undifferentiated "provisioning" state)
--   failed_retryable -> 'failed' (pre-198 code had no retry concept for
--        this table at all; it always treated any failure uniformly)
--
-- No safe remap (fail loud instead):
--   'deleted' has no equivalent — pre-198's only related value is
--   'deleting', which means "in progress", not "done". Remapping deleted
--   rows to deleting would misrepresent a finished deletion as an active
--   one, and anything that scans for "still deleting" to retry/finish
--   cleanup would wrongly act on it.
UPDATE environment_agent_sandbox
SET status = 'provisioning'
WHERE status IN ('credential_ready', 'sandbox_creating', 'runtime_waiting', 'agent_creating');

UPDATE environment_agent_sandbox
SET status = 'failed'
WHERE status = 'failed_retryable';

DO $$
DECLARE
    affected_count integer;
BEGIN
    SELECT count(*) INTO affected_count
      FROM environment_agent_sandbox WHERE status = 'deleted';
    IF affected_count > 0 THEN
        RAISE EXCEPTION 'migration 198 down cannot proceed: % row(s) in environment_agent_sandbox have status=''deleted''. There is no safe value to remap them to — the only related pre-198 value is ''deleting'', which means an active in-progress deletion, not a finished one; remapping would misrepresent completed cleanup as still running. If you accept losing the distinction (rows will read as still deleting), run: UPDATE environment_agent_sandbox SET status = ''deleting'' WHERE status = ''deleted''; -- then re-run this down migration.', affected_count;
    END IF;
END $$;

ALTER TABLE environment_agent_sandbox
  DROP CONSTRAINT IF EXISTS environment_agent_sandbox_status_check,
  ADD CONSTRAINT environment_agent_sandbox_status_check CHECK (status IN ('pending', 'provisioning', 'ready', 'failed', 'deleting'));

DROP INDEX IF EXISTS environment_agent_sandbox_source_uidx;
DROP INDEX IF EXISTS environment_agent_sandbox_id_uidx;

ALTER TABLE environment_agent_sandbox
  DROP COLUMN IF EXISTS model_config_owner_agent_id,
  DROP COLUMN IF EXISTS credential_kind,
  DROP COLUMN IF EXISTS training_session_key,
  DROP COLUMN IF EXISTS training_session_ref,
  DROP COLUMN IF EXISTS training_session_id,
  DROP COLUMN IF EXISTS derived_agent_id,
  DROP COLUMN IF EXISTS source_agent_id,
  DROP COLUMN IF EXISTS id;

ALTER TABLE agent DROP CONSTRAINT IF EXISTS agent_source_workspace_fk;
ALTER TABLE agent DROP COLUMN IF EXISTS source_agent_id;
