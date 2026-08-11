BEGIN;

-- Schema-only rollback: rows deleted by the up migration cannot be rebuilt.
ALTER TABLE agent_reminder
    ADD COLUMN origin_kind TEXT NOT NULL DEFAULT 'agent',
    ADD COLUMN managed_kind TEXT,
    ADD COLUMN origin_key TEXT,
    ADD COLUMN managed_backoff_step SMALLINT NOT NULL DEFAULT 0,
    ADD CONSTRAINT agent_reminder_origin_kind_check
        CHECK (origin_kind IN ('agent', 'group_manager_auto')),
    ADD CONSTRAINT agent_reminder_managed_kind_check
        CHECK (managed_kind IS NULL OR managed_kind = 'patrol'),
    ADD CONSTRAINT agent_reminder_managed_origin_check
        CHECK (
            (origin_kind = 'agent' AND managed_kind IS NULL AND origin_key IS NULL)
            OR
            (
                origin_kind = 'group_manager_auto'
                AND managed_kind IS NOT NULL
                AND origin_key IS NOT NULL
                AND btrim(origin_key) <> ''
            )
        ),
    ADD CONSTRAINT agent_reminder_managed_backoff_step_check
        CHECK (managed_backoff_step BETWEEN 0 AND 3);

CREATE UNIQUE INDEX agent_reminder_active_managed_patrol_uidx
    ON agent_reminder (workspace_id, agent_id, anchor_channel_id)
    WHERE origin_kind = 'group_manager_auto'
      AND managed_kind = 'patrol'
      AND status IN ('scheduled', 'firing');

COMMIT;
