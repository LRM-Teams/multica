-- Usage is produced by an agent execution, not necessarily an issue task.
-- Keep the existing rows and foreign key, but give the ledger a truthful
-- product name and an explicit source that lets reports separate chat from
-- issue work. This is a one-way cutover: no view, alias, or dual-write path
-- keeps the old task_usage name alive.
ALTER TABLE task_usage RENAME TO agent_usage;
ALTER TABLE agent_usage RENAME COLUMN task_id TO execution_id;
ALTER TABLE agent_usage RENAME CONSTRAINT task_usage_task_id_provider_model_key
    TO agent_usage_execution_id_provider_model_key;
ALTER INDEX idx_task_usage_task_id RENAME TO idx_agent_usage_execution_id;
ALTER INDEX idx_task_usage_updated_at RENAME TO idx_agent_usage_updated_at;
ALTER INDEX idx_task_usage_created_at RENAME TO idx_agent_usage_created_at;
ALTER INDEX idx_task_usage_created_at_legacy RENAME TO idx_agent_usage_created_at_legacy;

ALTER TABLE agent_usage
    ADD COLUMN source TEXT NOT NULL DEFAULT 'issue'
    CHECK (source IN ('chat', 'issue'));

UPDATE agent_usage au
SET source = 'chat'
FROM agent_task_queue atq
WHERE atq.id = au.execution_id
  AND atq.chat_session_id IS NOT NULL;

-- The source update above must become visible to the hourly rollup's
-- watermark. The daemon supplies source explicitly; the default keeps direct
-- SQL maintenance and historical fixtures safe for issue-backed rows.
UPDATE agent_usage SET updated_at = now();

ALTER TABLE task_usage_hourly RENAME TO agent_usage_hourly;
ALTER TABLE task_usage_hourly_dirty RENAME TO agent_usage_hourly_dirty;
ALTER TABLE task_usage_hourly_rollup_state RENAME TO agent_usage_hourly_rollup_state;

ALTER TABLE agent_usage_hourly RENAME CONSTRAINT uq_task_usage_hourly_key
    TO uq_agent_usage_hourly_key;
ALTER TABLE agent_usage_hourly_dirty RENAME CONSTRAINT uq_task_usage_hourly_dirty_key
    TO uq_agent_usage_hourly_dirty_key;
ALTER INDEX idx_task_usage_hourly_workspace_time RENAME TO idx_agent_usage_hourly_workspace_time;
ALTER INDEX idx_task_usage_hourly_runtime_time RENAME TO idx_agent_usage_hourly_runtime_time;
ALTER INDEX idx_task_usage_hourly_workspace_agent_time RENAME TO idx_agent_usage_hourly_workspace_agent_time;
ALTER INDEX idx_task_usage_hourly_workspace_project_time RENAME TO idx_agent_usage_hourly_workspace_project_time;
ALTER INDEX idx_task_usage_hourly_dirty_enqueued_at RENAME TO idx_agent_usage_hourly_dirty_enqueued_at;

-- The existing hourly totals remain valid because dashboard/runtime reports
-- aggregate all sources. Source-specific views read the raw ledger until a
-- source dimension is deliberately added to a future projection; do not
-- discard historical billing data during this naming cutover.

ALTER FUNCTION task_usage_hour_bucket(TIMESTAMPTZ) RENAME TO agent_usage_hour_bucket;
ALTER FUNCTION enqueue_task_usage_hourly_dirty_for_atq() RENAME TO enqueue_agent_usage_hourly_dirty_for_atq;
ALTER FUNCTION enqueue_task_usage_hourly_dirty_for_issue_delete() RENAME TO enqueue_agent_usage_hourly_dirty_for_issue_delete;
ALTER FUNCTION enqueue_task_usage_hourly_dirty_for_issue_project() RENAME TO enqueue_agent_usage_hourly_dirty_for_issue_project;
ALTER FUNCTION enqueue_task_usage_hourly_dirty_for_tu() RENAME TO enqueue_agent_usage_hourly_dirty_for_au;
ALTER FUNCTION rollup_task_usage_hourly_window(TIMESTAMPTZ, TIMESTAMPTZ) RENAME TO rollup_agent_usage_hourly_window;
ALTER FUNCTION prune_task_usage_hourly_dirty(INTERVAL) RENAME TO prune_agent_usage_hourly_dirty;
ALTER FUNCTION rollup_task_usage_hourly() RENAME TO rollup_agent_usage_hourly;
ALTER FUNCTION task_usage_hourly_rollup_lag_seconds() RENAME TO agent_usage_hourly_rollup_lag_seconds;

-- PostgreSQL tracks relation dependencies across ALTER TABLE ... RENAME, but
-- SQL-language and PL/pgSQL function bodies are stored as source text. Rebuild
-- the renamed hourly pipeline functions from their current definitions so no
-- live function body continues to reference task_usage/task_id.
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
               'agent_usage_hour_bucket',
               'enqueue_agent_usage_hourly_dirty_for_atq',
               'enqueue_agent_usage_hourly_dirty_for_issue_delete',
               'enqueue_agent_usage_hourly_dirty_for_issue_project',
               'enqueue_agent_usage_hourly_dirty_for_au',
               'rollup_agent_usage_hourly_window',
               'prune_agent_usage_hourly_dirty',
               'rollup_agent_usage_hourly',
               'agent_usage_hourly_rollup_lag_seconds'
           ])
    LOOP
        definition := pg_get_functiondef(fn.oid);
        definition := replace(definition, 'task_usage_hourly', 'agent_usage_hourly');
        definition := replace(definition, 'task_usage', 'agent_usage');
        definition := replace(definition, 'task_id', 'execution_id');
        EXECUTE definition;
    END LOOP;
END
$$;

ALTER TRIGGER trg_atq_dirty_hourly ON agent_task_queue RENAME TO trg_atq_dirty_agent_usage_hourly;
ALTER TRIGGER trg_issue_delete_dirty_hourly ON issue RENAME TO trg_issue_delete_dirty_agent_usage_hourly;
ALTER TRIGGER trg_issue_project_dirty_hourly ON issue RENAME TO trg_issue_project_dirty_agent_usage_hourly;
ALTER TRIGGER trg_tu_dirty_hourly ON agent_usage RENAME TO trg_au_dirty_hourly;

-- Preserve an existing pg_cron schedule when this deployment uses one. The
-- pipeline's function name is part of production schema terminology too.
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_extension WHERE extname = 'pg_cron') THEN
        UPDATE cron.job
           SET jobname = 'rollup_agent_usage_hourly',
               command = 'SELECT rollup_agent_usage_hourly()'
         WHERE jobname = 'rollup_task_usage_hourly';
    END IF;
END
$$;
