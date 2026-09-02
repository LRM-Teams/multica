-- Skill evolution ledger (spec §12.4, ADR 0021; migration 492).
--
-- Status transitions are CAS-shaped (the WHERE pins the expected current
-- status); zero affected rows is a conflict the caller must resolve, never
-- a silent no-op. Pattern revisions and candidate artifacts are INSERT-only
-- (DB triggers); the pattern parent row advances its revision pointer by
-- CAS exactly like the shared_evolution_unit current-version pointer.

-- name: InsertSkillEvolutionRun :execrows
INSERT INTO skill_evolution_run (
    id, workspace_id, target_agent_id, task_type, environment_major_version,
    status, pinned_inputs, created_by_actor
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8);

-- name: GetSkillEvolutionRun :one
SELECT * FROM skill_evolution_run
WHERE workspace_id = $1 AND id = $2;

-- name: ListSkillEvolutionRunsByKey :many
SELECT * FROM skill_evolution_run
WHERE workspace_id = $1 AND evolution_key = $2
ORDER BY created_at DESC;

-- name: ListActiveSkillEvolutionRunsByKey :many
-- Row-locked for the single-active-run admission check (the partial unique
-- index lands with migration 494; until then this is the serialized gate).
SELECT * FROM skill_evolution_run
WHERE workspace_id = $1 AND evolution_key = $2
  AND status IN ('queued', 'snapshotting', 'consolidating_patterns',
                 'proposing_candidate', 'awaiting_review', 'evaluating',
                 'awaiting_approval')
ORDER BY created_at DESC
FOR UPDATE;

-- name: TransitionSkillEvolutionRunStatus :execrows
UPDATE skill_evolution_run
SET status = $3
WHERE workspace_id = $1 AND id = $2 AND status = $4;

-- name: InsertSkillPatternIdentity :execrows
INSERT INTO skill_pattern (workspace_id, pattern_id, evolution_key, task_type, current_revision)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (workspace_id, pattern_id) DO NOTHING;

-- name: GetSkillPattern :one
SELECT * FROM skill_pattern
WHERE workspace_id = $1 AND pattern_id = $2;

-- name: AdvanceSkillPatternRevision :execrows
UPDATE skill_pattern
SET current_revision = $3, updated_at = now()
WHERE workspace_id = $1 AND pattern_id = $2 AND current_revision = $4;

-- name: InsertSkillPatternRevision :execrows
INSERT INTO skill_pattern_revision (
    workspace_id, pattern_id, revision, pattern_kind, status,
    problem, applicability, root_cause_summary, recommended_action,
    task_type, source_model_id, target_model_id, provider_id,
    tool_capability_id, runtime_id, environment_key,
    generator_version, policy_version, content_hash, created_by_actor
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20);

-- name: GetLatestSkillPatternRevision :one
SELECT * FROM skill_pattern_revision
WHERE workspace_id = $1 AND pattern_id = $2
ORDER BY revision DESC
LIMIT 1;

-- name: InsertSkillPatternEvidence :execrows
INSERT INTO skill_pattern_evidence (
    workspace_id, pattern_id, revision, polarity, ref_kind, ref_id, ref_workspace_id
) VALUES ($1, $2, $3, $4, $5, $6, $7);

-- name: ListSkillPatternEvidence :many
SELECT * FROM skill_pattern_evidence
WHERE workspace_id = $1 AND pattern_id = $2 AND revision = $3
ORDER BY polarity, ref_kind, ref_id;

-- name: InsertSkillCandidate :execrows
INSERT INTO skill_candidate (
    workspace_id, candidate_id, run_id, target_skill_id, new_skill_name,
    requested_scope, base_artifact_hash, candidate_artifact_hash,
    proposed_diff_hash, contract_hash, contract, status, current_artifact_version
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13);

-- name: GetSkillCandidate :one
SELECT * FROM skill_candidate
WHERE workspace_id = $1 AND candidate_id = $2;

-- name: TransitionSkillCandidateStatus :execrows
UPDATE skill_candidate
SET status = $3
WHERE workspace_id = $1 AND candidate_id = $2 AND status = $4;

-- name: InsertSkillCandidateArtifact :execrows
INSERT INTO skill_candidate_artifact (
    workspace_id, candidate_id, version, artifact_hash, storage_ref, created_by_actor
) VALUES ($1, $2, $3, $4, $5, $6);

-- name: GetSkillCandidateArtifact :one
SELECT * FROM skill_candidate_artifact
WHERE workspace_id = $1 AND candidate_id = $2 AND version = $3;

-- name: InsertSkillCandidatePattern :execrows
INSERT INTO skill_candidate_pattern (workspace_id, candidate_id, pattern_id)
VALUES ($1, $2, $3);

-- Evaluation plane (migration 493).

-- name: InsertSkillAssertionManifest :execrows
-- ON CONFLICT DO NOTHING backs manifest replay: zero rows means the version
-- already exists and the caller must compare hashes to tell an identical
-- replay from a conflicting payload.
INSERT INTO skill_assertion_manifest (
    workspace_id, manifest_id, version, manifest_hash,
    dataset_identity, dataset_version, lineage_split, domain_profile,
    task_slices, evaluator_version, scorer_version, environment_key,
    required_capabilities, data_residency, contract, created_by_actor
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16)
ON CONFLICT (workspace_id, manifest_id, version) DO NOTHING;

-- name: GetSkillAssertionManifest :one
SELECT * FROM skill_assertion_manifest
WHERE workspace_id = $1 AND manifest_id = $2 AND version = $3;

-- name: InsertSkillAssertion :execrows
INSERT INTO skill_assertion (
    workspace_id, manifest_id, manifest_version, assertion_id,
    kind, oracle_ref_hash, severity, hard, required, tolerance
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
ON CONFLICT (workspace_id, manifest_id, manifest_version, assertion_id) DO NOTHING;

-- name: ListSkillAssertions :many
SELECT * FROM skill_assertion
WHERE workspace_id = $1 AND manifest_id = $2 AND manifest_version = $3
ORDER BY assertion_id;

-- name: InsertSkillEvaluationRun :execrows
INSERT INTO skill_evaluation_run (
    workspace_id, evaluation_id, candidate_id, manifest_id, manifest_version,
    base_artifact_hash, candidate_artifact_hash, manifest_hash,
    target_agent_id, target_model_id, provider_id, tool_capability_id,
    runtime_id, environment_key, metrics, contamination_status,
    decision_policy_version, terminal_result, terminal_reason, created_by_actor
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20);

-- name: GetSkillEvaluationRun :one
SELECT * FROM skill_evaluation_run
WHERE workspace_id = $1 AND evaluation_id = $2;

-- name: ListSkillEvaluationRunsByCandidate :many
SELECT * FROM skill_evaluation_run
WHERE workspace_id = $1 AND candidate_id = $2
ORDER BY created_at DESC;

-- name: InsertSkillEvaluationAssertionResult :execrows
INSERT INTO skill_evaluation_assertion_result (
    workspace_id, evaluation_id, manifest_id, manifest_version,
    assertion_id, result, evidence_hash
) VALUES ($1, $2, $3, $4, $5, $6, $7);

-- name: ListSkillEvaluationAssertionResults :many
SELECT * FROM skill_evaluation_assertion_result
WHERE workspace_id = $1 AND evaluation_id = $2
ORDER BY assertion_id;

-- Decision/deployment plane (migration 494).

-- name: InsertSkillApproval :execrows
INSERT INTO skill_approval (
    workspace_id, approval_id, candidate_id, evaluation_id,
    manifest_hash, policy_hash, artifact_hash, target_scope, decision,
    approver_actor, approver_role, reason, risk_acknowledged,
    allow_auto_rollback, expires_at
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15);

-- name: GetSkillApproval :one
SELECT * FROM skill_approval
WHERE workspace_id = $1 AND approval_id = $2;

-- name: InsertSkillDeployment :execrows
INSERT INTO skill_deployment (
    workspace_id, deployment_id, candidate_id, approval_id, target_scope,
    target_agent_id, target_channel_id, binding_state_before, binding_state_after,
    from_artifact_hash, to_artifact_hash, materialization_status, created_by_actor
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13);

-- name: GetSkillDeployment :one
SELECT * FROM skill_deployment
WHERE workspace_id = $1 AND deployment_id = $2;

-- name: TransitionSkillDeploymentMaterialization :execrows
UPDATE skill_deployment
SET materialization_status = $3
WHERE workspace_id = $1 AND deployment_id = $2 AND materialization_status = $4;

-- name: InsertSkillRollback :execrows
INSERT INTO skill_rollback (
    workspace_id, rollback_id, deployment_id, rollback_trigger,
    from_artifact_hash, to_artifact_hash, in_flight_policy, actor,
    policy_version, roll_forward_status
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10);

-- name: GetSkillRollback :one
SELECT * FROM skill_rollback
WHERE workspace_id = $1 AND rollback_id = $2;

-- name: SetSkillRollForwardStatus :execrows
UPDATE skill_rollback
SET roll_forward_status = $3
WHERE workspace_id = $1 AND rollback_id = $2 AND roll_forward_status = $4;

-- Idempotency (migration 494). The row-locked read serializes claims
-- through this store; the PK is the hard fence.

-- name: GetSkillEvolutionIdempotency :one
SELECT * FROM skill_evolution_idempotency
WHERE workspace_id = $1 AND idempotency_key = $2
FOR UPDATE;

-- name: InsertSkillEvolutionIdempotency :execrows
INSERT INTO skill_evolution_idempotency (
    workspace_id, idempotency_key, request_kind, payload_hash, response
) VALUES ($1, $2, $3, $4, $5);

-- Outbox / reconciliation (migration 494).

-- name: InsertSkillEvolutionOutbox :execrows
INSERT INTO skill_evolution_outbox (
    workspace_id, aggregate_kind, aggregate_id, event_type, payload
) VALUES ($1, $2, $3, $4, $5);

-- name: ListPendingSkillEvolutionOutbox :many
SELECT * FROM skill_evolution_outbox
WHERE workspace_id = $1 AND dispatched_at IS NULL
ORDER BY created_at, id
LIMIT $2;

-- name: MarkSkillEvolutionOutboxDispatched :execrows
UPDATE skill_evolution_outbox
SET dispatched_at = now()
WHERE id = $1 AND workspace_id = $2 AND dispatched_at IS NULL;

-- name: NoteSkillEvolutionOutboxFailure :execrows
UPDATE skill_evolution_outbox
SET dispatch_attempts = dispatch_attempts + 1, last_error = $3
WHERE id = $1 AND workspace_id = $2 AND dispatched_at IS NULL;

-- name: UpsertSkillEvolutionReconciliation :exec
INSERT INTO skill_evolution_reconciliation (
    workspace_id, lane, last_reconciled_at, pending_count
) VALUES ($1, $2, $3, $4)
ON CONFLICT (workspace_id, lane) DO UPDATE SET
    last_reconciled_at = EXCLUDED.last_reconciled_at,
    pending_count = EXCLUDED.pending_count,
    updated_at = now();

-- Trajectory eligibility ledger + backfill checkpoints (migration 496).

-- name: InsertSkillTrajectoryEligibility :execrows
-- Pins eligibility at run start (spec §12.2). The update guard trigger
-- freezes every pinned column; revocation is the only later write.
INSERT INTO skill_trajectory_eligibility (
    workspace_id, run_id, run_kind, evolution_eligible, allowed_purposes,
    task_type, lineage_id, fixed_at, fixed_by_actor,
    revoked_by_actor, revoked_at, revoked_reason
) VALUES (
    $1, $2, $3, $4, $5,
    $6, $7, $8, $9,
    '', NULL, ''
)
ON CONFLICT (workspace_id, run_id) DO NOTHING;

-- name: GetSkillTrajectoryEligibility :one
SELECT workspace_id, run_id, run_kind, evolution_eligible, allowed_purposes,
       task_type, lineage_id, fixed_at, fixed_by_actor,
       revoked_by_actor, revoked_at, revoked_reason, created_at
FROM skill_trajectory_eligibility
WHERE workspace_id = $1 AND run_id = $2;

-- name: RevokeSkillTrajectoryEligibility :execrows
-- Revocation-only CAS: a row already revoked is left untouched; a live
-- row must flip eligible=false in the same statement or the update guard
-- trigger rejects it.
UPDATE skill_trajectory_eligibility
SET evolution_eligible = false,
    revoked_by_actor = $3,
    revoked_at = $4,
    revoked_reason = $5
WHERE workspace_id = $1 AND run_id = $2 AND revoked_at IS NULL;

-- name: InsertSkillBackfillCheckpoint :execrows
-- Append-only job report (spec §12.12): dry-run and executed passes both
-- record an audited checkpoint; the UPDATE/DELETE guard trigger enforces
-- immutability.
INSERT INTO skill_backfill_checkpoint (
    workspace_id, job_id, kind, mode, actor, policy_version,
    source_watermark, selected_count, rejected_count, reason
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
ON CONFLICT (workspace_id, job_id) DO NOTHING;

-- name: GetSkillBackfillCheckpoint :one
SELECT workspace_id, job_id, kind, mode, actor, policy_version,
       source_watermark, selected_count, rejected_count, reason, created_at
FROM skill_backfill_checkpoint
WHERE workspace_id = $1 AND job_id = $2;

-- name: ListSkillBackfillCheckpoints :many
SELECT workspace_id, job_id, kind, mode, actor, policy_version,
       source_watermark, selected_count, rejected_count, reason, created_at
FROM skill_backfill_checkpoint
WHERE workspace_id = $1
ORDER BY created_at DESC
LIMIT $2;

-- Orchestrator leases, active runs, and checkpoint recovery
-- (migration 497, spec §12.6, plan Slice 3.3).

-- name: ListActiveSkillEvolutionRuns :many
SELECT * FROM skill_evolution_run
WHERE workspace_id = $1 AND status IN (
    'queued', 'snapshotting', 'consolidating_patterns', 'proposing_candidate',
    'awaiting_review', 'evaluating', 'awaiting_approval'
)
ORDER BY created_at;

-- name: AcquireSkillEvolutionRunLease :one
-- First acquisition inserts attempt 1; a re-acquisition may only take the
-- lease once the previous one EXPIRED (the partial-update WHERE), and it
-- always increments the fencing token. No row means the lease is held.
INSERT INTO skill_evolution_run_lease (
    workspace_id, run_id, owner_id, attempt, acquired_at, expires_at, heartbeat_at
) VALUES (
    $1, $2, $3, 1, now(), now() + make_interval(secs => $4::double precision), now()
)
ON CONFLICT (workspace_id, run_id) DO UPDATE SET
    owner_id = EXCLUDED.owner_id,
    attempt = skill_evolution_run_lease.attempt + 1,
    acquired_at = now(),
    expires_at = now() + make_interval(secs => $4::double precision),
    heartbeat_at = now()
WHERE skill_evolution_run_lease.expires_at <= now()
RETURNING *;

-- name: RenewSkillEvolutionRunLease :execrows
UPDATE skill_evolution_run_lease
SET expires_at = now() + make_interval(secs => $5::double precision),
    heartbeat_at = now()
WHERE workspace_id = $1 AND run_id = $2 AND owner_id = $3 AND attempt = $4
  AND expires_at > now();

-- name: ReleaseSkillEvolutionRunLease :execrows
UPDATE skill_evolution_run_lease
SET expires_at = now(),
    heartbeat_at = now()
WHERE workspace_id = $1 AND run_id = $2 AND owner_id = $3 AND attempt = $4
  AND expires_at > now();

-- name: GetSkillEvolutionRunLease :one
SELECT * FROM skill_evolution_run_lease
WHERE workspace_id = $1 AND run_id = $2;

-- name: ListSkillEvolutionIdempotencyByKeyPrefix :many
SELECT * FROM skill_evolution_idempotency
WHERE workspace_id = $1 AND idempotency_key LIKE $2 || '%'
ORDER BY created_at DESC
LIMIT $3;

-- name: ListWorkspacesWithActiveSkillEvolutionRuns :many
-- Reconciliation only needs the workspaces that actually have live runs;
-- the rest of the fleet is not swept at all.
SELECT DISTINCT workspace_id FROM skill_evolution_run
WHERE status IN (
    'queued', 'snapshotting', 'consolidating_patterns', 'proposing_candidate',
    'awaiting_review', 'evaluating', 'awaiting_approval'
);
