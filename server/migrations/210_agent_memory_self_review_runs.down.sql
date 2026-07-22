ALTER TABLE agent_memory_write_event
  DROP CONSTRAINT IF EXISTS agent_memory_write_event_scope_type_check,
  ADD CONSTRAINT agent_memory_write_event_scope_type_check
    CHECK (scope_type IN ('agent_global', 'agent_state', 'user', 'channel', 'project'));

ALTER TABLE agent_memory_curation_candidate
  DROP CONSTRAINT IF EXISTS agent_memory_curation_candidate_candidate_type_check,
  ADD CONSTRAINT agent_memory_curation_candidate_candidate_type_check
    CHECK (candidate_type IN ('memory', 'user_preference', 'state', 'skill', 'team_memory', 'team_skill', 'conflict', 'follow_up')),
  DROP CONSTRAINT IF EXISTS agent_memory_curation_candidate_scope_check,
  ADD CONSTRAINT agent_memory_curation_candidate_scope_check
    CHECK (scope IN ('agent', 'user', 'workspace', 'team'));

DROP INDEX IF EXISTS idx_memory_curation_agent_run_agent_created;
DROP INDEX IF EXISTS idx_memory_curation_agent_run_workspace_status;
DROP INDEX IF EXISTS idx_memory_curation_agent_run_parent_created;
DROP TABLE IF EXISTS memory_curation_agent_run;
