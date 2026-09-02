-- Task 8A down: drop the retraction fence in dependency order.
BEGIN;

DROP TABLE IF EXISTS memory_deletion_audit;
DROP TABLE IF EXISTS quarantined_pending_recompute;
DROP TABLE IF EXISTS memory_source_provenance;
DROP TABLE IF EXISTS retraction_registry;
DROP TABLE IF EXISTS memory_read_phase_gate;
DROP TABLE IF EXISTS memory_source_guard;

COMMIT;
