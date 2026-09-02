-- 497 down: drop the orchestrator lease plane and the pin guard.
-- Pre-enable environments only (ADR 0021 D8); an enabled deployment
-- rolls forward, never down.

DROP TRIGGER IF EXISTS skill_evolution_run_pin_guard ON skill_evolution_run;
DROP FUNCTION IF EXISTS skill_evolution_run_pin_guard();
DROP TRIGGER IF EXISTS skill_evolution_run_lease_guard ON skill_evolution_run_lease;
DROP FUNCTION IF EXISTS skill_evolution_run_lease_guard();
DROP TABLE IF EXISTS skill_evolution_run_lease;
