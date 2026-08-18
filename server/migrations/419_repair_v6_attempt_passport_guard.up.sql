-- Migration 414 replayed the generic passport trigger without preserving the
-- V6 Work Item Attempt exclusion introduced by migration 410. Route attempts
-- only through their union-aware class guard.
DROP TRIGGER IF EXISTS research_artifact_passport_class_guard ON research_artifact_passport;
CREATE CONSTRAINT TRIGGER research_artifact_passport_class_guard
AFTER INSERT OR UPDATE OF workspace_id, session_id, entity_kind ON research_artifact_passport
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW
WHEN (NEW.entity_kind NOT IN ('attempt','hypothesis','branch','insight','inquiry_edge','research_director_identity'))
EXECUTE FUNCTION research_artifact_passport_class_guard_fn();
