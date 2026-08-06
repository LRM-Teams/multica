ALTER TABLE project_resource
  DROP COLUMN managed;

DROP INDEX IF EXISTS idx_chat_session_project;

ALTER TABLE chat_session
  DROP COLUMN project_id;
