-- Chapter E2b: Inquiry Graph lifecycle and polymorphic-edge integrity.

CREATE OR REPLACE FUNCTION research_inquiry_transition_allowed(
  entity_kind TEXT, old_status TEXT, new_status TEXT
) RETURNS BOOLEAN LANGUAGE sql IMMUTABLE AS $$
  SELECT old_status = new_status OR CASE entity_kind
    WHEN 'hypothesis' THEN CASE old_status
      WHEN 'proposed' THEN new_status IN ('investigating','obsolete')
      WHEN 'investigating' THEN new_status IN ('supported','weakened','refuted','conditional','obsolete')
      WHEN 'supported' THEN new_status IN ('investigating','weakened','refuted','conditional','obsolete')
      WHEN 'weakened' THEN new_status IN ('investigating','supported','refuted','conditional','obsolete')
      WHEN 'refuted' THEN new_status IN ('investigating','obsolete')
      WHEN 'conditional' THEN new_status IN ('investigating','supported','weakened','refuted','obsolete')
      ELSE false
    END
    WHEN 'branch' THEN CASE old_status
      WHEN 'proposed' THEN new_status IN ('active','terminated','obsolete')
      WHEN 'active' THEN new_status IN ('paused','completed','terminated','obsolete')
      WHEN 'paused' THEN new_status IN ('active','terminated','obsolete')
      WHEN 'completed' THEN new_status = 'obsolete'
      WHEN 'terminated' THEN new_status = 'obsolete'
      ELSE false
    END
    WHEN 'insight' THEN CASE old_status
      WHEN 'proposed' THEN new_status IN ('accepted','obsolete')
      WHEN 'accepted' THEN new_status IN ('stale','superseded','obsolete')
      WHEN 'stale' THEN new_status IN ('accepted','superseded','obsolete')
      WHEN 'superseded' THEN new_status = 'obsolete'
      ELSE false
    END
    ELSE false
  END;
$$;

CREATE OR REPLACE FUNCTION research_inquiry_status_guard_fn()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF NOT research_inquiry_transition_allowed(TG_ARGV[0], OLD.status, NEW.status) THEN
    RAISE check_violation USING CONSTRAINT = 'research_inquiry_status_transition_guard';
  END IF;
  IF TG_ARGV[0] = 'branch'
     AND NEW.status = 'terminated'
     AND btrim(COALESCE(to_jsonb(NEW)->>'termination_reason', '')) = '' THEN
    RAISE check_violation USING CONSTRAINT = 'research_branch_termination_reason_guard';
  END IF;
  RETURN NEW;
END;
$$;

CREATE TRIGGER research_hypothesis_status_guard
BEFORE UPDATE OF status ON research_hypothesis
FOR EACH ROW EXECUTE FUNCTION research_inquiry_status_guard_fn('hypothesis');
CREATE TRIGGER research_branch_status_guard
BEFORE UPDATE OF status ON research_branch
FOR EACH ROW EXECUTE FUNCTION research_inquiry_status_guard_fn('branch');
CREATE TRIGGER research_insight_status_guard
BEFORE UPDATE OF status ON research_insight
FOR EACH ROW EXECUTE FUNCTION research_inquiry_status_guard_fn('insight');

CREATE OR REPLACE FUNCTION research_inquiry_entity_exists(
  p_workspace_id UUID, p_session_id UUID, p_kind TEXT, p_id UUID
) RETURNS BOOLEAN LANGUAGE plpgsql STABLE AS $$
BEGIN
  CASE p_kind
    WHEN 'question' THEN RETURN EXISTS (SELECT 1 FROM research_question WHERE workspace_id=p_workspace_id AND session_id=p_session_id AND id=p_id);
    WHEN 'hypothesis' THEN RETURN EXISTS (SELECT 1 FROM research_hypothesis WHERE workspace_id=p_workspace_id AND session_id=p_session_id AND id=p_id);
    WHEN 'branch' THEN RETURN EXISTS (SELECT 1 FROM research_branch WHERE workspace_id=p_workspace_id AND session_id=p_session_id AND id=p_id);
    WHEN 'claim' THEN RETURN EXISTS (SELECT 1 FROM research_claim WHERE workspace_id=p_workspace_id AND session_id=p_session_id AND id=p_id);
    WHEN 'insight' THEN RETURN EXISTS (SELECT 1 FROM research_insight WHERE workspace_id=p_workspace_id AND session_id=p_session_id AND id=p_id);
    WHEN 'inquiry_edge' THEN RETURN EXISTS (SELECT 1 FROM research_inquiry_edge WHERE workspace_id=p_workspace_id AND session_id=p_session_id AND id=p_id);
    -- Dispute is reserved by the V6 vocabulary but is not writable until H creates it.
    WHEN 'dispute' THEN RETURN false;
    ELSE RETURN false;
  END CASE;
END;
$$;

CREATE OR REPLACE FUNCTION research_inquiry_edge_guard_fn()
RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
  creates_cycle BOOLEAN;
BEGIN
  IF NOT research_inquiry_entity_exists(NEW.workspace_id, NEW.session_id, NEW.from_kind, NEW.from_entity_id)
     OR NOT research_inquiry_entity_exists(NEW.workspace_id, NEW.session_id, NEW.to_kind, NEW.to_entity_id) THEN
    RAISE foreign_key_violation USING CONSTRAINT = 'research_inquiry_edge_endpoint_guard';
  END IF;

  IF NEW.relation IN ('decomposes','depends_on','refines') THEN
    WITH RECURSIVE reachable(kind, entity_id) AS (
      SELECT NEW.to_kind, NEW.to_entity_id
      UNION
      SELECT edge.to_kind, edge.to_entity_id
      FROM research_inquiry_edge edge
      JOIN reachable current
        ON edge.from_kind = current.kind AND edge.from_entity_id = current.entity_id
      WHERE edge.workspace_id = NEW.workspace_id
        AND edge.session_id = NEW.session_id
        AND edge.relation IN ('decomposes','depends_on','refines')
        AND (TG_OP = 'INSERT' OR edge.id <> NEW.id)
    )
    SELECT EXISTS (
      SELECT 1 FROM reachable
      WHERE kind = NEW.from_kind AND entity_id = NEW.from_entity_id
    ) INTO creates_cycle;
    IF creates_cycle THEN
      RAISE check_violation USING CONSTRAINT = 'research_inquiry_edge_acyclic_guard';
    END IF;
  END IF;
  RETURN NEW;
END;
$$;

CREATE TRIGGER research_inquiry_edge_guard
BEFORE INSERT OR UPDATE OF from_kind, from_entity_id, to_kind, to_entity_id, relation
ON research_inquiry_edge
FOR EACH ROW EXECUTE FUNCTION research_inquiry_edge_guard_fn();

-- Extend the D passport class guard without duplicating its legacy case table.
-- Legacy kinds keep the existing guard; Inquiry kinds use the typed resolver above.
DROP TRIGGER research_artifact_passport_class_guard ON research_artifact_passport;
CREATE OR REPLACE FUNCTION research_inquiry_passport_class_guard_fn()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF NOT research_inquiry_entity_exists(NEW.workspace_id, NEW.session_id, NEW.entity_kind, NEW.id) THEN
    RAISE foreign_key_violation USING CONSTRAINT = 'research_artifact_passport_class_guard';
  END IF;
  RETURN NEW;
END;
$$;
CREATE CONSTRAINT TRIGGER research_artifact_passport_class_guard
AFTER INSERT OR UPDATE OF workspace_id, session_id, entity_kind ON research_artifact_passport
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW
WHEN (NEW.entity_kind NOT IN ('hypothesis','branch','insight','inquiry_edge'))
EXECUTE FUNCTION research_artifact_passport_class_guard_fn();
CREATE CONSTRAINT TRIGGER research_inquiry_passport_class_guard
AFTER INSERT OR UPDATE OF workspace_id, session_id, entity_kind ON research_artifact_passport
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW
WHEN (NEW.entity_kind IN ('hypothesis','branch','insight','inquiry_edge'))
EXECUTE FUNCTION research_inquiry_passport_class_guard_fn();

-- Rows could have been created after 348 by migration tooling. Register them
-- before enforcing the reciprocal domain trigger; do not invent V6 semantics.
SELECT research_artifact_backfill_registered(workspace_id,session_id,id,'hypothesis',created_at,NULL,NULL)
FROM research_hypothesis;
SELECT research_artifact_backfill_registered(workspace_id,session_id,id,'branch',created_at,NULL,NULL)
FROM research_branch;
SELECT research_artifact_backfill_registered(workspace_id,session_id,id,'insight',created_at,NULL,NULL)
FROM research_insight;
SELECT research_artifact_backfill_registered(workspace_id,session_id,id,'inquiry_edge',created_at,NULL,NULL)
FROM research_inquiry_edge;

CREATE CONSTRAINT TRIGGER research_hypothesis_artifact_passport_guard
AFTER INSERT OR UPDATE OF id,workspace_id,session_id ON research_hypothesis
DEFERRABLE INITIALLY DEFERRED FOR EACH ROW
EXECUTE FUNCTION research_artifact_domain_passport_guard_fn('hypothesis');
CREATE CONSTRAINT TRIGGER research_branch_artifact_passport_guard
AFTER INSERT OR UPDATE OF id,workspace_id,session_id ON research_branch
DEFERRABLE INITIALLY DEFERRED FOR EACH ROW
EXECUTE FUNCTION research_artifact_domain_passport_guard_fn('branch');
CREATE CONSTRAINT TRIGGER research_insight_artifact_passport_guard
AFTER INSERT OR UPDATE OF id,workspace_id,session_id ON research_insight
DEFERRABLE INITIALLY DEFERRED FOR EACH ROW
EXECUTE FUNCTION research_artifact_domain_passport_guard_fn('insight');
CREATE CONSTRAINT TRIGGER research_inquiry_edge_artifact_passport_guard
AFTER INSERT OR UPDATE OF id,workspace_id,session_id ON research_inquiry_edge
DEFERRABLE INITIALLY DEFERRED FOR EACH ROW
EXECUTE FUNCTION research_artifact_domain_passport_guard_fn('inquiry_edge');

CREATE TRIGGER research_hypothesis_artifact_passport_delete_guard BEFORE DELETE ON research_hypothesis
FOR EACH ROW EXECUTE FUNCTION research_artifact_domain_passport_delete_guard_fn('hypothesis');
CREATE TRIGGER research_branch_artifact_passport_delete_guard BEFORE DELETE ON research_branch
FOR EACH ROW EXECUTE FUNCTION research_artifact_domain_passport_delete_guard_fn('branch');
CREATE TRIGGER research_insight_artifact_passport_delete_guard BEFORE DELETE ON research_insight
FOR EACH ROW EXECUTE FUNCTION research_artifact_domain_passport_delete_guard_fn('insight');
CREATE TRIGGER research_inquiry_edge_artifact_passport_delete_guard BEFORE DELETE ON research_inquiry_edge
FOR EACH ROW EXECUTE FUNCTION research_artifact_domain_passport_delete_guard_fn('inquiry_edge');

ALTER TABLE research_branch
  ADD CONSTRAINT research_branch_termination_reason_check
  CHECK (status <> 'terminated' OR btrim(termination_reason) <> '') NOT VALID;
