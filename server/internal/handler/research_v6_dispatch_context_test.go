package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/multica-ai/multica/server/internal/researchrun"
)

func TestResearchV6DispatchContextBindsEntireAttemptIdentity(t *testing.T) {
	request := researchrun.DispatchRequest{
		Run:          researchrun.Run{SessionID: "00000000-0000-4000-8000-000000000003"},
		WorkItemID:   "00000000-0000-4000-8000-000000000202",
		AttemptID:    "00000000-0000-4000-8000-000000000204",
		ManifestID:   "00000000-0000-4000-8000-000000000201",
		ManifestHash: "sha256:3333333333333333333333333333333333333333333333333333333333333333",
	}
	raw, err := encodeResearchDispatchInboxContext(request, "ignored-for-v6")
	if err != nil {
		t.Fatal(err)
	}
	var got researchV6InboxContext
	if err = json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got.Type != "research_run_work_item" || got.RunID != request.Run.SessionID || got.WorkItemID != request.WorkItemID ||
		got.AttemptID != request.AttemptID || got.ManifestID != request.ManifestID || got.ManifestHash != request.ManifestHash {
		t.Fatalf("context=%+v", got)
	}
}

func TestTerminalV6DispatchCanBeSupersededAfterPromptUpgrade(t *testing.T) {
	request := researchrun.DispatchRequest{
		Run: researchrun.Run{
			WorkspaceID: "00000000-0000-4000-8000-000000000002",
			SessionID:   "00000000-0000-4000-8000-000000000003",
		},
		WorkItemID:   "00000000-0000-4000-8000-000000000202",
		AttemptID:    "00000000-0000-4000-8000-000000000204",
		AgentID:      "00000000-0000-4000-8000-000000000009",
		ManifestID:   "00000000-0000-4000-8000-000000000201",
		ManifestHash: "sha256:3333333333333333333333333333333333333333333333333333333333333333",
	}
	raw, err := encodeResearchDispatchInboxContext(request, "old-mission-only-prompt-hash")
	if err != nil {
		t.Fatal(err)
	}
	if !canSupersedeTerminalV6Dispatch(request, request.Run.WorkspaceID, request.AgentID, "acked", true, raw) {
		t.Fatal("exact terminal V6 dispatch was not supersedable")
	}
	if canSupersedeTerminalV6Dispatch(request, request.Run.WorkspaceID, request.AgentID, "suppressed", true, raw) {
		t.Fatal("cancelled V6 dispatch was supersedable")
	}
	request.ManifestHash = "sha256:4444444444444444444444444444444444444444444444444444444444444444"
	if canSupersedeTerminalV6Dispatch(request, request.Run.WorkspaceID, request.AgentID, "acked", true, raw) {
		t.Fatal("dispatch with a different frozen manifest was supersedable")
	}
}

func TestResearchV6DurableCredentialBindsExactActiveAttemptServerSide(t *testing.T) {
	const (
		workspaceID = "00000000-0000-4000-8000-000000000002"
		runID       = "00000000-0000-4000-8000-000000000003"
		workID      = "00000000-0000-4000-8000-000000000212"
		attemptID   = "00000000-0000-4000-8000-000000000213"
		agentID     = "00000000-0000-4000-8000-000000000009"
		inboxID     = "00000000-0000-4000-8000-000000000214"
	)
	fake := &fakeRuntimeLookupDBTX{row: &fakeRuntimeRow{values: []string{inboxID}}}
	h := &Handler{DB: fake}
	req := researchV6AttemptRequest(runID, workID, attemptID, workspaceID, agentID)
	recorder := httptest.NewRecorder()

	access, ok := h.authorizeResearchV6Attempt(recorder, req)
	if !ok {
		t.Fatalf("authorization rejected: status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if access.InboxTaskID != inboxID || access.AgentID != agentID || access.AttemptID != attemptID {
		t.Fatalf("access=%+v", access)
	}
	for _, fragment := range []string{
		"e.workspace_id=a.workspace_id AND e.agent_id=a.assigned_agent_id",
		"a.assigned_agent_id=$5::uuid",
		"a.status IN ('dispatching','running') AND e.status='draining'",
		"a.inbox_task_id IS NULL OR e.id=a.inbox_task_id",
		"e.context->>'attempt_id'=a.id::text",
	} {
		if !strings.Contains(fake.queryRowSQL, fragment) {
			t.Fatalf("active-attempt query missing %q: %s", fragment, fake.queryRowSQL)
		}
	}
}

func TestResearchV6DurableCredentialRejectsInactiveAttempt(t *testing.T) {
	h := &Handler{DB: &fakeRuntimeLookupDBTX{row: &fakeRuntimeRow{err: pgx.ErrNoRows}}}
	req := researchV6AttemptRequest(
		"00000000-0000-4000-8000-000000000003",
		"00000000-0000-4000-8000-000000000212",
		"00000000-0000-4000-8000-000000000213",
		"00000000-0000-4000-8000-000000000002",
		"00000000-0000-4000-8000-000000000009",
	)
	recorder := httptest.NewRecorder()
	if _, ok := h.authorizeResearchV6Attempt(recorder, req); ok {
		t.Fatal("inactive attempt was authorized")
	}
	if recorder.Code != http.StatusConflict || !strings.Contains(recorder.Body.String(), "research attempt is not the active Agent task") {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestResearchV6SubmissionReplayAllowsExactSettledAttempt(t *testing.T) {
	const inboxID = "00000000-0000-4000-8000-000000000214"
	fake := &fakeRuntimeLookupDBTX{row: &fakeRuntimeRow{values: []string{inboxID}}}
	h := &Handler{DB: fake}
	req := researchV6AttemptRequest(
		"00000000-0000-4000-8000-000000000003",
		"00000000-0000-4000-8000-000000000212",
		"00000000-0000-4000-8000-000000000213",
		"00000000-0000-4000-8000-000000000002",
		"00000000-0000-4000-8000-000000000009",
	)
	recorder := httptest.NewRecorder()
	access, ok := h.authorizeResearchV6SubmissionAttempt(recorder, req)
	if !ok || access.InboxTaskID != inboxID {
		t.Fatalf("authorization=%v access=%+v status=%d body=%s", ok, access, recorder.Code, recorder.Body.String())
	}
	for _, fragment := range []string{
		"$6::boolean",
		"a.status IN ('succeeded','failed','cancelled')",
		"FROM research_v6_work_submission sub",
	} {
		if !strings.Contains(fake.queryRowSQL, fragment) {
			t.Fatalf("settled replay query missing %q: %s", fragment, fake.queryRowSQL)
		}
	}
}

func researchV6AttemptRequest(runID, workID, attemptID, workspaceID, agentID string) *http.Request {
	req := httptest.NewRequest(http.MethodGet, "/api/agent/research/sessions/"+runID+"/work-items/"+workID+"/attempts/"+attemptID+"/manifest", nil)
	route := chi.NewRouteContext()
	route.URLParams.Add("id", runID)
	route.URLParams.Add("workItemId", workID)
	route.URLParams.Add("attemptId", attemptID)
	req = req.WithContext(contextWithChiRoute(req, route))
	req = withAgentPrincipal(req, agentID, workspaceID, "00000000-0000-4000-8000-000000000010")
	req.Header.Set("X-Workspace-ID", workspaceID)
	return req
}

func contextWithChiRoute(req *http.Request, route *chi.Context) context.Context {
	return context.WithValue(req.Context(), chi.RouteCtxKey, route)
}
