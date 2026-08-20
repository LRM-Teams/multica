-- Drops blob retention tables (reverse of 424 up).
DROP TRIGGER IF EXISTS graph_memory_blob_ref_identity ON graph_memory_blob_ref;
DROP FUNCTION IF EXISTS graph_memory_blob_ref_validate_identity();
DROP TABLE IF EXISTS graph_memory_blob_ref;
DROP TABLE IF EXISTS graph_memory_blob;
