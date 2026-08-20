-- V6 session delete was blocked by child FKs that omitted ON DELETE CASCADE.
-- Bootstrap always writes research_v6_bootstrap_request; a started Director
-- cycle also points work items and cycles at the assignment.

DO $$
DECLARE fk_name text;
BEGIN
  SELECT conname INTO fk_name
  FROM pg_constraint
  WHERE conrelid = 'research_v6_bootstrap_request'::regclass
    AND contype = 'f'
    AND pg_get_constraintdef(oid) LIKE '%research_session%';
  IF fk_name IS NOT NULL THEN
    EXECUTE format('ALTER TABLE research_v6_bootstrap_request DROP CONSTRAINT %I', fk_name);
  END IF;
END $$;

ALTER TABLE research_v6_bootstrap_request
  ADD CONSTRAINT research_v6_bootstrap_request_session_fk
  FOREIGN KEY (workspace_id, session_id)
  REFERENCES research_session(workspace_id, id)
  ON DELETE CASCADE
  DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE research_director_cycle
  DROP CONSTRAINT research_v6_director_cycle_assignment_fk,
  ADD CONSTRAINT research_v6_director_cycle_assignment_fk
    FOREIGN KEY (workspace_id, session_id, director_assignment_id)
    REFERENCES research_director_assignment(workspace_id, session_id, id)
    ON DELETE CASCADE;

ALTER TABLE research_work_item
  DROP CONSTRAINT research_v6_work_item_cycle_fk,
  ADD CONSTRAINT research_v6_work_item_cycle_fk
    FOREIGN KEY (workspace_id, session_id, created_by_director_cycle_id)
    REFERENCES research_director_cycle(workspace_id, session_id, id)
    ON DELETE CASCADE;
