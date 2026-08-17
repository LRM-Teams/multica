CREATE TABLE research_v6_outbox (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id UUID NOT NULL,
  session_id UUID NOT NULL,
  kind TEXT NOT NULL CHECK (kind IN ('create_agent','update_agent','archive_agent','dispatch_work_item','notify_user')),
  idempotency_key TEXT NOT NULL,
  payload JSONB NOT NULL,
  status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','delivering','delivered','failed')),
  result JSONB NOT NULL DEFAULT '{}'::jsonb,
  delivery_attempts INTEGER NOT NULL DEFAULT 0 CHECK (delivery_attempts >= 0),
  lease_token UUID,
  lease_expires_at TIMESTAMPTZ,
  next_delivery_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  last_error TEXT NOT NULL DEFAULT '',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (workspace_id,session_id,id),
  UNIQUE (workspace_id,session_id,idempotency_key),
  CONSTRAINT research_v6_outbox_session_fk FOREIGN KEY (workspace_id,session_id)
    REFERENCES research_session(workspace_id,id) ON DELETE CASCADE
);
CREATE INDEX research_v6_outbox_due_idx
  ON research_v6_outbox(next_delivery_at,created_at,id) WHERE status IN ('pending','delivering');

ALTER TABLE research_director_cycle
  ADD COLUMN state_version BIGINT NOT NULL DEFAULT 1 CHECK (state_version >= 1),
  ADD COLUMN page_count INTEGER NOT NULL DEFAULT 0 CHECK (page_count >= 0),
  ADD COLUMN brief_manifest JSONB NOT NULL DEFAULT '[]'::jsonb;
