-- Evolution unit submissions and governed shared units.

-- name: UpsertEvolutionUnitSubmission :one
INSERT INTO evolution_unit_submission (
  workspace_id, source_agent_id, source_member_id,
  unit_type, local_unit_id, title, summary, content,
  payload, sanitized_payload, content_hash, bundle_hash, bundle_ref,
  sensitivity, confidence, suggested_scope, evidence, applies,
  tags, tools, task_types, project_types, languages, frameworks,
  source_created_at
) VALUES (
  @workspace_id, @source_agent_id, @source_member_id,
  @unit_type, @local_unit_id, @title, @summary, @content,
  @payload, @sanitized_payload, @content_hash, @bundle_hash, @bundle_ref,
  @sensitivity, @confidence, @suggested_scope, @evidence, @applies,
  @tags, @tools, @task_types, @project_types, @languages, @frameworks,
  @source_created_at
)
ON CONFLICT (workspace_id, source_agent_id, local_unit_id) DO UPDATE SET
  unit_type = EXCLUDED.unit_type,
  source_member_id = EXCLUDED.source_member_id,
  title = EXCLUDED.title,
  summary = EXCLUDED.summary,
  content = EXCLUDED.content,
  payload = EXCLUDED.payload,
  sanitized_payload = EXCLUDED.sanitized_payload,
  content_hash = EXCLUDED.content_hash,
  bundle_hash = EXCLUDED.bundle_hash,
  bundle_ref = EXCLUDED.bundle_ref,
  sensitivity = EXCLUDED.sensitivity,
  confidence = EXCLUDED.confidence,
  suggested_scope = EXCLUDED.suggested_scope,
  evidence = EXCLUDED.evidence,
  applies = EXCLUDED.applies,
  tags = EXCLUDED.tags,
  tools = EXCLUDED.tools,
  task_types = EXCLUDED.task_types,
  project_types = EXCLUDED.project_types,
  languages = EXCLUDED.languages,
  frameworks = EXCLUDED.frameworks,
  source_created_at = EXCLUDED.source_created_at,
  updated_at = now()
RETURNING *;

-- name: DeleteEvolutionSubmissionFiles :exec
DELETE FROM evolution_unit_submission_file
WHERE workspace_id = @workspace_id AND submission_id = @submission_id;

-- name: UpsertEvolutionSubmissionFile :one
INSERT INTO evolution_unit_submission_file (
  workspace_id, submission_id, path, content, content_hash, mime_type, size_bytes
) VALUES (
  @workspace_id, @submission_id, @path, @content, @content_hash, @mime_type, @size_bytes
)
ON CONFLICT (submission_id, path) DO UPDATE SET
  content = EXCLUDED.content,
  content_hash = EXCLUDED.content_hash,
  mime_type = EXCLUDED.mime_type,
  size_bytes = EXCLUDED.size_bytes
RETURNING *;

-- name: GetEvolutionUnitSubmissionInWorkspace :one
SELECT * FROM evolution_unit_submission
WHERE id = @id AND workspace_id = @workspace_id;

-- name: ListEvolutionUnitSubmissionsByWorkspace :many
SELECT * FROM evolution_unit_submission
WHERE workspace_id = @workspace_id
ORDER BY created_at DESC
LIMIT @limit_count;

-- name: ListActiveSharedEvolutionUnitsByWorkspace :many
SELECT * FROM shared_evolution_unit
WHERE workspace_id = @workspace_id AND status = 'active'
ORDER BY priority DESC, score DESC, updated_at DESC
LIMIT @limit_count;

-- name: MaxSharedEvolutionUnitVersion :one
SELECT COALESCE(MAX(version), 0)::int
FROM shared_evolution_unit_version
WHERE workspace_id = @workspace_id AND unit_id = @unit_id;

-- name: ListCandidateEvolutionSubmissions :many
SELECT * FROM evolution_unit_submission
WHERE workspace_id = @workspace_id AND status = 'candidate'
ORDER BY created_at ASC
LIMIT @limit_count;

-- name: ListEvolutionSubmissionFiles :many
SELECT * FROM evolution_unit_submission_file
WHERE workspace_id = @workspace_id AND submission_id = @submission_id
ORDER BY path ASC;

-- name: RejectEvolutionSubmission :one
UPDATE evolution_unit_submission
SET status = 'rejected', reject_reason = @reject_reason, updated_at = now()
WHERE id = @id AND workspace_id = @workspace_id
RETURNING *;

-- name: RejectEvolutionSubmissionWithReview :one
UPDATE evolution_unit_submission
SET status = 'rejected',
    reject_reason = @reject_reason,
    review_decision = @review_decision,
    review_confidence = @review_confidence,
    review_risk_level = @review_risk_level,
    review_reason = @review_reason,
    review_metadata = @review_metadata,
    reviewed_at = now(),
    updated_at = now()
WHERE id = @id AND workspace_id = @workspace_id
RETURNING *;

-- name: MarkEvolutionSubmissionNeedsReview :one
UPDATE evolution_unit_submission
SET status = 'needs_review',
    review_decision = @review_decision,
    review_confidence = @review_confidence,
    review_risk_level = @review_risk_level,
    review_reason = @review_reason,
    review_metadata = @review_metadata,
    reviewed_at = now(),
    updated_at = now()
WHERE id = @id AND workspace_id = @workspace_id
RETURNING *;

-- name: ListEvolutionSubmissionsForReview :many
SELECT * FROM evolution_unit_submission
WHERE workspace_id = @workspace_id
  AND status = @status
ORDER BY updated_at DESC
LIMIT @limit_count;

-- name: FindSharedEvolutionUnitByHash :one
SELECT * FROM shared_evolution_unit
WHERE workspace_id = @workspace_id
  AND unit_type = @unit_type
  AND metadata->>'dedupe_hash' = @dedupe_hash
LIMIT 1;

-- name: CreateSharedEvolutionUnit :one
INSERT INTO shared_evolution_unit (
  workspace_id, unit_type, title, canonical_summary, content, metadata, applies,
  scope, tags, tools, task_types, project_types, languages, frameworks, priority, score, status
) VALUES (
  @workspace_id, @unit_type, @title, @canonical_summary, @content, @metadata, @applies,
  @scope, @tags, @tools, @task_types, @project_types, @languages, @frameworks, @priority, @score, 'active'
)
RETURNING *;

-- name: CreateSharedEvolutionUnitVersion :one
INSERT INTO shared_evolution_unit_version (
  workspace_id, unit_id, version, title, content, metadata, applies, source_submission_ids, change_reason
) VALUES (
  @workspace_id, @unit_id, @version, @title, @content, @metadata, @applies, ARRAY[@submission_id]::uuid[], @change_reason
)
RETURNING *;

-- name: SetSharedEvolutionUnitCurrentVersion :one
UPDATE shared_evolution_unit
SET current_version_id = @current_version_id, updated_at = now()
WHERE id = @id AND workspace_id = @workspace_id
RETURNING *;

-- name: MarkEvolutionSubmissionPromoted :one
UPDATE evolution_unit_submission
SET status = 'promoted', promoted_unit_id = @promoted_unit_id, updated_at = now()
WHERE id = @id AND workspace_id = @workspace_id
RETURNING *;

-- name: MarkEvolutionSubmissionPromotedWithReview :one
UPDATE evolution_unit_submission
SET status = 'promoted',
    promoted_unit_id = @promoted_unit_id,
    review_decision = @review_decision,
    review_confidence = @review_confidence,
    review_risk_level = @review_risk_level,
    review_reason = @review_reason,
    review_metadata = @review_metadata,
    reviewed_at = now(),
    updated_at = now()
WHERE id = @id AND workspace_id = @workspace_id
RETURNING *;

-- name: UpsertSharedEvolutionUnitFile :one
INSERT INTO shared_evolution_unit_file (
  workspace_id, unit_id, version_id, path, content, content_hash, mime_type, size_bytes
) VALUES (
  @workspace_id, @unit_id, @version_id, @path, @content, @content_hash, @mime_type, @size_bytes
)
ON CONFLICT (unit_id, version_id, path) DO UPDATE SET
  content = EXCLUDED.content,
  content_hash = EXCLUDED.content_hash,
  mime_type = EXCLUDED.mime_type,
  size_bytes = EXCLUDED.size_bytes
RETURNING *;

-- name: CreateEvolutionDelivery :one
INSERT INTO evolution_unit_delivery (
  workspace_id, unit_id, version_id, target_agent_id, delivery_type, reason, matcher_score, matcher_details
) VALUES (
  @workspace_id, @unit_id, @version_id, @target_agent_id, @delivery_type, @reason, @matcher_score, @matcher_details
)
ON CONFLICT (unit_id, version_id, target_agent_id) DO UPDATE SET
  updated_at = evolution_unit_delivery.updated_at
RETURNING *;

-- name: ListEvolutionDeliveriesForRepairByAgent :many
SELECT
  d.*,
  u.unit_type, u.title, u.canonical_summary, u.content, u.metadata, u.applies,
  u.tags, u.tools, u.task_types, u.project_types, u.languages, u.frameworks
FROM evolution_unit_delivery d
JOIN shared_evolution_unit u ON u.id = d.unit_id AND u.workspace_id = d.workspace_id
WHERE d.workspace_id = @workspace_id
  AND d.target_agent_id = @target_agent_id
  AND u.status = 'active'
  AND COALESCE(d.delivered_path, '') <> ''
  AND d.delivery_type = 'generated'
  AND u.unit_type = 'skill'
  AND d.status = 'accepted'
  AND POSITION('/skills/enabled/' IN replace(d.delivered_path, chr(92), '/')) > 0
ORDER BY d.updated_at DESC, d.created_at DESC
LIMIT @limit_count;

-- name: ListPendingEvolutionDeliveryTargetAgentIDsByWorkspace :many
SELECT DISTINCT d.target_agent_id
FROM evolution_unit_delivery d
JOIN shared_evolution_unit u ON u.id = d.unit_id AND u.workspace_id = d.workspace_id
WHERE d.workspace_id = @workspace_id
  AND u.status = 'active'
  AND (
    d.status = 'pending'
    OR (
      d.status = 'accepted'
      AND d.delivery_type = 'generated'
      AND u.unit_type = 'skill'
      AND (d.delivered_path IS NULL OR POSITION('/skills/enabled/' IN replace(d.delivered_path, chr(92), '/')) = 0)
    )
    OR (
      d.status = 'accepted'
      AND d.delivery_type = 'generated'
      AND u.unit_type = 'skill'
      AND COALESCE(d.delivered_path, '') <> ''
      AND POSITION('/skills/enabled/' IN replace(d.delivered_path, chr(92), '/')) > 0
    )
  );

-- name: ListPendingEvolutionDeliveriesByAgent :many
SELECT
  d.*,
  u.unit_type, u.title, u.canonical_summary, u.content, u.metadata, u.applies,
  u.tags, u.tools, u.task_types, u.project_types, u.languages, u.frameworks
FROM evolution_unit_delivery d
JOIN shared_evolution_unit u ON u.id = d.unit_id AND u.workspace_id = d.workspace_id
WHERE d.workspace_id = @workspace_id
  AND d.target_agent_id = @target_agent_id
  AND u.status = 'active'
  AND (
    d.status = 'pending'
    OR (
      d.status = 'accepted'
      AND d.delivery_type = 'generated'
      AND u.unit_type = 'skill'
      AND (d.delivered_path IS NULL OR POSITION('/skills/enabled/' IN replace(d.delivered_path, chr(92), '/')) = 0)
    )
  )
ORDER BY d.matcher_score DESC, d.created_at ASC
LIMIT @limit_count;

-- name: ListSharedEvolutionUnitFiles :many
SELECT * FROM shared_evolution_unit_file
WHERE workspace_id = @workspace_id AND unit_id = @unit_id AND version_id = @version_id
ORDER BY path ASC;

-- name: ListGeneratedEvolutionSkillDeliveriesByAgent :many
SELECT
  d.*,
  u.unit_type, u.title, u.canonical_summary, u.content, u.metadata, u.applies,
  u.tags, u.tools, u.task_types, u.project_types, u.languages, u.frameworks
FROM evolution_unit_delivery d
JOIN shared_evolution_unit u ON u.id = d.unit_id AND u.workspace_id = d.workspace_id
WHERE d.workspace_id = @workspace_id
  AND d.target_agent_id = @target_agent_id
  AND d.delivery_type = 'generated'
  AND u.unit_type = 'skill'
  AND u.status = 'active'
ORDER BY d.updated_at DESC, d.created_at DESC
LIMIT @limit_count;

-- name: ListEvolutionMemoryDeliveriesByAgent :many
SELECT
  d.*,
  u.unit_type, u.title, u.canonical_summary, u.content, u.metadata, u.applies,
  u.tags, u.tools, u.task_types, u.project_types, u.languages, u.frameworks
FROM evolution_unit_delivery d
JOIN shared_evolution_unit u ON u.id = d.unit_id AND u.workspace_id = d.workspace_id
WHERE d.workspace_id = @workspace_id
  AND d.target_agent_id = @target_agent_id
  AND d.delivery_type = 'inbox'
  AND u.unit_type IN ('memory', 'preference', 'tool_pattern', 'workflow')
  AND u.status = 'active'
ORDER BY d.updated_at DESC, d.created_at DESC
LIMIT @limit_count;

-- name: MarkEvolutionDeliveryDelivered :one
UPDATE evolution_unit_delivery
SET status = CASE WHEN status = 'accepted' THEN status ELSE 'delivered' END,
    delivered_path = @delivered_path, delivered_at = now(), updated_at = now()
WHERE id = @id AND workspace_id = @workspace_id AND target_agent_id = @target_agent_id
RETURNING *;

-- name: FailEvolutionDelivery :one
UPDATE evolution_unit_delivery
SET status = 'failed', error = @error, updated_at = now()
WHERE id = @id AND workspace_id = @workspace_id AND target_agent_id = @target_agent_id
RETURNING *;

-- name: UpdateEvolutionDeliveryDecision :one
UPDATE evolution_unit_delivery
SET status = @status, decided_at = now(), updated_at = now()
WHERE id = @id AND workspace_id = @workspace_id AND target_agent_id = @target_agent_id
  AND @status IN ('accepted', 'ignored', 'rejected')
RETURNING *;

-- name: UpdateGeneratedEvolutionSkillDeliveryDecision :one
UPDATE evolution_unit_delivery d
SET status = @status, decided_at = now(), updated_at = now()
FROM shared_evolution_unit u
WHERE d.id = @id
  AND d.workspace_id = @workspace_id
  AND d.target_agent_id = @target_agent_id
  AND d.unit_id = u.id
  AND d.workspace_id = u.workspace_id
  AND d.delivery_type = 'generated'
  AND u.unit_type = 'skill'
  AND @status IN ('accepted', 'ignored', 'rejected')
RETURNING d.*;
