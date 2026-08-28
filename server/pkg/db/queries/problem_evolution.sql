-- name: CreateProblemEvolutionEvaluatorContract :one
INSERT INTO problem_evolution_evaluator_contract (
    workspace_id, mode, status, contract, feedback_policy, created_by
)
VALUES (@workspace_id, @mode, 'draft', @contract, @feedback_policy, @created_by)
RETURNING *;

-- name: GetProblemEvolutionEvaluatorContract :one
SELECT * FROM problem_evolution_evaluator_contract
WHERE id = @id AND workspace_id = @workspace_id;

-- name: UpdateProblemEvolutionEvaluatorContractDraft :one
UPDATE problem_evolution_evaluator_contract
SET contract = @contract,
    feedback_policy = @feedback_policy,
    status = @status,
    updated_at = now()
WHERE id = @id
  AND workspace_id = @workspace_id
  AND status IN ('draft', 'validating')
RETURNING *;

-- name: FreezeProblemEvolutionEvaluatorContract :one
UPDATE problem_evolution_evaluator_contract
SET status = 'frozen',
    content_hash = @content_hash,
    frozen_at = now(),
    updated_at = now()
WHERE id = @id
  AND workspace_id = @workspace_id
  AND status IN ('draft', 'validating')
RETURNING *;

-- name: MarkProblemEvolutionEvaluatorContractInvalid :one
UPDATE problem_evolution_evaluator_contract
SET status = 'invalid', updated_at = now()
WHERE id = @id AND workspace_id = @workspace_id AND status <> 'frozen'
RETURNING *;

-- name: CreateProblemEvolutionRun :one
INSERT INTO problem_evolution_run (
    workspace_id, created_by, mode, title, problem_spec, artifact_type,
    runtime_id, model_config, budget_config, stop_config, task_set_id
)
VALUES (
    @workspace_id, @created_by, @mode, @title, @problem_spec, @artifact_type,
    @runtime_id, @model_config, @budget_config, @stop_config, @task_set_id
)
RETURNING *;

-- name: GetProblemEvolutionRun :one
SELECT * FROM problem_evolution_run
WHERE id = @id AND workspace_id = @workspace_id;

-- name: GetProblemEvolutionRunByID :one
-- Daemon-facing lookup: the caller is authorised against the run's workspace
-- after the row is loaded, because a daemon addresses runs by id alone.
SELECT * FROM problem_evolution_run
WHERE id = @id;

-- name: ListProblemEvolutionRuns :many
SELECT * FROM problem_evolution_run
WHERE workspace_id = @workspace_id
ORDER BY created_at DESC
LIMIT @result_limit;

-- name: UpdateProblemEvolutionRunDraft :one
UPDATE problem_evolution_run
SET title = @title,
    problem_spec = @problem_spec,
    artifact_type = @artifact_type,
    runtime_id = @runtime_id,
    model_config = @model_config,
    budget_config = @budget_config,
    stop_config = @stop_config,
    task_set_id = @task_set_id,
    evaluator_contract_id = @evaluator_contract_id,
    updated_at = now()
WHERE id = @id
  AND workspace_id = @workspace_id
  AND status IN ('draft', 'validating_evaluator', 'ready')
RETURNING *;

-- name: QueueProblemEvolutionRun :one
-- Only a run whose evaluator contract is frozen may enter the queue; the
-- contract hash is pinned here so a later contract edit is detectable.
UPDATE problem_evolution_run run
SET status = 'queued',
    stage = 'queued',
    evaluator_content_hash = contract.content_hash,
    stop_reason = '',
    failure_reason = '',
    updated_at = now()
FROM problem_evolution_evaluator_contract contract
WHERE run.id = @id
  AND run.workspace_id = @workspace_id
  AND run.evaluator_contract_id = contract.id
  AND contract.workspace_id = run.workspace_id
  AND contract.status = 'frozen'
  AND contract.content_hash <> ''
  AND run.status IN ('draft', 'validating_evaluator', 'ready')
RETURNING run.*;

-- name: RequestStopProblemEvolutionRun :one
UPDATE problem_evolution_run
SET status = 'stopping',
    stop_requested_at = now(),
    stop_reason = @stop_reason,
    updated_at = now()
WHERE id = @id
  AND workspace_id = @workspace_id
  AND status IN ('queued', 'running', 'synthesizing')
RETURNING *;

-- name: ClaimProblemEvolutionRun :one
UPDATE problem_evolution_run
SET status = 'running',
    stage = 'claimed',
    claimed_runtime_id = @claimed_runtime_id,
    claim_token = gen_random_uuid(),
    claimed_at = now(),
    heartbeat_at = now(),
    started_at = COALESCE(started_at, now()),
    updated_at = now()
WHERE id = (
    SELECT queued.id FROM problem_evolution_run queued
    WHERE queued.workspace_id = @workspace_id
      AND queued.status = 'queued'
      AND (queued.runtime_id IS NULL OR queued.runtime_id = @claimed_runtime_id)
    ORDER BY queued.created_at
    FOR UPDATE SKIP LOCKED
    LIMIT 1
)
RETURNING *;

-- name: HeartbeatProblemEvolutionRun :one
UPDATE problem_evolution_run
SET heartbeat_at = now(), updated_at = now()
WHERE id = @id AND claim_token = @claim_token
RETURNING *;

-- name: SetProblemEvolutionRunStage :one
UPDATE problem_evolution_run
SET stage = @stage, updated_at = now()
WHERE id = @id AND claim_token = @claim_token
RETURNING *;

-- name: CompleteProblemEvolutionRun :one
UPDATE problem_evolution_run
SET status = 'completed',
    stage = 'completed',
    best_candidate_id = @best_candidate_id,
    final_candidate_id = @final_candidate_id,
    claim_token = NULL,
    finished_at = now(),
    updated_at = now()
WHERE id = @id AND claim_token = @claim_token
RETURNING *;

-- name: FailProblemEvolutionRun :one
UPDATE problem_evolution_run
SET status = 'failed',
    stage = 'failed',
    failure_reason = @failure_reason,
    claim_token = NULL,
    finished_at = now(),
    updated_at = now()
WHERE id = @id AND workspace_id = @workspace_id
RETURNING *;

-- name: CancelProblemEvolutionRun :one
UPDATE problem_evolution_run
SET status = 'cancelled',
    stage = 'cancelled',
    claim_token = NULL,
    finished_at = now(),
    updated_at = now()
WHERE id = @id AND workspace_id = @workspace_id AND status = 'stopping'
RETURNING *;

-- name: ReleaseProblemEvolutionRun :one
UPDATE problem_evolution_run
SET status = 'queued',
    stage = 'queued',
    claimed_runtime_id = NULL,
    claim_token = NULL,
    claimed_at = NULL,
    heartbeat_at = NULL,
    updated_at = now()
WHERE id = @id AND claim_token = @claim_token
RETURNING *;

-- name: BumpProblemEvolutionRunGraphVersion :one
UPDATE problem_evolution_run
SET graph_version = graph_version + 1, updated_at = now()
WHERE id = @id
RETURNING graph_version;

-- name: SetProblemEvolutionRunEvolverVersion :one
UPDATE problem_evolution_run
SET evolver_version = @evolver_version, updated_at = now()
WHERE id = @id AND claim_token = @claim_token
RETURNING *;

-- name: ListStaleProblemEvolutionRuns :many
SELECT * FROM problem_evolution_run
WHERE status IN ('running', 'synthesizing', 'stopping')
  AND heartbeat_at IS NOT NULL
  AND heartbeat_at < @stale_before
ORDER BY heartbeat_at
LIMIT @result_limit;

-- name: ListStopRequestedProblemEvolutionRuns :many
SELECT * FROM problem_evolution_run
WHERE status = 'stopping'
  AND stop_requested_at IS NOT NULL
  AND stop_requested_at < @deadline
ORDER BY stop_requested_at
LIMIT @result_limit;

-- name: UpsertProblemEvolutionCandidate :one
INSERT INTO problem_evolution_candidate (
    run_id, workspace_id, external_ref, generation, lane, operator, status
)
VALUES (
    @run_id, @workspace_id, @external_ref, @generation, @lane, @operator, @status
)
ON CONFLICT (run_id, external_ref)
DO UPDATE SET
    lane = EXCLUDED.lane,
    operator = EXCLUDED.operator,
    generation = EXCLUDED.generation,
    updated_at = now()
RETURNING *;

-- name: EnsureProblemEvolutionCandidate :one
-- Placeholder row for a candidate referenced before its own event arrived (an
-- out-of-order parent). Unlike the upsert, it must never overwrite lane,
-- operator or generation on an already-known candidate.
INSERT INTO problem_evolution_candidate (
    run_id, workspace_id, external_ref, generation, lane, operator, status
)
VALUES (
    @run_id, @workspace_id, @external_ref, 0, 'baseline', 'baseline', 'producing'
)
ON CONFLICT (run_id, external_ref)
DO UPDATE SET updated_at = problem_evolution_candidate.updated_at
RETURNING *;

-- name: GetProblemEvolutionCandidateByRef :one
SELECT * FROM problem_evolution_candidate
WHERE run_id = @run_id AND external_ref = @external_ref;

-- name: GetProblemEvolutionCandidate :one
SELECT * FROM problem_evolution_candidate
WHERE id = @id AND workspace_id = @workspace_id;

-- name: ListProblemEvolutionCandidates :many
SELECT * FROM problem_evolution_candidate
WHERE run_id = @run_id
ORDER BY generation, created_at;

-- name: SetProblemEvolutionCandidateStatus :one
UPDATE problem_evolution_candidate
SET status = @status,
    failure_class = @failure_class,
    updated_at = now()
WHERE run_id = @run_id AND external_ref = @external_ref
RETURNING *;

-- name: SetProblemEvolutionCandidateScore :one
UPDATE problem_evolution_candidate
SET score = @score,
    behavior_profile = @behavior_profile,
    status = @status,
    updated_at = now()
WHERE run_id = @run_id AND external_ref = @external_ref
RETURNING *;

-- name: SetProblemEvolutionCandidateArtifact :one
UPDATE problem_evolution_candidate
SET artifact_ref = @artifact_ref,
    artifact_hash = @artifact_hash,
    summary = @summary,
    updated_at = now()
WHERE run_id = @run_id AND external_ref = @external_ref
RETURNING *;

-- name: SetProblemEvolutionCandidateUsage :one
UPDATE problem_evolution_candidate
SET runtime_seconds = @runtime_seconds,
    token_usage = @token_usage,
    cost = @cost,
    updated_at = now()
WHERE run_id = @run_id AND external_ref = @external_ref
RETURNING *;

-- name: SetProblemEvolutionRunBestCandidate :one
UPDATE problem_evolution_run
SET best_candidate_id = @best_candidate_id, updated_at = now()
WHERE id = @id
RETURNING *;

-- name: InsertProblemEvolutionEvent :one
-- seq is server-owned: allocated inside this statement so parallel candidates
-- cannot mint conflicting sequences on the daemon side.
INSERT INTO problem_evolution_event (
    run_id, workspace_id, seq, client_event_id, event_type, candidate_id,
    actor_type, actor_id, payload
)
SELECT
    @run_id,
    @workspace_id,
    COALESCE((SELECT max(seq) FROM problem_evolution_event WHERE run_id = @run_id), 0) + 1,
    @client_event_id,
    @event_type,
    @candidate_id,
    @actor_type,
    @actor_id,
    @payload
ON CONFLICT (run_id, client_event_id) DO NOTHING
RETURNING *;

-- name: GetProblemEvolutionEventByClientID :one
SELECT * FROM problem_evolution_event
WHERE run_id = @run_id AND client_event_id = @client_event_id;

-- name: ListProblemEvolutionEventsAfterSeq :many
SELECT * FROM problem_evolution_event
WHERE run_id = @run_id AND seq > @after_seq
ORDER BY seq
LIMIT @result_limit;

-- name: CountProblemEvolutionCandidatesByRun :one
SELECT COUNT(*)::bigint FROM problem_evolution_candidate
WHERE run_id = @run_id;

-- name: UpsertProblemEvolutionCandidateEdge :one
INSERT INTO problem_evolution_candidate_edge (
    run_id, parent_id, child_id, relation, parent_index
) VALUES (
    @run_id, @parent_id, @child_id, @relation, @parent_index
)
ON CONFLICT (child_id, parent_id, relation) DO UPDATE
SET parent_index = EXCLUDED.parent_index
RETURNING *;

-- name: ListProblemEvolutionCandidateEdges :many
SELECT * FROM problem_evolution_candidate_edge
WHERE run_id = @run_id
ORDER BY created_at, parent_index;

-- name: ListProblemEvolutionCandidateParents :many
SELECT * FROM problem_evolution_candidate_edge
WHERE child_id = @child_id
ORDER BY parent_index;

-- name: UpsertProblemEvolutionEvaluation :one
INSERT INTO problem_evolution_evaluation (
    run_id, candidate_id, workspace_id, evaluator_contract_id,
    evaluator_content_hash, attempt, phase, verdict, score, behavior_profile,
    feedback_projection, runtime_seconds
) VALUES (
    @run_id, @candidate_id, @workspace_id, @evaluator_contract_id,
    @evaluator_content_hash, @attempt, @phase, @verdict, @score,
    @behavior_profile, @feedback_projection, @runtime_seconds
)
ON CONFLICT (candidate_id, phase, attempt) DO UPDATE
SET verdict = EXCLUDED.verdict,
    score = EXCLUDED.score,
    behavior_profile = EXCLUDED.behavior_profile,
    feedback_projection = EXCLUDED.feedback_projection,
    runtime_seconds = EXCLUDED.runtime_seconds
RETURNING *;

-- name: ListProblemEvolutionEvaluations :many
SELECT * FROM problem_evolution_evaluation
WHERE run_id = @run_id
ORDER BY created_at;

-- name: ListProblemEvolutionEvaluationsByCandidate :many
SELECT * FROM problem_evolution_evaluation
WHERE candidate_id = @candidate_id
ORDER BY phase, attempt;

-- name: CountProblemEvolutionEvaluationAttempts :one
SELECT COUNT(*)::bigint FROM problem_evolution_evaluation
WHERE candidate_id = @candidate_id AND phase = @phase;

-- name: UpsertProblemEvolutionArtifact :one
INSERT INTO problem_evolution_artifact (
    run_id, candidate_id, workspace_id, kind, storage_ref, content_hash,
    content_type, size_bytes
) VALUES (
    @run_id, @candidate_id, @workspace_id, @kind, @storage_ref, @content_hash,
    @content_type, @size_bytes
)
ON CONFLICT (run_id, storage_ref) DO UPDATE
SET candidate_id = EXCLUDED.candidate_id,
    kind = EXCLUDED.kind,
    content_hash = EXCLUDED.content_hash,
    content_type = EXCLUDED.content_type,
    size_bytes = EXCLUDED.size_bytes
RETURNING *;

-- name: ListProblemEvolutionArtifacts :many
SELECT * FROM problem_evolution_artifact
WHERE run_id = @run_id
ORDER BY created_at;

-- name: SetProblemEvolutionRunProgress :one
UPDATE problem_evolution_run
SET generation = @generation,
    candidate_count = @candidate_count,
    rounds_without_gain = @rounds_without_gain,
    best_score = @best_score,
    total_cost = @total_cost,
    updated_at = now()
WHERE id = @id
RETURNING *;

-- name: RecomputeProblemEvolutionRunProgress :one
-- Progress counters are derived, not accumulated, so a retried or duplicated
-- event cannot inflate the budget the stop conditions read.
UPDATE problem_evolution_run r
SET candidate_count = agg.candidate_count,
    generation = agg.generation,
    best_score = agg.best_score,
    total_cost = agg.total_cost,
    updated_at = now()
FROM (
    SELECT
        COUNT(*)::int AS candidate_count,
        COALESCE(max(generation), 0)::int AS generation,
        COALESCE(max((score->>'total')::double precision) FILTER (
            WHERE score IS NOT NULL AND (score->>'hard_gate_passed')::boolean
        ), 0) AS best_score,
        COALESCE(sum(cost), 0) AS total_cost
    FROM problem_evolution_candidate
    WHERE run_id = @id
) AS agg
WHERE r.id = @id
RETURNING r.*;

-- name: BumpProblemEvolutionRunRoundsWithoutGain :one
UPDATE problem_evolution_run
SET rounds_without_gain = CASE
        WHEN @gained::boolean THEN 0
        ELSE rounds_without_gain + 1
    END,
    updated_at = now()
WHERE id = @id
RETURNING *;

-- name: SetProblemEvolutionRunModelCalls :one
-- The evolver reports a cumulative count. Taking the maximum makes retries and
-- out-of-order progress events unable to inflate or reduce the counter.
UPDATE problem_evolution_run
SET model_call_count = GREATEST(model_call_count, @model_call_count::int),
    updated_at = now()
WHERE id = @id
RETURNING *;

-- name: SetProblemEvolutionRunFinalCandidate :one
UPDATE problem_evolution_run
SET final_candidate_id = @final_candidate_id, updated_at = now()
WHERE id = @id
RETURNING *;

-- name: UpsertProblemEvolutionUsage :one
-- Progress reports are cumulative. Max keeps retries from double charging the
-- local ledger while still allowing a later report to advance the totals.
INSERT INTO problem_evolution_usage (
    run_id, workspace_id, source_event_id, provider, model,
    model_calls, input_tokens, output_tokens, cost
)
VALUES (
    @run_id, @workspace_id, @source_event_id, @provider, @model,
    @model_calls, @input_tokens, @output_tokens, @cost
)
ON CONFLICT (run_id, provider, model)
DO UPDATE SET
    source_event_id = EXCLUDED.source_event_id,
    model_calls = GREATEST(problem_evolution_usage.model_calls, EXCLUDED.model_calls),
    input_tokens = GREATEST(problem_evolution_usage.input_tokens, EXCLUDED.input_tokens),
    output_tokens = GREATEST(problem_evolution_usage.output_tokens, EXCLUDED.output_tokens),
    cost = GREATEST(problem_evolution_usage.cost, EXCLUDED.cost),
    updated_at = now()
RETURNING *;

-- name: ListProblemEvolutionUsage :many
SELECT * FROM problem_evolution_usage
WHERE run_id = @run_id
ORDER BY provider, model;

-- name: SetProblemEvolutionCandidateLane :one
UPDATE problem_evolution_candidate
SET lane = @lane, operator = @operator, updated_at = now()
WHERE run_id = @run_id AND external_ref = @external_ref
RETURNING *;

-- name: ForceReleaseProblemEvolutionRun :one
-- Requeue a run whose daemon stopped heartbeating. No claim token is required
-- because the point is to recover from a daemon that will never come back; the
-- old token is cleared so a returning daemon's reports are rejected.
UPDATE problem_evolution_run
SET status = 'queued',
    stage = 'queued',
    claimed_runtime_id = NULL,
    claim_token = NULL,
    claimed_at = NULL,
    heartbeat_at = NULL,
    failure_reason = @failure_reason,
    updated_at = now()
WHERE id = @id AND status IN ('running', 'synthesizing')
RETURNING *;

-- name: ForceCancelProblemEvolutionRun :one
UPDATE problem_evolution_run
SET status = 'cancelled',
    stage = 'cancelled',
    claim_token = NULL,
    stop_reason = @stop_reason,
    finished_at = now(),
    updated_at = now()
WHERE id = @id AND status = 'stopping'
RETURNING *;

-- name: GetProblemEvolutionLatestEventSeq :one
SELECT COALESCE(max(seq), 0)::bigint FROM problem_evolution_event
WHERE run_id = @run_id;

-- name: BumpProblemEvolutionCandidateFeedbackRounds :one
UPDATE problem_evolution_candidate
SET feedback_rounds = feedback_rounds + 1, updated_at = now()
WHERE run_id = @run_id AND external_ref = @external_ref
RETURNING *;

-- name: SetProblemEvolutionRunSeeds :one
UPDATE problem_evolution_run
SET search_seed = @search_seed,
    blind_seed = @blind_seed,
    updated_at = now()
WHERE id = @id
RETURNING *;

-- name: SetProblemEvolutionRunBlindResult :one
UPDATE problem_evolution_run
SET blind_candidate_id = @blind_candidate_id,
    blind_score = @blind_score,
    overfit_gap = @overfit_gap,
    updated_at = now()
WHERE id = @id
RETURNING *;

-- name: ListProblemEvolutionEliteCandidates :many
SELECT * FROM problem_evolution_candidate
WHERE run_id = @run_id AND status IN ('elite', 'selected')
ORDER BY generation DESC, created_at DESC
LIMIT @result_limit;
