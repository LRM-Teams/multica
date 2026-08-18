-- Repair migration 387 for databases that already applied its original body.
-- Keep this idempotent: deployments may have partially applied the original
-- trigger replacement before the migration was corrected.
DROP TRIGGER IF EXISTS research_director_identity_passport_class_guard ON research_artifact_passport;
DROP TRIGGER IF EXISTS research_artifact_passport_class_guard ON research_artifact_passport;
DROP TRIGGER IF EXISTS research_director_identity_artifact_passport_guard ON research_director_identity;
DROP TRIGGER IF EXISTS research_director_identity_artifact_passport_delete_guard ON research_director_identity;

CREATE CONSTRAINT TRIGGER research_artifact_passport_class_guard
AFTER INSERT OR UPDATE OF workspace_id, session_id, entity_kind ON research_artifact_passport
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW
WHEN (NEW.entity_kind NOT IN ('hypothesis','branch','insight','inquiry_edge','research_director_identity'))
EXECUTE FUNCTION research_artifact_passport_class_guard_fn();

CREATE CONSTRAINT TRIGGER research_director_identity_passport_class_guard
AFTER INSERT OR UPDATE OF workspace_id, session_id, entity_kind ON research_artifact_passport
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW
WHEN (NEW.entity_kind = 'research_director_identity')
EXECUTE FUNCTION research_director_identity_passport_class_guard_fn();

CREATE CONSTRAINT TRIGGER research_director_identity_artifact_passport_guard
AFTER INSERT OR UPDATE OF id, workspace_id, session_id ON research_director_identity
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW
EXECUTE FUNCTION research_artifact_domain_passport_guard_fn('research_director_identity');

CREATE TRIGGER research_director_identity_artifact_passport_delete_guard
BEFORE DELETE ON research_director_identity
FOR EACH ROW
EXECUTE FUNCTION research_artifact_domain_passport_delete_guard_fn('research_director_identity');
