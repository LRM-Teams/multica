-- Freeze every Research Agent dispatch before the external Inbox mutation.
-- A claimed row is recoverable after lease expiry; the dispatch key and
-- request hash make re-delivery idempotent and detect conflicting payloads.

CREATE TABLE research_dispatch_outbox (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
  session_id UUID NOT NULL REFERENCES research_session(id) ON DELETE CASCADE,
  task_id UUID NOT NULL REFERENCES research_task(id) ON DELETE CASCADE,
  attempt_id UUID NOT NULL REFERENCES research_task_attempt(id) ON DELETE CASCADE,
  dispatch_key TEXT NOT NULL,
  request_version INTEGER NOT NULL DEFAULT 1 CHECK (request_version = 1),
  request_payload JSONB NOT NULL,
  request_hash TEXT NOT NULL CHECK (request_hash ~ '^[0-9a-f]{64}$'),
  status TEXT NOT NULL DEFAULT 'pending'
    CHECK (status IN ('pending', 'delivering', 'delivered', 'failed', 'cancelled')),
  delivery_attempts INTEGER NOT NULL DEFAULT 0 CHECK (delivery_attempts >= 0),
  next_delivery_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  lease_token UUID,
  lease_expires_at TIMESTAMPTZ,
  last_error TEXT NOT NULL DEFAULT '',
  delivered_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (attempt_id),
  UNIQUE (dispatch_key),
  CHECK (
    (status = 'delivering' AND lease_token IS NOT NULL AND lease_expires_at IS NOT NULL)
    OR (status <> 'delivering' AND lease_token IS NULL AND lease_expires_at IS NULL)
  ),
  CHECK ((status = 'delivered' AND delivered_at IS NOT NULL) OR status <> 'delivered')
);

CREATE INDEX research_dispatch_outbox_due_idx
  ON research_dispatch_outbox (next_delivery_at, created_at, id)
  WHERE status = 'pending';

CREATE INDEX research_dispatch_outbox_expired_lease_idx
  ON research_dispatch_outbox (lease_expires_at, id)
  WHERE status = 'delivering';

CREATE INDEX research_dispatch_outbox_session_idx
  ON research_dispatch_outbox (session_id, status, created_at);
