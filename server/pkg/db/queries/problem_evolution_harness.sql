-- name: UpsertProblemEvolutionHarness :one
INSERT INTO problem_evolution_harness (
    run_id, candidate_id, workspace_id, harness_ref, scope, spec, static_gate,
    gate_passed, prior_score
) VALUES (
    @run_id, @candidate_id, @workspace_id, @harness_ref, 'run', @spec,
    @static_gate, @gate_passed, @prior_score
)
ON CONFLICT (run_id, harness_ref) DO UPDATE
SET spec = EXCLUDED.spec,
    static_gate = EXCLUDED.static_gate,
    gate_passed = EXCLUDED.gate_passed,
    prior_score = EXCLUDED.prior_score,
    updated_at = now()
RETURNING *;

-- name: ListProblemEvolutionHarnesses :many
SELECT * FROM problem_evolution_harness
WHERE run_id = @run_id
ORDER BY created_at;

-- name: GetProblemEvolutionHarnessByRef :one
SELECT * FROM problem_evolution_harness
WHERE run_id = @run_id AND harness_ref = @harness_ref;

-- name: SetProblemEvolutionHarnessShortlisted :one
UPDATE problem_evolution_harness
SET shortlisted = @shortlisted, updated_at = now()
WHERE run_id = @run_id AND harness_ref = @harness_ref
RETURNING *;

-- name: SetProblemEvolutionHarnessReward :one
UPDATE problem_evolution_harness
SET reward = @reward,
    executed = true,
    repair_rounds = @repair_rounds,
    updated_at = now()
WHERE run_id = @run_id AND harness_ref = @harness_ref
RETURNING *;

-- name: ClearProblemEvolutionHarnessWinner :execrows
UPDATE problem_evolution_harness
SET winner = false, updated_at = now()
WHERE run_id = @run_id AND winner;

-- name: SetProblemEvolutionHarnessWinner :one
-- Only an executed, gate-passing harness can win: a winner that was never run
-- would be a claim with no evidence behind it.
UPDATE problem_evolution_harness
SET winner = true, updated_at = now()
WHERE run_id = @run_id
  AND harness_ref = @harness_ref
  AND gate_passed
  AND executed
RETURNING *;

-- name: SetProblemEvolutionRunHarnessBudget :one
UPDATE problem_evolution_run
SET harness_proposals = @harness_proposals,
    harness_execute_count = @harness_execute_count,
    benchmark_mode = @benchmark_mode,
    updated_at = now()
WHERE id = @id AND workspace_id = @workspace_id
  AND status IN ('draft', 'validating_evaluator', 'ready')
RETURNING *;

-- name: SetProblemEvolutionRunWinnerHarness :one
UPDATE problem_evolution_run
SET winner_harness_id = @winner_harness_id, updated_at = now()
WHERE id = @id
RETURNING *;
