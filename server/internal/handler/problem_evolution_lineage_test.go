package handler

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/internal/problemevolution"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func candidateStartedEvent(t *testing.T, candidateRef string, payload problemevolution.CandidateStartedPayload) problemevolution.EvolverEvent {
	t.Helper()
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	return problemevolution.EvolverEvent{
		SchemaVersion: problemevolution.SchemaVersion,
		ClientEventID: uuid.NewString(),
		EventType:     problemevolution.EventCandidateStarted,
		CandidateRef:  candidateRef,
		Payload:       raw,
	}
}

func TestProblemEvolutionLineageRecordsCrossoverParents(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	run := seedClaimedProblemEvolutionRun(t)

	for _, ref := range []string{"p1", "p2"} {
		if _, err := testHandler.persistProblemEvolutionEvent(ctx, run, candidateStartedEvent(t, ref,
			problemevolution.CandidateStartedPayload{Lane: problemevolution.LaneDiverse, Operator: "diverse", Generation: 0},
		)); err != nil {
			t.Fatalf("persist parent %s: %v", ref, err)
		}
	}
	if _, err := testHandler.persistProblemEvolutionEvent(ctx, run, candidateStartedEvent(t, "child",
		problemevolution.CandidateStartedPayload{
			Lane:       problemevolution.LaneBaseline,
			Operator:   "crossover",
			Relation:   problemevolution.RelationCrossoverOf,
			Generation: 1,
			ParentIDs:  []string{"p1", "p2"},
		},
	)); err != nil {
		t.Fatalf("persist child: %v", err)
	}

	child, err := testHandler.Queries.GetProblemEvolutionCandidateByRef(ctx,
		db.GetProblemEvolutionCandidateByRefParams{RunID: run.ID, ExternalRef: "child"})
	if err != nil {
		t.Fatalf("load child: %v", err)
	}
	// The evolver claimed `baseline`; the declared relation wins so the graph
	// cannot show a crossover as an unrelated root.
	if child.Lane != problemevolution.LaneCrossover {
		t.Fatalf("child lane = %q, want %q", child.Lane, problemevolution.LaneCrossover)
	}
	if child.Generation != 1 {
		t.Fatalf("child generation = %d, want 1", child.Generation)
	}
	edges, err := testHandler.Queries.ListProblemEvolutionCandidateParents(ctx, child.ID)
	if err != nil {
		t.Fatalf("list parents: %v", err)
	}
	if len(edges) != 2 {
		t.Fatalf("parent edges = %d, want 2", len(edges))
	}
	if edges[0].ParentIndex != 0 || edges[1].ParentIndex != 1 {
		t.Fatalf("parent indices = (%d, %d), want (0, 1)", edges[0].ParentIndex, edges[1].ParentIndex)
	}
}

func TestProblemEvolutionLineageCreatesMissingParentWithoutClobbering(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	run := seedClaimedProblemEvolutionRun(t)

	// Child arrives before its parent's own event, which is legal: the daemon
	// batches events and the ordering inside a batch is not guaranteed.
	if _, err := testHandler.persistProblemEvolutionEvent(ctx, run, candidateStartedEvent(t, "child",
		problemevolution.CandidateStartedPayload{
			Operator:  "repair",
			Relation:  problemevolution.RelationRepairOf,
			ParentIDs: []string{"late-parent"},
		},
	)); err != nil {
		t.Fatalf("persist child: %v", err)
	}
	if _, err := testHandler.persistProblemEvolutionEvent(ctx, run, candidateStartedEvent(t, "late-parent",
		problemevolution.CandidateStartedPayload{Lane: problemevolution.LaneDiverse, Operator: "diverse", Generation: 3},
	)); err != nil {
		t.Fatalf("persist late parent: %v", err)
	}

	parent, err := testHandler.Queries.GetProblemEvolutionCandidateByRef(ctx,
		db.GetProblemEvolutionCandidateByRefParams{RunID: run.ID, ExternalRef: "late-parent"})
	if err != nil {
		t.Fatalf("load parent: %v", err)
	}
	if parent.Lane != problemevolution.LaneDiverse || parent.Generation != 3 {
		t.Fatalf("placeholder overwrote the real parent: lane=%q generation=%d", parent.Lane, parent.Generation)
	}
	edges, err := testHandler.Queries.ListProblemEvolutionCandidateEdges(ctx, run.ID)
	if err != nil {
		t.Fatalf("list edges: %v", err)
	}
	if len(edges) != 1 || edges[0].Relation != problemevolution.RelationRepairOf {
		t.Fatalf("edges = %+v, want a single repair_of edge", edges)
	}
}

func TestProblemEvolutionScoredEventWritesEvaluationWithProjection(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	run := seedClaimedProblemEvolutionRun(t)

	if _, err := testHandler.persistProblemEvolutionEvent(ctx, run,
		candidateScoredEvent(t, uuid.NewString(), "c1")); err != nil {
		t.Fatalf("persist scored event: %v", err)
	}
	candidate, err := testHandler.Queries.GetProblemEvolutionCandidateByRef(ctx,
		db.GetProblemEvolutionCandidateByRefParams{RunID: run.ID, ExternalRef: "c1"})
	if err != nil {
		t.Fatalf("load candidate: %v", err)
	}
	evaluations, err := testHandler.Queries.ListProblemEvolutionEvaluationsByCandidate(ctx, candidate.ID)
	if err != nil {
		t.Fatalf("list evaluations: %v", err)
	}
	if len(evaluations) != 1 {
		t.Fatalf("evaluations = %d, want 1", len(evaluations))
	}
	evaluation := evaluations[0]
	if evaluation.Attempt != 1 || evaluation.Phase != "search" || evaluation.Verdict != "scored" {
		t.Fatalf("unexpected evaluation row: %+v", evaluation)
	}
	var projection problemevolution.FeedbackProjection
	if err := json.Unmarshal(evaluation.FeedbackProjection, &projection); err != nil {
		t.Fatalf("decode projection: %v", err)
	}
	// The projection is what the evolver may see. An exact total would let it
	// climb the verifier's gradient across rounds.
	if projection.Total != nil {
		t.Fatalf("projection leaked an exact total: %+v", projection)
	}
	if projection.TotalBucket == "" {
		t.Fatal("projection is missing the bucketed total")
	}
}

func TestProblemEvolutionRepeatedScoreAddsSecondAttempt(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	run := seedClaimedProblemEvolutionRun(t)

	for range 2 {
		if _, err := testHandler.persistProblemEvolutionEvent(ctx, run,
			candidateScoredEvent(t, uuid.NewString(), "c1")); err != nil {
			t.Fatalf("persist scored event: %v", err)
		}
	}
	candidate, err := testHandler.Queries.GetProblemEvolutionCandidateByRef(ctx,
		db.GetProblemEvolutionCandidateByRefParams{RunID: run.ID, ExternalRef: "c1"})
	if err != nil {
		t.Fatalf("load candidate: %v", err)
	}
	evaluations, err := testHandler.Queries.ListProblemEvolutionEvaluationsByCandidate(ctx, candidate.ID)
	if err != nil {
		t.Fatalf("list evaluations: %v", err)
	}
	// A second scoring round must be visible as history, not overwrite the
	// first: repair rounds are exactly what the audit needs to see.
	if len(evaluations) != 2 {
		t.Fatalf("evaluations = %d, want 2", len(evaluations))
	}
}

func TestProblemEvolutionProgressCountersAreDerived(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	run := seedClaimedProblemEvolutionRun(t)

	for _, ref := range []string{"c1", "c2"} {
		if _, err := testHandler.persistProblemEvolutionEvent(ctx, run,
			candidateScoredEvent(t, uuid.NewString(), ref)); err != nil {
			t.Fatalf("persist scored event: %v", err)
		}
	}
	updated, err := testHandler.Queries.GetProblemEvolutionRun(ctx, db.GetProblemEvolutionRunParams{
		ID:          run.ID,
		WorkspaceID: run.WorkspaceID,
	})
	if err != nil {
		t.Fatalf("reload run: %v", err)
	}
	if updated.CandidateCount != 2 {
		t.Fatalf("candidate_count = %d, want 2", updated.CandidateCount)
	}
	if updated.BestScore < 0.59 || updated.BestScore > 0.61 {
		t.Fatalf("best_score = %v, want ~0.6", updated.BestScore)
	}
}

func TestApplyProblemEvolutionSelectionPromotesAndPrunes(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	run := seedClaimedProblemEvolutionRun(t)

	for _, ref := range []string{"c1", "c2", "c3"} {
		if _, err := testHandler.persistProblemEvolutionEvent(ctx, run,
			candidateScoredEvent(t, uuid.NewString(), ref)); err != nil {
			t.Fatalf("persist scored event: %v", err)
		}
	}
	if err := testHandler.applyProblemEvolutionSelection(ctx, run, 2); err != nil {
		t.Fatalf("apply selection: %v", err)
	}
	candidates, err := testHandler.Queries.ListProblemEvolutionCandidates(ctx, run.ID)
	if err != nil {
		t.Fatalf("list candidates: %v", err)
	}
	elite, pruned := 0, 0
	for _, candidate := range candidates {
		switch candidate.Status {
		case "elite":
			elite++
		case "pruned":
			pruned++
		}
	}
	if elite != 2 || pruned != 1 {
		t.Fatalf("selection = %d elite / %d pruned, want 2 / 1", elite, pruned)
	}
	updated, err := testHandler.Queries.GetProblemEvolutionRun(ctx, db.GetProblemEvolutionRunParams{
		ID:          run.ID,
		WorkspaceID: run.WorkspaceID,
	})
	if err != nil {
		t.Fatalf("reload run: %v", err)
	}
	if !updated.BestCandidateID.Valid {
		t.Fatal("selection did not pin a best candidate")
	}
}

func TestEnforceProblemEvolutionStopConditionsStopsOnCandidateBudget(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	run := seedClaimedProblemEvolutionRun(t)

	if _, err := testHandler.persistProblemEvolutionEvent(ctx, run,
		candidateScoredEvent(t, uuid.NewString(), "c1")); err != nil {
		t.Fatalf("persist scored event: %v", err)
	}
	config := problemevolution.StopConfig{MaxCandidates: 1}.WithDefaults()
	if err := testHandler.enforceProblemEvolutionStopConditions(ctx, run, config); err != nil {
		t.Fatalf("enforce stop conditions: %v", err)
	}
	stopped, err := testHandler.Queries.GetProblemEvolutionRun(ctx, db.GetProblemEvolutionRunParams{
		ID:          run.ID,
		WorkspaceID: run.WorkspaceID,
	})
	if err != nil {
		t.Fatalf("reload run: %v", err)
	}
	if stopped.Status != "stopping" {
		t.Fatalf("status = %q, want stopping", stopped.Status)
	}
	if stopped.StopReason != problemevolution.StopReasonBudgetExhausted {
		t.Fatalf("stop_reason = %q, want %q", stopped.StopReason, problemevolution.StopReasonBudgetExhausted)
	}
}

func TestReapProblemEvolutionRunsCancelsOverdueStop(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	run := seedClaimedProblemEvolutionRun(t)

	if _, err := testPool.Exec(ctx, `
		UPDATE problem_evolution_run
		   SET status = 'stopping', stop_requested_at = now() - interval '10 minutes',
		       stop_reason = 'user_stopped'
		 WHERE id = $1
	`, run.ID); err != nil {
		t.Fatalf("seed overdue stop: %v", err)
	}
	if err := testHandler.ReapProblemEvolutionRuns(ctx, time.Now()); err != nil {
		t.Fatalf("reap runs: %v", err)
	}
	reaped, err := testHandler.Queries.GetProblemEvolutionRun(ctx, db.GetProblemEvolutionRunParams{
		ID:          run.ID,
		WorkspaceID: run.WorkspaceID,
	})
	if err != nil {
		t.Fatalf("reload run: %v", err)
	}
	// A daemon that never acknowledges the stop must not leave the run in
	// `stopping` forever, or the stop button is a lie.
	if reaped.Status != "cancelled" {
		t.Fatalf("status = %q, want cancelled", reaped.Status)
	}
}

func TestReapProblemEvolutionRunsRequeuesAbandonedClaim(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	run := seedClaimedProblemEvolutionRun(t)

	if _, err := testPool.Exec(ctx, `
		UPDATE problem_evolution_run
		   SET heartbeat_at = now() - interval '10 minutes'
		 WHERE id = $1
	`, run.ID); err != nil {
		t.Fatalf("age heartbeat: %v", err)
	}
	if err := testHandler.ReapProblemEvolutionRuns(ctx, time.Now()); err != nil {
		t.Fatalf("reap runs: %v", err)
	}
	reaped, err := testHandler.Queries.GetProblemEvolutionRun(ctx, db.GetProblemEvolutionRunParams{
		ID:          run.ID,
		WorkspaceID: run.WorkspaceID,
	})
	if err != nil {
		t.Fatalf("reload run: %v", err)
	}
	if reaped.Status != "queued" {
		t.Fatalf("status = %q, want queued", reaped.Status)
	}
	if reaped.ClaimToken.Valid {
		t.Fatal("requeued run kept its claim token, so a returning daemon could still report")
	}
}
