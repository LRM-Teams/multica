-- Usage belongs to a concrete provider execution, not to the mutable queue
-- row or to an inbox delivery lease. A source event may be run more than
-- once; each real provider start gets a distinct immutable execution UUID.
CREATE TABLE agent_execution (
    id               UUID PRIMARY KEY,
    source_kind      TEXT NOT NULL CHECK (source_kind IN ('queue', 'inbox')),
    source_event_id  UUID NOT NULL,
    source           TEXT NOT NULL CHECK (source IN ('chat', 'issue')),
    workspace_id     UUID NOT NULL,
    runtime_id       UUID,
    agent_id         UUID NOT NULL,
    chat_session_id  UUID,
    issue_id         UUID,
    project_id       UUID,
    execution_config JSONB,
    started_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_agent_execution_source_event
    ON agent_execution (source_kind, source_event_id, started_at);
CREATE INDEX idx_agent_execution_runtime_started
    ON agent_execution (runtime_id, started_at DESC);
CREATE INDEX idx_agent_execution_chat_session
    ON agent_execution (chat_session_id, started_at DESC)
    WHERE chat_session_id IS NOT NULL;
CREATE INDEX idx_agent_execution_issue
    ON agent_execution (issue_id, started_at DESC)
    WHERE issue_id IS NOT NULL;

-- The queue-era execution ID was the queue primary key. Preserve it as the
-- immutable execution ID exactly once, but copy every reader dimension now so
-- no report or rollup needs to join mutable queue/session state after this
-- migration.
INSERT INTO agent_execution (
    id, source_kind, source_event_id, source, workspace_id, runtime_id,
    agent_id, chat_session_id, issue_id, project_id, started_at, created_at
)
SELECT
    atq.id,
    'queue',
    atq.id,
    CASE WHEN atq.chat_session_id IS NULL THEN 'issue' ELSE 'chat' END,
    a.workspace_id,
    atq.runtime_id,
    atq.agent_id,
    atq.chat_session_id,
    atq.issue_id,
    i.project_id,
    COALESCE(atq.started_at, atq.created_at),
    atq.created_at
FROM agent_task_queue atq
JOIN agent a ON a.id = atq.agent_id
LEFT JOIN issue i ON i.id = atq.issue_id
WHERE EXISTS (SELECT 1 FROM agent_usage au WHERE au.execution_id = atq.id)
ON CONFLICT (id) DO NOTHING;

-- agent_usage already stores a source for the 182 cutover. Align it with the
-- immutable execution record so rows that predate the source column cannot
-- silently retain an incorrect default.
UPDATE agent_usage au
SET source = ae.source,
    updated_at = now()
FROM agent_execution ae
WHERE ae.id = au.execution_id
  AND au.source IS DISTINCT FROM ae.source;

-- Migration 182 renamed the column but intentionally retained the old
-- queue foreign key (and its historical constraint name). It rejects every
-- inbox execution, so remove it only after all existing rows have a durable
-- execution record above. The daemon/API start and usage endpoints now enforce
-- the execution boundary; source rows may later be deleted without erasing an
-- agent-owned ledger.
ALTER TABLE agent_usage DROP CONSTRAINT IF EXISTS task_usage_task_id_fkey;

-- Queue/issue changes must not rewrite historic ledger attribution. Replace
-- the old queue-based invalidation pipeline with a ledger-only version. The
-- existing dirty queue still handles explicit agent_usage deletion.
DROP TRIGGER IF EXISTS trg_atq_dirty_agent_usage_hourly ON agent_task_queue;
DROP TRIGGER IF EXISTS trg_issue_delete_dirty_agent_usage_hourly ON issue;
DROP TRIGGER IF EXISTS trg_issue_project_dirty_agent_usage_hourly ON issue;
DROP FUNCTION IF EXISTS enqueue_agent_usage_hourly_dirty_for_atq();
DROP FUNCTION IF EXISTS enqueue_agent_usage_hourly_dirty_for_issue_delete();
DROP FUNCTION IF EXISTS enqueue_agent_usage_hourly_dirty_for_issue_project();

CREATE OR REPLACE FUNCTION enqueue_agent_usage_hourly_dirty_for_au()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    INSERT INTO agent_usage_hourly_dirty (
        bucket_hour, workspace_id, runtime_id, agent_id,
        project_id, provider, model
    )
    SELECT
        agent_usage_hour_bucket(OLD.created_at),
        ae.workspace_id,
        ae.runtime_id,
        ae.agent_id,
        ae.project_id,
        OLD.provider,
        OLD.model
    FROM agent_execution ae
    WHERE ae.id = OLD.execution_id
      AND ae.runtime_id IS NOT NULL
    ON CONFLICT ON CONSTRAINT uq_agent_usage_hourly_dirty_key DO UPDATE
        SET enqueued_at = GREATEST(agent_usage_hourly_dirty.enqueued_at, EXCLUDED.enqueued_at);
    RETURN OLD;
END;
$$;

CREATE OR REPLACE FUNCTION rollup_agent_usage_hourly_window(
    p_from TIMESTAMPTZ,
    p_to   TIMESTAMPTZ
)
RETURNS BIGINT
LANGUAGE plpgsql
AS $$
DECLARE
    v_rows BIGINT;
BEGIN
    IF p_from >= p_to THEN
        RETURN 0;
    END IF;

    WITH
    dirty_from_updates AS (
        SELECT DISTINCT
            agent_usage_hour_bucket(au.created_at) AS bucket_hour,
            ae.workspace_id,
            ae.runtime_id,
            ae.agent_id,
            ae.project_id,
            au.provider,
            au.model
        FROM agent_usage au
        JOIN agent_execution ae ON ae.id = au.execution_id
        WHERE ae.runtime_id IS NOT NULL
          AND (
              (au.updated_at >= p_from AND au.updated_at < p_to)
              OR (au.updated_at IS NULL AND au.created_at >= p_from AND au.created_at < p_to)
          )
    ),
    dirty_from_queue AS (
        SELECT bucket_hour, workspace_id, runtime_id, agent_id,
               project_id, provider, model
        FROM agent_usage_hourly_dirty
        WHERE enqueued_at < p_to
    ),
    dirty_keys AS (
        SELECT * FROM dirty_from_updates
        UNION
        SELECT * FROM dirty_from_queue
    ),
    recomputed AS (
        SELECT
            dk.bucket_hour,
            dk.workspace_id,
            dk.runtime_id,
            dk.agent_id,
            dk.project_id,
            dk.provider,
            dk.model,
            SUM(au.input_tokens)::bigint AS input_tokens,
            SUM(au.output_tokens)::bigint AS output_tokens,
            SUM(au.cache_read_tokens)::bigint AS cache_read_tokens,
            SUM(au.cache_write_tokens)::bigint AS cache_write_tokens,
            COUNT(DISTINCT au.execution_id)::bigint AS task_count,
            COUNT(*)::bigint AS event_count
        FROM dirty_keys dk
        JOIN agent_execution ae
          ON ae.workspace_id = dk.workspace_id
         AND ae.runtime_id = dk.runtime_id
         AND ae.agent_id = dk.agent_id
         AND ae.project_id IS NOT DISTINCT FROM dk.project_id
        JOIN agent_usage au
          ON au.execution_id = ae.id
         AND au.provider = dk.provider
         AND au.model = dk.model
         AND agent_usage_hour_bucket(au.created_at) = dk.bucket_hour
        GROUP BY 1, 2, 3, 4, 5, 6, 7
    ),
    upserted AS (
        INSERT INTO agent_usage_hourly AS d (
            bucket_hour, workspace_id, runtime_id, agent_id,
            project_id, provider, model,
            input_tokens, output_tokens, cache_read_tokens, cache_write_tokens,
            task_count, event_count
        )
        SELECT bucket_hour, workspace_id, runtime_id, agent_id,
               project_id, provider, model,
               input_tokens, output_tokens, cache_read_tokens, cache_write_tokens,
               task_count, event_count
        FROM recomputed
        ON CONFLICT ON CONSTRAINT uq_agent_usage_hourly_key DO UPDATE
            SET input_tokens = EXCLUDED.input_tokens,
                output_tokens = EXCLUDED.output_tokens,
                cache_read_tokens = EXCLUDED.cache_read_tokens,
                cache_write_tokens = EXCLUDED.cache_write_tokens,
                task_count = EXCLUDED.task_count,
                event_count = EXCLUDED.event_count,
                updated_at = now()
        RETURNING 1
    ),
    deleted_empty AS (
        DELETE FROM agent_usage_hourly d
        USING dirty_keys dk
        WHERE d.bucket_hour = dk.bucket_hour
          AND d.workspace_id = dk.workspace_id
          AND d.runtime_id = dk.runtime_id
          AND d.agent_id = dk.agent_id
          AND d.project_id IS NOT DISTINCT FROM dk.project_id
          AND d.provider = dk.provider
          AND d.model = dk.model
          AND NOT EXISTS (
              SELECT 1 FROM recomputed r
              WHERE r.bucket_hour = dk.bucket_hour
                AND r.workspace_id = dk.workspace_id
                AND r.runtime_id = dk.runtime_id
                AND r.agent_id = dk.agent_id
                AND r.project_id IS NOT DISTINCT FROM dk.project_id
                AND r.provider = dk.provider
                AND r.model = dk.model
          )
        RETURNING 1
    ),
    drained AS (
        DELETE FROM agent_usage_hourly_dirty
        WHERE enqueued_at < p_to
        RETURNING 1
    )
    SELECT (SELECT count(*) FROM upserted) + (SELECT count(*) FROM deleted_empty)
    INTO v_rows;

    RETURN v_rows;
END;
$$;
