-- Session delete still failed for V6 runs with integrated results: the
-- integration/dispute tables from 350 kept plain NO ACTION session FKs, so
-- DELETE FROM research_session hit a foreign key violation once the Director
-- had absorbed results or opened a dispute. These five constraints are the
-- only remaining non-cascade FKs that reference research_session.

ALTER TABLE research_integration_round
  DROP CONSTRAINT research_integration_round_session_fk,
  ADD CONSTRAINT research_integration_round_session_fk
    FOREIGN KEY (workspace_id, session_id)
    REFERENCES research_session(workspace_id, id)
    ON DELETE CASCADE;

ALTER TABLE research_integration_contribution
  DROP CONSTRAINT research_integration_contribution_session_fk,
  ADD CONSTRAINT research_integration_contribution_session_fk
    FOREIGN KEY (workspace_id, session_id)
    REFERENCES research_session(workspace_id, id)
    ON DELETE CASCADE;

ALTER TABLE research_insight_derivation
  DROP CONSTRAINT research_insight_derivation_session_fk,
  ADD CONSTRAINT research_insight_derivation_session_fk
    FOREIGN KEY (workspace_id, session_id)
    REFERENCES research_session(workspace_id, id)
    ON DELETE CASCADE;

ALTER TABLE research_dispute
  DROP CONSTRAINT research_dispute_session_fk,
  ADD CONSTRAINT research_dispute_session_fk
    FOREIGN KEY (workspace_id, session_id)
    REFERENCES research_session(workspace_id, id)
    ON DELETE CASCADE;

ALTER TABLE research_dispute_position
  DROP CONSTRAINT research_dispute_position_session_fk,
  ADD CONSTRAINT research_dispute_position_session_fk
    FOREIGN KEY (workspace_id, session_id)
    REFERENCES research_session(workspace_id, id)
    ON DELETE CASCADE;
