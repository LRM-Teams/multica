DROP INDEX IF EXISTS idx_channel_project;

ALTER TABLE channel
  DROP COLUMN project_id;
