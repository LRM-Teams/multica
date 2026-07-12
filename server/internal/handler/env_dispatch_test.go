package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/multica-ai/multica/server/internal/middleware"
	"github.com/multica-ai/multica/server/internal/service"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// doEnvDispatch builds an authenticated env-dispatch request with a workspace
// context and invokes h.EnvDispatch, mirroring the inline setup the other
// validation tests use. It relies on a DB-less handler (Queries == nil) so
// only the handler's UUID-shape gate + the service's validation gate are
// exercised; the stub deps short-circuit before any DB access.
func doEnvDispatch(t *testing.T, body string) *httptest.ResponseRecorder {
	t.Helper()
	h := newTestHandler(Config{})
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/api/v1/env-dispatch", bytes.NewReader([]byte(body)))
	r.Header.Set("X-User-ID", "u1")
	r = r.WithContext(middleware.SetMemberContext(r.Context(), "ws1", db.Member{}))
	h.EnvDispatch(w, r)
	return w
}

func TestEnvDispatch_RequiresAuth(t *testing.T) {
	h := newTestHandler(Config{})
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/api/v1/env-dispatch", bytes.NewReader([]byte(`{}`)))
	h.EnvDispatch(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", w.Code)
	}
}

// validUUID is a syntactically valid UUID used by handler tests that need to
// pass the handler's UUID-shape gate and exercise deeper (service) validation.
const validUUID = "11111111-1111-1111-1111-111111111111"

func TestEnvDispatch_RejectsMissingMode(t *testing.T) {
	h := newTestHandler(Config{})
	w := httptest.NewRecorder()
	body := `{"env_id":"` + validUUID + `","dispatch_type":"issue","group_size":1,"agent_id":"` + validUUID + `","domain":"swe_lego","issue":{"title":"t"}}`
	r := httptest.NewRequest("POST", "/api/v1/env-dispatch", bytes.NewReader([]byte(body)))
	r.Header.Set("X-User-ID", "u1")
	r = r.WithContext(middleware.SetMemberContext(r.Context(), "ws1", db.Member{}))
	h.EnvDispatch(w, r)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestEnvDispatch_RejectsMalformedEnvID(t *testing.T) {
	h := newTestHandler(Config{})
	w := httptest.NewRecorder()
	body := `{"mode":"scratch","env_id":"not-a-uuid","domain":"swe_lego","dispatch_type":"issue","group_size":1,"agent_id":"` + validUUID + `","issue":{"title":"t"}}`
	r := httptest.NewRequest("POST", "/api/v1/env-dispatch", bytes.NewReader([]byte(body)))
	r.Header.Set("X-User-ID", "u1")
	r = r.WithContext(middleware.SetMemberContext(r.Context(), "ws1", db.Member{}))
	h.EnvDispatch(w, r)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (malformed env_id must not panic)", w.Code)
	}
}

func TestEnvDispatch_RejectsSweLegoMessage(t *testing.T) {
	h := newTestHandler(Config{})
	w := httptest.NewRecorder()
	body := `{"mode":"scratch","env_id":"` + validUUID + `","domain":"swe_lego","dispatch_type":"message","group_size":1,"agent_id":"` + validUUID + `","message":{"content":"q"}}`
	r := httptest.NewRequest("POST", "/api/v1/env-dispatch", bytes.NewReader([]byte(body)))
	r.Header.Set("X-User-ID", "u1")
	r = r.WithContext(middleware.SetMemberContext(r.Context(), "ws1", db.Member{}))
	h.EnvDispatch(w, r)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestEnvDispatch_SelfPlayIssue_Returns501(t *testing.T) {
	h := newTestHandler(Config{})
	w := httptest.NewRecorder()
	body := `{"mode":"scratch","env_id":"` + validUUID + `","domain":"self_play","dispatch_type":"issue","group_size":1,"agent_id":"` + validUUID + `","issue":{"title":"t"}}`
	r := httptest.NewRequest("POST", "/api/v1/env-dispatch", bytes.NewReader([]byte(body)))
	r.Header.Set("X-User-ID", "u1")
	r = r.WithContext(middleware.SetMemberContext(r.Context(), "ws1", db.Member{}))
	h.EnvDispatch(w, r)
	if w.Code != http.StatusNotImplemented {
		t.Fatalf("status = %d, want 501", w.Code)
	}
}

func TestDeleteEnvDispatchProject_RequiresProjectID(t *testing.T) {
	h := newTestHandler(Config{})
	w := httptest.NewRecorder()
	r := httptest.NewRequest("DELETE", "/api/v1/env-dispatch/", nil)
	r.Header.Set("X-User-ID", "u1")
	r = r.WithContext(middleware.SetMemberContext(r.Context(), "ws1", db.Member{}))
	h.DeleteEnvDispatchProject(w, r)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestEnvDispatch_RejectsBothAgentAndSquad(t *testing.T) {
	body := `{"mode":"scratch","env_id":"` + validUUID + `","domain":"self_play","dispatch_type":"message","group_size":1,"agent_id":"` + validUUID + `","squad_id":"` + validUUID + `","message":{"content":"hi"}}`
	rr := doEnvDispatch(t, body)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("both agent+squad: want 400, got %d %s", rr.Code, rr.Body.String())
	}
}

func TestEnvDispatch_AcceptsEmptyEnvIDShape(t *testing.T) {
	// empty env_id must not be rejected by the handler's UUID-shape gate
	// (which would emit "invalid env_id"); the service decides whether an
	// empty env_id is allowed (scratch self_play resolves a default).
	body := `{"mode":"scratch","env_id":"","domain":"self_play","dispatch_type":"message","group_size":1,"agent_id":"` + validUUID + `","message":{"content":"hi"}}`
	rr := doEnvDispatch(t, body)
	if rr.Code == http.StatusBadRequest && strings.Contains(rr.Body.String(), "invalid env_id") {
		t.Fatalf("empty env_id must pass the handler UUID gate, got %d %s", rr.Code, rr.Body.String())
	}
}

func TestEnvDispatch_RejectsMalformedTrainAgentID(t *testing.T) {
	// A malformed train_agent_id must be rejected by the handler's UUID-shape
	// gate with a 400 (mirroring the agent_id/squad_id shape checks) instead of
	// panicking deeper in the adapter.
	body := `{"mode":"scratch","env_id":"` + validUUID + `","domain":"swe_lego","dispatch_type":"issue","group_size":1,"agent_id":"` + validUUID + `","train_agent_id":"not-a-uuid","issue":{"title":"t"}}`
	rr := doEnvDispatch(t, body)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (malformed train_agent_id must not panic)", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "invalid train_agent_id") {
		t.Fatalf("body = %s, want it to mention invalid train_agent_id", rr.Body.String())
	}
}

func TestEnvDispatch_AcceptsWellFormedTrainAgentID(t *testing.T) {
	// A well-formed train_agent_id equal to agent_id (single-agent training)
	// must pass the handler's UUID-shape gate. Using train_agent_id == agent_id
	// also satisfies the service validate() rule so no 400 is emitted.
	body := `{"mode":"scratch","env_id":"` + validUUID + `","domain":"self_play","dispatch_type":"message","group_size":1,"agent_id":"` + validUUID + `","train_agent_id":"` + validUUID + `","message":{"content":"hi"}}`
	rr := doEnvDispatch(t, body)
	if rr.Code == http.StatusBadRequest && strings.Contains(rr.Body.String(), "train_agent_id") {
		t.Fatalf("well-formed train_agent_id must pass shape validation, got %d %s", rr.Code, rr.Body.String())
	}
}

func TestEnvDispatch_AcceptsResumeMode(t *testing.T) {
	body := `{"mode":"resume","env_id":"` + validUUID + `","domain":"swe_lego","dispatch_type":"issue","group_size":1,"agent_id":"` + validUUID + `","issue":{"title":"t"}}`
	rr := doEnvDispatch(t, body)
	if rr.Code == http.StatusBadRequest && strings.Contains(rr.Body.String(), "mode") {
		t.Fatalf("resume must be accepted as a mode, got %d %s", rr.Code, rr.Body.String())
	}
}

func TestEnvDispatchHandler_CriticAgentID_ShapeValidation(t *testing.T) {
	// 400 on malformed UUID
	body := `{"squad_id":"` + validUUID + `","mode":"scratch","env_id":"` + validUUID + `","domain":"self_play","dispatch_type":"message","group_size":1,"train_agent_id":"` + validUUID + `","critic_agent_id":"not-a-uuid","message":{"content":"hi"}}`
	req := httptest.NewRequest("POST", "/api/v1/env-dispatch", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	h := newTestHandler(Config{})
	w := httptest.NewRecorder()
	req.Header.Set("X-User-ID", "u1")
	req = req.WithContext(middleware.SetMemberContext(req.Context(), "ws1", db.Member{}))
	h.EnvDispatch(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
	if !strings.Contains(w.Body.String(), "invalid critic_agent_id") {
		t.Fatalf("body = %s, want it to mention invalid critic_agent_id", w.Body.String())
	}
}

// TestEnvDispatch_ParsesPerAgentEnv verifies that the per_agent_env JSON field
// is parsed from the request and passed to the service as PerAgentEnvSpecs.
// A spec with neither template nor base_env_id triggers the service's shape
// validation error, proving the field reached the service layer.
func TestEnvDispatch_ParsesPerAgentEnv(t *testing.T) {
	body := `{"mode":"scratch","env_id":"` + validUUID + `","domain":"self_play","dispatch_type":"message","group_size":1,"squad_id":"` + validUUID + `","message":{"content":"hi"},"per_agent_env":{"` + validUUID + `":{}}}`
	w := doEnvDispatch(t, body)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (shape validation); body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "per_agent_env spec for agent") {
		t.Fatalf("body should mention per_agent_env shape error; got %s", w.Body.String())
	}
}

// TestMapRollouts_IncludesSandboxRefs verifies that SandboxRefs and
// AgentSandboxRefs from the service rollout are carried into the handler
// response and serialized under their JSON field names.
func TestMapRollouts_IncludesSandboxRefs(t *testing.T) {
	rollouts := []service.EnvRollout{
		{
			EnvID: "env-1", ProjectID: "proj-1",
			SandboxRefs: []service.SandboxInstanceRef{
				{InstanceID: "inst-1", WorkspaceID: "ws", Template: "python"},
			},
			AgentSandboxRefs: map[string]service.SandboxInstanceRef{
				"a1": {InstanceID: "inst-1", WorkspaceID: "ws", Template: "python"},
			},
		},
	}
	out := mapRollouts(rollouts)
	if len(out) != 1 {
		t.Fatalf("want 1 rollout, got %d", len(out))
	}
	if len(out[0].SandboxRefs) != 1 || out[0].SandboxRefs[0].InstanceID != "inst-1" {
		t.Fatalf("unexpected sandbox_refs: %+v", out[0].SandboxRefs)
	}
	if len(out[0].AgentSandboxRefs) != 1 {
		t.Fatalf("unexpected agent_sandbox_refs: %+v", out[0].AgentSandboxRefs)
	}
	if ref, ok := out[0].AgentSandboxRefs["a1"]; !ok || ref.InstanceID != "inst-1" {
		t.Fatalf("missing or wrong ref for a1: %+v", out[0].AgentSandboxRefs)
	}
	body, _ := json.Marshal(out[0])
	if !strings.Contains(string(body), "sandbox_refs") {
		t.Fatalf("JSON should include sandbox_refs: %s", body)
	}
	if !strings.Contains(string(body), "agent_sandbox_refs") {
		t.Fatalf("JSON should include agent_sandbox_refs: %s", body)
	}
}

// TestMapRollouts_OmitsEmptySandboxRefs verifies that empty refs are omitted
// from the JSON response via omitempty, so non-checkpointed rollouts don't
// carry empty arrays/objects.
func TestMapRollouts_OmitsEmptySandboxRefs(t *testing.T) {
	out := mapRollouts([]service.EnvRollout{{EnvID: "env-1", ProjectID: "proj-1"}})
	body, _ := json.Marshal(out[0])
	if strings.Contains(string(body), "sandbox_refs") {
		t.Fatalf("JSON should omit empty sandbox_refs: %s", body)
	}
	if strings.Contains(string(body), "agent_sandbox_refs") {
		t.Fatalf("JSON should omit empty agent_sandbox_refs: %s", body)
	}
}

// --- GET /api/v1/env-dispatch/{projectID}/dag (Task 9, U8) ---
//
// AReaL polls this endpoint to read the assembled segment-DAG for a trained
// rollout. The contract:
//   - 202 + {"status":"in_progress"} when the rollout's root training task is
//     not yet terminal (queued/running/...).
//   - 200 + the AssembledDag JSON when the root training task is terminal AND
//     the recorded segments densely cover every session.
//   - 200 + {"status":"failed"} when the root task is terminal but the recorded
//     segments do NOT densely cover (a coverage gap) - D14: never serve a
//     partial DAG.
//   - 403 when the project exists but in another workspace.
//   - 404 when the project does not exist at all.
//
// The tests use the shared real-Postgres fixtures (testPool/testHandler) like
// cancel_task_by_user_test.go, seeding project + issue + training_dispatch +
// agent_task_queue rows directly.

// getDagStatusBody decodes the JSON status field from a GetDag response.
func getDagStatusBody(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body %q: %v", w.Body.String(), err)
	}
	if s, ok := body["status"].(string); ok {
		return s
	}
	return ""
}

// getDagRequest builds an authenticated GET /dag request scoped to the test
// workspace, with the projectID chi URL param set, mirroring the inline chi
// context setup used by the cancel-task tests.
func getDagRequest(t *testing.T, projectID string) *http.Request {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/env-dispatch/"+projectID+"/dag", nil)
	r.Header.Set("X-User-ID", testUserID)
	memberRow, err := testHandler.Queries.GetMemberByUserAndWorkspace(context.Background(), db.GetMemberByUserAndWorkspaceParams{
		UserID:      util.MustParseUUID(testUserID),
		WorkspaceID: util.MustParseUUID(testWorkspaceID),
	})
	if err != nil {
		t.Fatalf("load test member row: %v", err)
	}
	r = r.WithContext(middleware.SetMemberContext(r.Context(), testWorkspaceID, memberRow))
	return withURLParam(r, "projectID", projectID)
}

// seedTrainingRollout seeds a training rollout project in the test workspace:
// a project, an issue bound to it, a training_dispatch row, and the root
// training task (agent_task_queue) with the given status. It returns the
// project ID (string) and the root task ID. The agent is created in the given
// workspace (test workspace for in-workspace tests; a foreign workspace for the
// cross-workspace test). All rows are cleaned up via t.Cleanup in reverse
// dependency order.
func seedTrainingRollout(t *testing.T, workspaceID, agentID, taskStatus string) (projectID, taskID string) {
	t.Helper()
	ctx := context.Background()

	if err := testPool.QueryRow(ctx, `
		INSERT INTO project (workspace_id, title)
		VALUES ($1, 'GetDag Test Project')
		RETURNING id
	`, workspaceID).Scan(&projectID); err != nil {
		t.Fatalf("create project: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(context.Background(), `DELETE FROM project WHERE id = $1`, projectID) })

	var issueID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO issue (workspace_id, title, status, priority, creator_id, creator_type, number, position, project_id)
		VALUES ($1, 'GetDag Test Issue', 'todo', 'medium', $2, 'member', 93001, 0, $3)
		RETURNING id
	`, workspaceID, testUserID, projectID).Scan(&issueID); err != nil {
		t.Fatalf("create issue: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(context.Background(), `DELETE FROM issue WHERE id = $1`, issueID) })

	if _, err := testPool.Exec(ctx, `
		INSERT INTO training_dispatch (project_id, workspace_id, train_agent_id, default_reward)
		VALUES ($1, $2, $3, 1.0)
	`, projectID, workspaceID, agentID); err != nil {
		t.Fatalf("create training_dispatch: %v", err)
	}
	// training_dispatch cascades with project (project_id PK -> ON DELETE CASCADE).

	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_task_queue (agent_id, runtime_id, status, priority, issue_id)
		VALUES ($1, (SELECT runtime_id FROM agent WHERE id = $1), $2, 5, $3)
		RETURNING id
	`, agentID, taskStatus, issueID).Scan(&taskID); err != nil {
		t.Fatalf("create root training task: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(context.Background(), `DELETE FROM agent_task_queue WHERE id = $1`, taskID) })

	return projectID, taskID
}

// seedDAGSegment inserts a session_run + segment (with env snapshot) for a
// project so AssembleAssembledDag returns a non-empty DAG. agentRunID is the
// root training task's ID (D8: agent_run_id = task.ID). The interaction_dag
// tables use TEXT project_id, so the UUID string is passed through.
func seedDAGSegment(t *testing.T, projectID, sessionID, agentRunID string, trajectoryID int64) {
	t.Helper()
	ctx := context.Background()
	if _, err := testPool.Exec(ctx, `
		INSERT INTO interaction_dag_session_run (session_id, project_id, agent_run_id)
		VALUES ($1, $2, $3)
	`, sessionID, projectID, agentRunID); err != nil {
		t.Fatalf("insert session_run: %v", err)
	}
	segID := sessionID + "-" + strconv.FormatInt(trajectoryID, 10)
	if _, err := testPool.Exec(ctx, `
		WITH seg AS (
		  INSERT INTO interaction_dag_segment (segment_id, project_id, agent_run_id, trajectory_id, tensor_ref, closing_event)
		  VALUES ($1, $2, $3, $4, $5, NULL)
		)
		INSERT INTO interaction_dag_env_snapshot (segment_id, sandbox_ids, env_state)
		VALUES ($1, '[]'::jsonb, '{}'::jsonb)
	`, segID, projectID, agentRunID, trajectoryID, []byte(`{"shard_id":"shard-1"}`)); err != nil {
		t.Fatalf("insert segment+env_snapshot: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM interaction_dag_env_snapshot WHERE segment_id = $1`, segID)
		testPool.Exec(context.Background(), `DELETE FROM interaction_dag_segment WHERE segment_id = $1`, segID)
		testPool.Exec(context.Background(), `DELETE FROM interaction_dag_session_run WHERE session_id = $1`, sessionID)
	})
}

// seedDAGSessionRunOnly inserts a session_run WITHOUT a matching segment, so
// the session's agent_run has a coverage gap (denseCover == false). Used by the
// Incomplete test.
func seedDAGSessionRunOnly(t *testing.T, projectID, sessionID, agentRunID string) {
	t.Helper()
	ctx := context.Background()
	if _, err := testPool.Exec(ctx, `
		INSERT INTO interaction_dag_session_run (session_id, project_id, agent_run_id)
		VALUES ($1, $2, $3)
	`, sessionID, projectID, agentRunID); err != nil {
		t.Fatalf("insert session_run: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM interaction_dag_session_run WHERE session_id = $1`, sessionID)
	})
}

// TestGetDag_InProgressReturns202 verifies that a rollout whose root training
// task is still running returns 202 + {"status":"in_progress"} and does NOT
// attempt to assemble/serve a (partial) DAG.
func TestGetDag_InProgressReturns202(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	agentID := createHandlerTestAgent(t, "GetDagInProgressAgent", []byte("[]"))
	projectID, _ := seedTrainingRollout(t, testWorkspaceID, agentID, "running")

	w := httptest.NewRecorder()
	testHandler.GetDag(w, getDagRequest(t, projectID))
	if w.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202; body=%s", w.Code, w.Body.String())
	}
	if got := getDagStatusBody(t, w); got != "in_progress" {
		t.Fatalf("status body = %q, want \"in_progress\"; body=%s", got, w.Body.String())
	}
}

// TestGetDag_DoneReturns200AssembledDag verifies that a rollout whose root
// training task is terminal (completed) AND whose recorded segments densely
// cover the session returns 200 + the AssembledDag JSON (with segments +
// session_to_agent_run populated).
func TestGetDag_DoneReturns200AssembledDag(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	agentID := createHandlerTestAgent(t, "GetDagDoneAgent", []byte("[]"))
	projectID, taskID := seedTrainingRollout(t, testWorkspaceID, agentID, "completed")
	// Record the session -> agent_run mapping + a dense covering segment.
	seedDAGSegment(t, projectID, "dag-done-sess", taskID, 7)

	w := httptest.NewRecorder()
	testHandler.GetDag(w, getDagRequest(t, projectID))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	var dag service.AssembledDag
	if err := json.Unmarshal(w.Body.Bytes(), &dag); err != nil {
		t.Fatalf("decode AssembledDag %q: %v", w.Body.String(), err)
	}
	if len(dag.Segments) != 1 {
		t.Fatalf("want 1 segment, got %d (%+v)", len(dag.Segments), dag.Segments)
	}
	if dag.Segments[0].AgentRunID != taskID {
		t.Fatalf("segment agent_run_id = %q, want %q", dag.Segments[0].AgentRunID, taskID)
	}
	if len(dag.SessionToAgentRun) != 1 || dag.SessionToAgentRun["dag-done-sess"] != taskID {
		t.Fatalf("session_to_agent_run = %+v, want {dag-done-sess: %s}", dag.SessionToAgentRun, taskID)
	}
}

// TestGetDag_UnknownProjectReturns404 verifies that a project ID that does not
// exist at all returns 404 (not 403 and not a partial DAG).
func TestGetDag_UnknownProjectReturns404(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	// A syntactically valid UUID that does not correspond to any project row.
	const unknown = "00000000-0000-0000-0000-000000000000"

	w := httptest.NewRecorder()
	testHandler.GetDag(w, getDagRequest(t, unknown))
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", w.Code, w.Body.String())
	}
}

// TestGetDag_CrossWorkspaceReturns403 verifies that a project which exists but
// in a different workspace returns 403 (cross-workspace forbidden), not 404
// and not 200.
func TestGetDag_CrossWorkspaceReturns403(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	// Create an agent (and its project) in a foreign workspace.
	foreignAgentID := createForeignWorkspaceAgent(t)
	// seedTrainingRollout creates the project in the foreign agent's workspace.
	foreignWorkspaceID := agentWorkspaceID(t, foreignAgentID)
	projectID, _ := seedTrainingRollout(t, foreignWorkspaceID, foreignAgentID, "completed")

	// The request is scoped to the TEST workspace, so the project (in the
	// foreign workspace) must be rejected with 403.
	w := httptest.NewRecorder()
	testHandler.GetDag(w, getDagRequest(t, projectID))
	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body=%s", w.Code, w.Body.String())
	}
}

// TestGetDag_IncompleteRolloutReturnsFailedStatus verifies that a rollout whose
// root training task is terminal (completed) but whose recorded segments do NOT
// densely cover the session returns 200 + {"status":"failed"} (D14: never serve
// a partial DAG). The gap: a session_run exists for an agent_run with no
// matching segment.
func TestGetDag_IncompleteRolloutReturnsFailedStatus(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	agentID := createHandlerTestAgent(t, "GetDagIncompleteAgent", []byte("[]"))
	projectID, taskID := seedTrainingRollout(t, testWorkspaceID, agentID, "completed")
	// Record a session_run for the root task's agent_run WITHOUT a covering
	// segment -> denseCover == false -> failed status.
	seedDAGSessionRunOnly(t, projectID, "dag-incomplete-sess", taskID)

	w := httptest.NewRecorder()
	testHandler.GetDag(w, getDagRequest(t, projectID))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	if got := getDagStatusBody(t, w); got != "failed" {
		t.Fatalf("status body = %q, want \"failed\"; body=%s", got, w.Body.String())
	}
}

// agentWorkspaceID resolves the workspace_id of an agent (used to scope a
// foreign-workspace rollout to the agent's own workspace).
func agentWorkspaceID(t *testing.T, agentID string) string {
	t.Helper()
	var wsID string
	if err := testPool.QueryRow(context.Background(),
		`SELECT workspace_id FROM agent WHERE id = $1`, agentID,
	).Scan(&wsID); err != nil {
		t.Fatalf("load agent workspace: %v", err)
	}
	return wsID
}
