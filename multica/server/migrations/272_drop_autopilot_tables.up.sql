-- LRM-1051: drop Autopilot runtime tables after retirement_export audit (271).
-- KEEP autopilot_retirement_export until the retention window ends:
--   7 calendar days after migration 271 is applied in production
--   (target drop-export follow-up: on/after 2026-08-10 if 271 lands 2026-08-03).
-- Reminder / agent_reminder is untouched.

BEGIN;

-- Refuse if export is incomplete vs remaining autopilot rows, or if a
-- migrated_reminder row lacks reminder_id (AC gate for LRM-1051).
DO $$
BEGIN
    IF to_regclass('public.autopilot') IS NOT NULL
       AND to_regclass('public.autopilot_retirement_export') IS NOT NULL
       AND EXISTS (
            SELECT 1
            FROM autopilot a
            WHERE NOT EXISTS (
                SELECT 1
                FROM autopilot_retirement_export e
                WHERE e.autopilot_id = a.id
            )
       ) THEN
        RAISE EXCEPTION
            'refusing drop: autopilot rows missing from autopilot_retirement_export (run 271 first)';
    END IF;

    IF to_regclass('public.autopilot_retirement_export') IS NOT NULL
       AND EXISTS (
            SELECT 1
            FROM autopilot_retirement_export
            WHERE disposition = 'migrated_reminder'
              AND reminder_id IS NULL
       ) THEN
        RAISE EXCEPTION
            'refusing drop: migrated_reminder rows must have reminder_id';
    END IF;
END $$;

-- Autopilot-only ingress ledger (FK → autopilot / trigger / run).
DROP TABLE IF EXISTS webhook_delivery;

-- Drop FK from inbox events onto autopilot_run; keep the column as orphan UUID
-- so historical task rows and metrics CASE expressions stay readable without
-- forcing a wide AutopilotRunID field rewrite in this slice.
ALTER TABLE agent_inbox_event
    DROP CONSTRAINT IF EXISTS agent_inbox_event_autopilot_run_id_fkey;

DROP TABLE IF EXISTS autopilot_run;
DROP TABLE IF EXISTS autopilot_trigger;
DROP TABLE IF EXISTS autopilot;

-- issue.origin_type='autopilot' remains a historical stamp (no FK).

COMMIT;
