-- 465_universal_dag_projection down: remove the canonical mapping surface.
-- Frozen snapshot rows and their immutability guards are owned by migration
-- 315 and remain untouched; only the Universal mapping columns, their
-- uniqueness, and the backfill audit trail are dropped.
DROP INDEX IF EXISTS interaction_dag_causal_edge_universal_uidx;
ALTER TABLE interaction_dag_causal_edge
  DROP COLUMN IF EXISTS universal_edge_id;
DROP INDEX IF EXISTS interaction_dag_run_segment_universal_uidx;
ALTER TABLE interaction_dag_run_segment
  DROP COLUMN IF EXISTS universal_segment_id;
DROP TABLE IF EXISTS interaction_dag_projection_backfill_audit;
