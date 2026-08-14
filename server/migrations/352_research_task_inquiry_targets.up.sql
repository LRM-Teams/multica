-- Chapter E3b: durable Task -> Inquiry Graph targeting.
-- The binding is execution input, not a standalone artifact; Task and target
-- identities remain authoritative through their own Artifact Passports.

CREATE TABLE research_task_inquiry_target (
  workspace_id UUID NOT NULL,
  session_id UUID NOT NULL,
  task_id UUID NOT NULL,
  target_kind TEXT NOT NULL,
  target_entity_id UUID NOT NULL,
  ordinal INTEGER NOT NULL CHECK (ordinal >= 0),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (workspace_id, session_id, task_id, ordinal),
  CONSTRAINT research_task_inquiry_target_kind_check CHECK (
    target_kind IN ('question','hypothesis','branch','claim','insight','dispute')
  ),
  CONSTRAINT research_task_inquiry_target_task_fk
    FOREIGN KEY (workspace_id,session_id,task_id)
    REFERENCES research_task(workspace_id,session_id,id) ON DELETE CASCADE,
  CONSTRAINT research_task_inquiry_target_session_fk
    FOREIGN KEY (workspace_id,session_id)
    REFERENCES research_session(workspace_id,id) ON DELETE CASCADE,
  CONSTRAINT research_task_inquiry_target_identity_uidx
    UNIQUE (workspace_id,session_id,task_id,target_kind,target_entity_id)
);

CREATE INDEX research_task_inquiry_target_reverse_idx
  ON research_task_inquiry_target(workspace_id,session_id,target_kind,target_entity_id,task_id);

CREATE OR REPLACE FUNCTION research_task_inquiry_target_guard_fn()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF NOT research_inquiry_entity_exists(
    NEW.workspace_id, NEW.session_id, NEW.target_kind, NEW.target_entity_id
  ) THEN
    RAISE foreign_key_violation
      USING CONSTRAINT = 'research_task_inquiry_target_endpoint_guard';
  END IF;
  RETURN NEW;
END;
$$;

CREATE TRIGGER research_task_inquiry_target_guard
BEFORE INSERT OR UPDATE OF workspace_id,session_id,target_kind,target_entity_id
ON research_task_inquiry_target
FOR EACH ROW EXECUTE FUNCTION research_task_inquiry_target_guard_fn();
