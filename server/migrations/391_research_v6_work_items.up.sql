ALTER TABLE research_work_item DROP CONSTRAINT research_work_item_kind_check;
ALTER TABLE research_work_item ADD CONSTRAINT research_work_item_kind_check CHECK (kind IN (
  'expand_subquestion','evidence_gap','resolve_conflict','advance_probe',
  'research','match','discussion','integration','director','report','review'
));
ALTER TABLE research_work_item DROP CONSTRAINT research_work_item_status_check;
ALTER TABLE research_work_item ADD CONSTRAINT research_work_item_status_check CHECK (status IN (
  'pending','enqueued','done','ready','dispatching','running','awaiting_input','succeeded','failed','cancelled','stale'
));
ALTER TABLE research_work_item
  ADD COLUMN target_kind TEXT NOT NULL DEFAULT '',
  ADD COLUMN target_id UUID,
  ADD COLUMN client_key TEXT NOT NULL DEFAULT '',
  ADD COLUMN idempotency_key TEXT NOT NULL DEFAULT '',
  ADD COLUMN goal_version INTEGER,
  ADD COLUMN input_state_version BIGINT NOT NULL DEFAULT 0,
  ADD COLUMN input_event_sequence BIGINT NOT NULL DEFAULT 0,
  ADD COLUMN created_by_director_cycle_id UUID,
  ADD COLUMN assigned_agent_id UUID,
  ADD COLUMN priority DOUBLE PRECISION NOT NULL DEFAULT 0.5,
  ADD COLUMN max_attempts INTEGER NOT NULL DEFAULT 3,
  ADD COLUMN attempt_count INTEGER NOT NULL DEFAULT 0,
  ADD COLUMN lease_token UUID,
  ADD COLUMN lease_expires_at TIMESTAMPTZ,
  ADD COLUMN payload_schema_id TEXT NOT NULL DEFAULT '',
  ADD COLUMN terminal_reason_code TEXT NOT NULL DEFAULT '',
  ADD COLUMN terminal_reason_detail TEXT NOT NULL DEFAULT '',
  ADD COLUMN ready_at TIMESTAMPTZ,
  ADD COLUMN started_at TIMESTAMPTZ,
  ADD COLUMN cancelled_at TIMESTAMPTZ;

ALTER TABLE research_work_item ADD CONSTRAINT research_v6_work_item_values_check CHECK (
  priority BETWEEN 0 AND 1 AND max_attempts BETWEEN 1 AND 100 AND attempt_count BETWEEN 0 AND max_attempts
  AND input_state_version >= 0 AND input_event_sequence >= 0
);
ALTER TABLE research_work_item ADD CONSTRAINT research_v6_work_item_cycle_fk
  FOREIGN KEY (workspace_id,session_id,created_by_director_cycle_id)
  REFERENCES research_director_cycle(workspace_id,session_id,id);
CREATE UNIQUE INDEX research_v6_work_item_idempotency_idx
  ON research_work_item(session_id,goal_version,idempotency_key)
  WHERE goal_version IS NOT NULL AND idempotency_key<>'';
CREATE INDEX research_v6_work_item_claim_idx
  ON research_work_item(status,lease_expires_at,priority DESC,created_at,id)
  WHERE status IN ('ready','dispatching','running');

CREATE FUNCTION research_v6_work_item_assignee_sync_fn() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF NEW.assigned_agent_id IS NULL THEN NEW.assigned_agent_id := NEW.assignee_agent_id; END IF;
  IF NEW.assignee_agent_id IS NULL THEN NEW.assignee_agent_id := NEW.assigned_agent_id; END IF;
  IF NEW.assigned_agent_id IS DISTINCT FROM NEW.assignee_agent_id THEN
    RAISE EXCEPTION 'research work item assignee columns disagree' USING ERRCODE='23514';
  END IF;
  RETURN NEW;
END;
$$;
CREATE TRIGGER research_v6_work_item_assignee_sync BEFORE INSERT OR UPDATE OF assignee_agent_id,assigned_agent_id
  ON research_work_item FOR EACH ROW EXECUTE FUNCTION research_v6_work_item_assignee_sync_fn();
UPDATE research_work_item SET assigned_agent_id=assignee_agent_id WHERE assignee_agent_id IS NOT NULL;

CREATE TABLE research_work_item_attempt (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(), workspace_id UUID NOT NULL, session_id UUID NOT NULL,
  work_item_id UUID NOT NULL, attempt_number INTEGER NOT NULL CHECK (attempt_number >= 1),
  assigned_agent_id UUID NOT NULL, membership_id UUID NOT NULL,
  inbox_task_id UUID, dispatch_key TEXT NOT NULL,
  manifest_id UUID NOT NULL, manifest_hash TEXT NOT NULL CHECK (manifest_hash ~ '^sha256:[0-9a-f]{64}$'),
  status TEXT NOT NULL CHECK (status IN ('dispatching','running','succeeded','failed','cancelled','lost')),
  result_kind TEXT NOT NULL DEFAULT '', result_entity_id UUID, result_artifact_id UUID,
  result_hash TEXT, client_request_id UUID,
  failure_class TEXT NOT NULL DEFAULT '', diagnostics TEXT NOT NULL DEFAULT '',
  dispatched_at TIMESTAMPTZ NOT NULL DEFAULT now(), started_at TIMESTAMPTZ,
  result_submitted_at TIMESTAMPTZ, completed_at TIMESTAMPTZ,
  cancellation_completed_at TIMESTAMPTZ, created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(), UNIQUE (workspace_id,session_id,id),
  UNIQUE (work_item_id,attempt_number), UNIQUE (dispatch_key), UNIQUE (inbox_task_id), UNIQUE (client_request_id),
  CONSTRAINT research_v6_attempt_work_item_fk FOREIGN KEY (work_item_id) REFERENCES research_work_item(id) ON DELETE CASCADE,
  CONSTRAINT research_v6_attempt_membership_fk FOREIGN KEY (workspace_id,session_id,membership_id)
    REFERENCES research_team_membership(workspace_id,session_id,id)
);
CREATE UNIQUE INDEX research_v6_attempt_one_active_idx ON research_work_item_attempt(work_item_id)
  WHERE status IN ('dispatching','running');
CREATE INDEX research_v6_attempt_inbox_idx ON research_work_item_attempt(inbox_task_id) WHERE inbox_task_id IS NOT NULL;

CREATE TABLE research_work_catalog_page (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(), workspace_id UUID NOT NULL, session_id UUID NOT NULL,
  work_item_attempt_id UUID NOT NULL, catalog_view TEXT NOT NULL CHECK (catalog_view IN ('same_tier','higher_candidates')),
  tier TEXT CHECK (tier IN ('S','M','L','XL','XXL')), branch_scope JSONB NOT NULL DEFAULT '[]'::jsonb,
  through_event_sequence BIGINT NOT NULL CHECK (through_event_sequence >= 0),
  page_key TEXT NOT NULL, ordinal INTEGER NOT NULL CHECK (ordinal >= 0),
  content_hash TEXT NOT NULL CHECK (content_hash ~ '^sha256:[0-9a-f]{64}$'),
  next_cursor TEXT, reviewed_at TIMESTAMPTZ, created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (workspace_id,session_id,id), UNIQUE (work_item_attempt_id,catalog_view,through_event_sequence,page_key),
  CONSTRAINT research_v6_catalog_attempt_fk FOREIGN KEY (workspace_id,session_id,work_item_attempt_id)
    REFERENCES research_work_item_attempt(workspace_id,session_id,id) ON DELETE CASCADE
);

ALTER TABLE research_task DROP CONSTRAINT research_task_kind_check;
ALTER TABLE research_task ADD CONSTRAINT research_task_kind_check CHECK (kind IN (
  'plan','discover','deep_read','verify','counter_search','replan','synthesize','quality_gate','citation_audit','custom'
));
ALTER TABLE research_task
  ADD COLUMN task_type TEXT NOT NULL DEFAULT '',
  ADD COLUMN task_schema_id TEXT NOT NULL DEFAULT '',
  ADD COLUMN task_payload JSONB NOT NULL DEFAULT '{}'::jsonb,
  ADD COLUMN required_capabilities JSONB NOT NULL DEFAULT '[]'::jsonb,
  ADD COLUMN expected_result_schema_id TEXT NOT NULL DEFAULT '',
  ADD COLUMN work_item_id UUID REFERENCES research_work_item(id);

ALTER TABLE research_director_cycle ADD CONSTRAINT research_v6_cycle_work_item_fk
  FOREIGN KEY (work_item_id) REFERENCES research_work_item(id);
ALTER TABLE research_director_cycle ADD CONSTRAINT research_v6_cycle_attempt_fk
  FOREIGN KEY (workspace_id,session_id,work_item_attempt_id)
  REFERENCES research_work_item_attempt(workspace_id,session_id,id);

