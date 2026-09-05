package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/internal/problemevolution"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// newDaemonProblemEvolutionRequest builds a daemon-facing request. These routes
// sit behind daemon auth rather than the workspace middleware, so the caller is
// identified by header the way the daemon does it.
func newDaemonProblemEvolutionRequest(t *testing.T, method, path string, body []byte, runID string) *http.Request {
	t.Helper()
	req := httptest.NewRequest(method, path, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-User-ID", testUserID)
	req.Header.Set("X-Workspace-ID", testWorkspaceID)
	return withRouteParams(req, "runId", runID)
}

func repairEvent(t *testing.T, childRef, parentRef string) problemevolution.EvolverEvent {
	t.Helper()
	return candidateStartedEvent(t, childRef, problemevolution.CandidateStartedPayload{
		Operator:  "repair",
		Relation:  problemevolution.RelationRepairOf,
		ParentIDs: []string{parentRef},
	})
}

func TestProblemEvolutionRepairBudgetIsEnforcedAtIngestion(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	run := seedClaimedProblemEvolutionRun(t)
	policy := testHandler.problemEvolutionFeedbackPolicy(ctx, run)

	if _, err := testHandler.persistProblemEvolutionEvent(ctx, run,
		candidateScoredEvent(t, uuid.NewString(), "parent")); err != nil {
		t.Fatalf("seed parent: %v", err)
	}
	for round := range policy.MaxRounds {
		event := repairEvent(t, "repair-ok-"+uuid.NewString()[:8], "parent")
		if _, err := testHandler.persistProblemEvolutionEvent(ctx, run, event); err != nil {
			t.Fatalf("repair round %d was refused: %v", round+1, err)
		}
	}
	// Repeatedly repairing against the same reward is how a run stops solving
	// the problem and starts guessing the verifier, so the bound is enforced
	// here rather than trusted to the evolver.
	_, err := testHandler.persistProblemEvolutionEvent(ctx, run, repairEvent(t, "repair-over", "parent"))
	if err == nil {
		t.Fatal("a repair past the policy budget was accepted")
	}
	if !isProblemEvolutionRejection(err) {
		t.Fatalf("error %v is not an event rejection", err)
	}
}

func isProblemEvolutionRejection(err error) bool {
	return errors.Is(err, problemevolution.ErrEventRejected)
}

func TestReportProblemEvolutionEventsCountsRejectionsWithoutFailingBatch(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	run := seedClaimedProblemEvolutionRun(t)
	policy := testHandler.problemEvolutionFeedbackPolicy(ctx, run)

	if _, err := testHandler.persistProblemEvolutionEvent(ctx, run,
		candidateScoredEvent(t, uuid.NewString(), "parent")); err != nil {
		t.Fatalf("seed parent: %v", err)
	}
	for range policy.MaxRounds {
		if _, err := testHandler.persistProblemEvolutionEvent(ctx, run,
			repairEvent(t, "spend-"+uuid.NewString()[:8], "parent")); err != nil {
			t.Fatalf("spend repair round: %v", err)
		}
	}

	events := []problemevolution.EvolverEvent{
		repairEvent(t, "over-budget", "parent"),
		candidateScoredEvent(t, uuid.NewString(), "fresh"),
	}
	body, err := json.Marshal(map[string]any{
		"claim_token": uuidToString(run.ClaimToken),
		"events":      events,
	})
	if err != nil {
		t.Fatalf("marshal events: %v", err)
	}
	rec := httptest.NewRecorder()
	req := newDaemonProblemEvolutionRequest(t, http.MethodPost,
		"/api/daemon/problem-evolution/runs/"+uuidToString(run.ID)+"/events",
		body, uuidToString(run.ID))
	testHandler.ReportProblemEvolutionEvents(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var resp ProblemEvolutionEventsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	// One rejected event must not cost the batch its valid events.
	if resp.Rejected != 1 || resp.Accepted != 1 {
		t.Fatalf("accepted = %d rejected = %d, want 1 and 1", resp.Accepted, resp.Rejected)
	}
}

func TestBuildProblemEvolutionInputProjectsFeedbackWithoutExactScores(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	run := seedClaimedProblemEvolutionRun(t)

	for _, ref := range []string{"c1", "c2"} {
		if _, err := testHandler.persistProblemEvolutionEvent(ctx, run,
			candidateScoredEvent(t, uuid.NewString(), ref)); err != nil {
			t.Fatalf("score %s: %v", ref, err)
		}
	}
	if err := testHandler.applyProblemEvolutionSelection(ctx, run, 2); err != nil {
		t.Fatalf("apply selection: %v", err)
	}
	input, policy, err := testHandler.buildProblemEvolutionInput(ctx, run)
	if err != nil {
		t.Fatalf("build input: %v", err)
	}
	if len(input.Feedback.Parents) == 0 {
		t.Fatal("feedback bundle has no elite parents")
	}
	if policy.Bandwidth != problemevolution.FeedbackBandwidthBucketed {
		t.Fatalf("bandwidth = %q, want bucketed by default", policy.Bandwidth)
	}
	for _, parent := range input.Feedback.Parents {
		if parent.Projection.Total != nil {
			t.Fatalf("parent %s leaked an exact total", parent.CandidateRef)
		}
		if parent.Projection.TotalBucket == "" {
			t.Fatalf("parent %s has no bucketed total", parent.CandidateRef)
		}
	}
	// The blind seed must not reach the evolver, or it could tune to the final
	// check instead of generalising.
	if input.Seeds.Blind != 0 {
		t.Fatalf("input exposed the blind seed: %+v", input.Seeds)
	}
}

func TestStartProblemEvolutionRunPinsDistinctSeeds(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	runID := createSolveRun(t)
	attachEvaluator(t, runID)
	freezeEvaluator(t, runID)

	rec := httptest.NewRecorder()
	req := newProblemEvolutionRequest(t, http.MethodPost,
		"/api/problem-evolution/runs/"+runID+"/start", nil, "runId", runID)
	testHandler.StartProblemEvolutionRun(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("start status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	run, err := testHandler.Queries.GetProblemEvolutionRun(ctx, db.GetProblemEvolutionRunParams{
		ID:          parseUUID(runID),
		WorkspaceID: parseUUID(testWorkspaceID),
	})
	if err != nil {
		t.Fatalf("reload run: %v", err)
	}
	if run.SearchSeed == 0 || run.BlindSeed == 0 {
		t.Fatalf("seeds were not pinned at start: %d / %d", run.SearchSeed, run.BlindSeed)
	}
	if run.SearchSeed == run.BlindSeed {
		t.Fatal("search and blind validation share a seed")
	}
}

func reportBlindValidation(t *testing.T, run db.ProblemEvolutionRun, outcome problemevolution.BlindValidationOutcome) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(map[string]any{
		"claim_token": uuidToString(run.ClaimToken),
		"outcome":     outcome,
	})
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}
	rec := httptest.NewRecorder()
	req := newDaemonProblemEvolutionRequest(t, http.MethodPost,
		"/api/daemon/problem-evolution/runs/"+uuidToString(run.ID)+"/blind-validation",
		body, uuidToString(run.ID))
	testHandler.ReportProblemEvolutionBlindValidation(rec, req)
	return rec
}

func TestReportProblemEvolutionBlindValidationRejectsSearchSeed(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	run := seedClaimedProblemEvolutionRun(t)
	if _, err := testHandler.persistProblemEvolutionEvent(ctx, run,
		candidateScoredEvent(t, uuid.NewString(), "c1")); err != nil {
		t.Fatalf("score candidate: %v", err)
	}

	rec := reportBlindValidation(t, run, problemevolution.BlindValidationOutcome{
		CandidateRef: "c1",
		Score:        blindScore(0.5),
		Seed:         run.SearchSeed,
	})
	// Scoring the blind phase on the search sample proves nothing about
	// generalisation, so it is refused instead of recorded with a caveat.
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body.String())
	}
}

func TestReportProblemEvolutionBlindValidationRecordsOverfitGap(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	run := seedClaimedProblemEvolutionRun(t)
	if _, err := testHandler.persistProblemEvolutionEvent(ctx, run,
		candidateScoredEvent(t, uuid.NewString(), "c1")); err != nil {
		t.Fatalf("score candidate: %v", err)
	}
	current, err := testHandler.Queries.GetProblemEvolutionRun(ctx, db.GetProblemEvolutionRunParams{
		ID:          run.ID,
		WorkspaceID: run.WorkspaceID,
	})
	if err != nil {
		t.Fatalf("reload run: %v", err)
	}

	rec := reportBlindValidation(t, current, problemevolution.BlindValidationOutcome{
		CandidateRef: "c1",
		Score:        blindScore(0.2),
		Seed:         current.BlindSeed,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	settled, err := testHandler.Queries.GetProblemEvolutionRun(ctx, db.GetProblemEvolutionRunParams{
		ID:          run.ID,
		WorkspaceID: run.WorkspaceID,
	})
	if err != nil {
		t.Fatalf("reload run: %v", err)
	}
	if !settled.BlindScore.Valid || !settled.OverfitGap.Valid {
		t.Fatal("blind validation did not record a score and gap")
	}
	// Search reported 0.6, blind reported 0.2: the gap is the signal that the
	// run adapted to its search sample.
	if gap := settled.OverfitGap.Float64; gap < 0.39 || gap > 0.41 {
		t.Fatalf("overfit gap = %v, want ~0.4", gap)
	}
}

func blindScore(total float64) problemevolution.Score {
	return problemevolution.Score{
		SchemaVersion:  problemevolution.SchemaVersion,
		Total:          total,
		Scale:          problemevolution.ScaleUnitInterval,
		HardGatePassed: total >= 0.5,
		Dimensions: []problemevolution.ScoreDimension{
			{DimensionID: "correctness", Score: total, Weight: 1, Hard: true},
		},
	}
}
