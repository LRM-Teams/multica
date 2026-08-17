BEGIN;

-- Cloud Computer liveness is the current connect socket, matching Raft
-- 1.0.16 /daemon/connect. The machine-wide generation fence is unused.
-- Keep machine_upgrade.accepted_workspace_ids / attested_workspace_ids;
-- those belong to Workspace attestation, not the retired cloud fence.
ALTER TABLE daemon_heartbeat
    DROP COLUMN IF EXISTS computer_generation;

DROP TABLE IF EXISTS computer_generation;

COMMIT;
