BEGIN;

-- Backward deployment note: removing the fence permits older residents to
-- connect again. No Workspace, Agent, or binding data is deleted.
ALTER TABLE machine_upgrade
    DROP COLUMN IF EXISTS attested_workspace_ids,
    DROP COLUMN IF EXISTS accepted_workspace_ids;

ALTER TABLE daemon_heartbeat
    DROP COLUMN IF EXISTS computer_generation;

DROP TABLE IF EXISTS computer_generation;

COMMIT;
