-- Bind durable reports to their authors and preserve claim-to-prose anchors.

ALTER TABLE research_report
  ADD COLUMN produced_by_task_id UUID REFERENCES research_task(id) ON DELETE SET NULL,
  ADD COLUMN produced_by_attempt_id UUID REFERENCES research_task_attempt(id) ON DELETE SET NULL,
  ADD COLUMN author_agent_id UUID;

ALTER TABLE research_report_claim
  ADD COLUMN anchor_quote TEXT NOT NULL DEFAULT '';

CREATE INDEX research_report_author_idx
  ON research_report (session_id, author_agent_id, revision DESC)
  WHERE author_agent_id IS NOT NULL;
