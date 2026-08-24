-- name: UpsertAgentActivitySnapshot :one
-- Current Activity is one replaceable observation per Workspace Agent. Server
-- fencing decides whether a producer is current before this query runs; this
-- query only makes the replacement durable.
INSERT INTO agent_activity_snapshot (
    workspace_id, agent_id, runtime_id, daemon_id, daemon_instance_id,
    process_instance_id, provider_session_id, turn_id, runtime_generation,
    client_sequence, producer_fact_id, probe_id, activity_kind, detail_kind,
    observed_at
) VALUES (
    @workspace_id, @agent_id, @runtime_id, @daemon_id, @daemon_instance_id,
    @process_instance_id, @provider_session_id, @turn_id, @runtime_generation,
    @client_sequence, @producer_fact_id, @probe_id, @activity_kind, @detail_kind,
    @observed_at
)
ON CONFLICT (workspace_id, agent_id) DO UPDATE SET
    runtime_id = EXCLUDED.runtime_id,
    daemon_id = EXCLUDED.daemon_id,
    daemon_instance_id = EXCLUDED.daemon_instance_id,
    process_instance_id = EXCLUDED.process_instance_id,
    provider_session_id = EXCLUDED.provider_session_id,
    turn_id = EXCLUDED.turn_id,
    runtime_generation = EXCLUDED.runtime_generation,
    client_sequence = EXCLUDED.client_sequence,
    producer_fact_id = EXCLUDED.producer_fact_id,
    probe_id = EXCLUDED.probe_id,
    activity_kind = EXCLUDED.activity_kind,
    detail_kind = EXCLUDED.detail_kind,
    observed_at = EXCLUDED.observed_at,
    received_at = now()
RETURNING *;

-- name: InsertAgentActivityEntry :one
-- A producer fact plus entry position is the durable deduplication identity.
-- Replays return no row, which lets the intake boundary remain idempotent.
INSERT INTO agent_activity_entry (
    workspace_id, agent_id, runtime_id, daemon_id, daemon_instance_id,
    process_instance_id, client_sequence, producer_fact_id, entry_position,
    entry_kind, entry_body, observed_at
) VALUES (
    @workspace_id, @agent_id, @runtime_id, @daemon_id, @daemon_instance_id,
    @process_instance_id, @client_sequence, @producer_fact_id, @entry_position,
    @entry_kind, @entry_body, @observed_at
)
ON CONFLICT (workspace_id, agent_id, daemon_instance_id, producer_fact_id, entry_position)
DO NOTHING
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
