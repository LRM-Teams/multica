DROP INDEX IF EXISTS env_dispatch_run_run_id_uidx;

ALTER TABLE env_dispatch_run
  DROP COLUMN IF EXISTS local_channel_id,
  DROP COLUMN IF EXISTS local_issue_id,
  DROP COLUMN IF EXISTS sample_index,
  DROP COLUMN IF EXISTS source_task_id,
  DROP COLUMN IF EXISTS run_id;

DROP TABLE IF EXISTS source_task;
