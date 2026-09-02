-- 494 down: drop the evaluation plane. Only valid for environments where
-- the evolution feature was never enabled (ADR 0021 D8); production
-- rollbacks fence writers instead of dropping append-only audit history.

DROP TABLE IF EXISTS skill_evaluation_assertion_result;
DROP TABLE IF EXISTS skill_evaluation_run;
DROP TABLE IF EXISTS skill_assertion;
DROP TABLE IF EXISTS skill_assertion_manifest;
DROP FUNCTION IF EXISTS skill_ledger_append_only();
