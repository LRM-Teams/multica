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
WHERE id = @id AND workspace_id = @workspace_id AND status = 'candidate'
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
WHERE id = @id AND workspace_id = @workspace_id AND status IN ('candidate', 'needs_review')
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
WHERE id = @id AND workspace_id = @workspace_id AND status = 'candidate'
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

-- name: SyncSharedEvolutionUnitMatchMetadata :one
UPDATE shared_evolution_unit
SET
  title = @title,
  canonical_summary = @canonical_summary,
  content = @content,
  tags = @tags,
  tools = @tools,
  task_types = @task_types,
  project_types = @project_types,
  languages = @languages,
  frameworks = @frameworks,
  updated_at = now()
WHERE id = @id AND workspace_id = @workspace_id
RETURNING *;

-- name: MarkEvolutionSubmissionPromoted :one
UPDATE evolution_unit_submission
SET status = 'promoted', promoted_unit_id = @promoted_unit_id, updated_at = now()
WHERE id = @id AND workspace_id = @workspace_id AND status = 'candidate'
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
WHERE id = @id AND workspace_id = @workspace_id AND status IN ('candidate', 'needs_review')
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

-- name: ListSharedEvolutionUnitFiles :many
SELECT * FROM shared_evolution_unit_file
WHERE workspace_id = @workspace_id AND unit_id = @unit_id AND version_id = @version_id
ORDER BY path ASC;
