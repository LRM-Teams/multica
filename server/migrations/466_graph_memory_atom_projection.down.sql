-- Task 7 down: drop the Atom ledger and the durable Graph projection
-- request surface. Segment rows themselves are untouched (454 lifecycle).

DROP TABLE IF EXISTS graph_memory_projection_outbox;
DROP TRIGGER IF EXISTS graph_memory_atom_validate ON graph_memory_atom;
DROP FUNCTION IF EXISTS validate_graph_memory_atom();
DROP TRIGGER IF EXISTS graph_memory_atom_write_once ON graph_memory_atom;
DROP FUNCTION IF EXISTS protect_graph_memory_atom();
DROP TABLE IF EXISTS graph_memory_atom;
DROP INDEX IF EXISTS interaction_dag_segment_workspace_segment_466_key;
