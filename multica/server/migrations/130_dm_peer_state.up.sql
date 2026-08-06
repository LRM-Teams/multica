CREATE TABLE dm_peer_state (
  workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
  user_id UUID NOT NULL REFERENCES "user"(id) ON DELETE CASCADE,
  peer_type TEXT NOT NULL CHECK (peer_type IN ('user', 'agent')),
  peer_id UUID NOT NULL,
  pinned_at TIMESTAMPTZ,
  hidden_at TIMESTAMPTZ,
  manual_unread_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (workspace_id, user_id, peer_type, peer_id)
);

CREATE INDEX idx_dm_peer_state_user_sort
  ON dm_peer_state(workspace_id, user_id, hidden_at, pinned_at DESC, updated_at DESC);
