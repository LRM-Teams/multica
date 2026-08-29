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
	if _, err := testPool.Exec(context.Background(), `
		INSERT INTO computer_workspace_bindings (
			daemon_id, workspace_id, user_id, execution_token_hash, active, revoked_at
		) VALUES ($1, $2, $3, $4, TRUE, NULL)
		ON CONFLICT (daemon_id, workspace_id)
		DO UPDATE SET user_id = EXCLUDED.user_id, active = TRUE, revoked_at = NULL
	`, daemonID, testWorkspaceID, testUserID, "crash-binding-"+daemonID); err != nil {
		t.Fatalf("seed daemon binding: %v", err)
	}
	credentialID := uuid.NewString()
	if _, err := testPool.Exec(context.Background(), `
		INSERT INTO agent_credential (
			id, token_hash, token_prefix, agent_id, workspace_id, user_id, issuance_source
		) VALUES ($1, $2, 'sk_agent_test', $3, $4, $5, 'daemon')
	`, credentialID, "crash-token-hash-"+credentialID, agentID, testWorkspaceID, testUserID); err != nil {
		t.Fatalf("seed launch credential: %v", err)
	}

	w := httptest.NewRecorder()
	req := newDaemonTokenRequest(http.MethodPost,
		"/api/daemon/runtimes/"+runtimeID+"/agents/"+agentID+"/crashed",
		map[string]any{"credential_id": credentialID}, testWorkspaceID, daemonID)
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
	var revokedAt *time.Time
	if err := testPool.QueryRow(context.Background(),
		`SELECT revoked_at FROM agent_credential WHERE id = $1`, credentialID).Scan(&revokedAt); err != nil {
		t.Fatalf("read credential revoked_at: %v", err)
	}
	if revokedAt == nil {
		t.Fatal("crashed launch credential remains active")
	}
	if _, err := testPool.Exec(context.Background(), `UPDATE agent SET crashed_since = NULL WHERE id = $1`, agentID); err != nil {
		t.Fatalf("clear crash signal: %v", err)
	}
	replayed := httptest.NewRecorder()
	replayRequest := newDaemonTokenRequest(http.MethodPost,
		"/api/daemon/runtimes/"+runtimeID+"/agents/"+agentID+"/crashed",
		map[string]any{"credential_id": credentialID}, testWorkspaceID, daemonID)
	replayRequest = withURLParams(replayRequest, "runtimeId", runtimeID, "agentId", agentID)
	testHandler.ReportAgentProviderCrashed(replayed, replayRequest)
	if replayed.Code != http.StatusConflict {
		t.Fatalf("revoked launch crash status = %d, want 409", replayed.Code)
	}
	if err := testPool.QueryRow(context.Background(),
		`SELECT crashed_since FROM agent WHERE id = $1`, agentID).Scan(&crashedSince); err != nil {
		t.Fatalf("read replayed crashed_since: %v", err)
	}
	if crashedSince != nil {
		t.Fatal("revoked launch credential marked current Agent launch crashed")
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
