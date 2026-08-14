CREATE TABLE research_task_inquiry_target (
  workspace_id UUID NOT NULL,
  session_id UUID NOT NULL,
  task_id UUID NOT NULL,
  target_kind TEXT NOT NULL CHECK (target_kind IN ('question','hypothesis','branch','claim','insight','dispute')),
  target_entity_id UUID NOT NULL,
  goal_version INTEGER NOT NULL CHECK (goal_version >= 1),
  plan_version INTEGER NOT NULL CHECK (plan_version >= 1),
  bound_by_attempt_id UUID NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (workspace_id, session_id, task_id, target_kind, target_entity_id),
  CONSTRAINT research_task_inquiry_target_session_fk
    FOREIGN KEY (workspace_id, session_id)
    REFERENCES research_session(workspace_id, id) ON DELETE CASCADE,
  CONSTRAINT research_task_inquiry_target_task_fk
    FOREIGN KEY (workspace_id, session_id, task_id)
    REFERENCES research_task(workspace_id, session_id, id) ON DELETE CASCADE,
  CONSTRAINT research_task_inquiry_target_attempt_fk
    FOREIGN KEY (workspace_id, session_id, bound_by_attempt_id)
    REFERENCES research_task_attempt(workspace_id, session_id, id) ON DELETE CASCADE
);

CREATE OR REPLACE FUNCTION research_task_inquiry_target_guard() RETURNS trigger AS $$
BEGIN
  IF NOT (
    CASE WHEN NEW.target_kind='dispute' THEN EXISTS (
      SELECT 1 FROM research_dispute
      WHERE workspace_id=NEW.workspace_id AND session_id=NEW.session_id AND id=NEW.target_entity_id
    ) ELSE research_inquiry_entity_exists(NEW.workspace_id, NEW.session_id, NEW.target_kind, NEW.target_entity_id) END
  ) THEN
    RAISE EXCEPTION 'Task Inquiry target is outside the Research Run' USING ERRCODE = '23514';
  END IF;
  IF NOT EXISTS (
    SELECT 1 FROM research_task t
    WHERE t.workspace_id=NEW.workspace_id AND t.session_id=NEW.session_id AND t.id=NEW.task_id
      AND t.goal_version=NEW.goal_version AND t.plan_version=NEW.plan_version
  ) THEN
    RAISE EXCEPTION 'Task Inquiry target version does not match Task' USING ERRCODE = '23514';
  END IF;
  RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER research_task_inquiry_target_insert_guard
BEFORE INSERT ON research_task_inquiry_target
FOR EACH ROW EXECUTE FUNCTION research_task_inquiry_target_guard();

CREATE OR REPLACE FUNCTION research_task_inquiry_target_append_only() RETURNS trigger AS $$
BEGIN
  IF TG_OP='DELETE' AND pg_trigger_depth() > 1 THEN
    RETURN OLD;
  END IF;
  RAISE EXCEPTION 'Task Inquiry targets are append-only' USING ERRCODE = '23514';
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER research_task_inquiry_target_immutable
BEFORE UPDATE OR DELETE ON research_task_inquiry_target
FOR EACH ROW EXECUTE FUNCTION research_task_inquiry_target_append_only();

CREATE INDEX research_task_inquiry_target_branch_idx
  ON research_task_inquiry_target(workspace_id, session_id, target_entity_id, task_id)
  WHERE target_kind='branch';
