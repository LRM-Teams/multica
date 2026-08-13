ALTER TABLE research_branch DROP CONSTRAINT IF EXISTS research_branch_termination_reason_check;
DROP TRIGGER IF EXISTS research_inquiry_edge_artifact_passport_delete_guard ON research_inquiry_edge;
DROP TRIGGER IF EXISTS research_insight_artifact_passport_delete_guard ON research_insight;
DROP TRIGGER IF EXISTS research_branch_artifact_passport_delete_guard ON research_branch;
DROP TRIGGER IF EXISTS research_hypothesis_artifact_passport_delete_guard ON research_hypothesis;
DROP TRIGGER IF EXISTS research_inquiry_edge_artifact_passport_guard ON research_inquiry_edge;
DROP TRIGGER IF EXISTS research_insight_artifact_passport_guard ON research_insight;
DROP TRIGGER IF EXISTS research_branch_artifact_passport_guard ON research_branch;
DROP TRIGGER IF EXISTS research_hypothesis_artifact_passport_guard ON research_hypothesis;
DROP TRIGGER IF EXISTS research_inquiry_passport_class_guard ON research_artifact_passport;
DROP TRIGGER IF EXISTS research_artifact_passport_class_guard ON research_artifact_passport;
DROP TRIGGER IF EXISTS research_artifact_passport_delete_guard ON research_artifact_passport;
DELETE FROM research_artifact_passport
WHERE entity_kind IN ('hypothesis','branch','insight','inquiry_edge');
CREATE TRIGGER research_artifact_passport_delete_guard
BEFORE DELETE ON research_artifact_passport
FOR EACH ROW EXECUTE FUNCTION research_artifact_passport_delete_guard_fn();
CREATE CONSTRAINT TRIGGER research_artifact_passport_class_guard
AFTER INSERT OR UPDATE OF workspace_id, session_id, entity_kind ON research_artifact_passport
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION research_artifact_passport_class_guard_fn();
DROP FUNCTION IF EXISTS research_inquiry_passport_class_guard_fn();
DROP TRIGGER IF EXISTS research_inquiry_edge_guard ON research_inquiry_edge;
DROP FUNCTION IF EXISTS research_inquiry_edge_guard_fn();
DROP FUNCTION IF EXISTS research_inquiry_entity_exists(UUID, UUID, TEXT, UUID);
DROP TRIGGER IF EXISTS research_insight_status_guard ON research_insight;
DROP TRIGGER IF EXISTS research_branch_status_guard ON research_branch;
DROP TRIGGER IF EXISTS research_hypothesis_status_guard ON research_hypothesis;
DROP FUNCTION IF EXISTS research_inquiry_status_guard_fn();
DROP FUNCTION IF EXISTS research_inquiry_transition_allowed(TEXT, TEXT, TEXT);
