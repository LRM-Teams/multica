package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/middleware"
	"github.com/multica-ai/multica/server/internal/service"
	"github.com/multica-ai/multica/server/internal/util"
	"github.com/multica-ai/multica/server/internal/util/stackerr"
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

// TestWriteEnvDispatchError_TracebackFromAdapterStack verifies that an
// adapter-origin error (stackerr-wrapped, as envDispatchDepsAdapter produces)
// surfaces its origin goroutine stack in a response "traceback" field, plus the
// wrapped message chain. Exercises the default 503 (internal) branch.
func TestWriteEnvDispatchError_TracebackFromAdapterStack(t *testing.T) {
	err := stackerr.Wrap(errors.New("connection refused"), "get env")
	w := httptest.NewRecorder()
	writeEnvDispatchError(w, err, service.EnvDispatchResult{})
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", w.Code)
	}
	var body map[string]any
	if decErr := json.NewDecoder(w.Body).Decode(&body); decErr != nil {
		t.Fatalf("decode body: %v", decErr)
	}
	if body["error"] != "internal" {
		t.Fatalf("error = %v, want internal", body["error"])
	}
	if msg, _ := body["message"].(string); !strings.Contains(msg, "get env") || !strings.Contains(msg, "connection refused") {
		t.Fatalf("message = %q, want the wrapped chain", msg)
	}
	tb, _ := body["traceback"].(string)
	if !strings.Contains(tb, "TestWriteEnvDispatchError_TracebackFromAdapterStack") {
		t.Fatalf("traceback missing the capturing test frame:\n%s", tb)
	}
}

// TestWriteEnvDispatchError_ValidationFailureNotLoggedAtError pins LRM-640: a
// 4xx client-validation failure (the caller sent a malformed body, e.g. an
// empty message.content) must NOT be logged at Error level — the traceback is
// the server's stack, which is meaningless when the server did nothing wrong,
// and a per-request Error line is what turned one runaway client into a
// multi-thousand-line log flood. The 400 response + body are still returned so
// the caller sees the reason; the RequestLogger still records the 4xx as WARN.
func TestWriteEnvDispatchError_ValidationFailureNotLoggedAtError(t *testing.T) {
	// Capture slog output for the duration of the call.
	buf := &bytes.Buffer{}
	orig := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(orig) })

	err := fmt.Errorf("validation_failed: message.content required")
	w := httptest.NewRecorder()
	writeEnvDispatchError(w, err, service.EnvDispatchResult{})

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
	var body map[string]any
	if decErr := json.NewDecoder(w.Body).Decode(&body); decErr != nil {
		t.Fatalf("decode body: %v", decErr)
	}
	if body["error"] != "validation_failed" {
		t.Fatalf("error = %v, want validation_failed", body["error"])
	}
	if out := buf.String(); strings.Contains(out, "level=ERROR") || strings.Contains(out, "env_dispatch failed") {
		t.Fatalf("validation_failed (client error) must not be logged at Error; got:\n%s", out)
	}
}

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

func TestEnvDispatch_AcceptsResumeMode(t *testing.T) {
	body := `{"mode":"resume","env_id":"` + validUUID + `","domain":"swe_lego","dispatch_type":"issue","group_size":1,"agent_id":"` + validUUID + `","issue":{"title":"t"}}`
	rr := doEnvDispatch(t, body)
	if rr.Code == http.StatusBadRequest && strings.Contains(rr.Body.String(), "mode") {
		t.Fatalf("resume must be accepted as a mode, got %d %s", rr.Code, rr.Body.String())
	}
}

func TestEnvDispatchHandler_CriticAgentID_ShapeValidation(t *testing.T) {
	// 400 on malformed UUID
	body := `{"agent_id":"` + validUUID + `","mode":"scratch","env_id":"` + validUUID + `","domain":"self_play","dispatch_type":"message","group_size":1,"critic_agent_id":"not-a-uuid","message":{"content":"hi"}}`
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
	body := `{"mode":"scratch","env_id":"` + validUUID + `","domain":"self_play","dispatch_type":"message","group_size":1,"agent_id":"` + validUUID + `","message":{"content":"hi"},"per_agent_env":{"` + validUUID + `":{}}}`
	w := doEnvDispatch(t, body)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (shape validation); body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "per_agent_env spec for agent") {
		t.Fatalf("body should mention per_agent_env shape error; got %s", w.Body.String())
	}

	// A runtime-only spec advances past the old "needs a template or base_env_id"
	// shape error: the runtime is a valid scratch policy, so synchronous shape
	// validation passes and the dispatch proceeds. The synthetic API key must
	// not surface in the response.
	runtimeBody := `{"mode":"scratch","env_id":"` + validUUID + `","domain":"self_play","dispatch_type":"message","group_size":1,"agent_id":"` + validUUID + `","message":{"content":"hi"},"per_agent_env":{"` + validUUID + `":{"runtime":{"base_url":"https://provider.invalid/v1","api_key":"synthetic-secret-for-tests","model":"model-a"}}}}`
	w2 := doEnvDispatch(t, runtimeBody)
	if strings.Contains(w2.Body.String(), "needs a template") {
		t.Fatalf("runtime-only spec must advance past the shape error; got %s", w2.Body.String())
	}
	if strings.Contains(w2.Body.String(), "synthetic-secret-for-tests") {
		t.Fatalf("response must not contain the runtime API key; got %s", w2.Body.String())
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
	// The runtime policy is a separate internal type
	// (ResolvedPerAgentSandboxPolicy) and is never stored on SandboxInstanceRef,
	// so the marshaled rollout JSON - which carries only SandboxInstanceRef -
	// cannot leak the API key. The type-level guarantee is asserted in
	// TestEnvDispatchSandboxConfigCodec_SandboxInstanceRefHasNoRuntimeSecret.
	if strings.Contains(string(body), "synthetic-secret-for-tests") {
		t.Fatalf("rollout JSON must not contain the runtime API key: %s", body)
	}
}

// TestMapPerAgentEnvSpecs verifies mapPerAgentEnvSpecs maps template,
// base_env_id, and runtime from the request to the service spec. The runtime
// is mapped field-by-field into a new service.ExternalModelRuntime (the handler
// and service types differ, so the compiler enforces a fresh allocation rather
// than retaining the handler-layer pointer).
func TestMapPerAgentEnvSpecs(t *testing.T) {
	in := map[string]PerAgentEnvRequest{
		"agent-template": {Template: "python"},
		"agent-base":     {BaseEnvID: "base-env-1"},
		"agent-runtime": {Runtime: &ExternalModelRuntimeRequest{
			Provider: "anthropic",
			BaseURL:  "https://provider.invalid/v1",
			APIKey:   "synthetic-secret-for-tests",
			Model:    "model-a",
		}},
	}
	out := mapPerAgentEnvSpecs(in)
	if len(out) != 3 {
		t.Fatalf("want 3 specs, got %d (%+v)", len(out), out)
	}
	byAgent := map[string]service.PerAgentEnvSpec{}
	for _, s := range out {
		byAgent[s.AgentID] = s
	}
	if byAgent["agent-template"].Template != "python" {
		t.Fatalf("template not mapped: %+v", byAgent["agent-template"])
	}
	if byAgent["agent-base"].BaseEnvID != "base-env-1" {
		t.Fatalf("base_env_id not mapped: %+v", byAgent["agent-base"])
	}
	rt := byAgent["agent-runtime"].Runtime
	if rt == nil {
		t.Fatalf("runtime not mapped")
	}
	if rt.Provider != "anthropic" || rt.BaseURL != "https://provider.invalid/v1" || rt.APIKey != "synthetic-secret-for-tests" || rt.Model != "model-a" {
		t.Fatalf("runtime fields not mapped: %+v", rt)
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
// agent_inbox_event rows directly.

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
// training task (agent_inbox_event) with the given status. It returns the
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
		INSERT INTO agent_inbox_event (agent_id, runtime_id, status, priority, issue_id)
		VALUES ($1, (SELECT runtime_id FROM agent WHERE id = $1), $2, 5, $3)
		RETURNING id
	`, agentID, taskStatus, issueID).Scan(&taskID); err != nil {
		t.Fatalf("create root training task: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(context.Background(), `DELETE FROM agent_inbox_event WHERE id = $1`, taskID) })

	if _, err := testPool.Exec(ctx, `
		INSERT INTO env_dispatch_run (project_id, workspace_id, training_mode, root_task_id)
		VALUES ($1, $2, true, $3)
	`, projectID, workspaceID, taskID); err != nil {
		t.Fatalf("create env_dispatch_run: %v", err)
	}

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
		  INSERT INTO interaction_dag_segment (
		    segment_id, project_id, project_id_at_event, workspace_id, agent_run_id, generation, trajectory_id, tensor_ref, closing_event,
		    memory_type_at_event, graph_projection_eligible_at_event, derivative, trainable_eligible,
		    content_status, provider_capture_status
		  )
		  VALUES ($1, $2, $7, (SELECT workspace_id FROM project WHERE id = $6), $3, $4, $4, $5, NULL,
		    'legacy', false, false, false,
		    'legacy_unverified', 'not_expected')
		)
		INSERT INTO interaction_dag_env_snapshot (segment_id, sandbox_ids, env_state)
		VALUES ($1, '[]'::jsonb, '{}'::jsonb)
	`, segID, projectID, agentRunID, trajectoryID, []byte(`{"shard_id":"shard-1"}`), projectID, projectID); err != nil {
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
	// Stamp a distinctive diagnosis score max so the assertion confirms the
	// /dag boundary serves it (not just the default).
	t.Setenv("DIAGNOSIS_AGENT_SCORE_MAX", "20")
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
	// The /dag boundary stamps the diagnosis scoring scale so AReaL can
	// normalize per-turn scores to [0, 1] without guessing Multica's max.
	if dag.ScoreMax != 20 {
		t.Fatalf("score_max = %d, want 20 (DIAGNOSIS_AGENT_SCORE_MAX)", dag.ScoreMax)
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

// Legacy classification fields are rejected for every value and are never
// translated into mixed classifications. Both malformed and well-formed
// train_agent_id values must fail as removed fields before UUID translation.
func TestEnvDispatch_LegacyTrainingFieldsAreRemoved(t *testing.T) {
	base := `"mode":"scratch","env_id":"` + validUUID + `","domain":"self_play","dispatch_type":"message","group_size":1,"agent_id":"` + validUUID + `","message":{"content":"hi"}`
	cases := map[string]struct {
		fieldName string
		field     string
	}{
		"training_mode true":         {"training_mode", `"training_mode":true`},
		"training_mode false":        {"training_mode", `"training_mode":false`},
		"train_agent_id well-formed": {"train_agent_id", `"train_agent_id":"` + validUUID + `"`},
		"train_agent_id malformed":   {"train_agent_id", `"train_agent_id":"not-a-uuid"`},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			rr := doEnvDispatch(t, `{`+base+`,`+tc.field+`}`)
			if rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), tc.fieldName) || !strings.Contains(rr.Body.String(), "removed") {
				t.Fatalf("legacy %s response = %d %s, want field-specific removed-field 400", tc.fieldName, rr.Code, rr.Body.String())
			}
		})
	}
}

func TestEnvDispatchSuccessResponseIncludesRunIdentityAndStatus(t *testing.T) {
	response := envDispatchSuccessResponse(service.EnvDispatchResult{
		ProjectID:           "project-1",
		QuietWindowMS:       2000,
		TotalTimeoutSeconds: 3300,
		Rollouts: []service.EnvRollout{
			{RunID: "run-1", ProjectID: "project-1"},
		},
	})
	body, err := json.Marshal(response)
	if err != nil {
		t.Fatalf("marshal response: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got := payload["run_id"]; got != "run-1" {
		t.Fatalf("run_id = %v, want run-1", got)
	}
	if got := payload["status"]; got != "running" {
		t.Fatalf("status = %v, want running", got)
	}
}

// TestGetDag_NoEnvDispatchRun_Returns202 verifies the /dag endpoint returns
// 202 {"status":"in_progress"} when a project exists but has no env_dispatch_run
// row (rollout not started). Readiness is derived exclusively from
// env_dispatch_run, so the absence of a row means in_progress. Requires
// Postgres (skips locally); the readiness decision logic is also covered by
// service-level TestEnvDispatch_GetDagReadiness_NoRun.
func TestGetDag_NoEnvDispatchRun_Returns202(t *testing.T) {
	if testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	projectID := seedHandlerDagProject(t, ctx, testWorkspaceID)
	t.Cleanup(func() { testPool.Exec(ctx, `DELETE FROM project WHERE id = $1`, projectID) })

	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/api/v1/env-dispatch/"+projectID+"/dag", nil)
	r = r.WithContext(middleware.SetMemberContext(r.Context(), testWorkspaceID, db.Member{}))
	testHandler.getEnvDispatchDagForProject(w, r, projectID)
	if w.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202 (no env_dispatch_run -> in_progress); body=%s", w.Code, w.Body.String())
	}
}

// TestGetDag_NonTrainingCompletedRoot_ReturnsNot202 verifies the /dag endpoint
// derives readiness from env_dispatch_run.root_task_id, NOT training_dispatch: a
// training_mode=false dispatch with a terminal (completed) root task and NO
// training_dispatch row returns a 200 (not 202). Requires Postgres (skips
// locally); the readiness decision is also covered by service-level
// TestEnvDispatch_GetDagReadiness_Terminal_NonTrainingRoot.
func TestGetDag_NonTrainingCompletedRoot_ReturnsNot202(t *testing.T) {
	if testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	projectID, rootTaskID := seedHandlerDagNonTrainingCompletedRoot(t, ctx, testWorkspaceID)
	t.Cleanup(func() {
		testPool.Exec(ctx, `DELETE FROM env_dispatch_run WHERE project_id = $1`, projectID)
		testPool.Exec(ctx, `DELETE FROM project WHERE id = $1`, projectID)
	})

	// Override the root task status to completed after fixture creation.
	if _, err := testPool.Exec(ctx, `UPDATE agent_inbox_event SET status = 'acked' WHERE id = $1`, rootTaskID); err != nil {
		t.Fatalf("set root task completed: %v", err)
	}

	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/api/v1/env-dispatch/"+projectID+"/dag", nil)
	r = r.WithContext(middleware.SetMemberContext(r.Context(), testWorkspaceID, db.Member{}))
	testHandler.getEnvDispatchDagForProject(w, r, projectID)
	if w.Code == http.StatusAccepted {
		t.Fatalf("status = 202, want non-202 (completed non-training root via env_dispatch_run, no training_dispatch); body=%s", w.Body.String())
	}
}

// seedHandlerDagProject creates a minimal project in the given workspace and
// returns its UUID string. Used by /dag handler tests that need a project row.
func seedHandlerDagProject(t *testing.T, ctx context.Context, workspaceID string) string {
	t.Helper()
	var projectID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO project (workspace_id, title, status)
		VALUES ($1, 'env-dispatch-dag-test', 'planned')
		RETURNING id
	`, workspaceID).Scan(&projectID); err != nil {
		t.Fatalf("create test project: %v", err)
	}
	return projectID
}

// seedHandlerDagNonTrainingCompletedRoot creates a project, an agent, an issue,
// an agent_inbox_event row (the root task), and an env_dispatch_run row with
// training_mode=false and the root task bound. No training_dispatch row is
// created. Returns (projectID, rootTaskID).
func seedHandlerDagNonTrainingCompletedRoot(t *testing.T, ctx context.Context, workspaceID string) (string, string) {
	t.Helper()
	projectID := seedHandlerDagProject(t, ctx, workspaceID)
	// Agent (required FK for agent_inbox_event).
	runtimeID := handlerTestRuntimeID(t)
	var agentID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent (
			workspace_id, name, display_name, description, runtime_mode, runtime_config, runtime_id, max_concurrent_tasks, owner_id, instructions, custom_env, custom_args, mcp_config
		, model) VALUES ($1, 'dag-test-agent', 'DAG Test Agent', '', 'cloud', '{}'::jsonb, $2, 1, NULL, '', '{}'::jsonb, '[]'::jsonb, '[]'::jsonb, 'composer-1.5')
		RETURNING id
	`, workspaceID, runtimeID).Scan(&agentID); err != nil {
		t.Fatalf("create test agent: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(ctx, `DELETE FROM agent WHERE id = $1`, agentID) })
	// Issue (required FK for agent_inbox_event).
	var issueID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO issue (workspace_id, project_id, title, status, priority, creator_type, creator_id)
		VALUES ($1, $2, 'dag-test-issue', 'backlog', 'none', 'member', $3)
		RETURNING id
	`, workspaceID, projectID, testUserID).Scan(&issueID); err != nil {
		t.Fatalf("create test issue: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(ctx, `DELETE FROM issue WHERE id = $1`, issueID) })
	// Root task (agent_inbox_event with status=queued initially; the test sets it
	// to completed before the /dag call).
	var rootTaskID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_inbox_event (agent_id, runtime_id, issue_id, status, priority)
		VALUES ($1, $2, $3, 'pending', 0)
		RETURNING id
	`, agentID, runtimeID, issueID).Scan(&rootTaskID); err != nil {
		t.Fatalf("create test root task: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(ctx, `DELETE FROM agent_inbox_event WHERE id = $1`, rootTaskID) })
	// env_dispatch_run: training_mode=false, root_task_id bound. No
	// training_dispatch row is created.
	if _, err := testPool.Exec(ctx, `
		INSERT INTO env_dispatch_run (project_id, workspace_id, training_mode, root_task_id)
		VALUES ($1, $2, false, $3)
	`, projectID, workspaceID, rootTaskID); err != nil {
		t.Fatalf("create env_dispatch_run: %v", err)
	}
	return projectID, rootTaskID
}

// --- T007: shared_sandbox 201 response refs (FR-005) -------------------------

// recordingSandboxCreator is a minimal service.SandboxInstanceCreator fake for
// handler-package tests: it hands out distinct refs per Create call so sharing
// assertions can distinguish per-agent from per-rollout provisioning.
type recordingSandboxCreator struct {
	mu    sync.Mutex
	calls []service.CreateSandboxInstanceInput
}

func (c *recordingSandboxCreator) CreateSandboxInstance(_ context.Context, in service.CreateSandboxInstanceInput, _ string) (service.SandboxInstanceRef, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls = append(c.calls, in)
	return service.SandboxInstanceRef{
		InstanceID:  fmt.Sprintf("inst-%d", len(c.calls)),
		WorkspaceID: in.WorkspaceID,
		Template:    in.Template,
	}, nil
}

func (c *recordingSandboxCreator) GetSandboxInstanceRef(_ context.Context, _, instanceID string) (service.SandboxInstanceRef, error) {
	return service.SandboxInstanceRef{}, fmt.Errorf("sandbox_instance not found: %s", instanceID)
}

func (c *recordingSandboxCreator) DeleteSandboxInstance(_ context.Context, _ service.SandboxInstanceRef, _ string) error {
	return nil
}

// countingRuntimeDeps overrides two stub methods so squad dispatch tests can
// run a full Dispatch: GetEnv returns a base-mode env (the stub's zero Env
// fails the scratch base-env check), and PrecreateAgentRuntime hands out
// distinct runtimes (the stub's constant "stub-runtime" would hide
// cross-rollout sharing violations).
type countingRuntimeDeps struct {
	service.EnvDispatchDeps
	n atomic.Int64
}

func (d *countingRuntimeDeps) GetEnv(_ context.Context, envID, workspaceID string) (service.Env, error) {
	return service.Env{ID: envID, WorkspaceID: workspaceID, Mode: service.EnvModeBase}, nil
}

func (d *countingRuntimeDeps) PrecreateAgentRuntime(_ context.Context, _, _, _ string) (string, string, error) {
	n := d.n.Add(1)
	return fmt.Sprintf("rt-%d", n), fmt.Sprintf("daemon-%d", n), nil
}

// dispatchSquadForResponseTest drives the same service + mapRollouts pipeline
// the EnvDispatch handler uses to build its 201 body, with the stub deps the
// DB-less handler falls back to. shared selects the shared_sandbox request
// field (resolved by the handler into EnvDispatchInput.SharedSandbox).
func dispatchSquadForResponseTest(t *testing.T, shared bool) EnvDispatchResponse {
	t.Helper()
	creator := &recordingSandboxCreator{}
	svc := service.NewEnvDispatchService(&countingRuntimeDeps{EnvDispatchDeps: &stubEnvDispatchDeps{}}, 8).
		WithSandboxLifecycle(creator)
	res, err := svc.Dispatch(context.Background(), service.EnvDispatchInput{
		WorkspaceID: "ws1", UserID: "u1", Mode: service.EnvModeScratch, EnvID: validUUID,
		Domain: service.EnvDomainSweLego, DispatchType: service.EnvDispatchIssue, GroupSize: 2,
		AgentID: validUUID, TrainAgentID: validUUID, TrainingMode: true,
		SharedSandbox: shared,
		Issue:         &service.IssueInput{Title: "t"},
		PerAgentEnvSpecs: []service.PerAgentEnvSpec{
			{AgentID: validUUID, Template: "py312"},
			{AgentID: "22222222-2222-2222-2222-222222222222", Template: "py312"},
		},
	})
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	// Exactly what EnvDispatch writes on success (handler/env_dispatch.go).
	return EnvDispatchResponse{ChannelID: res.ChannelID, ProjectID: res.ProjectID, Rollouts: mapRollouts(res.Rollouts)}
}

// TestEnvDispatch_SharedSandboxResponseRefs asserts the FR-005 response
// contract: in shared mode every agent_sandbox_refs entry within one rollout
// reports the SAME sandbox_instance_id/runtime_id, and entries in different
// rollouts report different identifiers.
func TestEnvDispatch_SharedSandboxResponseRefs(t *testing.T) {
	resp := dispatchSquadForResponseTest(t, true)
	if len(resp.Rollouts) != 2 {
		t.Fatalf("want 2 rollouts, got %d", len(resp.Rollouts))
	}
	seen := map[string]bool{}
	for i, r := range resp.Rollouts {
		if r.Error != "" {
			t.Fatalf("rollout %d errored: %s", i, r.Error)
		}
		if len(r.AgentSandboxRefs) != 2 {
			t.Fatalf("rollout %d: want 2 agent_sandbox_refs, got %+v", i, r.AgentSandboxRefs)
		}
		var instanceID, runtimeID string
		first := true
		for agentID, ref := range r.AgentSandboxRefs {
			if first {
				instanceID, runtimeID, first = ref.InstanceID, ref.RuntimeID, false
				continue
			}
			if ref.InstanceID != instanceID || ref.RuntimeID != runtimeID {
				t.Fatalf("rollout %d: agent %s ref %+v must share the rollout's sandbox/runtime (%s/%s)", i, agentID, ref, instanceID, runtimeID)
			}
		}
		if instanceID == "" || runtimeID == "" {
			t.Fatalf("rollout %d: shared refs must carry instance+runtime ids, got %+v", i, r.AgentSandboxRefs)
		}
		if seen[instanceID] {
			t.Fatalf("rollout %d: sandbox instance %s already used by another rollout (samples must stay isolated)", i, instanceID)
		}
		seen[instanceID] = true
	}
	if seen[resp.Rollouts[0].AgentSandboxRefs[validUUID].InstanceID] != seen[resp.Rollouts[1].AgentSandboxRefs[validUUID].InstanceID] {
		t.Fatal("internal: rollout isolation bookkeeping broken")
	}
}

// TestEnvDispatch_NonSharedResponseRefsStayDistinct pins today's response
// values when shared_sandbox is false/omitted: per-agent refs within a rollout
// report distinct identifiers (FR-001/002, SC-002).
func TestEnvDispatch_NonSharedResponseRefsStayDistinct(t *testing.T) {
	resp := dispatchSquadForResponseTest(t, false)
	if len(resp.Rollouts) != 2 {
		t.Fatalf("want 2 rollouts, got %d", len(resp.Rollouts))
	}
	for i, r := range resp.Rollouts {
		if len(r.AgentSandboxRefs) != 2 {
			t.Fatalf("rollout %d: want 2 agent_sandbox_refs, got %+v", i, r.AgentSandboxRefs)
		}
		a := r.AgentSandboxRefs[validUUID]
		b := r.AgentSandboxRefs["22222222-2222-2222-2222-222222222222"]
		if a.InstanceID == b.InstanceID {
			t.Fatalf("rollout %d: non-shared agents must report distinct sandboxes, got %+v", i, r.AgentSandboxRefs)
		}
	}
}

func TestMapRolloutsIncludesSourceAndRunIdentity(t *testing.T) {
	got := mapRollouts([]service.EnvRollout{{
		RunID: "run-1", SourceTaskID: "source-1", ProjectID: "project-1",
	}})
	if len(got) != 1 {
		t.Fatalf("rollouts = %d, want 1", len(got))
	}
	if got[0].RunID != "run-1" || got[0].SourceTaskID != "source-1" {
		t.Fatalf("mapped identity = %+v", got[0])
	}
}

func TestEnvDispatchMixedRequestDTODoesNotExposeLegacyFields(t *testing.T) {
	encoded, err := json.Marshal(EnvDispatchRequest{})
	if err != nil {
		t.Fatal(err)
	}
	for _, removed := range []string{`"training_mode"`, `"train_agent_id"`} {
		if strings.Contains(string(encoded), removed) {
			t.Fatalf("request DTO exposed removed field %s: %s", removed, encoded)
		}
	}
}

func TestEnvDispatchMixedContractTimingBoundsAreInclusive(t *testing.T) {
	valid := `{"mode":"scratch","env_id":"` + validUUID + `","domain":"self_play","dispatch_type":"message","group_size":1,"agent_id":"` + validUUID + `","message":{"content":"hi"}}`
	accepted := map[string]string{
		"quiet lower bound":   `"quiet_window_ms":100,"total_timeout_seconds":30`,
		"quiet upper bound":   `"quiet_window_ms":60000,"total_timeout_seconds":61`,
		"timeout lower bound": `"quiet_window_ms":100,"total_timeout_seconds":30`,
		"timeout upper bound": `"quiet_window_ms":60000,"total_timeout_seconds":86400`,
	}
	for name, fragment := range accepted {
		t.Run(name, func(t *testing.T) {
			body := strings.TrimSuffix(valid, "}") + `,` + fragment + `}`
			var request EnvDispatchRequest
			if err := json.Unmarshal([]byte(body), &request); err != nil {
				t.Fatalf("decode request boundary: %v", err)
			}
			plan, err := service.PreflightMixedDispatch(service.MixedDispatchPreflightInput{
				Roster:        []service.MixedDispatchRosterAgent{{SourceAgentID: validUUID, Provider: "pi"}},
				QuietWindowMS: request.QuietWindowMS, TotalTimeoutSeconds: request.TotalTimeoutSeconds,
			})
			if err != nil || len(plan.RunAgents) != 1 || plan.RunAgents[0].TrainingMode != "none" {
				t.Fatalf("inclusive boundary preflight plan=%+v err=%v, want accepted none classification", plan, err)
			}
		})
	}

	rejected := map[string]struct {
		fragment string
		field    string
	}{
		"quiet below lower bound":     {`"quiet_window_ms":99,"total_timeout_seconds":30`, "quiet_window_ms"},
		"quiet above upper bound":     {`"quiet_window_ms":60001,"total_timeout_seconds":61`, "quiet_window_ms"},
		"timeout below lower bound":   {`"quiet_window_ms":100,"total_timeout_seconds":29`, "total_timeout_seconds"},
		"timeout above upper bound":   {`"quiet_window_ms":100,"total_timeout_seconds":86401`, "total_timeout_seconds"},
		"total duration equals quiet": {`"quiet_window_ms":60000,"total_timeout_seconds":60`, "total_timeout_seconds"},
	}
	for name, fragment := range rejected {
		t.Run(name, func(t *testing.T) {
			body := strings.TrimSuffix(valid, "}") + `,` + fragment.fragment + `}`
			rr := doEnvDispatch(t, body)
			if rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), fragment.field) {
				t.Fatalf("invalid timing response = %d %s, want 400 naming %s", rr.Code, rr.Body.String(), fragment.field)
			}
		})
	}
}

func TestEnvDispatchMixedContractSuccessResponseDefaultsAndClassification(t *testing.T) {
	submittedAt := time.Date(2026, time.August, 10, 10, 0, 0, 0, time.UTC)
	encoded, err := json.Marshal(EnvDispatchResponse{
		ChannelID: "channel-1",
		ProjectID: "project-1",
		Rollouts: []EnvRolloutResponse{{
			RunID: "run-1", EnvID: "env-1", ProjectID: "project-1",
			ChatSessionID: "secret-chat-session",
		}},
		InitialMessageSubmittedAt: submittedAt,
		RunAgents: mapMixedDispatchRunAgents([]service.MixedDispatchRunAgent{
			{SourceAgentID: "source-online", ExecutionAgentID: "execution-online", RuntimeID: "runtime-online", PiSessionID: "secret-pi-online", AReALSessionID: "secret-areal-online", TrainingMode: "online_rl"},
			{SourceAgentID: "source-offline", ExecutionAgentID: "execution-offline", TrainingMode: "offline_rl"},
			{SourceAgentID: "source-none", ExecutionAgentID: "execution-none", TrainingMode: "none"},
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	var response struct {
		ChannelID                 string                          `json:"channel_id"`
		ProjectID                 string                          `json:"project_id"`
		Rollouts                  []EnvRolloutResponse            `json:"rollouts"`
		QuietWindowMS             int                             `json:"quiet_window_ms"`
		TotalTimeoutSeconds       int                             `json:"total_timeout_seconds"`
		InitialMessageSubmittedAt time.Time                       `json:"initial_message_submitted_at"`
		RunAgents                 []service.MixedDispatchRunAgent `json:"run_agents"`
	}
	if err := json.Unmarshal(encoded, &response); err != nil {
		t.Fatal(err)
	}
	if response.ChannelID != "channel-1" || response.ProjectID != "project-1" || len(response.Rollouts) != 1 || response.Rollouts[0].RunID != "run-1" {
		t.Fatalf("success identity fields = %+v, want channel/project/rollout identity", response)
	}
	if response.QuietWindowMS != 2000 || response.TotalTimeoutSeconds != 3300 || !response.InitialMessageSubmittedAt.Equal(submittedAt) {
		t.Fatalf("success timing fields = %+v, want defaults 2000/3300 and submission time %s", response, submittedAt)
	}
	wantModes := map[string]string{"source-online": "online_rl", "source-offline": "offline_rl", "source-none": "none"}
	if len(response.RunAgents) != len(wantModes) {
		t.Fatalf("run_agents = %+v, want three classifications", response.RunAgents)
	}
	for _, agent := range response.RunAgents {
		if wantModes[agent.SourceAgentID] != agent.TrainingMode || agent.ExecutionAgentID == "" {
			t.Fatalf("run agent classification = %+v, want source/execution identity and %q", agent, wantModes[agent.SourceAgentID])
		}
	}
	for _, forbidden := range []string{
		`"training_mode":false`, "train_agent_id", `"runtime_id"`, `"daemon_id"`, `"chat_session_id"`,
		`"agent_sandboxes"`, `"sandbox_refs"`, `"agent_sandbox_refs"`, `"leader_run_id"`, `"agent_run_id"`,
		"secret-pi-online", "secret-areal-online", "secret-chat-session", "secret-runtime", "secret-daemon",
		"secret-leader-run", "secret-agent-run", "secret-sandbox-runtime", "secret-sandbox-daemon",
	} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("success response exposed forbidden legacy or session field %q: %s", forbidden, encoded)
		}
	}
}

type canonicalSendFailureEnvDispatchDeps struct {
	stubEnvDispatchDeps
	envs             map[string]bool
	projects         map[string]bool
	channels         map[string]bool
	runtimes         map[string]bool
	sendAttempts     int
	acceptedMessages int
	startedRuns      int
	enqueuedRuns     int
}

func newCanonicalSendFailureEnvDispatchDeps() *canonicalSendFailureEnvDispatchDeps {
	return &canonicalSendFailureEnvDispatchDeps{
		envs: map[string]bool{}, projects: map[string]bool{}, channels: map[string]bool{}, runtimes: map[string]bool{},
	}
}

func (f *canonicalSendFailureEnvDispatchDeps) GetEnv(_ context.Context, envID, _ string) (service.Env, error) {
	if envID == "base-env" {
		return service.Env{ID: envID, Mode: service.EnvModeBase}, nil
	}
	return service.Env{ID: envID, Mode: service.EnvModeScratch}, nil
}

func (f *canonicalSendFailureEnvDispatchDeps) CreateEnv(context.Context, string, []string, string, service.EnvMode, service.EnvDomain) (string, error) {
	f.envs["provisional-env"] = true
	return "provisional-env", nil
}

func (f *canonicalSendFailureEnvDispatchDeps) DeleteEnv(_ context.Context, envID, _ string) error {
	delete(f.envs, envID)
	return nil
}

func (f *canonicalSendFailureEnvDispatchDeps) CreateProject(context.Context, string, string, string) (string, error) {
	f.projects["provisional-project"] = true
	return "provisional-project", nil
}

func (f *canonicalSendFailureEnvDispatchDeps) DeleteProject(_ context.Context, projectID, _ string) error {
	delete(f.projects, projectID)
	return nil
}

func (f *canonicalSendFailureEnvDispatchDeps) ResolveMessageRoster(context.Context, string, string, []service.PerAgentEnvSpec) (service.MessageRoster, error) {
	return service.MessageRoster{LeaderID: "source-a", AgentIDs: []string{"source-a", "source-b"}}, nil
}

func (f *canonicalSendFailureEnvDispatchDeps) CreateEnvDispatchChannel(context.Context, string, string, string, string, service.MessageRoster, map[string]service.ResolvedPerAgentSandboxPolicy) (string, error) {
	f.channels["provisional-channel"] = true
	return "provisional-channel", nil
}

func (f *canonicalSendFailureEnvDispatchDeps) DeleteChannel(_ context.Context, _ string, channelID string) error {
	delete(f.channels, channelID)
	return nil
}

func (f *canonicalSendFailureEnvDispatchDeps) ProvisionEnvDispatchAgent(_ context.Context, in service.EnvDispatchAgentProvisionInput) (service.EnvDispatchAgentProvisionResult, error) {
	runtimeID := "provisional-runtime-" + in.AgentID
	f.runtimes[runtimeID] = true
	arealSessionID := ""
	if in.TrainingMode == "online_rl" {
		arealSessionID = "areal-session-" + in.AgentID
	}
	return service.EnvDispatchAgentProvisionResult{
		AgentID:           "execution-" + in.AgentID,
		SandboxInstanceID: "sandbox-" + in.AgentID,
		RuntimeID:         runtimeID,
		DaemonID:          "daemon-" + in.AgentID,
		ChatSessionID:     "pi-session-" + in.AgentID,
		AReALSessionID:    arealSessionID,
	}, nil
}

func (f *canonicalSendFailureEnvDispatchDeps) DeleteAgentRuntime(_ context.Context, _ string, runtimeID string) error {
	delete(f.runtimes, runtimeID)
	return nil
}

func (f *canonicalSendFailureEnvDispatchDeps) PrepareMixedDispatchRunAgent(_ context.Context, runID string, agent service.MixedDispatchRunAgent) (service.MixedDispatchRunAgent, error) {
	agent.RunAgentID = "run-agent-" + agent.SourceAgentID
	agent.PiSessionID = "native-pi-" + runID + "-" + agent.SourceAgentID
	agent.CaptureBoundary = "boundary-" + agent.RunAgentID
	return agent, nil
}

func (f *canonicalSendFailureEnvDispatchDeps) BindMixedDispatchRunAgent(context.Context, string, service.MixedDispatchRunAgent) error {
	return nil
}

func (f *canonicalSendFailureEnvDispatchDeps) RevokeMixedDispatchRunAgent(context.Context, string, service.MixedDispatchRunAgent) error {
	return nil
}

func (f *canonicalSendFailureEnvDispatchDeps) PersistMixedDispatchInitialMessage(context.Context, string, string, string, string) (service.PreparedMixedDispatchMessage, error) {
	f.sendAttempts++
	return service.PreparedMixedDispatchMessage{}, errors.New("synthetic canonical send failure")
}

func (f *canonicalSendFailureEnvDispatchDeps) CreateChannelMessage(context.Context, string, string, string, string) (string, error) {
	f.sendAttempts++
	return "", errors.New("synthetic canonical send failure")
}

func (f *canonicalSendFailureEnvDispatchDeps) StartMixedDispatchRun(context.Context, string, time.Time) error {
	f.startedRuns++
	return nil
}

func (f *canonicalSendFailureEnvDispatchDeps) EnqueueEnvDispatchChannelRun(context.Context, string, string, service.ChannelRunInput, int) (string, error) {
	f.enqueuedRuns++
	return "unexpected-run", nil
}

func TestEnvDispatchMixedCanonicalSendFailureRollsBackBeforeAcceptance(t *testing.T) {
	deps := newCanonicalSendFailureEnvDispatchDeps()
	_, err := service.NewEnvDispatchService(deps, 1).Dispatch(context.Background(), service.EnvDispatchInput{
		WorkspaceID: "workspace-1", UserID: "user-1", Mode: service.EnvModeScratch, EnvID: "base-env",
		Domain: service.EnvDomainSelfPlay, DispatchType: service.EnvDispatchMessage, GroupSize: 1, AgentID: "source-a",
		OnlineTrainableAgents: []string{"source-a"}, OfflineTrainableAgents: []string{"source-b"},
		QuietWindowMS: 2000, TotalTimeoutSeconds: 3300, Message: &service.MessageInput{Content: "must not be accepted"},
	})
	if err == nil || !strings.Contains(err.Error(), "synthetic canonical send failure") {
		t.Fatalf("dispatch error = %v, want canonical send failure", err)
	}
	if deps.sendAttempts != 1 || deps.acceptedMessages != 0 || deps.startedRuns != 0 || deps.enqueuedRuns != 0 {
		t.Fatalf("post-failure activity attempts=%d accepted=%d starts=%d enqueues=%d, want one failed send and no accepted activity", deps.sendAttempts, deps.acceptedMessages, deps.startedRuns, deps.enqueuedRuns)
	}
	if len(deps.envs) != 0 || len(deps.projects) != 0 || len(deps.channels) != 0 || len(deps.runtimes) != 0 {
		t.Fatalf("provisional resources survived rollback: envs=%v projects=%v channels=%v runtimes=%v", deps.envs, deps.projects, deps.channels, deps.runtimes)
	}
}
