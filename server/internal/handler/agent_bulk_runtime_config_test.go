package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestBulkUpdateAgentRuntimeConfigUpdatesAllAgentsAtomically(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	daemonID := "bulk-config-" + uuid.NewString()
	oldRuntimeID := seedMachineLockedRuntime(t, daemonID, "Bulk Config Old")
	targetRuntimeID := seedMachineLockedRuntime(t, daemonID, "Bulk Config Target")
	if _, err := testPool.Exec(ctx, `UPDATE agent_runtime SET provider = 'claude' WHERE id = $1`, targetRuntimeID); err != nil {
		t.Fatalf("set target provider: %v", err)
	}
	firstAgentID := createHandlerTestAgentOnRuntime(t, "bulk-config-first-"+uuid.NewString()[:8], oldRuntimeID)
	secondAgentID := createHandlerTestAgentOnRuntime(t, "bulk-config-second-"+uuid.NewString()[:8], oldRuntimeID)

	rec := httptest.NewRecorder()
	req := newRequest(http.MethodPut, "/api/agents/runtime-config", map[string]any{
		"agent_ids":      []string{firstAgentID, secondAgentID},
		"runtime_id":     targetRuntimeID,
		"model":          "bulk-model",
		"thinking_level": "high",
	})
	testHandler.BulkUpdateAgentRuntimeConfig(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("bulk update status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var response struct {
		UpdatedAgentIDs []string `json:"updated_agent_ids"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(response.UpdatedAgentIDs) != 2 {
		t.Fatalf("updated_agent_ids = %v, want both agents", response.UpdatedAgentIDs)
	}

	for _, agentID := range []string{firstAgentID, secondAgentID} {
		var runtimeID, model, thinkingLevel string
		if err := testPool.QueryRow(ctx, `
			SELECT runtime_id::text, model, thinking_level
			FROM agent WHERE id = $1
		`, agentID).Scan(&runtimeID, &model, &thinkingLevel); err != nil {
			t.Fatalf("read updated agent %s: %v", agentID, err)
		}
		if runtimeID != targetRuntimeID || model != "bulk-model" || thinkingLevel != "high" {
			t.Fatalf("agent %s config = (%s, %s, %s)", agentID, runtimeID, model, thinkingLevel)
		}
	}
}

func TestBulkUpdateAgentRuntimeConfigRejectsUnknownAgentWithoutPartialWrite(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	daemonID := "bulk-config-atomic-" + uuid.NewString()
	oldRuntimeID := seedMachineLockedRuntime(t, daemonID, "Bulk Config Atomic Old")
	targetRuntimeID := seedMachineLockedRuntime(t, daemonID, "Bulk Config Atomic Target")
	agentID := createHandlerTestAgentOnRuntime(t, "bulk-config-atomic-"+uuid.NewString()[:8], oldRuntimeID)

	rec := httptest.NewRecorder()
	req := newRequest(http.MethodPut, "/api/agents/runtime-config", map[string]any{
		"agent_ids":  []string{agentID, uuid.NewString()},
		"runtime_id": targetRuntimeID,
		"model":      "must-not-apply",
	})
	testHandler.BulkUpdateAgentRuntimeConfig(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("bulk update status = %d, want 404: %s", rec.Code, rec.Body.String())
	}
	var runtimeID, model string
	if err := testPool.QueryRow(ctx, `SELECT runtime_id::text, model FROM agent WHERE id = $1`, agentID).Scan(&runtimeID, &model); err != nil {
		t.Fatalf("read unchanged agent: %v", err)
	}
	if runtimeID != oldRuntimeID || model == "must-not-apply" {
		t.Fatalf("bulk update partially applied: runtime=%s model=%s", runtimeID, model)
	}
}

func TestBulkUpdateAgentRuntimeConfigReturnsInternalErrorForUnexpectedUpdateFailure(t *testing.T) {
	testBulkRuntimeConfigUpdateError(t, errors.New("injected update failure"), http.StatusInternalServerError, "failed to update agents")
}

func TestBulkUpdateAgentRuntimeConfigPreservesDaemonOutdatedError(t *testing.T) {
	testBulkRuntimeConfigUpdateError(t, &pgconn.PgError{Code: "P0001", Message: "daemon_outdated"}, http.StatusConflict, "daemon_outdated")
}

func testBulkRuntimeConfigUpdateError(t *testing.T, updateErr error, wantStatus int, wantBody string) {
	t.Helper()
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	daemonID := "bulk-config-error-" + uuid.NewString()
	runtimeID := seedMachineLockedRuntime(t, daemonID, "Bulk Config Error")
	agentID := createHandlerTestAgentOnRuntime(t, "bulk-config-error-"+uuid.NewString()[:8], runtimeID)
	previousStarter := testHandler.TxStarter
	testHandler.TxStarter = queryFailingTxStarter{
		base:        previousStarter,
		sqlContains: "UPDATE agent SET",
		err:         updateErr,
	}
	t.Cleanup(func() { testHandler.TxStarter = previousStarter })

	rec := httptest.NewRecorder()
	req := newRequest(http.MethodPut, "/api/agents/runtime-config", map[string]any{
		"agent_ids":  []string{agentID},
		"runtime_id": runtimeID,
		"model":      "bulk-model",
	})
	testHandler.BulkUpdateAgentRuntimeConfig(rec, req)

	if rec.Code != wantStatus || !strings.Contains(rec.Body.String(), wantBody) {
		t.Fatalf("bulk update response = %d %s, want %d containing %q", rec.Code, rec.Body.String(), wantStatus, wantBody)
	}
}
