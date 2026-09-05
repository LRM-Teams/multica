package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/internal/middleware"
	"github.com/multica-ai/multica/server/internal/problemevolution"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// newProblemEvolutionRequest builds a workspace-scoped request the way the
// workspace middleware would, since these handlers are invoked directly.
func newProblemEvolutionRequest(t *testing.T, method, path string, body any, routeParams ...string) *http.Request {
	t.Helper()
	req := newRequest(method, path, body)
	member, err := testHandler.Queries.GetMemberByUserAndWorkspace(context.Background(), db.GetMemberByUserAndWorkspaceParams{
		WorkspaceID: parseUUID(testWorkspaceID),
		UserID:      parseUUID(testUserID),
	})
	if err != nil {
		t.Fatalf("load workspace member: %v", err)
	}
	if len(routeParams) > 0 {
		req = withRouteParams(req, routeParams...)
	}
	return req.WithContext(middleware.SetMemberContext(req.Context(), testWorkspaceID, member))
}

func createSolveRun(t *testing.T) string {
	t.Helper()
	rec := httptest.NewRecorder()
	req := newProblemEvolutionRequest(t, http.MethodPost, "/api/problem-evolution/runs", map[string]any{
		"mode":  "solution",
		"title": "solve-" + uuid.NewString()[:8],
		"problem_spec": map[string]any{
			"statement":     "Prove the bound and report the complexity.",
			"artifact_type": "markdown",
		},
	})
	testHandler.CreateProblemEvolutionRun(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create run status = %d, want 201: %s", rec.Code, rec.Body.String())
	}
	var run ProblemEvolutionRunResponse
	if err := json.NewDecoder(rec.Body).Decode(&run); err != nil {
		t.Fatalf("decode run: %v", err)
	}
	return run.ID
}

func validEvaluatorBody() map[string]any {
	return map[string]any{
		"contract": map[string]any{
			"schema_version": problemevolution.SchemaVersion,
			"kind":           problemevolution.EvaluatorKindBuiltinDeterministic,
			"dimensions": []map[string]any{
				{"dimension_id": "correctness", "weight": 0.5, "hard": true},
				{"dimension_id": "coverage", "weight": 0.5},
			},
			"pass_threshold": 0.8,
			"invoke": map[string]any{
				"transport":   "cli",
				"command":     []string{"multica", "problem-evolution", "evaluate"},
				"input_path":  problemevolution.DefaultEvaluatorInput,
				"output_path": problemevolution.DefaultEvaluatorOutput,
			},
		},
	}
}

func attachEvaluator(t *testing.T, runID string) {
	t.Helper()
	rec := httptest.NewRecorder()
	req := newProblemEvolutionRequest(t, http.MethodPost,
		"/api/problem-evolution/runs/"+runID+"/evaluator", validEvaluatorBody(), "runId", runID)
	testHandler.CreateProblemEvolutionEvaluator(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create evaluator status = %d, want 201: %s", rec.Code, rec.Body.String())
	}
}

func freezeEvaluator(t *testing.T, runID string) ProblemEvolutionEvaluatorContractResponse {
	t.Helper()
	rec := httptest.NewRecorder()
	req := newProblemEvolutionRequest(t, http.MethodPost,
		"/api/problem-evolution/runs/"+runID+"/evaluator/freeze", nil, "runId", runID)
	testHandler.FreezeProblemEvolutionEvaluator(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("freeze status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var contract ProblemEvolutionEvaluatorContractResponse
	if err := json.NewDecoder(rec.Body).Decode(&contract); err != nil {
		t.Fatalf("decode contract: %v", err)
	}
	return contract
}

func TestStartProblemEvolutionRunRequiresFrozenEvaluator(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	runID := createSolveRun(t)
	attachEvaluator(t, runID)

	rec := httptest.NewRecorder()
	req := newProblemEvolutionRequest(t, http.MethodPost,
		"/api/problem-evolution/runs/"+runID+"/start", nil, "runId", runID)
	testHandler.StartProblemEvolutionRun(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("start with a draft contract status = %d, want 409: %s", rec.Code, rec.Body.String())
	}
}

func TestStartProblemEvolutionRunPinsFrozenContractHash(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	runID := createSolveRun(t)
	attachEvaluator(t, runID)
	contract := freezeEvaluator(t, runID)
	if contract.ContentHash == "" {
		t.Fatal("expected freezing to produce a content hash")
	}

	rec := httptest.NewRecorder()
	req := newProblemEvolutionRequest(t, http.MethodPost,
		"/api/problem-evolution/runs/"+runID+"/start", nil, "runId", runID)
	testHandler.StartProblemEvolutionRun(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("start status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var run ProblemEvolutionRunResponse
	if err := json.NewDecoder(rec.Body).Decode(&run); err != nil {
		t.Fatalf("decode run: %v", err)
	}
	if run.Status != "queued" {
		t.Fatalf("run status = %q, want queued", run.Status)
	}
	if run.EvaluatorContentHash != contract.ContentHash {
		t.Fatalf("pinned hash = %q, want %q", run.EvaluatorContentHash, contract.ContentHash)
	}
}

func TestFreezeProblemEvolutionEvaluatorRejectsUserVerifierKind(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	runID := createSolveRun(t)
	body := validEvaluatorBody()
	contract := body["contract"].(map[string]any)
	contract["kind"] = "user_python_verifier"

	rec := httptest.NewRecorder()
	req := newProblemEvolutionRequest(t, http.MethodPost,
		"/api/problem-evolution/runs/"+runID+"/evaluator", body, "runId", runID)
	testHandler.CreateProblemEvolutionEvaluator(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("create evaluator status = %d, want 400: %s", rec.Code, rec.Body.String())
	}
}

func TestUpdateProblemEvolutionRunRejectedAfterQueueing(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	runID := createSolveRun(t)
	attachEvaluator(t, runID)
	freezeEvaluator(t, runID)

	startRec := httptest.NewRecorder()
	testHandler.StartProblemEvolutionRun(startRec, newProblemEvolutionRequest(t, http.MethodPost,
		"/api/problem-evolution/runs/"+runID+"/start", nil, "runId", runID))
	if startRec.Code != http.StatusOK {
		t.Fatalf("start status = %d: %s", startRec.Code, startRec.Body.String())
	}

	rec := httptest.NewRecorder()
	title := "renamed after queueing"
	req := newProblemEvolutionRequest(t, http.MethodPatch,
		"/api/problem-evolution/runs/"+runID, map[string]any{"title": title}, "runId", runID)
	testHandler.UpdateProblemEvolutionRun(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("update status = %d, want 409: %s", rec.Code, rec.Body.String())
	}
}

func TestProblemEvolutionSnapshotReportsGraphVersion(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	runID := createSolveRun(t)
	attachEvaluator(t, runID)
	freezeEvaluator(t, runID)

	rec := httptest.NewRecorder()
	req := newProblemEvolutionRequest(t, http.MethodGet,
		"/api/problem-evolution/runs/"+runID+"/snapshot", nil, "runId", runID)
	testHandler.GetProblemEvolutionSnapshot(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("snapshot status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var snapshot ProblemEvolutionSnapshotResponse
	if err := json.NewDecoder(rec.Body).Decode(&snapshot); err != nil {
		t.Fatalf("decode snapshot: %v", err)
	}
	if snapshot.Evaluator == nil || snapshot.Evaluator.Status != "frozen" {
		t.Fatalf("expected a frozen evaluator in the snapshot, got %+v", snapshot.Evaluator)
	}
	if len(snapshot.Candidates) != 0 {
		t.Fatalf("expected no candidates before the run executes, got %d", len(snapshot.Candidates))
	}
}
