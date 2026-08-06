-- PR-5 foundation: immutable collaboration/attention decision audit. This is
-- append-only evidence for Evolution Center and later teacher-data extraction.

CREATE TABLE channel_decision_audit (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
  channel_id UUID REFERENCES channel(id) ON DELETE SET NULL,
  source_kind TEXT NOT NULL
    CHECK (source_kind IN ('attention_round', 'attention_participant', 'response_grant', 'convergence_vote', 'collaboration_session', 'collaboration_turn', 'agent_transport')),
  source_id UUID,
  event_type TEXT NOT NULL,
  agent_id UUID REFERENCES agent(id) ON DELETE SET NULL,
  message_id UUID REFERENCES channel_message(id) ON DELETE SET NULL,
  inbox_event_id UUID REFERENCES agent_inbox_event(id) ON DELETE SET NULL,
  payload JSONB NOT NULL DEFAULT '{}'::jsonb
    CHECK (jsonb_typeof(payload) = 'object'),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_channel_decision_audit_workspace_created
  ON channel_decision_audit(workspace_id, created_at DESC, id DESC);

CREATE INDEX idx_channel_decision_audit_channel_created
  ON channel_decision_audit(channel_id, created_at DESC, id DESC)
  WHERE channel_id IS NOT NULL;

CREATE INDEX idx_channel_decision_audit_source
  ON channel_decision_audit(source_kind, source_id, created_at DESC)
  WHERE source_id IS NOT NULL;
