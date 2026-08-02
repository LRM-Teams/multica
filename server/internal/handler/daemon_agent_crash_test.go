package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
)

// TestReportAgentProviderCrashed_SetsCrashedSince pins the write side of
// Raft status ②: a daemon-authenticated POST must set agent.crashed_since.
func TestReportAgentProviderCrashed_SetsCrashedSince(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	agentID, runtimeID := createAgentHealthFixture(t, "online",
		time.Now().Add(-10*time.Second),
		time.Now().Add(-10*time.Second),
	)
	daemonID := "crash-signal-" + uuid.NewString()
	if _, err := testPool.Exec(context.Background(),
		`UPDATE agent_runtime SET daemon_id = $1 WHERE id = $2`, daemonID, runtimeID); err != nil {
		t.Fatalf("set daemon_id: %v", err)
	}

	w := httptest.NewRecorder()
	req := newDaemonTokenRequest(http.MethodPost,
		"/api/daemon/runtimes/"+runtimeID+"/agents/"+agentID+"/crashed",
		map[string]any{}, testWorkspaceID, daemonID)
	req = withURLParams(req, "runtimeId", runtimeID, "agentId", agentID)
	testHandler.ReportAgentProviderCrashed(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("ReportAgentProviderCrashed status = %d, want 200: %s", w.Code, w.Body.String())
	}

	var crashedSince *time.Time
	if err := testPool.QueryRow(context.Background(),
		`SELECT crashed_since FROM agent WHERE id = $1`, agentID).Scan(&crashedSince); err != nil {
		t.Fatalf("read crashed_since: %v", err)
	}
	if crashedSince == nil {
		t.Fatal("crashed_since is NULL after ReportAgentProviderCrashed, want set")
	}
}

func TestClearAgentProviderCrashed_ClearsCrashedSince(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	agentID, runtimeID := createAgentHealthFixture(t, "online",
		time.Now().Add(-10*time.Second),
		time.Now().Add(-10*time.Second),
	)
	daemonID := "crash-clear-" + uuid.NewString()
	if _, err := testPool.Exec(context.Background(),
		`UPDATE agent_runtime SET daemon_id = $1 WHERE id = $2`, daemonID, runtimeID); err != nil {
		t.Fatalf("set daemon_id: %v", err)
	}
	if _, err := testPool.Exec(context.Background(),
		`UPDATE agent SET crashed_since = now() WHERE id = $1`, agentID); err != nil {
		t.Fatalf("seed crashed_since: %v", err)
	}

	w := httptest.NewRecorder()
	req := newDaemonTokenRequest(http.MethodPost,
		"/api/daemon/runtimes/"+runtimeID+"/agents/"+agentID+"/crashed/clear",
		map[string]any{}, testWorkspaceID, daemonID)
	req = withURLParams(req, "runtimeId", runtimeID, "agentId", agentID)
	testHandler.ClearAgentProviderCrashed(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("ClearAgentProviderCrashed status = %d, want 200: %s", w.Code, w.Body.String())
	}

	var crashedSince *time.Time
	if err := testPool.QueryRow(context.Background(),
		`SELECT crashed_since FROM agent WHERE id = $1`, agentID).Scan(&crashedSince); err != nil {
		t.Fatalf("read crashed_since: %v", err)
	}
	if crashedSince != nil {
		t.Fatalf("crashed_since = %v after clear, want NULL", *crashedSince)
	}
}
