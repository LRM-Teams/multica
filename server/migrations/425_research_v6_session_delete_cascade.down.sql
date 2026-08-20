ALTER TABLE research_work_item
  DROP CONSTRAINT research_v6_work_item_cycle_fk,
  ADD CONSTRAINT research_v6_work_item_cycle_fk
    FOREIGN KEY (workspace_id, session_id, created_by_director_cycle_id)
    REFERENCES research_director_cycle(workspace_id, session_id, id);

ALTER TABLE research_director_cycle
  DROP CONSTRAINT research_v6_director_cycle_assignment_fk,
  ADD CONSTRAINT research_v6_director_cycle_assignment_fk
    FOREIGN KEY (workspace_id, session_id, director_assignment_id)
    REFERENCES research_director_assignment(workspace_id, session_id, id);

ALTER TABLE research_v6_bootstrap_request
  DROP CONSTRAINT research_v6_bootstrap_request_session_fk,
  ADD CONSTRAINT research_v6_bootstrap_request_session_fk
    FOREIGN KEY (workspace_id, session_id)
    REFERENCES research_session(workspace_id, id)
    DEFERRABLE INITIALLY DEFERRED;
