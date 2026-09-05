package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/internal/problemevolution"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func createHarnessRun(t *testing.T) string {
	t.Helper()
	t.Setenv(problemevolution.ModeBFlagEnv, "true")
	rec := httptest.NewRecorder()
	req := newProblemEvolutionRequest(t, http.MethodPost, "/api/problem-evolution/runs", map[string]any{
		"mode":  problemevolution.ModeTaskHarnessRewardOnly,
		"title": "harness-" + uuid.NewString()[:8],
		"problem_spec": map[string]any{
			"statement":     "Make the failing integration test pass.",
			"artifact_type": "harness",
		},
	})
	testHandler.CreateProblemEvolutionRun(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create harness run status = %d, want 201: %s", rec.Code, rec.Body.String())
	}
	var run ProblemEvolutionRunResponse
	if err := json.NewDecoder(rec.Body).Decode(&run); err != nil {
		t.Fatalf("decode run: %v", err)
	}
	return run.ID
}

func seedClaimedHarnessRun(t *testing.T) db.ProblemEvolutionRun {
	t.Helper()
	ctx := context.Background()
	runID := createHarnessRun(t)
	attachEvaluator(t, runID)
	freezeEvaluator(t, runID)
	if _, err := testHandler.Queries.QueueProblemEvolutionRun(ctx, db.QueueProblemEvolutionRunParams{
		ID:          parseUUID(runID),
		WorkspaceID: parseUUID(testWorkspaceID),
	}); err != nil {
		t.Fatalf("queue run: %v", err)
	}
	if _, err := testPool.Exec(ctx, `
		UPDATE problem_evolution_run
		   SET status = 'running', claim_token = gen_random_uuid(), claimed_at = now(), heartbeat_at = now()
		 WHERE id = $1
	`, runID); err != nil {
		t.Fatalf("seed claimed run: %v", err)
	}
	run, err := testHandler.Queries.GetProblemEvolutionRun(ctx, db.GetProblemEvolutionRunParams{
		ID:          parseUUID(runID),
		WorkspaceID: parseUUID(testWorkspaceID),
	})
	if err != nil {
		t.Fatalf("load run: %v", err)
	}
	return run
}

func harnessProposedEvent(t *testing.T, ref string, spec problemevolution.HarnessSpec, priorScore float64) problemevolution.EvolverEvent {
	t.Helper()
	payload, err := json.Marshal(problemevolution.HarnessProposedPayload{
		Spec:       spec,
		PriorScore: priorScore,
	})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	return problemevolution.EvolverEvent{
		SchemaVersion: problemevolution.SchemaVersion,
		ClientEventID: uuid.NewString(),
		EventType:     problemevolution.EventHarnessProposed,
		CandidateRef:  ref,
		Payload:       payload,
	}
}

func gatePassingSpec(harnessID string) problemevolution.HarnessSpec {
	return problemevolution.HarnessSpec{
		SchemaVersion: problemevolution.SchemaVersion,
		HarnessID:     harnessID,
		Scope:         problemevolution.HarnessScopeRun,
		DeclaredTools: []string{"run_tests"},
		EntryStepID:   "plan",
		MaxSteps:      4,
		MaxRepairs:    1,
		Steps: []problemevolution.HarnessStep{
			{StepID: "plan", Kind: "think"},
			{StepID: "verify", Kind: "tool", Tool: "run_tests", Needs: []string{"plan"}},
		},
	}
}

func TestCreateProblemEvolutionRunRejectsModeBWithoutFlag(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	t.Setenv(problemevolution.ModeBFlagEnv, "")
	rec := httptest.NewRecorder()
	req := newProblemEvolutionRequest(t, http.MethodPost, "/api/problem-evolution/runs", map[string]any{
		"mode":  problemevolution.ModeTaskHarnessRewardOnly,
		"title": "harness-disabled",
	})
	testHandler.CreateProblemEvolutionRun(rec, req)
	// Mode B runs untrusted harnesses against hidden answers; it must not be
	// reachable on a deployment that has not opted in.
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body.String())
	}
}

func TestProblemEvolutionHarnessProposalIsGatedByPlatform(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	run := seedClaimedHarnessRun(t)

	bad := gatePassingSpec("h-bad")
	bad.DeclaredTools = append(bad.DeclaredTools, "hidden_answer")
	if _, err := testHandler.persistProblemEvolutionEvent(ctx, run,
		harnessProposedEvent(t, "h-bad", bad, 0.9)); err != nil {
		t.Fatalf("persist bad proposal: %v", err)
	}
	if _, err := testHandler.persistProblemEvolutionEvent(ctx, run,
		harnessProposedEvent(t, "h-good", gatePassingSpec("h-good"), 0.5)); err != nil {
		t.Fatalf("persist good proposal: %v", err)
	}

	rows, err := testHandler.Queries.ListProblemEvolutionHarnesses(ctx, run.ID)
	if err != nil {
		t.Fatalf("list harnesses: %v", err)
	}
	byRef := map[string]db.ProblemEvolutionHarness{}
	for _, row := range rows {
		byRef[row.HarnessRef] = row
	}
	if byRef["h-bad"].GatePassed {
		t.Fatal("a proposal requesting the hidden answer passed the gate")
	}
	if !byRef["h-good"].GatePassed {
		t.Fatalf("a valid proposal failed the gate: %s", byRef["h-good"].StaticGate)
	}
}

func TestProblemEvolutionHarnessEventCannotSelfCertify(t *testing.T) {
	payload, err := json.Marshal(map[string]any{
		"spec":        gatePassingSpec("h1"),
		"gate_passed": true,
	})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	event := problemevolution.EvolverEvent{
		SchemaVersion: problemevolution.SchemaVersion,
		ClientEventID: uuid.NewString(),
		EventType:     problemevolution.EventHarnessProposed,
		CandidateRef:  "h1",
		Payload:       payload,
	}
	if err := event.ValidatePayload(); err == nil {
		t.Fatal("an evolver-asserted gate pass was accepted")
	}
}

func TestProblemEvolutionShortlistExecutesOnlyBudgetedHarnesses(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	run := seedClaimedHarnessRun(t)

	for ref, prior := range map[string]float64{"h1": 0.9, "h2": 0.8, "h3": 0.7, "h4": 0.6} {
		if _, err := testHandler.persistProblemEvolutionEvent(ctx, run,
			harnessProposedEvent(t, ref, gatePassingSpec(ref), prior)); err != nil {
			t.Fatalf("persist proposal %s: %v", ref, err)
		}
	}
	if err := testHandler.applyProblemEvolutionShortlist(ctx, run); err != nil {
		t.Fatalf("apply shortlist: %v", err)
	}
	rows, err := testHandler.Queries.ListProblemEvolutionHarnesses(ctx, run.ID)
	if err != nil {
		t.Fatalf("list harnesses: %v", err)
	}
	shortlisted := 0
	for _, row := range rows {
		if row.Shortlisted {
			shortlisted++
		}
	}
	// Generate four, execute two: the point of the JIT structure is that
	// selection is cheap and execution is not.
	if shortlisted != int(run.HarnessExecuteCount) {
		t.Fatalf("shortlisted = %d, want %d", shortlisted, run.HarnessExecuteCount)
	}
}

func TestProblemEvolutionBenchmarkModeExecutesASingleHarness(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	run := seedClaimedHarnessRun(t)
	if _, err := testPool.Exec(ctx,
		`UPDATE problem_evolution_run SET benchmark_mode = true WHERE id = $1`, run.ID); err != nil {
		t.Fatalf("enable benchmark mode: %v", err)
	}
	run.BenchmarkMode = true

	for ref, prior := range map[string]float64{"h1": 0.9, "h2": 0.8} {
		if _, err := testHandler.persistProblemEvolutionEvent(ctx, run,
			harnessProposedEvent(t, ref, gatePassingSpec(ref), prior)); err != nil {
			t.Fatalf("persist proposal %s: %v", ref, err)
		}
	}
	if err := testHandler.applyProblemEvolutionShortlist(ctx, run); err != nil {
		t.Fatalf("apply shortlist: %v", err)
	}
	rows, err := testHandler.Queries.ListProblemEvolutionHarnesses(ctx, run.ID)
	if err != nil {
		t.Fatalf("list harnesses: %v", err)
	}
	shortlisted := []string{}
	for _, row := range rows {
		if row.Shortlisted {
			shortlisted = append(shortlisted, row.HarnessRef)
		}
	}
	if len(shortlisted) != 1 || shortlisted[0] != "h1" {
		t.Fatalf("shortlisted = %v, want [h1]", shortlisted)
	}
}

func TestProblemEvolutionHarnessWinnerRequiresExecution(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	run := seedClaimedHarnessRun(t)

	for _, ref := range []string{"h1", "h2"} {
		if _, err := testHandler.persistProblemEvolutionEvent(ctx, run,
			harnessProposedEvent(t, ref, gatePassingSpec(ref), 0.5)); err != nil {
			t.Fatalf("persist proposal %s: %v", ref, err)
		}
	}
	// No harness has been executed yet, so there is nothing a winner claim
	// could be based on.
	if err := testHandler.selectProblemEvolutionHarnessWinner(ctx, run); err != nil {
		t.Fatalf("select winner: %v", err)
	}
	beforeRun, err := testHandler.Queries.GetProblemEvolutionRun(ctx, db.GetProblemEvolutionRunParams{
		ID:          run.ID,
		WorkspaceID: run.WorkspaceID,
	})
	if err != nil {
		t.Fatalf("reload run: %v", err)
	}
	if beforeRun.WinnerHarnessID.Valid {
		t.Fatal("a winner was pinned before any harness ran")
	}

	if _, err := testHandler.persistProblemEvolutionEvent(ctx, run,
		candidateScoredEvent(t, uuid.NewString(), "h2")); err != nil {
		t.Fatalf("score h2: %v", err)
	}
	if err := testHandler.selectProblemEvolutionHarnessWinner(ctx, run); err != nil {
		t.Fatalf("select winner: %v", err)
	}
	rows, err := testHandler.Queries.ListProblemEvolutionHarnesses(ctx, run.ID)
	if err != nil {
		t.Fatalf("list harnesses: %v", err)
	}
	winners := []string{}
	for _, row := range rows {
		if row.Winner {
			winners = append(winners, row.HarnessRef)
		}
		if row.Winner && row.Scope != problemevolution.HarnessScopeRun {
			t.Fatalf("winner scope = %q, want run scope", row.Scope)
		}
	}
	if len(winners) != 1 || winners[0] != "h2" {
		t.Fatalf("winners = %v, want [h2]", winners)
	}
}

func TestUpdateProblemEvolutionHarnessBudgetRejectsOverExecution(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	runID := createHarnessRun(t)

	rec := httptest.NewRecorder()
	req := newProblemEvolutionRequest(t, http.MethodPatch,
		"/api/problem-evolution/runs/"+runID+"/harness-budget", map[string]any{
			"harness_proposals":     2,
			"harness_execute_count": 4,
		}, "runId", runID)
	testHandler.UpdateProblemEvolutionHarnessBudget(rec, req)
	// Executing more harnesses than were generated is incoherent, and would
	// quietly mean "execute everything".
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body.String())
	}
}

func TestUpdateProblemEvolutionHarnessBudgetForcesSingleExecutionInBenchmarkMode(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	runID := createHarnessRun(t)

	rec := httptest.NewRecorder()
	req := newProblemEvolutionRequest(t, http.MethodPatch,
		"/api/problem-evolution/runs/"+runID+"/harness-budget", map[string]any{
			"harness_proposals":     4,
			"harness_execute_count": 3,
			"benchmark_mode":        true,
		}, "runId", runID)
	testHandler.UpdateProblemEvolutionHarnessBudget(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	run, err := testHandler.Queries.GetProblemEvolutionRun(context.Background(), db.GetProblemEvolutionRunParams{
		ID:          parseUUID(runID),
		WorkspaceID: parseUUID(testWorkspaceID),
	})
	if err != nil {
		t.Fatalf("reload run: %v", err)
	}
	if run.HarnessExecuteCount != 1 {
		t.Fatalf("execute count = %d, want 1 in benchmark mode", run.HarnessExecuteCount)
	}
}
