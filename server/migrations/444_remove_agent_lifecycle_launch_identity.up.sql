ALTER TABLE agent ADD COLUMN provider_session_id TEXT CHECK (length(provider_session_id) <= 200);

UPDATE agent a
SET provider_session_id = p.provider_session_id
FROM agent_runner_launch_projection p
WHERE p.agent_id = a.id AND p.provider_session_id IS NOT NULL;

DROP TRIGGER IF EXISTS agent_runner_launch_projection_trigger ON agent;
DROP FUNCTION IF EXISTS project_agent_runner_launch();
DROP TABLE agent_runner_launch_projection;

DROP TABLE IF EXISTS agent_activity_launch;

DROP INDEX IF EXISTS agent_activity_entry_launch_idx;
ALTER TABLE agent_activity_entry
  DROP COLUMN launch_id,
  ADD CONSTRAINT agent_activity_entry_fact_key
    UNIQUE (workspace_id, agent_id, daemon_instance_id, producer_fact_id, entry_position);

ALTER TABLE agent_activity_snapshot DROP COLUMN launch_id;
ALTER TABLE agent_activity_probe DROP COLUMN launch_id;
