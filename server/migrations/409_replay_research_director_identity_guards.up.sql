-- Replay the guard portion that was incorrectly appended to migration 387
-- after that migration had already shipped. All statements are idempotent so
-- both pre-fix and post-fix databases converge here.

CREATE OR REPLACE FUNCTION research_director_identity_passport_class_guard_fn()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM research_director_identity entity
    WHERE (entity.workspace_id,entity.session_id,entity.id)=(NEW.workspace_id,NEW.session_id,NEW.id)
  ) THEN
    RAISE foreign_key_violation USING CONSTRAINT = 'research_artifact_passport_class_guard';
  END IF;
  RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS research_artifact_passport_class_guard ON research_artifact_passport;
CREATE CONSTRAINT TRIGGER research_artifact_passport_class_guard
AFTER INSERT OR UPDATE OF workspace_id, session_id, entity_kind ON research_artifact_passport
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW
WHEN (NEW.entity_kind NOT IN ('hypothesis','branch','insight','inquiry_edge','research_director_identity'))
EXECUTE FUNCTION research_artifact_passport_class_guard_fn();

DROP TRIGGER IF EXISTS research_director_identity_passport_class_guard ON research_artifact_passport;
CREATE CONSTRAINT TRIGGER research_director_identity_passport_class_guard
AFTER INSERT OR UPDATE OF workspace_id, session_id, entity_kind ON research_artifact_passport
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW
WHEN (NEW.entity_kind = 'research_director_identity')
EXECUTE FUNCTION research_director_identity_passport_class_guard_fn();

DROP TRIGGER IF EXISTS research_director_identity_artifact_passport_guard ON research_director_identity;
CREATE CONSTRAINT TRIGGER research_director_identity_artifact_passport_guard
AFTER INSERT OR UPDATE OF id,workspace_id,session_id ON research_director_identity
DEFERRABLE INITIALLY DEFERRED FOR EACH ROW
EXECUTE FUNCTION research_artifact_domain_passport_guard_fn('research_director_identity');

DROP TRIGGER IF EXISTS research_director_identity_artifact_passport_delete_guard ON research_director_identity;
CREATE TRIGGER research_director_identity_artifact_passport_delete_guard
BEFORE DELETE ON research_director_identity FOR EACH ROW
EXECUTE FUNCTION research_artifact_domain_passport_delete_guard_fn('research_director_identity');
