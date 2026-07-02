package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestInMemoryUpdateStore_HasPending(t *testing.T) {
	ctx := context.Background()
	store := NewInMemoryUpdateStore()

	if has, err := store.HasPending(ctx, "rt-1"); err != nil || has {
		t.Fatalf("empty store should not report pending: has=%v err=%v", has, err)
	}

	if _, err := store.Create(ctx, "rt-1", "v1.2.3"); err != nil {
		t.Fatalf("create: %v", err)
	}
	if has, err := store.HasPending(ctx, "rt-1"); err != nil || !has {
		t.Fatalf("expected pending=true after Create: has=%v err=%v", has, err)
	}
	if has, err := store.HasPending(ctx, "rt-2"); err != nil || has {
		t.Fatalf("expected pending=false for unrelated runtime: has=%v err=%v", has, err)
	}

	if _, err := store.PopPending(ctx, "rt-1"); err != nil {
		t.Fatalf("pop: %v", err)
	}
	if has, err := store.HasPending(ctx, "rt-1"); err != nil || has {
		t.Fatalf("expected pending=false after PopPending: has=%v err=%v", has, err)
	}
}

func TestInMemoryUpdateStore_LatestForRuntimeIncludesTerminalHistory(t *testing.T) {
	ctx := context.Background()
	store := NewInMemoryUpdateStore()

	first, err := store.Create(ctx, "rt-1", "v1.2.3")
	if err != nil {
		t.Fatalf("create first: %v", err)
	}
	if latest, err := store.LatestForRuntime(ctx, "rt-1"); err != nil || latest == nil || latest.ID != first.ID || latest.Status != UpdatePending {
		t.Fatalf("latest after create = %+v err=%v", latest, err)
	}
	if err := store.Complete(ctx, first.ID, "done"); err != nil {
		t.Fatalf("complete first: %v", err)
	}
	if latest, err := store.LatestForRuntime(ctx, "rt-1"); err != nil || latest == nil || latest.ID != first.ID || latest.Status != UpdateCompleted {
		t.Fatalf("latest after complete = %+v err=%v", latest, err)
	}
	if latest, err := store.LatestForRuntime(ctx, "rt-2"); err != nil || latest != nil {
		t.Fatalf("latest for unrelated runtime = %+v err=%v", latest, err)
	}
}

func TestInMemoryUpdateStore_PopPendingIgnoresTerminalHistory(t *testing.T) {
	ctx := context.Background()
	store := NewInMemoryUpdateStore()

	first, err := store.Create(ctx, "rt-1", "v1.2.3")
	if err != nil {
		t.Fatalf("create first: %v", err)
	}
	if err := store.Fail(ctx, first.ID, "allow next request"); err != nil {
		t.Fatalf("fail first: %v", err)
	}
	time.Sleep(2 * time.Millisecond)
	second, err := store.Create(ctx, "rt-1", "v1.2.4")
	if err != nil {
		t.Fatalf("create second: %v", err)
	}

	got, err := store.PopPending(ctx, "rt-1")
	if err != nil {
		t.Fatalf("pop: %v", err)
	}
	if got == nil || got.ID != second.ID {
		t.Fatalf("expected second request, got %+v", got)
	}
}

func TestInMemoryUpdateStore_RunningRequestTimesOut(t *testing.T) {
	ctx := context.Background()
	store := NewInMemoryUpdateStore()

	req, err := store.Create(ctx, "rt-timeout", "v1.2.3")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	claimed, err := store.PopPending(ctx, "rt-timeout")
	if err != nil {
		t.Fatalf("pop: %v", err)
	}
	if claimed == nil || claimed.Status != UpdateRunning {
		t.Fatalf("expected running request, got %+v", claimed)
	}
	if claimed.RunStartedAt == nil {
		t.Fatal("expected RunStartedAt after PopPending")
	}

	aged := time.Now().Add(-(updateRunningTimeout + time.Second))
	claimed.RunStartedAt = &aged
	got, err := store.Get(ctx, req.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Status != UpdateTimeout {
		t.Fatalf("status = %s, want timeout", got.Status)
	}
	if got.Error == "" {
		t.Fatal("expected timeout error")
	}
}

func TestInMemoryUpdateStore_RejectsConcurrentActiveUntilTerminal(t *testing.T) {
	ctx := context.Background()
	store := NewInMemoryUpdateStore()

	req, err := store.Create(ctx, "rt-1", "v1.2.3")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := store.Create(ctx, "rt-1", "v1.2.4"); err != errUpdateInProgress {
		t.Fatalf("second create error = %v, want errUpdateInProgress", err)
	}
	if err := store.Complete(ctx, req.ID, "done"); err != nil {
		t.Fatalf("complete: %v", err)
	}
	if _, err := store.Create(ctx, "rt-1", "v1.2.4"); err != nil {
		t.Fatalf("create after terminal should succeed: %v", err)
	}
}

func TestReportUpdateResult_CompletedLeavesCurrentVersionUntilRegisterConfirms(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	daemonID := "update-report-version"
	w := httptest.NewRecorder()
	req := newDaemonTokenRequest(http.MethodPost, "/api/daemon/register", map[string]any{
		"workspace_id": testWorkspaceID,
		"daemon_id":    daemonID,
		"device_name":  "update-report-device",
		"cli_version":  "v0.3.0",
		"runtimes": []map[string]any{
			{"name": "update-report-runtime", "type": "claude", "version": "1.0.0", "status": "online"},
		},
	}, testWorkspaceID, daemonID)
	testHandler.DaemonRegister(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("DaemonRegister: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var registerResp struct {
		Runtimes []struct {
			ID string `json:"id"`
		} `json:"runtimes"`
	}
	if err := json.NewDecoder(w.Body).Decode(&registerResp); err != nil {
		t.Fatalf("decode register response: %v", err)
	}
	if len(registerResp.Runtimes) != 1 {
		t.Fatalf("registered runtimes = %d, want 1", len(registerResp.Runtimes))
	}
	runtimeID := registerResp.Runtimes[0].ID
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM agent_runtime WHERE id = $1`, runtimeID)
	})

	update, err := testHandler.UpdateStore.Create(context.Background(), runtimeID, "v0.3.1")
	if err != nil {
		t.Fatalf("create update: %v", err)
	}

	reportReq := newDaemonTokenRequest(
		http.MethodPost,
		"/api/daemon/runtimes/"+runtimeID+"/update/"+update.ID+"/result",
		map[string]any{"status": "completed", "output": "ok"},
		testWorkspaceID,
		daemonID,
	)
	reportReq = withURLParams(reportReq, "runtimeId", runtimeID, "updateId", update.ID)
	reportW := httptest.NewRecorder()

	testHandler.ReportUpdateResult(reportW, reportReq)
	if reportW.Code != http.StatusOK {
		t.Fatalf("ReportUpdateResult: expected 200, got %d: %s", reportW.Code, reportW.Body.String())
	}

	var cliVersion string
	if err := testPool.QueryRow(context.Background(), `SELECT metadata->>'cli_version' FROM agent_runtime WHERE id = $1`, runtimeID).Scan(&cliVersion); err != nil {
		t.Fatalf("read runtime cli_version: %v", err)
	}
	if cliVersion != "v0.3.0" {
		t.Fatalf("metadata.cli_version = %q, want v0.3.0 before daemon re-registers", cliVersion)
	}

	rt, err := testHandler.Queries.GetAgentRuntime(context.Background(), parseUUID(runtimeID))
	if err != nil {
		t.Fatalf("get runtime: %v", err)
	}
	resp := testHandler.runtimeToResponse(context.Background(), rt)
	if resp.CurrentVersion == nil || *resp.CurrentVersion != "v0.3.0" {
		t.Fatalf("current_version = %v, want v0.3.0", resp.CurrentVersion)
	}
	if resp.TargetVersion == nil || *resp.TargetVersion != "v0.3.1" {
		t.Fatalf("target_version = %v, want v0.3.1", resp.TargetVersion)
	}
	if resp.UpdateState != "completed" {
		t.Fatalf("update_state = %q, want completed", resp.UpdateState)
	}
	if resp.RuntimeHealth != "awaiting_confirmation" {
		t.Fatalf("runtime_health = %q, want awaiting_confirmation", resp.RuntimeHealth)
	}

	reconnectW := httptest.NewRecorder()
	reconnectReq := newDaemonTokenRequest(http.MethodPost, "/api/daemon/register", map[string]any{
		"workspace_id": testWorkspaceID,
		"daemon_id":    daemonID,
		"device_name":  "update-report-device",
		"cli_version":  "v0.3.1",
		"runtimes": []map[string]any{
			{"name": "update-report-runtime", "type": "claude", "version": "1.0.0", "status": "online"},
		},
	}, testWorkspaceID, daemonID)
	testHandler.DaemonRegister(reconnectW, reconnectReq)
	if reconnectW.Code != http.StatusOK {
		t.Fatalf("DaemonRegister reconnect: expected 200, got %d: %s", reconnectW.Code, reconnectW.Body.String())
	}

	rt, err = testHandler.Queries.GetAgentRuntime(context.Background(), parseUUID(runtimeID))
	if err != nil {
		t.Fatalf("get runtime after reconnect: %v", err)
	}
	resp = testHandler.runtimeToResponse(context.Background(), rt)
	if resp.CurrentVersion == nil || *resp.CurrentVersion != "v0.3.1" {
		t.Fatalf("current_version after reconnect = %v, want v0.3.1", resp.CurrentVersion)
	}
	if resp.UpdateState != "completed" {
		t.Fatalf("update_state after reconnect = %q, want completed", resp.UpdateState)
	}
	if resp.RuntimeHealth != "ok" {
		t.Fatalf("runtime_health after reconnect = %q, want ok", resp.RuntimeHealth)
	}
}
