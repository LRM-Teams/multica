DROP TRIGGER IF EXISTS research_work_item_attempt_artifact_passport_delete_guard ON research_work_item_attempt;
DROP TRIGGER IF EXISTS research_work_item_attempt_artifact_passport_guard ON research_work_item_attempt;
DROP TRIGGER IF EXISTS research_attempt_passport_class_guard ON research_artifact_passport;

DROP TRIGGER research_artifact_passport_class_guard ON research_artifact_passport;
CREATE CONSTRAINT TRIGGER research_artifact_passport_class_guard
AFTER INSERT OR UPDATE OF workspace_id, session_id, entity_kind ON research_artifact_passport
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW
WHEN (NEW.entity_kind NOT IN ('hypothesis','branch','insight','inquiry_edge','research_director_identity'))
EXECUTE FUNCTION research_artifact_passport_class_guard_fn();

DROP FUNCTION IF EXISTS research_attempt_passport_class_guard_fn();
