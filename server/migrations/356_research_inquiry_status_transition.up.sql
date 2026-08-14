-- Evidence-driven Inquiry lifecycle transitions are canonical facts, not
-- mutable status fields without provenance.

CREATE TABLE research_inquiry_status_transition (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id UUID NOT NULL,
  session_id UUID NOT NULL,
  target_kind TEXT NOT NULL CHECK (target_kind IN ('question','hypothesis','branch','insight')),
  target_entity_id UUID NOT NULL,
  before_status TEXT NOT NULL,
  after_status TEXT NOT NULL,
  reason TEXT NOT NULL CHECK (char_length(btrim(reason)) BETWEEN 1 AND 32768),
  goal_version INTEGER NOT NULL CHECK (goal_version >= 1),
  plan_version INTEGER NOT NULL CHECK (plan_version >= 1),
  produced_by_attempt_id UUID NOT NULL,
  event_sequence BIGINT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (workspace_id, session_id, id),
  UNIQUE (workspace_id, session_id, event_sequence),
  CONSTRAINT research_inquiry_status_transition_session_fk
    FOREIGN KEY (workspace_id,session_id) REFERENCES research_session(workspace_id,id) ON DELETE CASCADE,
  CONSTRAINT research_inquiry_status_transition_attempt_fk
    FOREIGN KEY (workspace_id,session_id,produced_by_attempt_id)
    REFERENCES research_task_attempt(workspace_id,session_id,id),
  CONSTRAINT research_inquiry_status_transition_event_fk
    FOREIGN KEY (session_id,event_sequence)
    REFERENCES research_run_event(session_id,sequence)
);

CREATE TABLE research_inquiry_status_evidence (
  workspace_id UUID NOT NULL,
  session_id UUID NOT NULL,
  transition_id UUID NOT NULL,
  ordinal INTEGER NOT NULL CHECK (ordinal >= 0 AND ordinal < 128),
  evidence_kind TEXT NOT NULL CHECK (evidence_kind IN ('question','hypothesis','branch','claim','insight','task','source')),
  evidence_id UUID NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (workspace_id,session_id,transition_id,ordinal),
  UNIQUE (workspace_id,session_id,transition_id,evidence_kind,evidence_id),
  CONSTRAINT research_inquiry_status_evidence_transition_fk
    FOREIGN KEY (workspace_id,session_id,transition_id)
    REFERENCES research_inquiry_status_transition(workspace_id,session_id,id) ON DELETE CASCADE
);

CREATE OR REPLACE FUNCTION research_inquiry_status_transition_target_guard() RETURNS trigger AS $$
BEGIN
  IF NOT research_inquiry_entity_exists(NEW.workspace_id,NEW.session_id,NEW.target_kind,NEW.target_entity_id) THEN
    RAISE foreign_key_violation USING CONSTRAINT = 'research_inquiry_status_transition_target_guard';
  END IF;
  RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER research_inquiry_status_transition_target_guard
BEFORE INSERT ON research_inquiry_status_transition
FOR EACH ROW EXECUTE FUNCTION research_inquiry_status_transition_target_guard();

CREATE OR REPLACE FUNCTION research_inquiry_status_evidence_guard() RETURNS trigger AS $$
DECLARE evidence_exists BOOLEAN;
BEGIN
  CASE NEW.evidence_kind
    WHEN 'task' THEN SELECT EXISTS (
      SELECT 1 FROM research_task WHERE workspace_id=NEW.workspace_id AND session_id=NEW.session_id AND id=NEW.evidence_id
    ) INTO evidence_exists;
    WHEN 'source' THEN SELECT EXISTS (
      SELECT 1 FROM research_source_snapshot WHERE workspace_id=NEW.workspace_id AND session_id=NEW.session_id AND id=NEW.evidence_id
    ) INTO evidence_exists;
    ELSE evidence_exists := research_inquiry_entity_exists(NEW.workspace_id,NEW.session_id,NEW.evidence_kind,NEW.evidence_id);
  END CASE;
  IF NOT evidence_exists THEN
    RAISE foreign_key_violation USING CONSTRAINT = 'research_inquiry_status_evidence_target_guard';
  END IF;
  RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER research_inquiry_status_evidence_target_guard
BEFORE INSERT ON research_inquiry_status_evidence
FOR EACH ROW EXECUTE FUNCTION research_inquiry_status_evidence_guard();

CREATE OR REPLACE FUNCTION research_inquiry_status_audit_append_only() RETURNS trigger AS $$
BEGIN
  IF TG_OP='DELETE' AND pg_trigger_depth() > 1 THEN RETURN OLD; END IF;
  RAISE check_violation USING CONSTRAINT = 'research_inquiry_status_audit_append_only';
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER research_inquiry_status_transition_immutable
BEFORE UPDATE OR DELETE ON research_inquiry_status_transition
FOR EACH ROW EXECUTE FUNCTION research_inquiry_status_audit_append_only();
CREATE TRIGGER research_inquiry_status_evidence_immutable
BEFORE UPDATE OR DELETE ON research_inquiry_status_evidence
FOR EACH ROW EXECUTE FUNCTION research_inquiry_status_audit_append_only();

-- Questions predate the Inquiry Graph tables; bring their lifecycle under the
-- same database-enforced transition vocabulary.
CREATE OR REPLACE FUNCTION research_inquiry_transition_allowed(
  entity_kind TEXT, old_status TEXT, new_status TEXT
) RETURNS BOOLEAN LANGUAGE sql IMMUTABLE AS $$
  SELECT old_status = new_status OR CASE entity_kind
    WHEN 'question' THEN CASE old_status
      WHEN 'open' THEN new_status IN ('in_progress','answered','unresolved','obsolete')
      WHEN 'in_progress' THEN new_status IN ('answered','unresolved','obsolete')
      WHEN 'answered' THEN new_status IN ('in_progress','unresolved','obsolete')
      WHEN 'unresolved' THEN new_status IN ('in_progress','answered','obsolete')
      ELSE false
    END
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

CREATE TRIGGER research_question_status_guard
BEFORE UPDATE OF status ON research_question
FOR EACH ROW EXECUTE FUNCTION research_inquiry_status_guard_fn('question');

CREATE INDEX research_inquiry_status_transition_target_idx
  ON research_inquiry_status_transition(workspace_id,session_id,target_kind,target_entity_id,created_at,id);
