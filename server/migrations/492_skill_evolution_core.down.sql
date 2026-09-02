-- 492 down: drop the skill evolution core ledger.
--
-- Append-only audit tables: down is for pre-enable environments only. A
-- production rollback disables the writers and readers via the fail-closed
-- feature gates and keeps the audit rows (ADR 0021 D8).

DROP TABLE IF EXISTS skill_candidate_pattern;
DROP TABLE IF EXISTS skill_candidate_artifact;
DROP TRIGGER IF EXISTS skill_candidate_terminal_guard ON skill_candidate;
DROP FUNCTION IF EXISTS skill_candidate_terminal_guard();
DROP TABLE IF EXISTS skill_candidate;
DROP TRIGGER IF EXISTS skill_pattern_evidence_append_only ON skill_pattern_evidence;
DROP TABLE IF EXISTS skill_pattern_evidence;
DROP TRIGGER IF EXISTS skill_pattern_revision_append_only ON skill_pattern_revision;
DROP FUNCTION IF EXISTS skill_pattern_revision_append_only();
DROP TABLE IF EXISTS skill_pattern_revision;
DROP TABLE IF EXISTS skill_pattern;
DROP TRIGGER IF EXISTS skill_evolution_run_terminal_guard ON skill_evolution_run;
DROP FUNCTION IF EXISTS skill_evolution_run_terminal_guard();
DROP TABLE IF EXISTS skill_evolution_run;
DROP INDEX IF EXISTS skill_workspace_id_id_key;
