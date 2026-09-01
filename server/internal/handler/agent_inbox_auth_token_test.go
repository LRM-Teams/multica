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

// Inbox drain must mint a short-lived AuthToken even when the runtime
// advertises agent_credential_transport. After #3919 the daemon fail-closes
// inbox tasks with credential_unavailable if this field is empty; durable
// launch credentials are for resident Message delivery, not collector /
// note-worker inbox wakes.
func TestDrainAgentInboxMintsAuthTokenForCredentialTransportRuntime(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	daemonID := "inbox-auth-token-" + uuid.NewString()
	runtimeID := createClaimReclaimRuntime(t, ctx, "inbox-auth-token-"+uuid.NewString())

	metadata, err := json.Marshal(map[string]any{
		"capabilities": []string{protocol.DaemonCapabilityAgentCredentialTransport},
	})
	if err != nil {
		t.Fatalf("marshal capabilities: %v", err)
	}
	if _, err := testPool.Exec(ctx, `
		UPDATE agent_runtime SET daemon_id = $1, metadata = $2 WHERE id = $3`,
		daemonID, metadata, runtimeID); err != nil {
		t.Fatalf("bind runtime daemon and capabilities: %v", err)
	}
	if _, err := testPool.Exec(ctx, `
		INSERT INTO computer_workspace_bindings (
			daemon_id, workspace_id, user_id, execution_token_hash, active
		) VALUES ($1, $2, $3, 'inbox-auth-token-owner', TRUE)
		ON CONFLICT (daemon_id, workspace_id)
		DO UPDATE SET user_id = EXCLUDED.user_id, active = TRUE, revoked_at = NULL`,
		daemonID, testWorkspaceID, testUserID); err != nil {
		t.Fatalf("seed runtime owner: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(),
			`DELETE FROM computer_workspace_bindings WHERE daemon_id = $1 AND workspace_id = $2`,
			daemonID, testWorkspaceID)
	})

	agentID := createHandlerTestAgentOnRuntime(t, "Inbox Auth Token Agent "+uuid.NewString()[:8], runtimeID)
	noteID := createNotePageForAITest(t, "Inbox auth token note "+uuid.NewString())
	req := withURLParam(newRequest(http.MethodPost, "/api/notes/pages/"+noteID+"/worker-jobs", map[string]any{
		"agent_id":    agentID,
		"instruction": "Collect a period-brief pack",
		"intent":      NoteIntentWorker,
	}), "id", noteID)
	createRec := httptest.NewRecorder()
	testHandler.CreateNoteWorkerJob(createRec, req)
	if createRec.Code != http.StatusCreated {
		t.Fatalf("CreateNoteWorkerJob = %d: %s", createRec.Code, createRec.Body.String())
	}

	drainReq := newDaemonTokenRequest(http.MethodPost, "/api/daemon/runtimes/"+runtimeID+"/agent-inbox/drain", nil, testWorkspaceID, daemonID)
	drainReq = withURLParam(drainReq, "runtimeId", runtimeID)
	drainRec := httptest.NewRecorder()
	testHandler.DrainAgentInboxByRuntime(drainRec, drainReq)
	if drainRec.Code != http.StatusOK {
		t.Fatalf("drain = %d: %s", drainRec.Code, drainRec.Body.String())
	}
	var drained DrainAgentInboxResponse
	if err := json.Unmarshal(drainRec.Body.Bytes(), &drained); err != nil {
		t.Fatalf("decode drain: %v", err)
	}
	if len(drained.Events) != 1 {
		t.Fatalf("drained events = %d, want 1", len(drained.Events))
	}
	task := drained.Events[0].Task
	if task == nil {
		t.Fatal("drain returned no task payload")
	}
	if task.AuthToken == "" {
		t.Fatal("credential-transport inbox drain left AuthToken empty; daemon will fail closed with credential_unavailable")
	}
}
