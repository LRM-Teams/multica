-- A chat-originated issue keeps one durable message anchor. This is distinct
-- from issue.origin_* (which identifies internal producers such as quick
-- create and autopilot runs): the anchor is user-facing navigation state.
-- A separate one-to-one table avoids widening every issue list/board query
-- with a detail-only field, while the primary key enforces V1's one-anchor
-- contract.
CREATE TABLE issue_source_message (
  issue_id UUID PRIMARY KEY REFERENCES issue(id) ON DELETE CASCADE,
  workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
  channel_id UUID NOT NULL REFERENCES channel(id) ON DELETE CASCADE,
  message_id UUID NOT NULL REFERENCES channel_message(id) ON DELETE CASCADE,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_issue_source_message_channel_message
  ON issue_source_message(workspace_id, channel_id, message_id);
