ALTER TABLE agent_inbox_event
  ADD COLUMN IF NOT EXISTS terminal_outcome TEXT
    CHECK (terminal_outcome IN ('replied', 'no_reply', 'failed')),
  ADD COLUMN IF NOT EXISTS terminal_delivery_id UUID REFERENCES agent_event_delivery(id) ON DELETE SET NULL,
  ADD COLUMN IF NOT EXISTS retryable BOOLEAN NOT NULL DEFAULT FALSE,
  ADD COLUMN IF NOT EXISTS terminal_at TIMESTAMPTZ;

CREATE INDEX IF NOT EXISTS idx_agent_inbox_event_terminal_retry
  ON agent_inbox_event(workspace_id, channel_id, source_message_id, agent_id, terminal_at DESC)
  WHERE terminal_outcome IS NOT NULL;
