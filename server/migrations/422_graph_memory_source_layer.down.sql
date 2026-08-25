-- Drops the source-layer registry (reverse of 422 up).
DROP TRIGGER IF EXISTS graph_memory_source_identity ON graph_memory_source;
DROP FUNCTION IF EXISTS graph_memory_source_validate_identity();
DROP TABLE IF EXISTS graph_memory_source;
