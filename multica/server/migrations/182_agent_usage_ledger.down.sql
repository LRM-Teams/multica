ALTER TRIGGER trg_au_dirty_hourly ON agent_usage RENAME TO trg_tu_dirty_hourly;
ALTER TRIGGER trg_issue_project_dirty_agent_usage_hourly ON issue RENAME TO trg_issue_project_dirty_hourly;
ALTER TRIGGER trg_issue_delete_dirty_agent_usage_hourly ON issue RENAME TO trg_issue_delete_dirty_hourly;
ALTER TRIGGER trg_atq_dirty_agent_usage_hourly ON agent_task_queue RENAME TO trg_atq_dirty_hourly;

ALTER FUNCTION agent_usage_hourly_rollup_lag_seconds() RENAME TO task_usage_hourly_rollup_lag_seconds;
ALTER FUNCTION rollup_agent_usage_hourly() RENAME TO rollup_task_usage_hourly;
ALTER FUNCTION prune_agent_usage_hourly_dirty(INTERVAL) RENAME TO prune_task_usage_hourly_dirty;
ALTER FUNCTION rollup_agent_usage_hourly_window(TIMESTAMPTZ, TIMESTAMPTZ) RENAME TO rollup_task_usage_hourly_window;
ALTER FUNCTION enqueue_agent_usage_hourly_dirty_for_au() RENAME TO enqueue_task_usage_hourly_dirty_for_tu;
ALTER FUNCTION enqueue_agent_usage_hourly_dirty_for_issue_project() RENAME TO enqueue_task_usage_hourly_dirty_for_issue_project;
ALTER FUNCTION enqueue_agent_usage_hourly_dirty_for_issue_delete() RENAME TO enqueue_task_usage_hourly_dirty_for_issue_delete;
ALTER FUNCTION enqueue_agent_usage_hourly_dirty_for_atq() RENAME TO enqueue_task_usage_hourly_dirty_for_atq;
ALTER FUNCTION agent_usage_hour_bucket(TIMESTAMPTZ) RENAME TO task_usage_hour_bucket;

-- See the forward migration: rebuild textual function bodies after the
-- backwards rename so rollback does not leave agent_usage identifiers behind.
DO $$
DECLARE
    fn RECORD;
    definition TEXT;
BEGIN
    FOR fn IN
        SELECT p.oid
          FROM pg_proc p
          JOIN pg_namespace n ON n.oid = p.pronamespace
         WHERE n.nspname = current_schema()
           AND p.proname = ANY (ARRAY[
               'task_usage_hour_bucket',
               'enqueue_task_usage_hourly_dirty_for_atq',
               'enqueue_task_usage_hourly_dirty_for_issue_delete',
               'enqueue_task_usage_hourly_dirty_for_issue_project',
               'enqueue_task_usage_hourly_dirty_for_tu',
               'rollup_task_usage_hourly_window',
               'prune_task_usage_hourly_dirty',
               'rollup_task_usage_hourly',
               'task_usage_hourly_rollup_lag_seconds'
           ])
    LOOP
        definition := pg_get_functiondef(fn.oid);
        definition := replace(definition, 'agent_usage_hourly', 'task_usage_hourly');
        definition := replace(definition, 'agent_usage', 'task_usage');
        definition := replace(definition, 'execution_id', 'task_id');
        EXECUTE definition;
    END LOOP;
END
$$;

ALTER INDEX idx_agent_usage_hourly_dirty_enqueued_at RENAME TO idx_task_usage_hourly_dirty_enqueued_at;
ALTER INDEX idx_agent_usage_hourly_workspace_project_time RENAME TO idx_task_usage_hourly_workspace_project_time;
ALTER INDEX idx_agent_usage_hourly_workspace_agent_time RENAME TO idx_task_usage_hourly_workspace_agent_time;
ALTER INDEX idx_agent_usage_hourly_runtime_time RENAME TO idx_task_usage_hourly_runtime_time;
ALTER INDEX idx_agent_usage_hourly_workspace_time RENAME TO idx_task_usage_hourly_workspace_time;
ALTER TABLE agent_usage_hourly_dirty RENAME CONSTRAINT uq_agent_usage_hourly_dirty_key
    TO uq_task_usage_hourly_dirty_key;
ALTER TABLE agent_usage_hourly RENAME CONSTRAINT uq_agent_usage_hourly_key
    TO uq_task_usage_hourly_key;

ALTER TABLE agent_usage_hourly_rollup_state RENAME TO task_usage_hourly_rollup_state;
ALTER TABLE agent_usage_hourly_dirty RENAME TO task_usage_hourly_dirty;
ALTER TABLE agent_usage_hourly RENAME TO task_usage_hourly;

ALTER TABLE agent_usage DROP COLUMN source;
ALTER INDEX idx_agent_usage_created_at_legacy RENAME TO idx_task_usage_created_at_legacy;
ALTER INDEX idx_agent_usage_created_at RENAME TO idx_task_usage_created_at;
ALTER INDEX idx_agent_usage_updated_at RENAME TO idx_task_usage_updated_at;
ALTER INDEX idx_agent_usage_execution_id RENAME TO idx_task_usage_task_id;
ALTER TABLE agent_usage RENAME CONSTRAINT agent_usage_execution_id_provider_model_key
    TO task_usage_task_id_provider_model_key;
ALTER TABLE agent_usage RENAME COLUMN execution_id TO task_id;
ALTER TABLE agent_usage RENAME TO task_usage;

DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_extension WHERE extname = 'pg_cron') THEN
        UPDATE cron.job
           SET jobname = 'rollup_task_usage_hourly',
               command = 'SELECT rollup_task_usage_hourly()'
         WHERE jobname = 'rollup_agent_usage_hourly';
    END IF;
END
$$;
