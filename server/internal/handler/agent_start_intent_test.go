package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func TestAgentStartIntentRetriesAndLifecycleReports(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	params := db.CreateAgentParams{
		WorkspaceID:        parseUUID(testWorkspaceID),
		Description:        "durable start intent",
		RuntimeMode:        "cloud",
		RuntimeConfig:      []byte("{}"),
		RuntimeID:          parseUUID(testRuntimeID),
		MaxConcurrentTasks: 1,
		OwnerID:            parseUUID(testUserID),
		CustomEnv:          []byte("{}"),
		CustomArgs:         []byte("[]"),
		Model:              pgtype.Text{String: "composer-1.5", Valid: true},
	}
	created, err := testHandler.createAgentManagedCommit(ctx, parseUUID(testWorkspaceID), params, "Start Intent Agent")
	if err != nil {
		t.Fatalf("create managed agent: %v", err)
	}
	t.Cleanup(func() { _, _ = testPool.Exec(ctx, `DELETE FROM agent WHERE id = $1`, created.ID) })

	first, err := testHandler.pendingAgentStartIntents(ctx, parseUUID(testRuntimeID))
	if err != nil || len(first) != 1 {
		t.Fatalf("first pending intents = %#v, err=%v; want one", first, err)
	}
	if first[0].AgentID != uuidToString(created.ID) || first[0].StartDispatchID == "" {
		t.Fatalf("unexpected first intent: %#v", first[0])
	}
	second, err := testHandler.pendingAgentStartIntents(ctx, parseUUID(testRuntimeID))
	if err != nil || len(second) != 1 {
		t.Fatalf("retry pending intents = %#v, err=%v; want same one", second, err)
	}
	if second[0].StartDispatchID != first[0].StartDispatchID {
		t.Fatalf("retry changed start dispatch id: %q -> %q", first[0].StartDispatchID, second[0].StartDispatchID)
	}

	report := func(status string, seq int64, failureCode string) *httptest.ResponseRecorder {
		t.Helper()
		req := newDaemonTokenRequest(http.MethodPost,
			"/api/daemon/runtimes/"+testRuntimeID+"/agent-start-intents/"+first[0].StartDispatchID+"/report",
			map[string]any{"status": status, "lifecycle_seq": seq, "failure_code": failureCode},
			testWorkspaceID, "start-intent-report-"+randomID())
		req = withURLParams(req, "runtimeId", testRuntimeID, "startDispatchId", first[0].StartDispatchID)
		w := httptest.NewRecorder()
		testHandler.ReportAgentStartIntent(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("report %s/%d = %d: %s", status, seq, w.Code, w.Body.String())
		}
		return w
	}

	report("accepted", 1, "")
	noLongerPending, err := testHandler.pendingAgentStartIntents(ctx, parseUUID(testRuntimeID))
	if err != nil {
		t.Fatalf("load pending after acceptance: %v", err)
	}
	for _, intent := range noLongerPending {
		if intent.StartDispatchID == first[0].StartDispatchID {
			t.Fatalf("accepted intent was re-dispatched: %#v", intent)
		}
	}

	// Ready is a separate observation, and an older replay cannot regress it.
	report("ready", 2, "")
	report("accepted", 1, "")
	readyAgent, err := testHandler.Queries.GetAgent(ctx, created.ID)
	if err != nil {
		t.Fatalf("load agent after ready report: %v", err)
	}
	readyResponse := agentToResponse(readyAgent)
	testHandler.attachAgentRuntimeName(ctx, &readyResponse)
	if readyResponse.StartIntentStatus != "ready" || readyResponse.StartIntentFailureCode != "" {
		t.Fatalf("agent start projection after ready = status=%q failure=%q", readyResponse.StartIntentStatus, readyResponse.StartIntentFailureCode)
	}

	var status, failureCode string
	var lifecycleSeq int64
	if err := testPool.QueryRow(ctx, `
		SELECT status, lifecycle_seq, COALESCE(failure_code, '')
		FROM agent_start_intent WHERE start_dispatch_id = $1`, first[0].StartDispatchID).Scan(&status, &lifecycleSeq, &failureCode); err != nil {
		t.Fatalf("load ready start intent: %v", err)
	}
	if status != "ready" || lifecycleSeq != 2 || failureCode != "" {
		t.Fatalf("ready state = status=%q sequence=%d failure=%q", status, lifecycleSeq, failureCode)
	}

	// A later runtime failure is preserved for human correction and never rolls
	// the proposal or Agent back into an uncommitted state.
	report("failed", 3, "local_start_apply_failed")
	if err := testPool.QueryRow(ctx, `
		SELECT status, lifecycle_seq, COALESCE(failure_code, '')
		FROM agent_start_intent WHERE start_dispatch_id = $1`, first[0].StartDispatchID).Scan(&status, &lifecycleSeq, &failureCode); err != nil {
		t.Fatalf("load failed start intent: %v", err)
	}
	if status != "failed" || lifecycleSeq != 3 || failureCode != "local_start_apply_failed" {
		t.Fatalf("failed state = status=%q sequence=%d failure=%q", status, lifecycleSeq, failureCode)
	}
	failedAgent, err := testHandler.Queries.GetAgent(ctx, created.ID)
	if err != nil {
		t.Fatalf("load agent after failed report: %v", err)
	}
	failedResponse := agentToResponse(failedAgent)
	testHandler.attachAgentRuntimeName(ctx, &failedResponse)
	if failedResponse.StartIntentStatus != "failed" || failedResponse.StartIntentFailureCode != "local_start_apply_failed" {
		t.Fatalf("agent start projection after failure = status=%q failure=%q", failedResponse.StartIntentStatus, failedResponse.StartIntentFailureCode)
	}
}
