-- Task 21 down-migration: the shadow gate registry and its append-only
-- transition audit are Task 21-local state; dropping them loses the rollout
-- audit history, which is the intended rollback cost (routes revert to the
-- Task 8A default-off memory_read_phase_gate booleans).
DROP TABLE IF EXISTS universal_dag_gate_transition;
DROP TABLE IF EXISTS universal_dag_shadow_gate;
