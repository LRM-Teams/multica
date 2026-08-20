DROP TRIGGER IF EXISTS graph_memory_dive_job_identity ON graph_memory_dive_job;
DROP FUNCTION IF EXISTS graph_memory_dive_job_validate_identity();
DROP TABLE IF EXISTS graph_memory_dive_job;

ALTER TABLE graph_memory_trajectory
  DROP COLUMN IF EXISTS dive_status,
  DROP COLUMN IF EXISTS score_relevance,
  DROP COLUMN IF EXISTS score_groundedness,
  DROP COLUMN IF EXISTS score_completeness,
  DROP COLUMN IF EXISTS overall_score,
  DROP COLUMN IF EXISTS reward;
