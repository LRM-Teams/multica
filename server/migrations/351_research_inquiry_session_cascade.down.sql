ALTER TABLE research_inquiry_edge
  DROP CONSTRAINT research_inquiry_edge_session_fk,
  ADD CONSTRAINT research_inquiry_edge_session_fk
    FOREIGN KEY (workspace_id, session_id)
    REFERENCES research_session (workspace_id, id);

ALTER TABLE research_insight
  DROP CONSTRAINT research_insight_session_fk,
  ADD CONSTRAINT research_insight_session_fk
    FOREIGN KEY (workspace_id, session_id)
    REFERENCES research_session (workspace_id, id);

ALTER TABLE research_branch
  DROP CONSTRAINT research_branch_session_fk,
  ADD CONSTRAINT research_branch_session_fk
    FOREIGN KEY (workspace_id, session_id)
    REFERENCES research_session (workspace_id, id);

ALTER TABLE research_hypothesis
  DROP CONSTRAINT research_hypothesis_session_fk,
  ADD CONSTRAINT research_hypothesis_session_fk
    FOREIGN KEY (workspace_id, session_id)
    REFERENCES research_session (workspace_id, id);
