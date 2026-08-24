-- name: UpsertAgentActivitySnapshot :one
-- Current Activity is one replaceable observation per Workspace Agent. Server
-- fencing decides whether a producer is current before this query runs; this
-- query only makes the replacement durable.
INSERT INTO agent_activity_snapshot (
    workspace_id, agent_id, runtime_id, daemon_id, daemon_instance_id,
    provider_session_id, turn_id, runtime_generation, activity_kind, detail_kind,
    summary_label, observed_at
) VALUES (
    @workspace_id, @agent_id, @runtime_id, @daemon_id, @daemon_instance_id,
    @provider_session_id, @turn_id, @runtime_generation, @activity_kind, @detail_kind,
    @summary_label, @observed_at
)
ON CONFLICT (workspace_id, agent_id) DO UPDATE SET
    runtime_id = EXCLUDED.runtime_id,
    daemon_id = EXCLUDED.daemon_id,
    daemon_instance_id = EXCLUDED.daemon_instance_id,
    provider_session_id = EXCLUDED.provider_session_id,
    turn_id = EXCLUDED.turn_id,
    runtime_generation = EXCLUDED.runtime_generation,
    activity_kind = EXCLUDED.activity_kind,
    detail_kind = EXCLUDED.detail_kind,
    summary_label = EXCLUDED.summary_label,
    observed_at = EXCLUDED.observed_at,
    received_at = now()
RETURNING *;

-- name: InsertAgentActivityEntry :one
INSERT INTO agent_activity_entry (
    workspace_id, agent_id, runtime_id, daemon_id, daemon_instance_id,
    activity_kind, detail_kind, title, subtext, body_kind, body, observed_at
) VALUES (
    @workspace_id, @agent_id, @runtime_id, @daemon_id, @daemon_instance_id,
    @activity_kind, @detail_kind, @title, @subtext, @body_kind, @body, @observed_at
)
RETURNING *;

-- name: GetAgentActivitySnapshot :one
SELECT *
FROM agent_activity_snapshot
WHERE workspace_id = @workspace_id
  AND agent_id = @agent_id;

-- name: ListAgentActivityEntries :many
-- Caller supplies a keyset cursor; newest rows come first for efficient
-- timeline paging and clients reverse only the page they render.
SELECT *
FROM agent_activity_entry
WHERE workspace_id = @workspace_id
  AND agent_id = @agent_id
  AND (
      NOT @has_cursor::boolean
      OR (observed_at, id) < (@before_observed_at, @before_id)
  )
ORDER BY observed_at DESC, id DESC
LIMIT @page_size;
