-- Migration 441: typed Issue completion/review sidecar. The Issue and its visible comments
-- remain the product surface; this row supplies Run/revision/evidence fences
-- and immutable review audit for Goal execution projection.
-- Migration 438 made canonical Runs require issue_id, while the pre-existing
-- Issue FK intentionally uses ON DELETE SET NULL to retain Run history. Relax
-- only that post-delete state; live creation/admission still requires an Issue
-- in CreateCanonicalIssueRunEvent and active_issue_execution.
ALTER TABLE agent_inbox_event
  DROP CONSTRAINT agent_inbox_event_issue_run_contract_check,
  ADD CONSTRAINT agent_inbox_event_issue_run_contract_check CHECK (
    (
      issue_run_kind IS NULL
      AND issue_execution_revision IS NULL
      AND issue_execution_attempt_number IS NULL
    )
    OR (
      issue_run_kind = 'canonical'
      AND reason = 'issue'
      AND issue_execution_revision >= 0
      AND issue_execution_attempt_number > 0
    )
  );

CREATE TABLE issue_completion_report (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id UUID NOT NULL,
  issue_id UUID NOT NULL,
  -- Durable logical Run identity. Deliberately no FK: Inbox retention may
  -- delete the transport row while completion audit must remain explainable.
  run_id UUID NOT NULL UNIQUE,
  issue_execution_revision BIGINT NOT NULL CHECK (issue_execution_revision >= 0),
  -- Immutable actor identity, intentionally not an Agent FK: permanent Agent
  -- deletion must not erase or block durable completion/review audit.
  submitted_by_agent_id UUID NOT NULL,
  summary TEXT NOT NULL CHECK (length(btrim(summary)) BETWEEN 1 AND 8000),
  acceptance_results JSONB NOT NULL CHECK (jsonb_typeof(acceptance_results) = 'array'),
  artifact_refs JSONB NOT NULL DEFAULT '[]'::jsonb CHECK (jsonb_typeof(artifact_refs) = 'array'),
  risks JSONB NOT NULL DEFAULT '[]'::jsonb CHECK (jsonb_typeof(risks) = 'array'),
  request_hash TEXT NOT NULL CHECK (request_hash ~ '^[0-9a-f]{64}$'),
  visible_comment_id UUID REFERENCES comment(id) ON DELETE SET NULL,
  review_status TEXT NOT NULL DEFAULT 'pending'
    CHECK (review_status IN ('pending', 'accepted', 'rejected', 'superseded')),
  reviewer_type TEXT CHECK (reviewer_type IN ('member', 'agent')),
  reviewer_id UUID,
  review_reason TEXT,
  review_results JSONB NOT NULL DEFAULT '[]'::jsonb CHECK (jsonb_typeof(review_results) = 'array'),
  review_comment_id UUID REFERENCES comment(id) ON DELETE SET NULL,
  reviewed_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  FOREIGN KEY (workspace_id, issue_id)
    REFERENCES issue(workspace_id, id) ON DELETE CASCADE,
  CHECK (
    (review_status = 'pending'
      AND reviewer_type IS NULL AND reviewer_id IS NULL AND reviewed_at IS NULL
      AND review_results = '[]'::jsonb AND review_comment_id IS NULL)
    OR
    (review_status IN ('accepted', 'rejected')
      AND reviewer_type IS NOT NULL AND reviewer_id IS NOT NULL AND reviewed_at IS NOT NULL
      AND jsonb_array_length(review_results) > 0)
    OR
    (review_status = 'superseded' AND reviewed_at IS NULL)
  )
);

CREATE UNIQUE INDEX issue_completion_report_visible_comment_idx
  ON issue_completion_report(visible_comment_id)
  WHERE visible_comment_id IS NOT NULL;

CREATE UNIQUE INDEX issue_completion_report_review_comment_idx
  ON issue_completion_report(review_comment_id)
  WHERE review_comment_id IS NOT NULL;

CREATE INDEX issue_completion_report_issue_history_idx
  ON issue_completion_report(workspace_id, issue_id, created_at DESC, id DESC);

CREATE UNIQUE INDEX issue_completion_report_one_pending_issue_idx
  ON issue_completion_report(issue_id)
  WHERE review_status = 'pending';
