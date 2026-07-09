-- 127_environment_state.down.sql
DROP TABLE IF EXISTS env_dispatch_request;

DROP INDEX IF EXISTS idx_project_env_unique;
ALTER TABLE project DROP COLUMN IF EXISTS env_id;

DROP INDEX IF EXISTS idx_environment_parent;
DROP INDEX IF EXISTS idx_environment_workspace;
DROP TABLE IF EXISTS environment;
