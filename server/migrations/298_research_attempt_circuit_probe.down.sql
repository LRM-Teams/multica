DROP TABLE IF EXISTS research_attempt_circuit_probe;
DROP FUNCTION IF EXISTS research_attempt_circuit_probe_guard();

UPDATE research_execution_circuit_transition
SET cause = 'probe_failed',
    diagnostics = CASE
      WHEN diagnostics = '' THEN '[rollback 298] probe ended without a successful result'
      ELSE '[rollback 298] probe ended without a successful result: ' || diagnostics
    END
WHERE cause IN ('probe_abandoned', 'probe_inconclusive');

ALTER TABLE research_execution_circuit_transition
  DROP CONSTRAINT research_execution_circuit_transition_cause_check,
  ADD CONSTRAINT research_execution_circuit_transition_cause_check
  CHECK (cause IN (
    'failure_observed', 'configuration_changed', 'probe_claimed',
    'probe_succeeded', 'probe_failed', 'success_observed'
  ));
