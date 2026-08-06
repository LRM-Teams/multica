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
	"testing"

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
	body := `{"training_mode":false,"env_id":"` + validUUID + `","dispatch_type":"issue","group_size":1,"agent_id":"` + validUUID + `","domain":"swe_lego","issue":{"title":"t"}}`
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
	body := `{"training_mode":false,"mode":"scratch","env_id":"not-a-uuid","domain":"swe_lego","dispatch_type":"issue","group_size":1,"agent_id":"` + validUUID + `","issue":{"title":"t"}}`
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
	body := `{"training_mode":false,"mode":"scratch","env_id":"` + validUUID + `","domain":"swe_lego","dispatch_type":"message","group_size":1,"agent_id":"` + validUUID + `","message":{"content":"q"}}`
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
	body := `{"training_mode":false,"mode":"scratch","env_id":"` + validUUID + `","domain":"self_play","dispatch_type":"issue","group_size":1,"agent_id":"` + validUUID + `","issue":{"title":"t"}}`
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
	body := `{"training_mode":false,"mode":"scratch","env_id":"","domain":"self_play","dispatch_type":"message","group_size":1,"agent_id":"` + validUUID + `","message":{"content":"hi"}}`
	rr := doEnvDispatch(t, body)
	if rr.Code == http.StatusBadRequest && strings.Contains(rr.Body.String(), "invalid env_id") {
		t.Fatalf("empty env_id must pass the handler UUID gate, got %d %s", rr.Code, rr.Body.String())
	}
}

func TestEnvDispatch_RejectsMalformedTrainAgentID(t *testing.T) {
	// A malformed train_agent_id must be rejected by the handler's UUID-shape
	// gate with a 400 (mirroring the agent_id/squad_id shape checks) instead of
	// panicking deeper in the adapter.
	body := `{"training_mode":true,"mode":"scratch","env_id":"` + validUUID + `","domain":"swe_lego","dispatch_type":"issue","group_size":1,"agent_id":"` + validUUID + `","train_agent_id":"not-a-uuid","issue":{"title":"t"}}`
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
	body := `{"training_mode":true,"mode":"scratch","env_id":"` + validUUID + `","domain":"self_play","dispatch_type":"message","group_size":1,"agent_id":"` + validUUID + `","train_agent_id":"` + validUUID + `","message":{"content":"hi"}}`
	rr := doEnvDispatch(t, body)
	if rr.Code == http.StatusBadRequest && strings.Contains(rr.Body.String(), "train_agent_id") {
		t.Fatalf("well-formed train_agent_id must pass shape validation, got %d %s", rr.Code, rr.Body.String())
	}
}

func TestEnvDispatch_AcceptsResumeMode(t *testing.T) {
	body := `{"training_mode":false,"mode":"resume","env_id":"` + validUUID + `","domain":"swe_lego","dispatch_type":"issue","group_size":1,"agent_id":"` + validUUID + `","issue":{"title":"t"}}`
	rr := doEnvDispatch(t, body)
	if rr.Code == http.StatusBadRequest && strings.Contains(rr.Body.String(), "mode") {
		t.Fatalf("resume must be accepted as a mode, got %d %s", rr.Code, rr.Body.String())
	}
}

func TestEnvDispatchHandler_CriticAgentID_ShapeValidation(t *testing.T) {
	// 400 on malformed UUID
	body := `{"training_mode":true,"agent_id":"` + validUUID + `","mode":"scratch","env_id":"` + validUUID + `","domain":"self_play","dispatch_type":"message","group_size":1,"train_agent_id":"` + validUUID + `","critic_agent_id":"not-a-uuid","message":{"content":"hi"}}`
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
	body := `{"training_mode":false,"mode":"scratch","env_id":"` + validUUID + `","domain":"self_play","dispatch_type":"message","group_size":1,"agent_id":"` + validUUID + `","message":{"content":"hi"},"per_agent_env":{"` + validUUID + `":{}}}`
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
	runtimeBody := `{"training_mode":false,"mode":"scratch","env_id":"` + validUUID + `","domain":"self_play","dispatch_type":"message","group_size":1,"agent_id":"` + validUUID + `","message":{"content":"hi"},"per_agent_env":{"` + validUUID + `":{"runtime":{"base_url":"https://provider.invalid/v1","api_key":"synthetic-secret-for-tests","model":"model-a"}}}}`
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

// TestEnvDispatch_TrainingMode exercises the required training_mode request
// contract (Task 1): omitted training_mode returns HTTP 400 at the handler
// boundary; training_mode=true without train_agent_id and training_mode=false
// with a training ID are rejected by service validation (surfaced as 400 with
// "training_mode"). The two valid forms (true + train_agent_id, false + no
// training IDs) must reach the service with the exact boolean - verified by
// the service validation behavior: a wrong boolean would trigger the
// opposite training_mode 400.
func TestEnvDispatch_TrainingMode(t *testing.T) {
	t.Run("Omitted_Returns400", func(t *testing.T) {
		body := `{"mode":"scratch","env_id":"` + validUUID + `","domain":"self_play","dispatch_type":"message","group_size":1,"agent_id":"` + validUUID + `","message":{"content":"hi"}}`
		rr := doEnvDispatch(t, body)
		if rr.Code != http.StatusBadRequest {
			t.Fatalf("omitted training_mode: want 400, got %d %s", rr.Code, rr.Body.String())
		}
		if !strings.Contains(rr.Body.String(), "training_mode") {
			t.Fatalf("body should mention training_mode; got %s", rr.Body.String())
		}
	})

	t.Run("TrueWithoutTrainAgentID_Returns400", func(t *testing.T) {
		body := `{"training_mode":true,"mode":"scratch","env_id":"` + validUUID + `","domain":"self_play","dispatch_type":"message","group_size":1,"agent_id":"` + validUUID + `","message":{"content":"hi"}}`
		rr := doEnvDispatch(t, body)
		if rr.Code != http.StatusBadRequest {
			t.Fatalf("true without train_agent_id: want 400, got %d %s", rr.Code, rr.Body.String())
		}
		if !strings.Contains(rr.Body.String(), "training_mode") {
			t.Fatalf("body should mention training_mode; got %s", rr.Body.String())
		}
	})

	t.Run("FalseWithTrainAgentID_Returns400", func(t *testing.T) {
		body := `{"training_mode":false,"mode":"scratch","env_id":"` + validUUID + `","domain":"self_play","dispatch_type":"message","group_size":1,"agent_id":"` + validUUID + `","train_agent_id":"` + validUUID + `","message":{"content":"hi"}}`
		rr := doEnvDispatch(t, body)
		if rr.Code != http.StatusBadRequest {
			t.Fatalf("false with train_agent_id: want 400, got %d %s", rr.Code, rr.Body.String())
		}
		if !strings.Contains(rr.Body.String(), "training_mode") {
			t.Fatalf("body should mention training_mode; got %s", rr.Body.String())
		}
	})

	t.Run("TrueWithTrainAgentID_ReachesServiceWithExactBoolean", func(t *testing.T) {
		// training_mode=true with train_agent_id == agent_id (single-agent
		// training) must pass the training_mode validation rules. If the
		// handler incorrectly passed false, the service would emit
		// "training_mode false forbids train_agent_id" -> 400.
		body := `{"training_mode":true,"mode":"scratch","env_id":"` + validUUID + `","domain":"self_play","dispatch_type":"message","group_size":1,"agent_id":"` + validUUID + `","train_agent_id":"` + validUUID + `","message":{"content":"hi"}}`
		rr := doEnvDispatch(t, body)
		if rr.Code == http.StatusBadRequest && strings.Contains(rr.Body.String(), "training_mode") {
			t.Fatalf("true with train_agent_id must pass training_mode validation, got %d %s", rr.Code, rr.Body.String())
		}
	})

	t.Run("FalseWithoutTrainingIDs_ReachesServiceWithExactBoolean", func(t *testing.T) {
		// training_mode=false with no train_agent_id/critic_agent_id must pass
		// the training_mode validation rules. If the handler incorrectly
		// passed true, the service would emit "training_mode true requires
		// train_agent_id" -> 400.
		body := `{"training_mode":false,"mode":"scratch","env_id":"` + validUUID + `","domain":"self_play","dispatch_type":"message","group_size":1,"agent_id":"` + validUUID + `","message":{"content":"hi"}}`
		rr := doEnvDispatch(t, body)
		if rr.Code == http.StatusBadRequest && strings.Contains(rr.Body.String(), "training_mode") {
			t.Fatalf("false without training IDs must pass training_mode validation, got %d %s", rr.Code, rr.Body.String())
		}
	})
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
