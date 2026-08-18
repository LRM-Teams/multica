CREATE TABLE research_v6_bootstrap_request (
  workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
  client_request_id UUID NOT NULL,
  request_hash TEXT NOT NULL CHECK (request_hash ~ '^sha256:[0-9a-f]{64}$'),
  session_id UUID NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (workspace_id, client_request_id),
  UNIQUE (workspace_id, session_id),
  FOREIGN KEY (workspace_id, session_id)
    REFERENCES research_session(workspace_id, id)
    DEFERRABLE INITIALLY DEFERRED
);
