-- Add custom_env to agent_runtime for machine-level, user-configurable
-- environment variables injected into every agent subprocess launched on this
-- runtime. Runtime-level env is the base layer; agent-level custom_env
-- (migration 040) is applied after and wins on key collision.
ALTER TABLE agent_runtime ADD COLUMN custom_env JSONB NOT NULL DEFAULT '{}';
