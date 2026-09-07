-- 495 down: drop the decision/deployment plane and its fences. Only valid
-- for environments where the evolution feature was never enabled
-- (ADR 0021 D8); production rollbacks fence writers instead.

DROP INDEX IF EXISTS skill_evolution_run_single_active_key;
DROP TABLE IF EXISTS skill_evolution_reconciliation;
DROP TABLE IF EXISTS skill_evolution_outbox;
DROP TABLE IF EXISTS skill_evolution_idempotency;
DROP TRIGGER IF EXISTS skill_rollback_no_delete ON skill_rollback;
DROP TRIGGER IF EXISTS skill_rollback_update_guard ON skill_rollback;
DROP TABLE IF EXISTS skill_rollback;
DROP FUNCTION IF EXISTS skill_rollback_update_guard();
DROP TRIGGER IF EXISTS skill_deployment_materialization_guard ON skill_deployment;
DROP TABLE IF EXISTS skill_deployment;
DROP FUNCTION IF EXISTS skill_deployment_materialization_guard();
DROP TABLE IF EXISTS skill_approval;
