-- 496 down: drop the retention/eligibility additions. Only valid for
-- environments where the evolution feature was never enabled
-- (ADR 0021 D8); production rollbacks fence writers instead.

DROP TABLE IF EXISTS skill_backfill_checkpoint;
DROP TRIGGER IF EXISTS skill_trajectory_eligibility_no_delete ON skill_trajectory_eligibility;
DROP TRIGGER IF EXISTS skill_trajectory_eligibility_update_guard ON skill_trajectory_eligibility;
DROP TABLE IF EXISTS skill_trajectory_eligibility;
DROP FUNCTION IF EXISTS skill_trajectory_eligibility_update_guard();
ALTER TABLE memory_retention_sweep_cursor DROP COLUMN IF EXISTS last_thinking_sweep_at;
ALTER TABLE memory_retention_policy DROP COLUMN IF EXISTS diagnostic_thinking_days;
