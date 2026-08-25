-- Drops the recall lifecycle ledger (reverse of 419 up).
DROP TRIGGER IF EXISTS graph_memory_submission_shape ON graph_memory_submission;
DROP TRIGGER IF EXISTS graph_memory_view_event_identity ON graph_memory_view_event;
DROP TRIGGER IF EXISTS graph_memory_trajectory_identity ON graph_memory_trajectory;
DROP TRIGGER IF EXISTS graph_memory_recall_identity ON graph_memory_recall;
DROP FUNCTION IF EXISTS graph_memory_submission_validate();
DROP FUNCTION IF EXISTS graph_memory_view_event_validate_identity();
DROP FUNCTION IF EXISTS graph_memory_trajectory_validate_identity();
DROP FUNCTION IF EXISTS graph_memory_recall_validate_identity();
DROP TABLE IF EXISTS graph_memory_version_lease;
DROP TABLE IF EXISTS graph_memory_submission;
DROP TABLE IF EXISTS graph_memory_view_event;
DROP TABLE IF EXISTS graph_memory_expansion_batch;
DROP TABLE IF EXISTS graph_memory_trajectory;
DROP TABLE IF EXISTS graph_memory_recall;
