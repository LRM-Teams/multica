ALTER TABLE research_integration_round
  DROP CONSTRAINT research_integration_round_session_fk,
  ADD CONSTRAINT research_integration_round_session_fk
    FOREIGN KEY (workspace_id, session_id)
    REFERENCES research_session(workspace_id, id);

ALTER TABLE research_integration_contribution
  DROP CONSTRAINT research_integration_contribution_session_fk,
  ADD CONSTRAINT research_integration_contribution_session_fk
    FOREIGN KEY (workspace_id, session_id)
    REFERENCES research_session(workspace_id, id);

ALTER TABLE research_insight_derivation
  DROP CONSTRAINT research_insight_derivation_session_fk,
  ADD CONSTRAINT research_insight_derivation_session_fk
    FOREIGN KEY (workspace_id, session_id)
    REFERENCES research_session(workspace_id, id);

ALTER TABLE research_dispute
  DROP CONSTRAINT research_dispute_session_fk,
  ADD CONSTRAINT research_dispute_session_fk
    FOREIGN KEY (workspace_id, session_id)
    REFERENCES research_session(workspace_id, id);

ALTER TABLE research_dispute_position
  DROP CONSTRAINT research_dispute_position_session_fk,
  ADD CONSTRAINT research_dispute_position_session_fk
    FOREIGN KEY (workspace_id, session_id)
    REFERENCES research_session(workspace_id, id);
