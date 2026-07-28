ALTER TABLE agent_credential
  ADD COLUMN issuance_source TEXT NOT NULL DEFAULT 'manual';

ALTER TABLE agent_credential
  ADD CONSTRAINT agent_credential_issuance_source_check
  CHECK (issuance_source IN ('manual', 'daemon'));

UPDATE agent_credential AS credential
SET issuance_source = 'daemon'
WHERE EXISTS (
  SELECT 1
  FROM activity_log AS audit
  WHERE audit.workspace_id = credential.workspace_id
    AND audit.action = 'agent_credential_daemon_ensured'
    AND audit.details->>'source' = 'daemon_runtime_ensure'
    AND audit.details->>'reused' = 'false'
    AND audit.details->>'agent_credential_id' = credential.id::text
);

WITH ranked AS MATERIALIZED (
  SELECT
    credential.id,
    ROW_NUMBER() OVER (
      PARTITION BY credential.agent_id, credential.workspace_id, credential.user_id
      ORDER BY
        (
          credential.disabled_at IS NULL
          AND (
            credential.expires_at IS NULL
            OR credential.expires_at > now()
          )
        ) DESC,
        (credential.last_used_at IS NOT NULL) DESC,
        credential.last_used_at DESC NULLS LAST,
        credential.created_at DESC,
        credential.id DESC
    ) AS live_rank
  FROM agent_credential AS credential
  WHERE credential.issuance_source = 'daemon'
    AND credential.revoked_at IS NULL
),
revoked AS (
  UPDATE agent_credential AS credential
  SET revoked_at = now(),
      updated_at = now()
  FROM ranked
  WHERE credential.id = ranked.id
    AND ranked.live_rank > 1
  RETURNING
    credential.id,
    credential.agent_id,
    credential.workspace_id,
    credential.user_id
)
INSERT INTO activity_log (
  workspace_id,
  actor_type,
  action,
  details
)
SELECT
  revoked.workspace_id,
  'system',
  'agent_credential_revoked',
  jsonb_build_object(
    'agent_id', revoked.agent_id,
    'owner_user_id', revoked.user_id,
    'agent_credential_id', revoked.id,
    'reason', 'daemon_issuance_backfill_deduplicate',
    'revoked_count', 1
  )
FROM revoked;

CREATE UNIQUE INDEX idx_agent_credential_daemon_subject_unrevoked
  ON agent_credential(agent_id, workspace_id, user_id)
  WHERE issuance_source = 'daemon'
    AND revoked_at IS NULL;
