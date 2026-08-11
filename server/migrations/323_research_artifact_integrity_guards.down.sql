-- Roll back Chapter D1f integrity guards.

DROP TRIGGER IF EXISTS research_result_attempt_projection_guard ON research_result_artifact;
DROP FUNCTION IF EXISTS research_result_attempt_projection_guard_fn();

DROP TRIGGER IF EXISTS research_artifact_version_producer_guard ON research_artifact_version;
DROP FUNCTION IF EXISTS research_artifact_version_producer_guard_fn();
