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

-- name: GetEvolutionSubmissionForReview :one
SELECT * FROM evolution_unit_submission
WHERE id = @id AND workspace_id = @workspace_id AND status = 'needs_review'
FOR UPDATE;

-- name: ClaimEvolutionCandidate :one
UPDATE evolution_unit_submission
SET status = 'clustered',
    review_metadata = jsonb_set(review_metadata, '{candidate_claim}', jsonb_build_object('token', @claim_token::text), true),
    updated_at = now()
WHERE id = @id AND workspace_id = @workspace_id
  AND (status = 'candidate' OR (status = 'clustered' AND updated_at < now() - interval '5 minutes'))
RETURNING *;

-- name: ReleaseEvolutionCandidate :exec
UPDATE evolution_unit_submission
SET status = 'candidate',
    review_metadata = review_metadata - 'candidate_claim',
    updated_at = now()
WHERE id = @id AND workspace_id = @workspace_id AND status = 'clustered'
  AND review_metadata->'candidate_claim'->>'token' = @claim_token;

-- name: ListEvolutionSubmissionFiles :many
SELECT * FROM evolution_unit_submission_file
WHERE workspace_id = @workspace_id AND submission_id = @submission_id
ORDER BY path ASC;

-- name: RejectEvolutionSubmission :one
UPDATE evolution_unit_submission
SET status = 'rejected', reject_reason = @reject_reason, updated_at = now()
WHERE id = @id AND workspace_id = @workspace_id AND status = 'clustered'
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
WHERE id = @id AND workspace_id = @workspace_id AND status IN ('clustered', 'needs_review')
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
WHERE id = @id AND workspace_id = @workspace_id AND status = 'clustered'
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
  @workspace_id, @unit_id, @version, @title, @content, @metadata, @applies, @submission_id::uuid[], @change_reason
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
SET status = 'promoted', promoted_unit_id = @promoted_unit_id,
    review_metadata = review_metadata - 'candidate_claim', updated_at = now()
WHERE id = @id AND workspace_id = @workspace_id AND status = 'clustered'
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
WHERE id = @id AND workspace_id = @workspace_id AND status IN ('clustered', 'needs_review')
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

-- name: GetSharedEvolutionUnitCurrentVersionID :one
SELECT current_version_id
FROM shared_evolution_unit
WHERE workspace_id = @workspace_id AND id = @unit_id AND unit_type = 'skill' AND status = 'active';

-- name: RecordEvolutionSkillInjection :exec
INSERT INTO evolution_unit_feedback_event (
  workspace_id, agent_id, task_id, unit_type, unit_id, event, outcome, source, metadata
)
SELECT @workspace_id, @agent_id, @task_id, 'skill', @unit_id, 'injected', '', 'runtime',
       jsonb_build_object('version_id', @version_id::uuid, 'execution_id', @execution_id::uuid)
WHERE NOT EXISTS (
  SELECT 1 FROM evolution_unit_feedback_event
  WHERE unit_type = 'skill' AND unit_id = @unit_id AND event = 'injected'
    AND metadata->>'execution_id' = @execution_id::text
    AND metadata->>'version_id' = @version_id::text
);

-- name: RecordEvolutionSkillOutcome :exec
INSERT INTO evolution_unit_feedback_event (
  workspace_id, agent_id, task_id, unit_type, unit_id, event, outcome, source, metadata
)
SELECT DISTINCT workspace_id, agent_id, task_id, unit_type, unit_id,
       @event::text, @outcome::text, 'runtime',
       jsonb_build_object('version_id', metadata->>'version_id', 'execution_id', @execution_id::uuid)
FROM evolution_unit_feedback_event
WHERE unit_type = 'skill'
  AND event = 'injected'
  AND metadata->>'execution_id' = @execution_id::text
  AND COALESCE(metadata->>'version_id', '') <> ''
  AND NOT EXISTS (
    SELECT 1 FROM evolution_unit_feedback_event recorded
    WHERE recorded.unit_type = evolution_unit_feedback_event.unit_type
      AND recorded.unit_id = evolution_unit_feedback_event.unit_id
      AND recorded.event = @event::text
      AND recorded.outcome = @outcome::text
      AND recorded.metadata->>'execution_id' = @execution_id::text
      AND recorded.metadata->>'version_id' = evolution_unit_feedback_event.metadata->>'version_id'
  );

-- name: RecordEvolutionMemoryInjection :exec
-- LRM-984: claim-time proof that a memory was retrieved into the run.
INSERT INTO evolution_unit_feedback_event (
  workspace_id, agent_id, task_id, unit_type, unit_id, local_unit_id, event, outcome, source, metadata
)
SELECT @workspace_id, @agent_id, @task_id, 'memory', @unit_id, @local_unit_id,
       'injected', '', 'runtime',
       jsonb_build_object(
         'execution_id', @execution_id::uuid,
         'sync_key', @sync_key::text,
         'scope', @scope::text,
         'inbox_event_id', @execution_id::uuid
       )
WHERE NOT EXISTS (
  SELECT 1 FROM evolution_unit_feedback_event
  WHERE unit_type = 'memory' AND unit_id = @unit_id AND event = 'injected'
    AND metadata->>'execution_id' = @execution_id::text
);

-- name: RecordEvolutionUnitUsed :exec
-- LRM-984: mark every memory/skill injected for this execution as used when
-- the run completes successfully (auditable unit_id + execution_id).
INSERT INTO evolution_unit_feedback_event (
  workspace_id, agent_id, task_id, unit_type, unit_id, local_unit_id, event, outcome, source, metadata
)
SELECT DISTINCT workspace_id, agent_id, task_id, unit_type, unit_id, local_unit_id,
       'used', '', 'runtime',
       jsonb_build_object(
         'execution_id', @execution_id::uuid,
         'version_id', metadata->>'version_id',
         'sync_key', metadata->>'sync_key',
         'scope', metadata->>'scope',
         'inbox_event_id', @execution_id::uuid
       )
FROM evolution_unit_feedback_event
WHERE event = 'injected'
  AND unit_type IN ('memory', 'skill')
  AND metadata->>'execution_id' = @execution_id::text
  AND NOT EXISTS (
    SELECT 1 FROM evolution_unit_feedback_event recorded
    WHERE recorded.unit_type = evolution_unit_feedback_event.unit_type
      AND recorded.unit_id IS NOT DISTINCT FROM evolution_unit_feedback_event.unit_id
      AND recorded.local_unit_id = evolution_unit_feedback_event.local_unit_id
      AND recorded.event = 'used'
      AND recorded.metadata->>'execution_id' = @execution_id::text
  );
