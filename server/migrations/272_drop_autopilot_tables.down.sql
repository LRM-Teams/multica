-- LRM-1051 down: refuse. Autopilot runtime tables are retired.
-- Restore from DB backup + autopilot_retirement_export offline if needed.
-- Recreating empty shells would not restore runs/triggers/webhooks safely.

DO $$
BEGIN
    RAISE EXCEPTION
        'down migration 272_drop_autopilot_tables refused: Autopilot tables dropped (LRM-1051); restore from backup + autopilot_retirement_export manually if needed';
END $$;
