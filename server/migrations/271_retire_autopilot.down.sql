-- LRM-1049: refuse loud. Restoring Autopilot would reintroduce an abolished
-- product surface; reverse by restoring from autopilot_retirement_export offline
-- if ever required — do not auto-unpause.
DO $$
BEGIN
  RAISE EXCEPTION
    'down migration 271_retire_autopilot refused: Autopilot is retired (LRM-1049); restore from autopilot_retirement_export manually if needed';
END $$;
