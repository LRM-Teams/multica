-- Allow start_work handoffs when a newly actionable issue should begin.
ALTER TABLE pending_handoff
  DROP CONSTRAINT IF EXISTS pending_handoff_reason_code_check;

ALTER TABLE pending_handoff
  ADD CONSTRAINT pending_handoff_reason_code_check
  CHECK (reason_code IN (
    'unlock',
    'block_route',
    'interrupt_stop',
    'stalled_ask_why',
    'progress_nudge',
    'start_work'
  ));

-- Per-channel ambient watch for Wendy: human chatter → debounce → review once.
CREATE TABLE wendy_channel_ambient (
  channel_id UUID PRIMARY KEY REFERENCES channel(id) ON DELETE CASCADE,
  workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
  wendy_agent_id UUID NOT NULL REFERENCES agent(id) ON DELETE CASCADE,
  last_human_message_at TIMESTAMPTZ NOT NULL,
  last_human_message_id UUID REFERENCES channel_message(id) ON DELETE SET NULL,
  last_reviewed_message_at TIMESTAMPTZ,
  review_not_before TIMESTAMPTZ NOT NULL,
  dirty BOOLEAN NOT NULL DEFAULT TRUE,
  status TEXT NOT NULL DEFAULT 'idle'
    CHECK (status IN ('idle', 'claimed', 'running')),
  claim_token UUID,
  claimed_at TIMESTAMPTZ,
  active_radar_run_id UUID,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX wendy_channel_ambient_due_idx
  ON wendy_channel_ambient (review_not_before, updated_at)
  WHERE dirty = TRUE AND status = 'idle';

CREATE INDEX wendy_channel_ambient_workspace_idx
  ON wendy_channel_ambient (workspace_id);
