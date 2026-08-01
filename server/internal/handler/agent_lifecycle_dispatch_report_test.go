package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestReportAgentLifecycleOperationResultUpdatesOperationStatus pins the
// other missing half of task #52: after CreateAgentLifecycleOperation
// dispatches an operation, the daemon's eventual result report must move the
// row out of running into succeeded/failed — otherwise a client polling
// GetAgentLifecycleOperation would see "running" forever even though the
// daemon finished.
func TestReportAgentLifecycleOperationResultUpdatesOperationStatus(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	agentID, runtimeID := createAgentLifecycleFixture(t, true)
	create := invokeCreateAgentLifecycle(t, agentID, "11111111-1111-1111-1111-111111111111", agentLifecycleRestart)
	if create.Code != http.StatusAccepted {
		t.Fatalf("create status=%d body=%s", create.Code, create.Body.String())
	}
	var operation AgentLifecycleOperation
	if err := json.Unmarshal(create.Body.Bytes(), &operation); err != nil {
		t.Fatalf("decode operation: %v", err)
	}

	reportReq := newDaemonTokenRequest(
		http.MethodPost,
		"/api/daemon/runtimes/"+runtimeID+"/agent-lifecycle/"+operation.ID+"/result",
		map[string]any{"status": "succeeded", "step": "done"},
		testWorkspaceID,
		"agent-lifecycle-report-daemon",
	)
	reportReq = withURLParams(reportReq, "runtimeId", runtimeID, "operationId", operation.ID)
	reportRec := httptest.NewRecorder()
	testHandler.ReportAgentLifecycleOperationResult(reportRec, reportReq)
	if reportRec.Code != http.StatusOK {
		t.Fatalf("ReportAgentLifecycleOperationResult: expected 200, got %d: %s", reportRec.Code, reportRec.Body.String())
	}

	got, err := getAgentLifecycleOperation(context.Background(), testPool, parseUUID(agentID), parseUUID(operation.ID))
	if err != nil {
		t.Fatalf("reload operation: %v", err)
	}
	if got == nil {
		t.Fatal("operation not found after report")
	}
	if got.Status != agentLifecycleSucceeded {
		t.Fatalf("operation status = %q, want %q", got.Status, agentLifecycleSucceeded)
	}
	if got.Step != "done" {
		t.Fatalf("operation step = %q, want %q", got.Step, "done")
	}
	if got.FinishedAt == nil {
		t.Fatal("expected finished_at to be set")
	}
}

// TestReportAgentLifecycleOperationResultRequiresDaemonWorkspaceAccess pins
// the auth bar: this is a daemon-authenticated endpoint (matching the other
// ReportXResult endpoints), not the human owner/admin bar CreateAgentLifecycleOperation
// uses. A daemon token for a different workspace must be rejected.
func TestReportAgentLifecycleOperationResultRequiresDaemonWorkspaceAccess(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	agentID, runtimeID := createAgentLifecycleFixture(t, true)
	create := invokeCreateAgentLifecycle(t, agentID, "22222222-2222-2222-2222-222222222222", agentLifecycleRestart)
	if create.Code != http.StatusAccepted {
		t.Fatalf("create status=%d body=%s", create.Code, create.Body.String())
	}
	var operation AgentLifecycleOperation
	if err := json.Unmarshal(create.Body.Bytes(), &operation); err != nil {
		t.Fatalf("decode operation: %v", err)
	}

	reportReq := newDaemonTokenRequest(
		http.MethodPost,
		"/api/daemon/runtimes/"+runtimeID+"/agent-lifecycle/"+operation.ID+"/result",
		map[string]any{"status": "succeeded"},
		"00000000-0000-0000-0000-000000000000",
		"unrelated-daemon",
	)
	reportReq = withURLParams(reportReq, "runtimeId", runtimeID, "operationId", operation.ID)
	reportRec := httptest.NewRecorder()
	testHandler.ReportAgentLifecycleOperationResult(reportRec, reportReq)
	if reportRec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for mismatched daemon workspace, got %d: %s", reportRec.Code, reportRec.Body.String())
	}
}
