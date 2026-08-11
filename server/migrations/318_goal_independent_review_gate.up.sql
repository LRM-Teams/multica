-- Goal review is a kernel gate. Verification records the actual reviewer
-- identity and producer node, while derived reviewers are ephemeral identities
-- with no inherited memory or provider session.
ALTER TABLE work_verification_attempt
  ADD COLUMN producer_node_id UUID REFERENCES work_graph_node(id) ON DELETE CASCADE,
  ADD COLUMN reviewer_agent_id UUID REFERENCES agent(id) ON DELETE SET NULL,
  ADD COLUMN context_policy TEXT NOT NULL DEFAULT 'bounded'
    CHECK (context_policy IN ('full','bounded','blind','adversarial','replication','sealed'));

UPDATE work_verification_attempt attempt
SET producer_node_id = artifact.producer_node_id,
    reviewer_agent_id = item.assignee_id
FROM work_artifact_revision artifact,
     work_graph_node verifier,
     issue item
WHERE artifact.id = attempt.subject_artifact_revision_id
  AND verifier.id = attempt.verifier_node_id
  AND item.id = verifier.issue_id
  AND item.assignee_type = 'agent';

ALTER TABLE work_verification_attempt
  ALTER COLUMN producer_node_id SET NOT NULL;

CREATE INDEX work_verification_producer_active_idx
  ON work_verification_attempt(producer_node_id, verifier_node_id, created_at DESC)
  WHERE stale_at IS NULL;

CREATE INDEX work_verification_reviewer_agent_idx
  ON work_verification_attempt(reviewer_agent_id)
  WHERE reviewer_agent_id IS NOT NULL;

CREATE TABLE work_review_agent_assignment (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
  graph_id UUID NOT NULL REFERENCES work_graph(id) ON DELETE CASCADE,
  verifier_node_id UUID NOT NULL UNIQUE REFERENCES work_graph_node(id) ON DELETE CASCADE,
  issue_id UUID NOT NULL UNIQUE REFERENCES issue(id) ON DELETE CASCADE,
  source_agent_id UUID NOT NULL REFERENCES agent(id) ON DELETE CASCADE,
  derived_agent_id UUID NOT NULL UNIQUE REFERENCES agent(id) ON DELETE CASCADE,
  memory_policy TEXT NOT NULL DEFAULT 'review_baseline_only'
    CHECK (memory_policy IN ('review_baseline_only')),
  lifecycle_policy TEXT NOT NULL DEFAULT 'archive_on_review_terminal'
    CHECK (lifecycle_policy IN ('archive_on_review_terminal')),
  status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active','archived')),
  archived_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX work_review_agent_assignment_source_idx
  ON work_review_agent_assignment(source_agent_id);
CREATE INDEX work_review_agent_assignment_graph_idx
  ON work_review_agent_assignment(graph_id, status);

ALTER TABLE agent_inbox_event
  DROP CONSTRAINT IF EXISTS agent_inbox_event_reason_check;

ALTER TABLE agent_inbox_event
  ADD CONSTRAINT agent_inbox_event_reason_check
  CHECK (reason IN (
    'mention','dm','ambient','thread_reply','channel_message',
    'chat_session','voice_call','issue_thread_backflow','collaboration_turn',
    'collaboration_manager_fallback','channel_onboarding','issue','quick_create',
    'autopilot','agent_radar','training','environment_dispatch','memory_curation',
    'reminder','channel_role_changed','goal_graph_delta'
  ));
