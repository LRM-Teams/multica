ALTER TABLE research_session
  ALTER COLUMN fleet_id DROP NOT NULL,
  ADD COLUMN director_state_version BIGINT NOT NULL DEFAULT 0,
  ADD COLUMN current_director_assignment_id UUID,
  ADD COLUMN v6_projection_version BIGINT NOT NULL DEFAULT 0;

ALTER TABLE research_session DROP CONSTRAINT research_session_status_check;
ALTER TABLE research_session ADD CONSTRAINT research_session_status_check
  CHECK (status IN ('drafting','running','awaiting_user_confirm','awaiting_director','completed','archived','paused','failed','cancelled'));

CREATE TABLE research_director_assignment (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id UUID NOT NULL,
  session_id UUID NOT NULL,
  director_agent_id UUID NOT NULL,
  generation INTEGER NOT NULL CHECK (generation >= 1),
  status TEXT NOT NULL CHECK (status IN ('active','unavailable','replaced','ended')),
  assigned_by_user_id UUID NOT NULL REFERENCES "user"(id),
  reason TEXT NOT NULL CHECK (length(btrim(reason)) > 0),
  started_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  ended_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (workspace_id,session_id,id),
  UNIQUE (workspace_id,session_id,generation),
  CONSTRAINT research_v6_director_assignment_session_fk FOREIGN KEY (workspace_id,session_id)
    REFERENCES research_session(workspace_id,id) ON DELETE CASCADE,
  CHECK ((status IN ('active','unavailable') AND ended_at IS NULL) OR (status IN ('replaced','ended') AND ended_at IS NOT NULL))
);
CREATE UNIQUE INDEX research_v6_director_assignment_one_active_idx
  ON research_director_assignment(workspace_id,session_id) WHERE status='active';

ALTER TABLE research_session ADD CONSTRAINT research_session_current_v6_director_fk
  FOREIGN KEY (workspace_id,id,current_director_assignment_id)
  REFERENCES research_director_assignment(workspace_id,session_id,id)
  DEFERRABLE INITIALLY DEFERRED;

CREATE TABLE research_director_cycle (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(), workspace_id UUID NOT NULL, session_id UUID NOT NULL,
  director_assignment_id UUID NOT NULL, director_generation INTEGER NOT NULL,
  work_item_id UUID, work_item_attempt_id UUID,
  trigger_from_sequence BIGINT NOT NULL CHECK (trigger_from_sequence >= 0),
  trigger_through_sequence BIGINT NOT NULL CHECK (trigger_through_sequence >= trigger_from_sequence),
  brief_id UUID NOT NULL, brief_hash TEXT NOT NULL CHECK (brief_hash ~ '^sha256:[0-9a-f]{64}$'),
  model_session_ref TEXT NOT NULL DEFAULT '',
  status TEXT NOT NULL CHECK (status IN ('pending','running','applied','partially_rejected','failed','stale')),
  proposal JSONB, proposal_hash TEXT, execution_result JSONB NOT NULL DEFAULT '{}'::jsonb,
  reviewed_through_sequence BIGINT NOT NULL DEFAULT 0,
  failure_class TEXT NOT NULL DEFAULT '', diagnostics TEXT NOT NULL DEFAULT '',
  started_at TIMESTAMPTZ, completed_at TIMESTAMPTZ, created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (workspace_id,session_id,id),
  UNIQUE (session_id,director_assignment_id,trigger_through_sequence,brief_hash),
  CONSTRAINT research_v6_director_cycle_session_fk FOREIGN KEY (workspace_id,session_id)
    REFERENCES research_session(workspace_id,id) ON DELETE CASCADE,
  CONSTRAINT research_v6_director_cycle_assignment_fk FOREIGN KEY (workspace_id,session_id,director_assignment_id)
    REFERENCES research_director_assignment(workspace_id,session_id,id)
);

CREATE TABLE research_director_brief_page (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(), workspace_id UUID NOT NULL, session_id UUID NOT NULL,
  director_cycle_id UUID NOT NULL, page_kind TEXT NOT NULL CHECK (page_kind IN ('overview','research','control','terminal_summary')),
  brief_id UUID NOT NULL, brief_hash TEXT NOT NULL CHECK (brief_hash ~ '^sha256:[0-9a-f]{64}$'),
  page_key TEXT NOT NULL, ordinal INTEGER NOT NULL CHECK (ordinal >= 0),
  through_event_sequence BIGINT NOT NULL CHECK (through_event_sequence >= 0),
  content_bytes BYTEA NOT NULL, content_hash TEXT NOT NULL CHECK (content_hash ~ '^sha256:[0-9a-f]{64}$'),
  review_request_id UUID, reviewed_at TIMESTAMPTZ, inherited_review_from_page_id UUID,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(), UNIQUE (workspace_id,session_id,id),
  UNIQUE (director_cycle_id,page_key), UNIQUE (director_cycle_id,ordinal),
  CONSTRAINT research_v6_brief_page_cycle_fk FOREIGN KEY (workspace_id,session_id,director_cycle_id)
    REFERENCES research_director_cycle(workspace_id,session_id,id) ON DELETE CASCADE,
  CONSTRAINT research_v6_brief_page_inherited_fk FOREIGN KEY (workspace_id,session_id,inherited_review_from_page_id)
    REFERENCES research_director_brief_page(workspace_id,session_id,id)
);

CREATE TABLE research_steering_assessment (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(), workspace_id UUID NOT NULL, session_id UUID NOT NULL,
  research_message_id UUID NOT NULL, director_cycle_id UUID NOT NULL,
  goal_version_before INTEGER NOT NULL CHECK (goal_version_before >= 1),
  goal_version_after INTEGER NOT NULL CHECK (goal_version_after >= goal_version_before),
  selected_refs JSONB NOT NULL DEFAULT '[]'::jsonb, affected_refs JSONB NOT NULL DEFAULT '[]'::jsonb,
  assessment_kind TEXT NOT NULL CHECK (assessment_kind IN ('no_op','local_change','goal_revision','full_reassessment')),
  interpretation TEXT NOT NULL, reason TEXT NOT NULL, actions JSONB NOT NULL DEFAULT '[]'::jsonb,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(), UNIQUE (workspace_id,session_id,id),
  UNIQUE (workspace_id,session_id,research_message_id),
  CONSTRAINT research_v6_steering_session_fk FOREIGN KEY (workspace_id,session_id)
    REFERENCES research_session(workspace_id,id) ON DELETE CASCADE,
  CONSTRAINT research_v6_steering_cycle_fk FOREIGN KEY (workspace_id,session_id,director_cycle_id)
    REFERENCES research_director_cycle(workspace_id,session_id,id)
);

CREATE TABLE research_team_membership (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(), workspace_id UUID NOT NULL, session_id UUID NOT NULL,
  agent_id UUID NOT NULL, formation_decision_id UUID, director_cycle_id UUID,
  membership_generation INTEGER NOT NULL CHECK (membership_generation >= 1),
  mission_prompt TEXT NOT NULL, mission_hash TEXT NOT NULL CHECK (mission_hash ~ '^sha256:[0-9a-f]{64}$'),
  mission_revision INTEGER NOT NULL CHECK (mission_revision >= 1),
  model_config JSONB NOT NULL DEFAULT '{}'::jsonb, tool_config JSONB NOT NULL DEFAULT '{}'::jsonb,
  permission_config JSONB NOT NULL DEFAULT '{}'::jsonb,
  state TEXT NOT NULL CHECK (state IN ('idle','working','offline','retiring','archived','failed')),
  joined_at TIMESTAMPTZ NOT NULL DEFAULT now(), left_at TIMESTAMPTZ, terminal_reason TEXT NOT NULL DEFAULT '',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(), UNIQUE (workspace_id,session_id,id),
  UNIQUE (workspace_id,session_id,agent_id,membership_generation),
  CONSTRAINT research_v6_team_session_fk FOREIGN KEY (workspace_id,session_id)
    REFERENCES research_session(workspace_id,id) ON DELETE CASCADE,
  CONSTRAINT research_v6_team_cycle_fk FOREIGN KEY (workspace_id,session_id,director_cycle_id)
    REFERENCES research_director_cycle(workspace_id,session_id,id)
);
CREATE UNIQUE INDEX research_v6_team_one_active_agent_idx ON research_team_membership(workspace_id,session_id,agent_id)
  WHERE state IN ('idle','working','offline','retiring');

CREATE FUNCTION research_v6_team_cap_guard_fn() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE active_count INTEGER;
BEGIN
  IF NEW.state NOT IN ('idle','working','offline','retiring') THEN RETURN NEW; END IF;
  PERFORM 1 FROM research_session WHERE workspace_id=NEW.workspace_id AND id=NEW.session_id FOR UPDATE;
  SELECT count(*) INTO active_count FROM research_team_membership
    WHERE workspace_id=NEW.workspace_id AND session_id=NEW.session_id
      AND state IN ('idle','working','offline','retiring') AND id<>NEW.id;
  IF active_count >= 50 THEN RAISE EXCEPTION 'research V6 active team limit exceeded' USING ERRCODE='23514'; END IF;
  RETURN NEW;
END;
$$;
CREATE TRIGGER research_v6_team_cap_guard BEFORE INSERT OR UPDATE OF state ON research_team_membership
  FOR EACH ROW EXECUTE FUNCTION research_v6_team_cap_guard_fn();
