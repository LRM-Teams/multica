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
	if _, err := store.PopPending(ctx, "rt-1"); err != nil {
		t.Fatalf("pop first: %v", err)
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

func TestInMemoryUpdateStore_RetainsTerminalHistoryBeyondActiveWindow(t *testing.T) {
	ctx := context.Background()
	store := NewInMemoryUpdateStore()
	now := time.Now()
	failed := &UpdateRequest{
		ID:            "failed-update",
		RuntimeID:     "rt-1",
		Status:        UpdateFailed,
		TargetVersion: "v1.2.3",
		Error:         "binary_version_mismatch_after_update",
		CreatedAt:     now.Add(-(updateStoreRetention + time.Minute)),
		UpdatedAt:     now.Add(-(updateStoreRetention + time.Minute)),
	}
	store.requests[failed.ID] = failed

	latest, err := store.LatestForRuntime(ctx, "rt-1")
	if err != nil {
		t.Fatalf("latest: %v", err)
	}
	if latest == nil || latest.ID != failed.ID {
		t.Fatalf("terminal update should remain readable beyond active window, got %+v", latest)
	}

	failed.UpdatedAt = now.Add(-(updateTerminalRetention + time.Second))
	if _, err := store.Create(ctx, "rt-other", "v1.2.4"); err != nil {
		t.Fatalf("create unrelated update: %v", err)
	}
	latest, err = store.LatestForRuntime(ctx, "rt-1")
	if err != nil {
		t.Fatalf("latest after cleanup: %v", err)
	}
	if latest != nil {
		t.Fatalf("expired terminal update should be pruned, got %+v", latest)
	}
}

func TestInMemoryUpdateStore_FreshReadyBlocksCreate(t *testing.T) {
	// B0-T2: fresh ready_to_apply still conflicts.
	ctx := context.Background()
	store := NewInMemoryUpdateStore()

	ready, err := store.Create(ctx, "rt-ready", "v1.2.3")
	if err != nil {
		t.Fatalf("create ready update: %v", err)
	}
	if _, err := store.PopPending(ctx, "rt-ready"); err != nil {
		t.Fatalf("pop ready update: %v", err)
	}
	if err := store.ReadyToApply(ctx, ready.ID, "verified"); err != nil {
		t.Fatalf("mark ready: %v", err)
	}

	if _, err := store.Create(ctx, "rt-ready", "v1.2.5"); err != errUpdateInProgress {
		t.Fatalf("create while fresh ready error = %v, want errUpdateInProgress", err)
	}
	got, err := store.Get(ctx, ready.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got == nil || got.Status != UpdateReady {
		t.Fatalf("fresh ready should stay ready, got %+v", got)
	}
}

func TestInMemoryUpdateStore_AgedReadyTimesOutAndAllowsCreate(t *testing.T) {
	// B0-T1: ready_to_apply past 20m → timeout → Create succeeds.
	ctx := context.Background()
	store := NewInMemoryUpdateStore()

	ready, err := store.Create(ctx, "rt-ready", "v1.2.3")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := store.PopPending(ctx, "rt-ready"); err != nil {
		t.Fatalf("pop: %v", err)
	}
	if err := store.ReadyToApply(ctx, ready.ID, "verified"); err != nil {
		t.Fatalf("ready: %v", err)
	}
	// Clock for ready TTL is UpdatedAt at ready transition.
	ready.UpdatedAt = time.Now().Add(-(updateReadyTimeout + time.Second))

	got, err := store.Get(ctx, ready.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Status != UpdateTimeout {
		t.Fatalf("status = %s, want timeout", got.Status)
	}
	if got.Error != updateReadyTimeoutError {
		t.Fatalf("error = %q, want %q", got.Error, updateReadyTimeoutError)
	}

	next, err := store.Create(ctx, "rt-ready", "v1.2.5")
	if err != nil {
		t.Fatalf("create after ready timeout: %v", err)
	}
	if next.Status != UpdatePending {
		t.Fatalf("next status = %s, want pending", next.Status)
	}
}

func TestInMemoryUpdateStore_ReadyWithinTTLDoesNotTimeout(t *testing.T) {
	ctx := context.Background()
	store := NewInMemoryUpdateStore()

	ready, err := store.Create(ctx, "rt-ready", "v1.2.3")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := store.PopPending(ctx, "rt-ready"); err != nil {
		t.Fatalf("pop: %v", err)
	}
	if err := store.ReadyToApply(ctx, ready.ID, "verified"); err != nil {
		t.Fatalf("ready: %v", err)
	}
	// Still inside 20m window.
	ready.UpdatedAt = time.Now().Add(-(updateReadyTimeout - time.Minute))

	got, err := store.Get(ctx, ready.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Status != UpdateReady {
		t.Fatalf("status = %s, want ready_to_apply", got.Status)
	}
}

func TestInMemoryUpdateStore_ReadyToApplyMayFailWithDrainTimeout(t *testing.T) {
	// #815 path A: ready_to_apply → failed+drain_timeout frees the channel.
	ctx := context.Background()
	store := NewInMemoryUpdateStore()

	req, err := store.Create(ctx, "rt-drain", "v0.3.78")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := store.PopPending(ctx, "rt-drain"); err != nil {
		t.Fatalf("pop: %v", err)
	}
	if err := store.ReadyToApply(ctx, req.ID, "staged"); err != nil {
		t.Fatalf("ready: %v", err)
	}
	if err := store.Fail(ctx, req.ID, DrainTimeoutError); err != nil {
		t.Fatalf("ready→failed: %v", err)
	}
	got, err := store.Get(ctx, req.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Status != UpdateFailed || got.Error != DrainTimeoutError {
		t.Fatalf("got %+v", got)
	}
	// Channel free for a new Initiate.
	next, err := store.Create(ctx, "rt-drain", "v0.3.79")
	if err != nil {
		t.Fatalf("create after drain_timeout: %v", err)
	}
	if next.Status != UpdatePending {
		t.Fatalf("next = %+v", next)
	}
}

func TestUpdateReportTransitionAllowsReadyToFailed(t *testing.T) {
	if !updateReportTransitionAllowed(UpdateReady, UpdateFailed) {
		t.Fatal("ready→failed must be allowed")
	}
	if !updateReportTransitionAllowed(UpdateReady, UpdateCompleted) {
		t.Fatal("ready→completed must stay allowed")
	}
	if updateReportTransitionAllowed(UpdateCompleted, UpdateFailed) {
		t.Fatal("completed→failed must stay forbidden")
	}
}

func TestLateFailedReportIsNoOpOnlyForTimeoutDrainTimeout(t *testing.T) {
	timeoutRow := &UpdateRequest{Status: UpdateTimeout, Error: updateReadyTimeoutError}
	if !lateFailedReportIsNoOp(timeoutRow, UpdateFailed, DrainTimeoutError) {
		t.Fatal("timeout + drain_timeout should no-op")
	}
	if lateFailedReportIsNoOp(timeoutRow, UpdateFailed, "other_error") {
		t.Fatal("timeout + non-drain error must not no-op")
	}
	completed := &UpdateRequest{Status: UpdateCompleted}
	if lateFailedReportIsNoOp(completed, UpdateFailed, DrainTimeoutError) {
		t.Fatal("completed + failed must not no-op (409 path)")
	}
}

func TestInMemoryUpdateStore_TerminalHistoryRetainedAfterReadyTimeout(t *testing.T) {
	// B0-T4: timeout terminal history remains readable until terminal retention.
	ctx := context.Background()
	store := NewInMemoryUpdateStore()

	ready, err := store.Create(ctx, "rt-ready", "v1.2.3")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := store.PopPending(ctx, "rt-ready"); err != nil {
		t.Fatalf("pop: %v", err)
	}
	if err := store.ReadyToApply(ctx, ready.ID, "verified"); err != nil {
		t.Fatalf("ready: %v", err)
	}
	ready.UpdatedAt = time.Now().Add(-(updateReadyTimeout + time.Second))

	if _, err := store.Create(ctx, "rt-ready", "v1.2.5"); err != nil {
		t.Fatalf("create after timeout: %v", err)
	}
	latest, err := store.LatestForRuntime(ctx, "rt-ready")
	if err != nil {
		t.Fatalf("latest: %v", err)
	}
	// Latest may be the new pending; the timed-out row must still be Get-able.
	got, err := store.Get(ctx, ready.ID)
	if err != nil {
		t.Fatalf("get timed-out: %v", err)
	}
	if got == nil || got.Status != UpdateTimeout {
		t.Fatalf("timed-out row missing/wrong: %+v", got)
	}
	if latest == nil {
		t.Fatal("expected some latest row")
	}
}

func TestInMemoryUpdateStore_PopPendingIgnoresTerminalHistory(t *testing.T) {
	ctx := context.Background()
	store := NewInMemoryUpdateStore()

	first, err := store.Create(ctx, "rt-1", "v1.2.3")
	if err != nil {
		t.Fatalf("create first: %v", err)
	}
	if _, err := store.PopPending(ctx, "rt-1"); err != nil {
		t.Fatalf("pop first: %v", err)
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
	if _, err := store.PopPending(ctx, "rt-1"); err != nil {
		t.Fatalf("pop: %v", err)
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
	if _, err := testHandler.UpdateStore.PopPending(context.Background(), runtimeID); err != nil {
		t.Fatalf("pop update: %v", err)
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

	duplicateReq := newDaemonTokenRequest(
		http.MethodPost,
		"/api/daemon/runtimes/"+runtimeID+"/update/"+update.ID+"/result",
		map[string]any{"status": "completed", "output": "late duplicate"},
		testWorkspaceID,
		daemonID,
	)
	duplicateReq = withURLParams(duplicateReq, "runtimeId", runtimeID, "updateId", update.ID)
	duplicateW := httptest.NewRecorder()
	testHandler.ReportUpdateResult(duplicateW, duplicateReq)
	if duplicateW.Code != http.StatusOK {
		t.Fatalf("duplicate completed report: expected 200, got %d: %s", duplicateW.Code, duplicateW.Body.String())
	}
	persistedUpdate, err := testHandler.UpdateStore.Get(context.Background(), update.ID)
	if err != nil {
		t.Fatalf("get update after duplicate completion: %v", err)
	}
	if persistedUpdate == nil || persistedUpdate.Status != UpdateCompleted || persistedUpdate.Output != "ok" {
		t.Fatalf("duplicate completion overwrote winner: %+v", persistedUpdate)
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
	if resp.RuntimeHealth != "updating" {
		t.Fatalf("runtime_health = %q, want updating", resp.RuntimeHealth)
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

func TestReportUpdateResultReadyToApplyRejectsExpiredRunningUpdate(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	daemonID := "update-ready-timeout-" + randomID()
	registerW := httptest.NewRecorder()
	registerReq := newDaemonTokenRequest(http.MethodPost, "/api/daemon/register", map[string]any{
		"workspace_id": testWorkspaceID,
		"daemon_id":    daemonID,
		"device_name":  "update-ready-timeout-device",
		"cli_version":  "v0.3.0",
		"runtimes": []map[string]any{
			{"name": "update-ready-timeout-runtime", "type": "claude", "version": "1.0.0", "status": "online"},
		},
	}, testWorkspaceID, daemonID)
	testHandler.DaemonRegister(registerW, registerReq)
	if registerW.Code != http.StatusOK {
		t.Fatalf("DaemonRegister: expected 200, got %d: %s", registerW.Code, registerW.Body.String())
	}

	var registerResp struct {
		Runtimes []struct {
			ID string `json:"id"`
		} `json:"runtimes"`
	}
	if err := json.NewDecoder(registerW.Body).Decode(&registerResp); err != nil {
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
	if _, err := testHandler.UpdateStore.PopPending(context.Background(), runtimeID); err != nil {
		t.Fatalf("pop update: %v", err)
	}
	if _, err := testPool.Exec(context.Background(), `
		UPDATE daemon_runtime_update
		SET run_started_at = now() - ($2 * interval '1 second')
		WHERE id = $1
	`, update.ID, (updateRunningTimeout + time.Second).Seconds()); err != nil {
		t.Fatalf("age running update: %v", err)
	}

	reportReq := newDaemonTokenRequest(
		http.MethodPost,
		"/api/daemon/runtimes/"+runtimeID+"/update/"+update.ID+"/result",
		map[string]any{"status": "ready_to_apply", "output": "verified"},
		testWorkspaceID,
		daemonID,
	)
	reportReq = withURLParams(reportReq, "runtimeId", runtimeID, "updateId", update.ID)
	reportW := httptest.NewRecorder()
	testHandler.ReportUpdateResult(reportW, reportReq)
	if reportW.Code < 400 {
		t.Fatalf("ReportUpdateResult: got %d, want non-2xx for timeout -> ready conflict: %s", reportW.Code, reportW.Body.String())
	}

	got, err := testHandler.UpdateStore.Get(context.Background(), update.ID)
	if err != nil {
		t.Fatalf("get expired update: %v", err)
	}
	if got == nil || got.Status != UpdateTimeout {
		t.Fatalf("update after conflicting ready report = %+v, want timeout", got)
	}
}

func TestDaemonRegisterCompletesRunningUpdateOnTargetVersion(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	daemonID := "update-running-register"
	w := httptest.NewRecorder()
	req := newDaemonTokenRequest(http.MethodPost, "/api/daemon/register", map[string]any{
		"workspace_id": testWorkspaceID,
		"daemon_id":    daemonID,
		"device_name":  "update-running-device",
		"cli_version":  "v0.3.0",
		"runtimes": []map[string]any{
			{"name": "update-running-runtime", "type": "claude", "version": "1.0.0", "status": "online"},
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
	if _, err := testHandler.UpdateStore.PopPending(context.Background(), runtimeID); err != nil {
		t.Fatalf("pop update: %v", err)
	}

	reconnectW := httptest.NewRecorder()
	reconnectReq := newDaemonTokenRequest(http.MethodPost, "/api/daemon/register", map[string]any{
		"workspace_id": testWorkspaceID,
		"daemon_id":    daemonID,
		"device_name":  "update-running-device",
		"cli_version":  "v0.3.1",
		"runtimes": []map[string]any{
			{"name": "update-running-runtime", "type": "claude", "version": "1.0.0", "status": "online"},
		},
	}, testWorkspaceID, daemonID)
	testHandler.DaemonRegister(reconnectW, reconnectReq)
	if reconnectW.Code != http.StatusOK {
		t.Fatalf("DaemonRegister reconnect: expected 200, got %d: %s", reconnectW.Code, reconnectW.Body.String())
	}

	got, err := testHandler.UpdateStore.Get(context.Background(), update.ID)
	if err != nil {
		t.Fatalf("get update: %v", err)
	}
	if got == nil || got.Status != UpdateCompleted {
		t.Fatalf("update after target register = %+v, want completed", got)
	}

	rt, err := testHandler.Queries.GetAgentRuntime(context.Background(), parseUUID(runtimeID))
	if err != nil {
		t.Fatalf("get runtime after reconnect: %v", err)
	}
	resp := testHandler.runtimeToResponse(context.Background(), rt)
	if resp.UpdateState != "completed" {
		t.Fatalf("update_state after reconnect = %q, want completed", resp.UpdateState)
	}
	if resp.RuntimeHealth != "ok" {
		t.Fatalf("runtime_health after reconnect = %q, want ok", resp.RuntimeHealth)
	}
}
