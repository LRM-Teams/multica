package handler

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/multica-ai/multica/server/internal/problemevolution"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// seedClaimedProblemEvolutionRun creates a run already in the claimed state so
// event ingestion can be exercised without a daemon.
func seedClaimedProblemEvolutionRun(t *testing.T) db.ProblemEvolutionRun {
	t.Helper()
	ctx := context.Background()
	runID := createSolveRun(t)
	attachEvaluator(t, runID)
	freezeEvaluator(t, runID)
	// Go through the queue transition rather than writing `running` directly, so
	// the run carries the pinned evaluator hash a real claimed run has.
	if _, err := testHandler.Queries.QueueProblemEvolutionRun(ctx, db.QueueProblemEvolutionRunParams{
		ID:          parseUUID(runID),
		WorkspaceID: parseUUID(testWorkspaceID),
	}); err != nil {
		t.Fatalf("queue run: %v", err)
	}
	seeds := problemevolution.DeriveSeeds(runID)
	if _, err := testPool.Exec(ctx, `
		UPDATE problem_evolution_run
		   SET status = 'running', claim_token = gen_random_uuid(), claimed_at = now(),
		       heartbeat_at = now(), search_seed = $2, blind_seed = $3
		 WHERE id = $1
	`, runID, seeds.Search, seeds.Blind); err != nil {
		t.Fatalf("seed claimed run: %v", err)
	}
	run, err := testHandler.Queries.GetProblemEvolutionRun(ctx, db.GetProblemEvolutionRunParams{
		ID:          parseUUID(runID),
		WorkspaceID: parseUUID(testWorkspaceID),
	})
	if err != nil {
		t.Fatalf("load seeded run: %v", err)
	}
	return run
}

func candidateScoredEvent(t *testing.T, clientEventID, candidateRef string) problemevolution.EvolverEvent {
	t.Helper()
	payload, err := json.Marshal(problemevolution.CandidateScoredPayload{
		Score: problemevolution.Score{
			SchemaVersion:  problemevolution.SchemaVersion,
			Total:          0.6,
			Scale:          problemevolution.ScaleUnitInterval,
			HardGatePassed: true,
			Dimensions: []problemevolution.ScoreDimension{
				{DimensionID: "correctness", Score: 0.6, Weight: 1},
			},
		},
		BehaviorProfile: problemevolution.BehaviorProfile{
			SchemaVersion: problemevolution.SchemaVersion,
			Kind:          problemevolution.BehaviorKindDimensionVector,
			Entries:       []problemevolution.BehaviorEntry{{Key: "correctness", Value: 0.6}},
		},
	})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	return problemevolution.EvolverEvent{
		SchemaVersion: problemevolution.SchemaVersion,
		ClientEventID: clientEventID,
		EventType:     problemevolution.EventCandidateScored,
		CandidateRef:  candidateRef,
		Payload:       payload,
	}
}

func TestPersistProblemEvolutionEventAllocatesMonotonicSeq(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	run := seedClaimedProblemEvolutionRun(t)

	first, err := testHandler.persistProblemEvolutionEvent(ctx, run,
		candidateScoredEvent(t, uuid.NewString(), "c1"))
	if err != nil {
		t.Fatalf("persist first event: %v", err)
	}
	second, err := testHandler.persistProblemEvolutionEvent(ctx, run,
		candidateScoredEvent(t, uuid.NewString(), "c2"))
	if err != nil {
		t.Fatalf("persist second event: %v", err)
	}
	if first.seq != 1 || second.seq != 2 {
		t.Fatalf("seq allocation = (%d, %d), want (1, 2)", first.seq, second.seq)
	}
}

func TestPersistProblemEvolutionEventIsIdempotentPerClientEventID(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	run := seedClaimedProblemEvolutionRun(t)
	clientEventID := uuid.NewString()

	first, err := testHandler.persistProblemEvolutionEvent(ctx, run,
		candidateScoredEvent(t, clientEventID, "c1"))
	if err != nil {
		t.Fatalf("persist event: %v", err)
	}
	// A retried delivery must not allocate a second sequence number, otherwise
	// replaying the run would show the same candidate scored twice.
	retry, err := testHandler.persistProblemEvolutionEvent(ctx, run,
		candidateScoredEvent(t, clientEventID, "c1"))
	if err != nil {
		t.Fatalf("persist retry: %v", err)
	}
	if retry.disposition != problemEvolutionEventDuplicate {
		t.Fatalf("retry disposition = %v, want duplicate", retry.disposition)
	}
	if retry.seq != first.seq {
		t.Fatalf("retry seq = %d, want %d", retry.seq, first.seq)
	}
	var stored int
	if err := testPool.QueryRow(ctx,
		`SELECT count(*) FROM problem_evolution_event WHERE run_id = $1`, run.ID).Scan(&stored); err != nil {
		t.Fatalf("count events: %v", err)
	}
	if stored != 1 {
		t.Fatalf("stored events = %d, want 1", stored)
	}
	var evaluations int
	if err := testPool.QueryRow(ctx,
		`SELECT count(*) FROM problem_evolution_evaluation WHERE run_id = $1`, run.ID).Scan(&evaluations); err != nil {
		t.Fatalf("count evaluations: %v", err)
	}
	if evaluations != 1 {
		t.Fatalf("stored evaluations = %d, want 1", evaluations)
	}
}

func TestProblemEvolutionTerminalWritesRequireCurrentClaimToken(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	run := seedClaimedProblemEvolutionRun(t)
	wrongToken := parseUUID(uuid.NewString())

	_, err := testHandler.Queries.FailProblemEvolutionRun(ctx, db.FailProblemEvolutionRunParams{
		ID: run.ID, WorkspaceID: run.WorkspaceID, ClaimToken: wrongToken,
		FailureReason: "wrong daemon",
	})
	if !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("fail with stale claim error = %v, want pgx.ErrNoRows", err)
	}

	if _, err := testPool.Exec(ctx,
		`UPDATE problem_evolution_run SET status = 'stopping' WHERE id = $1`, run.ID); err != nil {
		t.Fatalf("mark run stopping: %v", err)
	}
	_, err = testHandler.Queries.CancelProblemEvolutionRun(ctx, db.CancelProblemEvolutionRunParams{
		ID: run.ID, WorkspaceID: run.WorkspaceID, ClaimToken: wrongToken,
	})
	if !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("cancel with stale claim error = %v, want pgx.ErrNoRows", err)
	}

	cancelled, err := testHandler.Queries.CancelProblemEvolutionRun(ctx, db.CancelProblemEvolutionRunParams{
		ID: run.ID, WorkspaceID: run.WorkspaceID, ClaimToken: run.ClaimToken,
	})
	if err != nil {
		t.Fatalf("cancel with current claim: %v", err)
	}
	if cancelled.Status != "cancelled" {
		t.Fatalf("run status = %q, want cancelled", cancelled.Status)
	}
}

func TestProblemEvolutionCandidateProjectionAppliesScore(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	run := seedClaimedProblemEvolutionRun(t)

	if _, err := testHandler.persistProblemEvolutionEvent(ctx, run,
		candidateScoredEvent(t, uuid.NewString(), "c1")); err != nil {
		t.Fatalf("persist event: %v", err)
	}
	candidate, err := testHandler.Queries.GetProblemEvolutionCandidateByRef(ctx,
		db.GetProblemEvolutionCandidateByRefParams{RunID: run.ID, ExternalRef: "c1"})
	if err != nil {
		t.Fatalf("load candidate: %v", err)
	}
	if candidate.Status != "selectable" {
		t.Fatalf("candidate status = %q, want selectable", candidate.Status)
	}
	var score problemevolution.Score
	if err := json.Unmarshal(candidate.Score, &score); err != nil {
		t.Fatalf("decode stored score: %v", err)
	}
	if err := score.Validate(); err != nil {
		t.Fatalf("stored score is not valid: %v", err)
	}
}

func TestBumpProblemEvolutionRunGraphVersionIsMonotonic(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	run := seedClaimedProblemEvolutionRun(t)

	first, err := testHandler.Queries.BumpProblemEvolutionRunGraphVersion(ctx, run.ID)
	if err != nil {
		t.Fatalf("bump graph version: %v", err)
	}
	second, err := testHandler.Queries.BumpProblemEvolutionRunGraphVersion(ctx, run.ID)
	if err != nil {
		t.Fatalf("bump graph version: %v", err)
	}
	if second <= first {
		t.Fatalf("graph version did not advance: %d then %d", first, second)
	}
}

func TestBuildProblemEvolutionInputDetectsContractDrift(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	run := seedClaimedProblemEvolutionRun(t)

	if _, _, err := testHandler.buildProblemEvolutionInput(ctx, run); err != nil {
		t.Fatalf("expected a valid input for an unmodified contract, got %v", err)
	}
	// Simulate an out-of-band contract edit: the run pinned a hash at start, so
	// the mismatch must surface instead of silently re-scoring.
	if _, err := testPool.Exec(ctx, `
		UPDATE problem_evolution_evaluator_contract
		   SET content_hash = 'sha256:tampered'
		 WHERE id = $1
	`, run.EvaluatorContractID); err != nil {
		t.Fatalf("tamper contract: %v", err)
	}
	if _, _, err := testHandler.buildProblemEvolutionInput(ctx, run); err == nil {
		t.Fatal("expected evaluator contract drift to be rejected")
	}
}
