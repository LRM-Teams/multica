-- Best-effort rollback: restores the Channel Attention Round schema shape.
-- Historical attention inbox events and decision-audit rows deleted by the
-- up migration cannot be recovered.

ALTER TABLE channel_decision_audit
  DROP CONSTRAINT IF EXISTS channel_decision_audit_source_kind_check;

ALTER TABLE channel_decision_audit
  ADD CONSTRAINT channel_decision_audit_source_kind_check
    CHECK (source_kind IN ('attention_round', 'attention_participant', 'response_grant', 'convergence_vote', 'collaboration_session', 'collaboration_turn', 'agent_transport'));

ALTER TABLE agent_inbox_event
  DROP CONSTRAINT IF EXISTS agent_inbox_event_reason_check;

ALTER TABLE agent_inbox_event
  ADD CONSTRAINT agent_inbox_event_reason_check
  CHECK (reason IN (
    'mention',
    'dm',
    'ambient',
    'thread_reply',
    'channel_message',
    'attention_response_grant',
    'attention_convergence',
    'attention_manager_fallback',
    'collaboration_turn'
  ));

ALTER TABLE agent_inbox_event
  DROP CONSTRAINT IF EXISTS agent_inbox_event_response_mode_check;

ALTER TABLE agent_inbox_event
  ADD CONSTRAINT agent_inbox_event_response_mode_check
    CHECK (response_mode IN ('no_public_output', 'contribution_offer', 'convergence_vote', 'public_response'));

ALTER TABLE agent_inbox_event
  DROP CONSTRAINT IF EXISTS agent_inbox_event_delivery_mode_check;

ALTER TABLE agent_inbox_event
  ADD CONSTRAINT agent_inbox_event_delivery_mode_check
    CHECK (delivery_mode IN ('observe', 'attention', 'execute'));

CREATE TABLE IF NOT EXISTS channel_attention_round (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
  channel_id UUID NOT NULL REFERENCES channel(id) ON DELETE CASCADE,
  trigger_message_id UUID REFERENCES channel_message(id) ON DELETE SET NULL,
  seq_from BIGINT NOT NULL,
  seq_to BIGINT NOT NULL,
  status TEXT NOT NULL DEFAULT 'collecting'
    CHECK (status IN ('collecting', 'resolving', 'resolved', 'timed_out', 'failed')),
  expected_agent_count INT NOT NULL DEFAULT 0,
  completed_agent_count INT NOT NULL DEFAULT 0,
  dispatch_at TIMESTAMPTZ NOT NULL,
  deadline_at TIMESTAMPTZ NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  resolved_at TIMESTAMPTZ,
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  CHECK (seq_from > 0),
  CHECK (seq_to >= seq_from),
  CHECK (expected_agent_count >= 0),
  CHECK (completed_agent_count >= 0),
  CHECK (completed_agent_count <= expected_agent_count),
  CHECK (deadline_at > created_at),
  CHECK (dispatch_at >= created_at),
  CHECK (dispatch_at < deadline_at)
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_channel_attention_round_collecting_channel
  ON channel_attention_round(channel_id)
  WHERE status = 'collecting';

CREATE INDEX IF NOT EXISTS idx_channel_attention_round_deadline
  ON channel_attention_round(deadline_at, id)
  WHERE status IN ('collecting', 'resolving');

CREATE TABLE IF NOT EXISTS channel_attention_participant (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  round_id UUID NOT NULL REFERENCES channel_attention_round(id) ON DELETE CASCADE,
  agent_id UUID NOT NULL REFERENCES agent(id) ON DELETE CASCADE,
  inbox_event_id UUID UNIQUE REFERENCES agent_inbox_event(id) ON DELETE SET NULL,
  execution_id UUID REFERENCES agent_execution(id) ON DELETE SET NULL,
  status TEXT NOT NULL DEFAULT 'pending'
    CHECK (status IN ('pending', 'running', 'decided', 'failed', 'timed_out', 'unavailable')),
  decision TEXT
    CHECK (decision IS NULL OR decision IN ('SILENT', 'ANSWER', 'CONTRIBUTE', 'COORDINATE')),
  confidence DOUBLE PRECISION
    CHECK (confidence IS NULL OR (confidence >= 0 AND confidence <= 1)),
  value_type TEXT
    CHECK (value_type IS NULL OR value_type IN ('none', 'direct_answer', 'unique_evidence', 'correction', 'task_claim', 'needs_protocol')),
  summary TEXT NOT NULL DEFAULT '',
  evidence_refs JSONB NOT NULL DEFAULT '[]'::jsonb
    CHECK (jsonb_typeof(evidence_refs) = 'array'),
  seen_up_to_seq BIGINT,
  input_tokens BIGINT NOT NULL DEFAULT 0,
  output_tokens BIGINT NOT NULL DEFAULT 0,
  model_version TEXT,
  latency_ms BIGINT,
  last_error TEXT,
  started_at TIMESTAMPTZ,
  completed_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (round_id, agent_id),
  CHECK (seen_up_to_seq IS NULL OR seen_up_to_seq >= 0),
  CHECK (input_tokens >= 0),
  CHECK (output_tokens >= 0),
  CHECK (latency_ms IS NULL OR latency_ms >= 0),
  CHECK ((status = 'decided') = (decision IS NOT NULL))
);

CREATE INDEX IF NOT EXISTS idx_channel_attention_participant_round_status
  ON channel_attention_participant(round_id, status, agent_id);

CREATE INDEX IF NOT EXISTS idx_channel_attention_participant_pending_event
  ON channel_attention_participant(inbox_event_id)
  WHERE status IN ('pending', 'running');

CREATE TABLE IF NOT EXISTS channel_attention_dispatch_outbox (
  message_id UUID PRIMARY KEY REFERENCES channel_message(id) ON DELETE CASCADE,
  workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
  channel_id UUID NOT NULL REFERENCES channel(id) ON DELETE CASCADE,
  initiator_user_id UUID REFERENCES "user"(id) ON DELETE SET NULL,
  attempt INT NOT NULL DEFAULT 0,
  last_error TEXT,
  next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  CHECK (attempt >= 0)
);

CREATE INDEX IF NOT EXISTS idx_channel_attention_dispatch_outbox_ready
  ON channel_attention_dispatch_outbox(next_attempt_at, created_at, message_id);

CREATE TABLE IF NOT EXISTS channel_attention_contribution_offer (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  round_id UUID NOT NULL REFERENCES channel_attention_round(id) ON DELETE CASCADE,
  participant_id UUID REFERENCES channel_attention_participant(id) ON DELETE SET NULL,
  agent_id UUID NOT NULL REFERENCES agent(id) ON DELETE CASCADE,
  offer_source TEXT NOT NULL DEFAULT 'attention_decision'
    CHECK (offer_source IN ('attention_decision', 'convergence_merge', 'late_contribution')),
  value_type TEXT NOT NULL
    CHECK (value_type IN ('unique_evidence', 'correction', 'task_claim', 'direct_answer', 'needs_protocol')),
  summary TEXT NOT NULL DEFAULT '',
  evidence_refs JSONB NOT NULL DEFAULT '[]'::jsonb
    CHECK (jsonb_typeof(evidence_refs) = 'array'),
  status TEXT NOT NULL DEFAULT 'pending'
    CHECK (status IN ('pending', 'merged', 'ignored', 'escalated')),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (round_id, agent_id, offer_source)
);

CREATE INDEX IF NOT EXISTS idx_channel_attention_contribution_offer_round
  ON channel_attention_contribution_offer(round_id, status, created_at);

CREATE TABLE IF NOT EXISTS channel_attention_convergence_vote (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  round_id UUID NOT NULL REFERENCES channel_attention_round(id) ON DELETE CASCADE,
  participant_id UUID REFERENCES channel_attention_participant(id) ON DELETE SET NULL,
  agent_id UUID NOT NULL REFERENCES agent(id) ON DELETE CASCADE,
  inbox_event_id UUID UNIQUE REFERENCES agent_inbox_event(id) ON DELETE SET NULL,
  status TEXT NOT NULL DEFAULT 'pending'
    CHECK (status IN ('pending', 'completed', 'failed')),
  vote TEXT
    CHECK (vote IS NULL OR vote IN ('YIELD', 'KEEP', 'MERGE', 'REQUEST_MANAGER')),
  target_agent_id UUID REFERENCES agent(id) ON DELETE SET NULL,
  summary TEXT NOT NULL DEFAULT '',
  input_tokens BIGINT NOT NULL DEFAULT 0,
  output_tokens BIGINT NOT NULL DEFAULT 0,
  model_version TEXT,
  last_error TEXT,
  completed_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (round_id, agent_id),
  CHECK ((status = 'completed') = (vote IS NOT NULL)),
  CHECK (input_tokens >= 0),
  CHECK (output_tokens >= 0)
);

CREATE INDEX IF NOT EXISTS idx_channel_attention_convergence_vote_round
  ON channel_attention_convergence_vote(round_id, status, agent_id);

CREATE TABLE IF NOT EXISTS channel_attention_response_grant (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  round_id UUID NOT NULL REFERENCES channel_attention_round(id) ON DELETE CASCADE,
  agent_id UUID NOT NULL REFERENCES agent(id) ON DELETE CASCADE,
  inbox_event_id UUID UNIQUE REFERENCES agent_inbox_event(id) ON DELETE SET NULL,
  grant_type TEXT NOT NULL
    CHECK (grant_type IN ('unique_answer', 'converged', 'manager_fallback')),
  status TEXT NOT NULL DEFAULT 'granted'
    CHECK (status IN ('granted', 'consumed', 'expired', 'revoked')),
  reason TEXT NOT NULL DEFAULT '',
  expires_at TIMESTAMPTZ NOT NULL DEFAULT now() + interval '10 minutes',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (round_id),
  CHECK (expires_at > created_at)
);

CREATE INDEX IF NOT EXISTS idx_channel_attention_response_grant_agent
  ON channel_attention_response_grant(agent_id, status, expires_at);
