-- V6 Work Item Attempts share the canonical `attempt` artifact kind with
-- V1-V5 Research Task Attempts. Route that kind through a union-aware class
-- guard and require every V6 attempt to have a reciprocal passport.

CREATE OR REPLACE FUNCTION research_attempt_passport_class_guard_fn()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM research_task_attempt entity
    WHERE (entity.workspace_id,entity.session_id,entity.id)=(NEW.workspace_id,NEW.session_id,NEW.id)
    UNION ALL
    SELECT 1 FROM research_work_item_attempt entity
    WHERE (entity.workspace_id,entity.session_id,entity.id)=(NEW.workspace_id,NEW.session_id,NEW.id)
  ) THEN
    RAISE foreign_key_violation USING CONSTRAINT = 'research_artifact_passport_class_guard';
  END IF;
  RETURN NEW;
END;
$$;

DROP TRIGGER research_artifact_passport_class_guard ON research_artifact_passport;
CREATE CONSTRAINT TRIGGER research_artifact_passport_class_guard
AFTER INSERT OR UPDATE OF workspace_id, session_id, entity_kind ON research_artifact_passport
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW
WHEN (NEW.entity_kind NOT IN ('attempt','hypothesis','branch','insight','inquiry_edge','research_director_identity'))
EXECUTE FUNCTION research_artifact_passport_class_guard_fn();

CREATE CONSTRAINT TRIGGER research_attempt_passport_class_guard
AFTER INSERT OR UPDATE OF workspace_id, session_id, entity_kind ON research_artifact_passport
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW WHEN (NEW.entity_kind='attempt')
EXECUTE FUNCTION research_attempt_passport_class_guard_fn();

-- V6 is still production-disabled, but internal fixture databases may already
-- contain attempts created before this reciprocal invariant existed.
SELECT research_artifact_backfill_registered(
  attempt.workspace_id, attempt.session_id, attempt.id, 'attempt',
  attempt.created_at, work.goal_version, NULL
)
FROM research_work_item_attempt attempt
JOIN research_work_item work
  ON (work.workspace_id,work.session_id,work.id)=(attempt.workspace_id,attempt.session_id,attempt.work_item_id)
WHERE NOT EXISTS (
  SELECT 1 FROM research_artifact_passport passport
  WHERE (passport.workspace_id,passport.session_id,passport.id)=(attempt.workspace_id,attempt.session_id,attempt.id)
);

CREATE CONSTRAINT TRIGGER research_work_item_attempt_artifact_passport_guard
AFTER INSERT OR UPDATE OF id,workspace_id,session_id ON research_work_item_attempt
DEFERRABLE INITIALLY DEFERRED FOR EACH ROW
EXECUTE FUNCTION research_artifact_domain_passport_guard_fn('attempt');

CREATE TRIGGER research_work_item_attempt_artifact_passport_delete_guard
BEFORE DELETE ON research_work_item_attempt FOR EACH ROW
EXECUTE FUNCTION research_artifact_domain_passport_delete_guard_fn('attempt');
