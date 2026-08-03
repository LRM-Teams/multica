-- Durable Research Run control, task/progress ledgers, and normalized evidence.

ALTER TABLE research_session
  ADD COLUMN goal_version INTEGER NOT NULL DEFAULT 1 CHECK (goal_version >= 1),
  ADD COLUMN plan_version INTEGER NOT NULL DEFAULT 1 CHECK (plan_version >= 1),
  ADD COLUMN state_version BIGINT NOT NULL DEFAULT 0 CHECK (state_version >= 0),
  ADD COLUMN orchestrator_version TEXT NOT NULL DEFAULT 'research-run-v1',
  ADD COLUMN run_config JSONB NOT NULL DEFAULT '{
    "max_tasks": 60,
    "max_parallel_tasks": 5,
    "max_attempts_per_task": 3,
    "max_snapshot_bytes": 65536,
    "max_result_bytes": 524288,
    "max_run_seconds": 28800,
    "task_timeout_seconds": 1800,
    "stale_after_seconds": 900,
    "marginal_gain_threshold": 0.03,
    "marginal_gain_rounds": 2
  }'::jsonb,
  ADD COLUMN run_stats JSONB NOT NULL DEFAULT '{}'::jsonb,
  ADD COLUMN run_initialized_at TIMESTAMPTZ,
  ADD COLUMN last_progress_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  ADD COLUMN next_reconcile_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  ADD COLUMN reconcile_lease_token UUID,
  ADD COLUMN reconcile_lease_expires_at TIMESTAMPTZ,
  ADD COLUMN stop_reason TEXT NOT NULL DEFAULT '',
  ADD COLUMN last_error TEXT NOT NULL DEFAULT '';

ALTER TABLE research_session
  DROP CONSTRAINT IF EXISTS research_session_status_check;
ALTER TABLE research_session
  ADD CONSTRAINT research_session_status_check
  CHECK (status IN (
    'drafting',
    'running',
    'awaiting_user_confirm',
    'completed',
    'archived',
    'paused',
    'failed',
    'cancelled'
  ));

CREATE INDEX research_session_reconcile_due_idx
  ON research_session (next_reconcile_at, id)
  WHERE status = 'running';
CREATE INDEX research_session_metrics_idx
  ON research_session (status, orchestrator_version, run_initialized_at);

ALTER TABLE research_report
  ADD COLUMN goal_version INTEGER NOT NULL DEFAULT 1 CHECK (goal_version >= 1),
  ADD COLUMN plan_version INTEGER NOT NULL DEFAULT 1 CHECK (plan_version >= 1);

CREATE INDEX research_report_run_version_idx
  ON research_report (session_id, goal_version, plan_version, revision DESC);

CREATE TABLE research_contract_revision (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
  session_id UUID NOT NULL REFERENCES research_session(id) ON DELETE CASCADE,
  goal_version INTEGER NOT NULL CHECK (goal_version >= 1),
  goal TEXT NOT NULL CHECK (length(btrim(goal)) > 0),
  scope JSONB NOT NULL DEFAULT '{}'::jsonb,
  audience TEXT NOT NULL DEFAULT '',
  freshness TEXT NOT NULL DEFAULT '',
  language TEXT NOT NULL DEFAULT '',
  source_policy JSONB NOT NULL DEFAULT '{}'::jsonb,
  run_limits JSONB NOT NULL DEFAULT '{}'::jsonb,
  authored_by UUID NOT NULL REFERENCES "user"(id),
  reason TEXT NOT NULL DEFAULT '',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (session_id, goal_version)
);

INSERT INTO research_contract_revision (
  workspace_id, session_id, goal_version, goal, authored_by, reason
)
SELECT workspace_id, id, 1, goal, created_by, 'legacy_session_backfill'
FROM research_session
ON CONFLICT (session_id, goal_version) DO NOTHING;

CREATE TABLE research_question (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
  session_id UUID NOT NULL REFERENCES research_session(id) ON DELETE CASCADE,
  parent_question_id UUID REFERENCES research_question(id) ON DELETE SET NULL,
  created_by_task_id UUID,
  client_key TEXT NOT NULL,
  kind TEXT NOT NULL CHECK (kind IN ('dimension', 'hypothesis', 'contradiction', 'gap', 'follow_up')),
  question TEXT NOT NULL CHECK (length(btrim(question)) > 0),
  required BOOLEAN NOT NULL DEFAULT false,
  status TEXT NOT NULL DEFAULT 'open'
    CHECK (status IN ('open', 'in_progress', 'answered', 'unresolved', 'obsolete')),
  priority DOUBLE PRECISION NOT NULL DEFAULT 0.5 CHECK (priority >= 0 AND priority <= 1),
  impact DOUBLE PRECISION NOT NULL DEFAULT 0.5 CHECK (impact >= 0 AND impact <= 1),
  uncertainty DOUBLE PRECISION NOT NULL DEFAULT 0.5 CHECK (uncertainty >= 0 AND uncertainty <= 1),
  novelty DOUBLE PRECISION NOT NULL DEFAULT 0.5 CHECK (novelty >= 0 AND novelty <= 1),
  coverage DOUBLE PRECISION NOT NULL DEFAULT 0 CHECK (coverage >= 0 AND coverage <= 1),
  goal_version INTEGER NOT NULL CHECK (goal_version >= 1),
  plan_version INTEGER NOT NULL CHECK (plan_version >= 1),
  answer_claim_id UUID,
  terminal_explanation TEXT NOT NULL DEFAULT '',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (session_id, goal_version, plan_version, client_key)
);

CREATE INDEX research_question_frontier_idx
  ON research_question (session_id, status, required DESC, priority DESC, created_at);
CREATE INDEX research_question_metrics_idx
  ON research_question (session_id, goal_version, plan_version, created_at)
  WHERE status IN ('open', 'in_progress', 'unresolved');

CREATE TABLE research_task (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
  session_id UUID NOT NULL REFERENCES research_session(id) ON DELETE CASCADE,
  question_id UUID REFERENCES research_question(id) ON DELETE SET NULL,
  parent_task_id UUID REFERENCES research_task(id) ON DELETE SET NULL,
  client_key TEXT NOT NULL,
  kind TEXT NOT NULL CHECK (kind IN (
    'plan', 'discover', 'deep_read', 'verify', 'counter_search',
    'replan', 'synthesize', 'quality_gate', 'citation_audit'
  )),
  objective TEXT NOT NULL CHECK (length(btrim(objective)) > 0),
  required_capability TEXT NOT NULL,
  expected_result TEXT NOT NULL,
  acceptance_criteria JSONB NOT NULL DEFAULT '{}'::jsonb,
  priority DOUBLE PRECISION NOT NULL DEFAULT 0.5 CHECK (priority >= 0 AND priority <= 1),
  status TEXT NOT NULL DEFAULT 'pending'
    CHECK (status IN (
      'pending', 'ready', 'dispatching', 'running', 'succeeded',
      'failed', 'blocked', 'obsolete', 'cancelled'
    )),
  assigned_agent_id UUID REFERENCES agent(id) ON DELETE SET NULL,
  goal_version INTEGER NOT NULL CHECK (goal_version >= 1),
  plan_version INTEGER NOT NULL CHECK (plan_version >= 1),
  max_attempts INTEGER NOT NULL DEFAULT 3 CHECK (max_attempts >= 1 AND max_attempts <= 10),
  timeout_seconds INTEGER NOT NULL DEFAULT 1800 CHECK (timeout_seconds >= 30 AND timeout_seconds <= 86400),
  ready_at TIMESTAMPTZ,
  started_at TIMESTAMPTZ,
  completed_at TIMESTAMPTZ,
  terminal_reason TEXT NOT NULL DEFAULT '',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (session_id, goal_version, plan_version, client_key)
);

ALTER TABLE research_question
  ADD CONSTRAINT research_question_created_by_task_fk
  FOREIGN KEY (created_by_task_id) REFERENCES research_task(id) ON DELETE SET NULL;

CREATE INDEX research_task_dispatch_idx
  ON research_task (session_id, status, priority DESC, ready_at, created_at);
CREATE INDEX research_task_assignee_idx
  ON research_task (assigned_agent_id, status)
  WHERE assigned_agent_id IS NOT NULL;
CREATE INDEX research_task_metrics_idx
  ON research_task (kind, status);

CREATE TABLE research_task_dependency (
  task_id UUID NOT NULL REFERENCES research_task(id) ON DELETE CASCADE,
  depends_on_task_id UUID NOT NULL REFERENCES research_task(id) ON DELETE CASCADE,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (task_id, depends_on_task_id),
  CHECK (task_id <> depends_on_task_id)
);

CREATE INDEX research_task_dependency_reverse_idx
  ON research_task_dependency (depends_on_task_id, task_id);

CREATE TABLE research_task_attempt (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
  session_id UUID NOT NULL REFERENCES research_session(id) ON DELETE CASCADE,
  task_id UUID NOT NULL REFERENCES research_task(id) ON DELETE CASCADE,
  attempt_number INTEGER NOT NULL CHECK (attempt_number >= 1),
  -- Immutable attribution, matching agent_execution: permanent Agent deletion
  -- must not erase or null the Research Attempt audit trail.
  assigned_agent_id UUID NOT NULL,
  inbox_task_id UUID REFERENCES agent_inbox_event(id) ON DELETE SET NULL,
  dispatch_key TEXT NOT NULL,
  client_request_id TEXT,
  status TEXT NOT NULL DEFAULT 'dispatching'
    CHECK (status IN ('dispatching', 'running', 'succeeded', 'failed', 'cancelled', 'lost')),
  result_hash TEXT,
  result JSONB,
  failure_class TEXT NOT NULL DEFAULT '',
  diagnostics TEXT NOT NULL DEFAULT '',
  dispatched_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  started_at TIMESTAMPTZ,
  result_submitted_at TIMESTAMPTZ,
  completed_at TIMESTAMPTZ,
  cancellation_completed_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (task_id, attempt_number),
  UNIQUE (dispatch_key),
  UNIQUE (client_request_id)
);

CREATE UNIQUE INDEX research_task_attempt_one_active_idx
  ON research_task_attempt (task_id)
  WHERE status IN ('dispatching', 'running');
CREATE INDEX research_task_attempt_inbox_idx
  ON research_task_attempt (inbox_task_id)
  WHERE inbox_task_id IS NOT NULL;
CREATE INDEX research_task_attempt_metrics_idx
  ON research_task_attempt (status, failure_class);
CREATE INDEX research_task_attempt_cancellation_pending_idx
  ON research_task_attempt (session_id, dispatched_at, id)
  WHERE status = 'cancelled' AND cancellation_completed_at IS NULL;

CREATE TABLE research_source_snapshot (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
  session_id UUID NOT NULL REFERENCES research_session(id) ON DELETE CASCADE,
  produced_by_task_id UUID REFERENCES research_task(id) ON DELETE SET NULL,
  canonical_url TEXT NOT NULL,
  title TEXT NOT NULL DEFAULT '',
  publisher TEXT NOT NULL DEFAULT '',
  source_class TEXT NOT NULL DEFAULT 'other',
  independence_key TEXT NOT NULL,
  retrieved_at TIMESTAMPTZ NOT NULL,
  snapshot_text TEXT NOT NULL DEFAULT '',
  content_hash TEXT NOT NULL,
  metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
  verification_status TEXT NOT NULL DEFAULT 'pending'
    CHECK (verification_status IN ('pending', 'verified', 'rejected')),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (session_id, canonical_url, content_hash)
);

CREATE INDEX research_source_snapshot_session_idx
  ON research_source_snapshot (session_id, created_at);
CREATE INDEX research_source_snapshot_independence_idx
  ON research_source_snapshot (session_id, independence_key);
CREATE INDEX research_source_snapshot_verification_idx
  ON research_source_snapshot (verification_status);

CREATE TABLE research_observation (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
  session_id UUID NOT NULL REFERENCES research_session(id) ON DELETE CASCADE,
  source_snapshot_id UUID NOT NULL REFERENCES research_source_snapshot(id) ON DELETE CASCADE,
  produced_by_task_id UUID REFERENCES research_task(id) ON DELETE SET NULL,
  quote TEXT NOT NULL DEFAULT '',
  datum JSONB NOT NULL DEFAULT '{}'::jsonb,
  locator TEXT NOT NULL DEFAULT '',
  interpretation TEXT NOT NULL DEFAULT '',
  content_hash TEXT NOT NULL,
  verification_status TEXT NOT NULL DEFAULT 'pending'
    CHECK (verification_status IN ('pending', 'verified', 'rejected')),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (session_id, source_snapshot_id, content_hash)
);

CREATE INDEX research_observation_source_idx
  ON research_observation (source_snapshot_id, created_at);
CREATE INDEX research_observation_verification_idx
  ON research_observation (verification_status);

CREATE TABLE research_claim (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
  session_id UUID NOT NULL REFERENCES research_session(id) ON DELETE CASCADE,
  produced_by_task_id UUID REFERENCES research_task(id) ON DELETE SET NULL,
  client_key TEXT NOT NULL,
  claim_text TEXT NOT NULL CHECK (length(btrim(claim_text)) > 0),
  significance TEXT NOT NULL DEFAULT 'medium'
    CHECK (significance IN ('low', 'medium', 'high', 'critical')),
  confidence DOUBLE PRECISION NOT NULL DEFAULT 0.5 CHECK (confidence >= 0 AND confidence <= 1),
  status TEXT NOT NULL DEFAULT 'proposed'
    CHECK (status IN ('proposed', 'supported', 'disputed', 'refuted', 'superseded', 'unresolved')),
  goal_version INTEGER NOT NULL CHECK (goal_version >= 1),
  plan_version INTEGER NOT NULL CHECK (plan_version >= 1),
  resolution TEXT NOT NULL DEFAULT '',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (session_id, goal_version, plan_version, client_key)
);

ALTER TABLE research_question
  ADD CONSTRAINT research_question_answer_claim_fk
  FOREIGN KEY (answer_claim_id) REFERENCES research_claim(id) ON DELETE SET NULL;

CREATE INDEX research_claim_gate_idx
  ON research_claim (session_id, status, significance, created_at);

CREATE TABLE research_claim_evidence (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
  session_id UUID NOT NULL REFERENCES research_session(id) ON DELETE CASCADE,
  claim_id UUID NOT NULL REFERENCES research_claim(id) ON DELETE CASCADE,
  observation_id UUID NOT NULL REFERENCES research_observation(id) ON DELETE CASCADE,
  relation TEXT NOT NULL CHECK (relation IN ('supports', 'contradicts')),
  strength DOUBLE PRECISION NOT NULL DEFAULT 0.5 CHECK (strength >= 0 AND strength <= 1),
  verification_status TEXT NOT NULL DEFAULT 'pending'
    CHECK (verification_status IN ('pending', 'verified', 'rejected')),
  verified_by_task_id UUID REFERENCES research_task(id) ON DELETE SET NULL,
  rationale TEXT NOT NULL DEFAULT '',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (claim_id, observation_id, relation)
);

CREATE INDEX research_claim_evidence_claim_idx
  ON research_claim_evidence (claim_id, relation, verification_status);
CREATE INDEX research_claim_evidence_verification_idx
  ON research_claim_evidence (verification_status);

CREATE TABLE research_decision (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
  session_id UUID NOT NULL REFERENCES research_session(id) ON DELETE CASCADE,
  decision_kind TEXT NOT NULL,
  actor_type TEXT NOT NULL CHECK (actor_type IN ('user', 'agent', 'system')),
  actor_id UUID,
  goal_version INTEGER NOT NULL CHECK (goal_version >= 1),
  plan_version INTEGER NOT NULL CHECK (plan_version >= 1),
  inputs JSONB NOT NULL DEFAULT '{}'::jsonb,
  outcome JSONB NOT NULL DEFAULT '{}'::jsonb,
  rationale TEXT NOT NULL DEFAULT '',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX research_decision_session_idx
  ON research_decision (session_id, created_at);

CREATE TABLE research_report_claim (
  report_id UUID NOT NULL REFERENCES research_report(id) ON DELETE CASCADE,
  claim_id UUID NOT NULL REFERENCES research_claim(id) ON DELETE CASCADE,
  section_id TEXT NOT NULL DEFAULT '',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (report_id, claim_id, section_id)
);

CREATE INDEX research_report_claim_claim_idx
  ON research_report_claim (claim_id, report_id);

CREATE TABLE research_run_event (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
  session_id UUID NOT NULL REFERENCES research_session(id) ON DELETE CASCADE,
  sequence BIGINT NOT NULL CHECK (sequence >= 1),
  event_type TEXT NOT NULL,
  idempotency_key TEXT NOT NULL,
  actor_type TEXT NOT NULL CHECK (actor_type IN ('user', 'agent', 'system')),
  actor_id UUID,
  payload JSONB NOT NULL DEFAULT '{}'::jsonb,
  projected_at TIMESTAMPTZ,
  projection_attempts INTEGER NOT NULL DEFAULT 0 CHECK (projection_attempts >= 0),
  projection_error TEXT NOT NULL DEFAULT '',
  next_projection_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (session_id, sequence),
  UNIQUE (session_id, idempotency_key)
);

CREATE INDEX research_run_event_session_idx
  ON research_run_event (session_id, sequence);
CREATE INDEX research_run_event_projection_due_idx
  ON research_run_event (next_projection_at, session_id, sequence)
  WHERE projected_at IS NULL;
CREATE INDEX research_run_event_projection_age_idx
  ON research_run_event (created_at)
  WHERE projected_at IS NULL;

ALTER TABLE research_graph_node
  ADD COLUMN run_event_id UUID REFERENCES research_run_event(id) ON DELETE SET NULL;
CREATE UNIQUE INDEX research_graph_node_run_event_idx
  ON research_graph_node (run_event_id)
  WHERE run_event_id IS NOT NULL;

ALTER TABLE research_message
  ADD COLUMN run_event_id UUID REFERENCES research_run_event(id) ON DELETE SET NULL;
CREATE UNIQUE INDEX research_message_run_event_idx
  ON research_message (run_event_id)
  WHERE run_event_id IS NOT NULL;

CREATE UNIQUE INDEX agent_inbox_event_research_dispatch_key_idx
  ON agent_inbox_event ((context->>'research_dispatch_key'))
  WHERE COALESCE(context->>'research_dispatch_key', '') <> '';

ALTER TABLE research_source
  ADD COLUMN source_snapshot_id UUID REFERENCES research_source_snapshot(id) ON DELETE SET NULL;
CREATE UNIQUE INDEX research_source_snapshot_projection_idx
  ON research_source (source_snapshot_id)
  WHERE source_snapshot_id IS NOT NULL;
