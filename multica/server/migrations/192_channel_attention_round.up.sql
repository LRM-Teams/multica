-- PR-2: human-authored, unmentioned channel messages are collected through
-- restricted Attention Probe runs. These tables are mutable run state only;
-- append-only decision audit and response grants arrive in later PRs.

ALTER TABLE agent_inbox_event
  ADD COLUMN delivery_mode TEXT NOT NULL DEFAULT 'execute',
  ADD COLUMN response_mode TEXT NOT NULL DEFAULT 'public_response';

UPDATE agent_inbox_event
SET delivery_mode = CASE WHEN requires_wake THEN 'execute' ELSE 'observe' END,
    response_mode = CASE WHEN requires_wake THEN 'public_response' ELSE 'no_public_output' END;

ALTER TABLE agent_inbox_event
  ADD CONSTRAINT agent_inbox_event_delivery_mode_check
    CHECK (delivery_mode IN ('observe', 'attention', 'execute')),
  ADD CONSTRAINT agent_inbox_event_response_mode_check
    CHECK (response_mode IN ('no_public_output', 'contribution_offer', 'public_response'));

-- Observe delivery keeps its historical coalescing contract. Attention events
-- use the same ambient source reason but are unique per round/participant.
DROP INDEX IF EXISTS idx_agent_inbox_event_ambient_pending_unique;
CREATE UNIQUE INDEX idx_agent_inbox_event_ambient_pending_unique
  ON agent_inbox_event(conversation_id, agent_id)
  WHERE reason = 'ambient'
    AND delivery_mode = 'observe'
    AND status IN ('pending', 'failed')
    AND conversation_id IS NOT NULL;

CREATE TABLE channel_attention_round (
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

-- Channel dispatch takes an advisory lock before mutating this state. The
-- partial unique index is the final idempotency fence for concurrent handlers.
CREATE UNIQUE INDEX idx_channel_attention_round_collecting_channel
  ON channel_attention_round(channel_id)
  WHERE status = 'collecting';

CREATE INDEX idx_channel_attention_round_deadline
  ON channel_attention_round(deadline_at, id)
  WHERE status IN ('collecting', 'resolving');

CREATE TABLE channel_attention_participant (
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

CREATE INDEX idx_channel_attention_participant_round_status
  ON channel_attention_participant(round_id, status, agent_id);

CREATE INDEX idx_channel_attention_participant_pending_event
  ON channel_attention_participant(inbox_event_id)
  WHERE status IN ('pending', 'running');

-- Channel messages are committed before their realtime side effects. This
-- durable outbox closes that crash window: round creation is idempotent by
-- message seq, and the row is deleted only after the full fan-out commits.
CREATE TABLE channel_attention_dispatch_outbox (
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

CREATE INDEX idx_channel_attention_dispatch_outbox_ready
  ON channel_attention_dispatch_outbox(next_attempt_at, created_at, message_id);
