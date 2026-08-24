ALTER TABLE research_run_event
  DROP CONSTRAINT research_run_event_actor_type_check;

ALTER TABLE research_run_event
  ADD CONSTRAINT research_run_event_actor_type_check
  CHECK (actor_type IN ('user', 'agent', 'director', 'system'));
