-- LRM-1049: retire Autopilot (Frank: force-migrate where possible + full hard-cut).
-- Reminder cannot map webhook / create_issue / arbitrary cron. Those rows are
-- exported then paused — never silently dropped. Schedule+run_only with a
-- daily M H * * * cron becomes an agent reminder on workspace general.

BEGIN;

CREATE TABLE IF NOT EXISTS autopilot_retirement_export (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL,
    autopilot_id UUID NOT NULL,
    snapshot JSONB NOT NULL,
    disposition TEXT NOT NULL
        CHECK (disposition IN ('migrated_reminder', 'retired_unmappable')),
    reminder_id UUID,
    reason TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_autopilot_retirement_export_ws
    ON autopilot_retirement_export(workspace_id, created_at DESC);

INSERT INTO autopilot_retirement_export (workspace_id, autopilot_id, snapshot, disposition, reason)
SELECT
    a.workspace_id,
    a.id,
    jsonb_build_object(
        'autopilot', to_jsonb(a),
        'triggers', COALESCE((
            SELECT jsonb_agg(to_jsonb(t) ORDER BY t.created_at)
            FROM autopilot_trigger t
            WHERE t.autopilot_id = a.id
        ), '[]'::jsonb)
    ),
    'retired_unmappable',
    'pending classification'
FROM autopilot a
WHERE NOT EXISTS (
    SELECT 1 FROM autopilot_retirement_export e WHERE e.autopilot_id = a.id
);

CREATE TEMP TABLE _autopilot_migrate_candidates ON COMMIT DROP AS
SELECT
    a.id AS autopilot_id,
    a.workspace_id,
    a.assignee_id AS agent_id,
    a.title,
    COALESCE(NULLIF(trim(t.timezone), ''), 'UTC') AS tz,
    lpad(split_part(t.cron_expression, ' ', 2), 2, '0')
        || ':'
        || lpad(split_part(t.cron_expression, ' ', 1), 2, '0') AS hhmm,
    COALESCE(t.next_run_at, now() + interval '1 day') AS next_fire,
    g.id AS general_channel_id
FROM autopilot a
JOIN autopilot_trigger t ON t.autopilot_id = a.id
JOIN LATERAL (
    SELECT c.id
    FROM channel c
    WHERE c.workspace_id = a.workspace_id
      AND c.system_key = 'general'
    LIMIT 1
) g ON true
WHERE a.execution_mode = 'run_only'
  AND COALESCE(a.assignee_type, 'agent') = 'agent'
  AND t.kind = 'schedule'
  AND t.enabled
  AND t.cron_expression ~ '^[0-9]{1,2} [0-9]{1,2} \* \* \*$'
  AND (
      SELECT count(*) FROM autopilot_trigger t2
      WHERE t2.autopilot_id = a.id AND t2.enabled
  ) = 1;

CREATE TEMP TABLE _autopilot_migrate_results (
    autopilot_id UUID PRIMARY KEY,
    reminder_id UUID NOT NULL
) ON COMMIT DROP;

DO $$
DECLARE
    r RECORD;
    new_reminder_id UUID;
BEGIN
    FOR r IN SELECT * FROM _autopilot_migrate_candidates LOOP
        INSERT INTO agent_reminder (
            workspace_id,
            agent_id,
            title,
            anchor_channel_id,
            fire_at,
            status,
            cadence,
            schedule_timezone,
            cadence_next_at
        ) VALUES (
            r.workspace_id,
            r.agent_id,
            left('migrated: ' || r.title, 500),
            r.general_channel_id,
            r.next_fire,
            'scheduled',
            'daily@' || r.hhmm,
            r.tz,
            r.next_fire
        )
        RETURNING id INTO new_reminder_id;

        INSERT INTO _autopilot_migrate_results (autopilot_id, reminder_id)
        VALUES (r.autopilot_id, new_reminder_id);
    END LOOP;
END $$;

UPDATE autopilot_retirement_export e
SET
    disposition = 'migrated_reminder',
    reminder_id = m.reminder_id,
    reason = 'schedule run_only daily cron → agent_reminder on workspace general'
FROM _autopilot_migrate_results m
WHERE e.autopilot_id = m.autopilot_id;

UPDATE autopilot_retirement_export e
SET reason = CASE
    WHEN (e.snapshot->'autopilot'->>'execution_mode') = 'create_issue'
        THEN 'create_issue has no Reminder mapping; exported then paused'
    WHEN EXISTS (
        SELECT 1
        FROM jsonb_array_elements(e.snapshot->'triggers') t
        WHERE t->>'kind' = 'webhook'
          AND COALESCE((t->>'enabled')::boolean, false)
    )
        THEN 'webhook trigger has no Reminder mapping; exported then paused'
    WHEN EXISTS (
        SELECT 1
        FROM jsonb_array_elements(e.snapshot->'triggers') t
        WHERE t->>'kind' = 'schedule'
          AND COALESCE((t->>'enabled')::boolean, false)
          AND COALESCE(t->>'cron_expression', '') !~ '^[0-9]{1,2} [0-9]{1,2} \* \* \*$'
    )
        THEN 'non-daily cron has no Reminder cadence mapping; exported then paused'
    ELSE 'no migratable schedule+run_only daily trigger; exported then paused'
END
WHERE e.disposition = 'retired_unmappable'
  AND e.reason = 'pending classification';

UPDATE autopilot
SET status = 'paused', updated_at = now()
WHERE status = 'active';

UPDATE autopilot_trigger
SET enabled = false, updated_at = now()
WHERE enabled = true;

COMMIT;
