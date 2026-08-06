-- name: RegisterDaemonUpdateStatus :execrows
INSERT INTO daemon_update_status (
    workspace_id,
    daemon_id,
    session_id,
    revision,
    observed_at,
    auto_update_effective_enabled,
    config_source,
    ineligible_reason,
    check_interval_seconds,
    phase,
    attempt_source,
    last_attempt_at,
    last_outcome,
    target_version,
    error_code,
    error_message,
    staged_version,
    activation_generation,
    payload_hash,
    received_at,
    updated_at
) VALUES (
    @workspace_id,
    @daemon_id,
    @session_id,
    @revision,
    @observed_at,
    @auto_update_effective_enabled,
    @config_source,
    @ineligible_reason,
    @check_interval_seconds,
    @phase,
    @attempt_source,
    @last_attempt_at,
    @last_outcome,
    @target_version,
    @error_code,
    @error_message,
    @staged_version,
    @activation_generation,
    @payload_hash,
    now(),
    now()
)
ON CONFLICT (workspace_id, daemon_id) DO UPDATE SET
    session_id = EXCLUDED.session_id,
    revision = EXCLUDED.revision,
    observed_at = EXCLUDED.observed_at,
    auto_update_effective_enabled = EXCLUDED.auto_update_effective_enabled,
    config_source = EXCLUDED.config_source,
    ineligible_reason = EXCLUDED.ineligible_reason,
    check_interval_seconds = EXCLUDED.check_interval_seconds,
    phase = EXCLUDED.phase,
    attempt_source = EXCLUDED.attempt_source,
    last_attempt_at = EXCLUDED.last_attempt_at,
    last_outcome = EXCLUDED.last_outcome,
    target_version = EXCLUDED.target_version,
    error_code = EXCLUDED.error_code,
    error_message = EXCLUDED.error_message,
    staged_version = EXCLUDED.staged_version,
    activation_generation = EXCLUDED.activation_generation,
    payload_hash = EXCLUDED.payload_hash,
    received_at = now(),
    updated_at = now()
WHERE daemon_update_status.session_id <> EXCLUDED.session_id
   OR (
       daemon_update_status.session_id = EXCLUDED.session_id
       AND daemon_update_status.revision < EXCLUDED.revision
   );

-- name: AdvanceDaemonUpdateStatus :execrows
UPDATE daemon_update_status
SET revision = @revision,
    observed_at = @observed_at,
    auto_update_effective_enabled = @auto_update_effective_enabled,
    config_source = @config_source,
    ineligible_reason = @ineligible_reason,
    check_interval_seconds = @check_interval_seconds,
    phase = @phase,
    attempt_source = @attempt_source,
    last_attempt_at = @last_attempt_at,
    last_outcome = @last_outcome,
    target_version = @target_version,
    error_code = @error_code,
    error_message = @error_message,
    staged_version = @staged_version,
    activation_generation = @activation_generation,
    payload_hash = @payload_hash,
    received_at = now(),
    updated_at = now()
WHERE workspace_id = @workspace_id
  AND daemon_id = @daemon_id
  AND session_id = @session_id
  AND revision < @revision;

-- name: GetDaemonUpdateStatus :one
SELECT *
FROM daemon_update_status
WHERE workspace_id = @workspace_id
  AND daemon_id = @daemon_id;

-- name: ListDaemonUpdateStatusesForWorkspace :many
SELECT *
FROM daemon_update_status
WHERE workspace_id = @workspace_id
  AND daemon_id = ANY(@daemon_ids::text[]);

-- name: DeleteDaemonUpdateStatus :exec
DELETE FROM daemon_update_status
WHERE workspace_id = @workspace_id
  AND daemon_id = @daemon_id;

-- name: DeleteDaemonUpdateStatusIfOrphan :exec
DELETE FROM daemon_update_status status
WHERE status.workspace_id = @workspace_id
  AND status.daemon_id = @daemon_id
  AND NOT EXISTS (
    SELECT 1
    FROM agent_runtime runtime
    WHERE runtime.workspace_id = status.workspace_id
      AND runtime.daemon_id = status.daemon_id
);
