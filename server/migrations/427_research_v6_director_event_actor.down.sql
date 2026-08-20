UPDATE research_run_event
SET actor_type = CASE
  WHEN actor_id IS NULL THEN 'system'
  ELSE 'agent'
END
WHERE actor_type = 'director';

ALTER TABLE research_run_event
  DROP CONSTRAINT research_run_event_actor_type_check;

ALTER TABLE research_run_event
  ADD CONSTRAINT research_run_event_actor_type_check
  CHECK (actor_type IN ('user', 'agent', 'system'));
