ALTER TABLE research_work_item
  ADD COLUMN state_version BIGINT NOT NULL DEFAULT 1 CHECK (state_version >= 1);

ALTER TABLE research_work_item_attempt
  ADD COLUMN manifest JSONB NOT NULL DEFAULT '{}'::jsonb,
  ADD COLUMN request_content_hash TEXT;

ALTER TABLE research_work_catalog_page
  ADD COLUMN page JSONB NOT NULL DEFAULT '{}'::jsonb;

ALTER TABLE research_work_item_attempt
  ADD CONSTRAINT research_v6_attempt_request_hash_check CHECK (
    request_content_hash IS NULL OR request_content_hash ~ '^sha256:[0-9a-f]{64}$'
  );

CREATE INDEX research_v6_work_item_recovery_idx
  ON research_work_item(session_id,status,lease_expires_at,state_version);

CREATE TABLE research_v6_work_submission (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id UUID NOT NULL,
  session_id UUID NOT NULL,
  work_item_id UUID NOT NULL,
  attempt_id UUID NOT NULL,
  client_request_id UUID NOT NULL,
  contract_kind TEXT NOT NULL CHECK (contract_kind IN (
    'director_action_proposal','atomic_result_submission','discussion_turn_submission',
    'integration_submission','report_package_submission'
  )),
  content_hash TEXT NOT NULL CHECK (content_hash ~ '^sha256:[0-9a-f]{64}$'),
  envelope JSONB NOT NULL,
  status TEXT NOT NULL DEFAULT 'received' CHECK (status IN ('received','processing','accepted','rejected')),
  outcome JSONB NOT NULL DEFAULT '{}'::jsonb,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (workspace_id,session_id,id),
  UNIQUE (workspace_id,session_id,client_request_id),
  CONSTRAINT research_v6_submission_attempt_fk
    FOREIGN KEY (workspace_id,session_id,attempt_id)
    REFERENCES research_work_item_attempt(workspace_id,session_id,id)
);
CREATE INDEX research_v6_submission_reconcile_idx
  ON research_v6_work_submission(status,created_at,id) WHERE status IN ('received','processing');
