DROP TRIGGER IF EXISTS research_director_identity_artifact_passport_delete_guard ON research_director_identity;
DROP TRIGGER IF EXISTS research_director_identity_artifact_passport_guard ON research_director_identity;
DROP TRIGGER IF EXISTS research_director_identity_passport_class_guard ON research_artifact_passport;
DROP TRIGGER IF EXISTS research_artifact_passport_class_guard ON research_artifact_passport;

CREATE CONSTRAINT TRIGGER research_artifact_passport_class_guard
AFTER INSERT OR UPDATE OF workspace_id, session_id, entity_kind ON research_artifact_passport
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW
WHEN (NEW.entity_kind NOT IN ('hypothesis','branch','insight','inquiry_edge'))
EXECUTE FUNCTION research_artifact_passport_class_guard_fn();
