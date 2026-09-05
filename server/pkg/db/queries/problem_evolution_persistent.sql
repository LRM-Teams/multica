-- name: CreateProblemEvolutionTaskSet :one
INSERT INTO problem_evolution_task_set (
    workspace_id, source, dataset_ref, dataset_revision,
    task_names, holdout_task_names, rollouts_per_task, max_parallel, created_by
)
VALUES (
    @workspace_id, @source, @dataset_ref, @dataset_revision,
    @task_names, @holdout_task_names, @rollouts_per_task, @max_parallel, @created_by
)
RETURNING *;

-- name: GetProblemEvolutionTaskSet :one
SELECT * FROM problem_evolution_task_set
WHERE id = @id AND workspace_id = @workspace_id;

-- name: ListProblemEvolutionTaskSets :many
SELECT * FROM problem_evolution_task_set
WHERE workspace_id = @workspace_id
ORDER BY created_at DESC
LIMIT @result_limit;

-- name: UpsertProblemEvolutionHarnessVersion :one
INSERT INTO problem_evolution_harness_version (
    run_id, workspace_id, iteration, parent_version_id, components, content_hash
)
VALUES (
    @run_id, @workspace_id, @iteration, @parent_version_id, @components, @content_hash
)
ON CONFLICT (run_id, content_hash)
DO UPDATE SET
    iteration = EXCLUDED.iteration,
    parent_version_id = EXCLUDED.parent_version_id,
    components = EXCLUDED.components
RETURNING *;

-- name: ListProblemEvolutionHarnessVersions :many
SELECT * FROM problem_evolution_harness_version
WHERE run_id = @run_id
ORDER BY iteration, created_at;

-- name: GetProblemEvolutionHarnessVersionByHash :one
SELECT * FROM problem_evolution_harness_version
WHERE run_id = @run_id AND content_hash = @content_hash;

-- name: GetProblemEvolutionHarnessVersion :one
SELECT * FROM problem_evolution_harness_version
WHERE id = @id AND run_id = @run_id AND workspace_id = @workspace_id;

-- name: SetProblemEvolutionHarnessVersionRolledBack :one
UPDATE problem_evolution_harness_version
SET rolled_back = @rolled_back, updated_at = now()
WHERE id = @id AND run_id = @run_id
RETURNING *;

-- name: PromoteProblemEvolutionHarnessVersion :one
UPDATE problem_evolution_harness_version
SET promoted_scope = 'workspace', updated_at = now()
WHERE id = @id AND workspace_id = @workspace_id
  AND rolled_back = false
RETURNING *;

-- name: UpsertProblemEvolutionHarnessRegistry :one
INSERT INTO problem_evolution_harness_registry (
    workspace_id, harness_version_id, content_hash
)
VALUES (@workspace_id, @harness_version_id, @content_hash)
ON CONFLICT (workspace_id)
DO UPDATE SET
    harness_version_id = EXCLUDED.harness_version_id,
    content_hash = EXCLUDED.content_hash,
    promoted_at = now()
RETURNING *;

-- name: GetProblemEvolutionHarnessRegistry :one
SELECT * FROM problem_evolution_harness_registry
WHERE workspace_id = @workspace_id;

-- name: GetProblemEvolutionWorkspaceHarnessVersion :one
SELECT hv.* FROM problem_evolution_harness_registry registry
JOIN problem_evolution_harness_version hv
  ON hv.id = registry.harness_version_id
WHERE registry.workspace_id = @workspace_id;

-- name: UpsertProblemEvolutionIteration :one
INSERT INTO problem_evolution_iteration (
    run_id, iteration, input_version_id, evolve_version_id, stage,
    pass_rate, holdout_pass_rate, cost, tokens
)
VALUES (
    @run_id, @iteration, @input_version_id, @evolve_version_id, @stage,
    @pass_rate, @holdout_pass_rate, @cost, @tokens
)
ON CONFLICT (run_id, iteration)
DO UPDATE SET
    input_version_id = COALESCE(EXCLUDED.input_version_id, problem_evolution_iteration.input_version_id),
    evolve_version_id = COALESCE(EXCLUDED.evolve_version_id, problem_evolution_iteration.evolve_version_id),
    stage = EXCLUDED.stage,
    pass_rate = EXCLUDED.pass_rate,
    holdout_pass_rate = EXCLUDED.holdout_pass_rate,
    cost = EXCLUDED.cost,
    tokens = EXCLUDED.tokens,
    updated_at = now()
RETURNING *;

-- name: ListProblemEvolutionIterations :many
SELECT * FROM problem_evolution_iteration
WHERE run_id = @run_id
ORDER BY iteration;

-- name: GetProblemEvolutionIteration :one
SELECT * FROM problem_evolution_iteration
WHERE run_id = @run_id AND iteration = @iteration;

-- name: UpsertProblemEvolutionTaskResult :one
INSERT INTO problem_evolution_task_result (
    run_id, iteration_id, task_name, rollout_index, split, reward, verdict,
    trace_ref, trace_digest_ref, tokens, cost
)
VALUES (
    @run_id, @iteration_id, @task_name, @rollout_index, @split, @reward, @verdict,
    @trace_ref, @trace_digest_ref, @tokens, @cost
)
ON CONFLICT (iteration_id, task_name, rollout_index)
DO UPDATE SET
    split = EXCLUDED.split,
    reward = EXCLUDED.reward,
    verdict = EXCLUDED.verdict,
    trace_ref = EXCLUDED.trace_ref,
    trace_digest_ref = EXCLUDED.trace_digest_ref,
    tokens = EXCLUDED.tokens,
    cost = EXCLUDED.cost
RETURNING *;

-- name: ListProblemEvolutionTaskResults :many
SELECT * FROM problem_evolution_task_result
WHERE run_id = @run_id
ORDER BY iteration_id, split, task_name, rollout_index;

-- name: ListProblemEvolutionTaskResultsByIteration :many
SELECT * FROM problem_evolution_task_result
WHERE iteration_id = @iteration_id
ORDER BY split, task_name, rollout_index;

-- name: InsertProblemEvolutionChangeRecord :one
INSERT INTO problem_evolution_change_record (
    run_id, iteration_id, harness_version_id, component, failure_evidence_ref,
    root_cause, fix_summary, predicted_pass_task_names, predicted_risk_task_names
)
VALUES (
    @run_id, @iteration_id, @harness_version_id, @component, @failure_evidence_ref,
    @root_cause, @fix_summary, @predicted_pass_task_names, @predicted_risk_task_names
)
RETURNING *;

-- name: ListProblemEvolutionChangeRecords :many
SELECT * FROM problem_evolution_change_record
WHERE run_id = @run_id
ORDER BY iteration_id, created_at;

-- name: SetProblemEvolutionChangeVerdict :one
UPDATE problem_evolution_change_record
SET observed_flips = @observed_flips,
    verdict = @verdict,
    action = @action,
    updated_at = now()
WHERE id = @id AND run_id = @run_id
RETURNING *;
