ALTER TABLE research_work_item ADD COLUMN expected_result_schema_id TEXT NOT NULL DEFAULT '';
UPDATE research_work_item SET expected_result_schema_id=payload_schema_id
WHERE payload_schema_id IN ('director_action_proposal','atomic_result_submission','discussion_turn_submission','integration_submission','report_package_submission');

CREATE TABLE research_v6_steering_trigger (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id UUID NOT NULL,
  session_id UUID NOT NULL,
  research_message_id UUID NOT NULL,
  client_request_id UUID NOT NULL,
  selected_refs JSONB NOT NULL DEFAULT '[]'::jsonb,
  event_sequence BIGINT NOT NULL CHECK (event_sequence >= 1),
  status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','processing','triggered','failed')),
  director_cycle_id UUID,
  delivery_attempts INTEGER NOT NULL DEFAULT 0 CHECK (delivery_attempts >= 0),
  lease_token UUID,
  lease_expires_at TIMESTAMPTZ,
  next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  last_error TEXT NOT NULL DEFAULT '',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (workspace_id,session_id,id),
  UNIQUE (workspace_id,session_id,research_message_id),
  UNIQUE (workspace_id,session_id,client_request_id),
  CONSTRAINT research_v6_steering_trigger_session_fk FOREIGN KEY (workspace_id,session_id)
    REFERENCES research_session(workspace_id,id) ON DELETE CASCADE,
  CONSTRAINT research_v6_steering_trigger_message_fk FOREIGN KEY (workspace_id,session_id,research_message_id)
    REFERENCES research_message(workspace_id,session_id,id) ON DELETE CASCADE,
  CONSTRAINT research_v6_steering_trigger_cycle_fk FOREIGN KEY (workspace_id,session_id,director_cycle_id)
    REFERENCES research_director_cycle(workspace_id,session_id,id)
);

CREATE INDEX research_v6_steering_trigger_due_idx ON research_v6_steering_trigger(next_attempt_at,created_at,id)
  WHERE status IN ('pending','processing');
