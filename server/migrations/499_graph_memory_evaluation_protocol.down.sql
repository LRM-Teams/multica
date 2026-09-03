-- 499 down: drop the Graph Memory evaluation protocol plane. Only valid for
-- environments where the test-only evaluation gate was never enabled (or is
-- being torn down deliberately); the plane holds no production data.

DROP TRIGGER IF EXISTS graph_memory_evaluation_usage_append_only ON graph_memory_evaluation_usage_event;
DROP FUNCTION IF EXISTS graph_memory_evaluation_usage_append_only();
DROP TABLE IF EXISTS graph_memory_evaluation_usage_event;

ALTER TABLE graph_memory_evaluation_episode
    DROP CONSTRAINT IF EXISTS graph_memory_evaluation_unsupported_never_scored_check;
ALTER TABLE graph_memory_evaluation_episode
    DROP CONSTRAINT IF EXISTS graph_memory_evaluation_score_settled_check;
ALTER TABLE graph_memory_evaluation_episode
    DROP CONSTRAINT IF EXISTS graph_memory_evaluation_scored_payload_check;
DROP TRIGGER IF EXISTS graph_memory_evaluation_episode_binding_immutable ON graph_memory_evaluation_episode;
DROP FUNCTION IF EXISTS graph_memory_evaluation_binding_immutable();
DROP TABLE IF EXISTS graph_memory_evaluation_episode;

DROP TABLE IF EXISTS graph_memory_evaluation_run;
