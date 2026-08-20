package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// Task #53: reconcileOfflineMemoryCurationAgentRuns trusted agent_runtime.status
// directly (rt.status = 'online'), which can read "online" for up to ~180s
// after the runtime actually went silent (sweeper lag). A waiting_runtime
// child bound to such a runtime would never get reconciled to 'skipped'.
func TestReconcileOfflineMemoryCurationAgentRuns_StaleHeartbeatSkips(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("handler test database unavailable")
	}
	ctx := context.Background()

	var staleRuntimeID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_runtime (workspace_id, daemon_id, name, runtime_mode, provider, status, device_info, metadata, visibility, last_seen_at, updated_at) VALUES ($1,  'memory-curation-reconcile-stale-daemon',  'Memory Curation Reconcile Stale Runtime',  'local',  'codex',  'online', 
		          '',  '{}'::jsonb,  'private',  now() - interval '10 minutes',  now() - interval '9 minutes')
		RETURNING id::text
	`,  testWorkspaceID).Scan(&staleRuntimeID); err != nil {
		t.Fatal(err)
	}
	var agentID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent (workspace_id, name, display_name, runtime_mode, runtime_id, owner_id, model)
		VALUES ($1, 'memory-curation-reconcile-stale-agent', 'Memory Curation Reconcile Stale Agent', 'local', $2, $3, 'composer-1.5')
		RETURNING id::text
	`, testWorkspaceID, staleRuntimeID, testUserID).Scan(&agentID); err != nil {
		t.Fatal(err)
	}
	var runID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO memory_curation_run (
		  workspace_id, stage, trigger_kind, status, date_from, date_to, runtime_id, execution_owner
		) VALUES ($1, 'agent_self_review', 'manual', 'running', '2026-08-01', '2026-08-01', $2, 'daemon')
		RETURNING id::text
	`, testWorkspaceID, staleRuntimeID).Scan(&runID); err != nil {
		t.Fatal(err)
	}
	if _, err := testPool.Exec(ctx, `
		INSERT INTO memory_curation_agent_run (parent_run_id, workspace_id, agent_id, runtime_id, stage, status)
		VALUES ($1, $2, $3, $4, 'agent_self_review', 'waiting_runtime')
	`, runID, testWorkspaceID, agentID, staleRuntimeID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM memory_curation_agent_run WHERE parent_run_id = $1`, runID)
		_, _ = testPool.Exec(context.Background(), `DELETE FROM memory_curation_run WHERE id = $1`, runID)
		_, _ = testPool.Exec(context.Background(), `DELETE FROM agent WHERE id = $1`, agentID)
		_, _ = testPool.Exec(context.Background(), `DELETE FROM agent_runtime WHERE id = $1`, staleRuntimeID)
	})

	if err := testHandler.reconcileOfflineMemoryCurationAgentRuns(ctx, testWorkspaceID, runID); err != nil {
		t.Fatalf("reconcileOfflineMemoryCurationAgentRuns: %v", err)
	}

	var status, errMsg string
	if err := testPool.QueryRow(ctx, `
		SELECT status, error FROM memory_curation_agent_run WHERE parent_run_id = $1 AND agent_id = $2
	`, runID, agentID).Scan(&status, &errMsg); err != nil {
		t.Fatalf("read reconciled run: %v", err)
	}
	if status != "skipped" {
		t.Fatalf("stale-heartbeat runtime (status column still 'online'): agent run status = %q, want %q (must key off heartbeat freshness, not the raw status column); error=%q", status, "skipped", errMsg)
	}
}

// Task #53: the sibling-skip step inside ReportMemoryCurationRunResult
// (triggered when one child in a wave reports its result) had the same
// rt.status = 'online' bug as reconcileOfflineMemoryCurationAgentRuns above,
// just inlined in the transaction instead of going through that helper.
func TestReportMemoryCurationRunResult_SkipsStaleHeartbeatSiblings(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("handler test database unavailable")
	}
	ctx := context.Background()

	var freshRuntimeID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_runtime (workspace_id, daemon_id, name, runtime_mode, provider, status, device_info, metadata, visibility, last_seen_at) VALUES ($1,  'memory-curation-report-fresh-daemon',  'Memory Curation Report Fresh Runtime',  'local',  'codex',  'online', 
		          '',  '{}'::jsonb,  'private',  now())
		RETURNING id::text
	`,  testWorkspaceID).Scan(&freshRuntimeID); err != nil {
		t.Fatal(err)
	}
	var staleRuntimeID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_runtime (workspace_id, daemon_id, name, runtime_mode, provider, status, device_info, metadata, visibility, last_seen_at, updated_at) VALUES ($1,  'memory-curation-report-stale-daemon',  'Memory Curation Report Stale Runtime',  'local',  'codex',  'online', 
		          '',  '{}'::jsonb,  'private',  now() - interval '10 minutes',  now() - interval '9 minutes')
		RETURNING id::text
	`,  testWorkspaceID).Scan(&staleRuntimeID); err != nil {
		t.Fatal(err)
	}
	var reportingAgentID, staleAgentID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent (workspace_id, name, display_name, runtime_mode, runtime_id, owner_id, model)
		VALUES ($1, 'memory-curation-report-fresh-agent', 'Memory Curation Report Fresh Agent', 'local', $2, $3, 'composer-1.5')
		RETURNING id::text
	`, testWorkspaceID, freshRuntimeID, testUserID).Scan(&reportingAgentID); err != nil {
		t.Fatal(err)
	}
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent (workspace_id, name, display_name, runtime_mode, runtime_id, owner_id, model)
		VALUES ($1, 'memory-curation-report-stale-agent', 'Memory Curation Report Stale Agent', 'local', $2, $3, 'composer-1.5')
		RETURNING id::text
	`, testWorkspaceID, staleRuntimeID, testUserID).Scan(&staleAgentID); err != nil {
		t.Fatal(err)
	}
	var runID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO memory_curation_run (
		  workspace_id, stage, trigger_kind, status, date_from, date_to, runtime_id, execution_owner
		) VALUES ($1, 'agent_self_review', 'manual', 'running', '2026-08-01', '2026-08-01', $2, 'daemon')
		RETURNING id::text
	`, testWorkspaceID, freshRuntimeID).Scan(&runID); err != nil {
		t.Fatal(err)
	}
	claimToken := "00000000-0000-0000-0000-0000000000aa"
	var reportingChildID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO memory_curation_agent_run (parent_run_id, workspace_id, agent_id, runtime_id, stage, status, claim_token)
		VALUES ($1, $2, $3, $4, 'agent_self_review', 'running', $5::uuid)
		RETURNING id::text
	`, runID, testWorkspaceID, reportingAgentID, freshRuntimeID, claimToken).Scan(&reportingChildID); err != nil {
		t.Fatal(err)
	}
	if _, err := testPool.Exec(ctx, `
		INSERT INTO memory_curation_agent_run (parent_run_id, workspace_id, agent_id, runtime_id, stage, status)
		VALUES ($1, $2, $3, $4, 'agent_self_review', 'waiting_runtime')
	`, runID, testWorkspaceID, staleAgentID, staleRuntimeID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM memory_curation_agent_run WHERE parent_run_id = $1`, runID)
		_, _ = testPool.Exec(context.Background(), `DELETE FROM memory_curation_run WHERE id = $1`, runID)
		_, _ = testPool.Exec(context.Background(), `DELETE FROM agent WHERE id = ANY($1::uuid[])`, []string{reportingAgentID, staleAgentID})
		_, _ = testPool.Exec(context.Background(), `DELETE FROM agent_runtime WHERE id = ANY($1::uuid[])`, []string{freshRuntimeID, staleRuntimeID})
	})

	w := httptest.NewRecorder()
	resultReq := withURLParams(newDaemonTokenRequest(http.MethodPost,
		"/api/daemon/runtimes/"+freshRuntimeID+"/memory-curation/"+reportingChildID+"/result",
		map[string]any{"status": "succeeded", "claim_token": claimToken, "result": map[string]any{}},
		testWorkspaceID, "memory-curation-report-fresh-daemon"), "runtimeId", freshRuntimeID, "runId", reportingChildID)
	testHandler.ReportMemoryCurationRunResult(w, resultReq)
	if w.Code != http.StatusOK {
		t.Fatalf("ReportMemoryCurationRunResult: status=%d body=%s", w.Code, w.Body.String())
	}
	var resp map[string]any
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}

	var staleStatus, staleErr string
	if err := testPool.QueryRow(ctx, `
		SELECT status, error FROM memory_curation_agent_run WHERE parent_run_id = $1 AND agent_id = $2
	`, runID, staleAgentID).Scan(&staleStatus, &staleErr); err != nil {
		t.Fatalf("read sibling run: %v", err)
	}
	if staleStatus != "skipped" {
		t.Fatalf("stale-heartbeat sibling runtime (status column still 'online'): agent run status = %q, want %q (must key off heartbeat freshness, not the raw status column); error=%q", staleStatus, "skipped", staleErr)
	}
}
