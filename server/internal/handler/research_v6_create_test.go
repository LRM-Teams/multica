package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestResearchV6UserCreateDoesNotRequireBootstrapFlag(t *testing.T) {
	if !researchV6UserCreateEnabled(Config{ResearchV6BootstrapEnabled: false}) {
		t.Fatal("users must be able to create V6 runs without RESEARCH_V6_BOOTSTRAP_ENABLED")
	}
}

func TestResearchV6DirectorReadinessRequiresOnlineCapableRuntime(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	agentID, runtimeID := createHandlerTestAgentWithIsolatedRuntime(t)

	notCapable := testHandler.researchV6DirectorReadiness(ctx, parseUUID(testWorkspaceID), parseUUID(agentID))
	if notCapable == nil || notCapable.code != "research.v6.director_runtime_incompatible" {
		t.Fatalf("readiness without V6 capability = %#v, want incompatible", notCapable)
	}

	if _, err := testPool.Exec(ctx, `
		UPDATE agent_runtime
		SET metadata = jsonb_build_object('capabilities', jsonb_build_array($2::text)),
		    status = 'online', last_seen_at = now()
		WHERE id = $1
	`, runtimeID, protocol.DaemonCapabilityResearchRunV6); err != nil {
		t.Fatalf("mark runtime V6 capable: %v", err)
	}
	if readiness := testHandler.researchV6DirectorReadiness(ctx, parseUUID(testWorkspaceID), parseUUID(agentID)); readiness != nil {
		t.Fatalf("readiness with online V6 runtime = %#v, want ready", readiness)
	}

	if _, err := testPool.Exec(ctx, `UPDATE agent_runtime SET status = 'offline' WHERE id = $1`, runtimeID); err != nil {
		t.Fatalf("mark runtime offline: %v", err)
	}
	offline := testHandler.researchV6DirectorReadiness(ctx, parseUUID(testWorkspaceID), parseUUID(agentID))
	if offline == nil || offline.code != "research.v6.director_runtime_offline" || !offline.retryable {
		t.Fatalf("readiness with offline runtime = %#v, want retryable offline", offline)
	}
}

func TestCreateResearchV6RejectsIncompatibleDirectorBeforeBootstrap(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	agentID, _ := createHandlerTestAgentWithIsolatedRuntime(t)
	var before int
	if err := testPool.QueryRow(context.Background(), `SELECT count(*)::int FROM research_session WHERE workspace_id=$1`, testWorkspaceID).Scan(&before); err != nil {
		t.Fatalf("count sessions before create: %v", err)
	}
	req := newRequest(http.MethodPost, "/api/research/sessions", map[string]any{
		"goal":                 "Reject incompatible Director",
		"title":                "Reject incompatible Director",
		"orchestrator_version": "research-run-v6",
		"director_agent_id":    agentID,
		"client_request_id":    uuid.NewString(),
	})
	rec := httptest.NewRecorder()
	testHandler.CreateResearchSession(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("create status=%d body=%s, want 409", rec.Code, rec.Body.String())
	}
	var body struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode create error: %v", err)
	}
	if body.Code != "research.v6.director_runtime_incompatible" {
		t.Fatalf("create error code=%q, want incompatible", body.Code)
	}
	var after int
	if err := testPool.QueryRow(context.Background(), `SELECT count(*)::int FROM research_session WHERE workspace_id=$1`, testWorkspaceID).Scan(&after); err != nil {
		t.Fatalf("count sessions after create: %v", err)
	}
	if after != before {
		t.Fatalf("incompatible create persisted %d sessions, want zero", after-before)
	}
}
