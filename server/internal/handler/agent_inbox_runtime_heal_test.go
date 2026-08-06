package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
)

// TestDrainAgentInbox_HealsStaleRuntimeAfterAgentReassignment is the
// LRM-927 regression: agent.runtime_id can change while claimable
// agent_inbox_event rows still pin the old runtime. Without lease-time
// heal, the old daemon leases those events, ensure-credential 403s with
// "agent is not bound to this runtime", and Activity loops on
// credential_unavailable. Drain on the old runtime must re-point the
// event and leave it for the agent's current runtime.
func TestDrainAgentInbox_HealsStaleRuntimeAfterAgentReassignment(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	agentName := "inbox-runtime-heal-" + uuid.NewString()[:8]
	agentID := createHandlerTestAgent(t, agentName, nil)
	oldRuntimeID := handlerTestRuntimeID(t)

	var newRuntimeID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_runtime (
			workspace_id, daemon_id, name, runtime_mode, provider, status,
			device_info, metadata, owner_id, last_seen_at
		)
		VALUES ($1, $2, 'Inbox Heal Runtime', 'local', 'test-heal', 'online', 'test runtime', '{}'::jsonb, $3, now())
		RETURNING id
	`, testWorkspaceID, "inbox-heal-"+uuid.NewString(), testUserID).Scan(&newRuntimeID); err != nil {
		t.Fatalf("create new runtime: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM agent_runtime WHERE id = $1`, newRuntimeID)
	})

	channelID := seedChannelForTest(t, "agent-inbox-runtime-heal-"+uuid.NewString(), testUserID)
	if _, err := testPool.Exec(ctx, `
		INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id)
		VALUES ($1, $2, 'agent', $3)
		ON CONFLICT DO NOTHING`, channelID, testWorkspaceID, agentID); err != nil {
		t.Fatalf("seed agent member: %v", err)
	}
	eventID := createProductInboxEventForRuntime(t, oldRuntimeID, agentID, channelID)

	var eventRuntimeID string
	if err := testPool.QueryRow(ctx, `
		SELECT id, runtime_id
		FROM agent_inbox_event
		WHERE agent_id = $1 AND status IN ('pending', 'failed')
		ORDER BY created_at DESC
		LIMIT 1`, agentID).Scan(&eventID, &eventRuntimeID); err != nil {
		t.Fatalf("load pending inbox event: %v", err)
	}
	if eventRuntimeID != oldRuntimeID {
		t.Fatalf("event runtime = %s, want old runtime %s", eventRuntimeID, oldRuntimeID)
	}

	if _, err := testPool.Exec(ctx, `UPDATE agent SET runtime_id = $2 WHERE id = $1`, agentID, newRuntimeID); err != nil {
		t.Fatalf("reassign agent to new runtime: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `UPDATE agent SET runtime_id = $2 WHERE id = $1`, agentID, oldRuntimeID)
	})

	oldDrainReq := newDaemonTokenRequest(http.MethodPost, "/api/daemon/runtimes/"+oldRuntimeID+"/agent-inbox/drain", nil, testWorkspaceID, "agent-inbox-heal-old")
	oldDrainReq = withURLParam(oldDrainReq, "runtimeId", oldRuntimeID)
	oldDrainRec := httptest.NewRecorder()
	testHandler.DrainAgentInboxByRuntime(oldDrainRec, oldDrainReq)
	if oldDrainRec.Code != http.StatusOK {
		t.Fatalf("old runtime drain: status=%d body=%s", oldDrainRec.Code, oldDrainRec.Body.String())
	}
	var oldDrainResp DrainAgentInboxResponse
	if err := json.Unmarshal(oldDrainRec.Body.Bytes(), &oldDrainResp); err != nil {
		t.Fatalf("decode old drain response: %v", err)
	}
	if len(oldDrainResp.Events) != 0 {
		t.Fatalf("old runtime drain returned %d events, want 0 after heal: %s", len(oldDrainResp.Events), oldDrainRec.Body.String())
	}

	if err := testPool.QueryRow(ctx, `SELECT runtime_id FROM agent_inbox_event WHERE id = $1`, eventID).Scan(&eventRuntimeID); err != nil {
		t.Fatalf("reload event runtime: %v", err)
	}
	if eventRuntimeID != newRuntimeID {
		t.Fatalf("healed event runtime = %s, want new runtime %s", eventRuntimeID, newRuntimeID)
	}
	var sessionRuntimeID string
	if err := testPool.QueryRow(ctx, `
		SELECT s.runtime_id
		FROM agent_inbox_event e
		JOIN agent_session s ON s.id = e.agent_session_id
		WHERE e.id = $1`, eventID).Scan(&sessionRuntimeID); err != nil {
		t.Fatalf("load session runtime: %v", err)
	}
	if sessionRuntimeID != newRuntimeID {
		t.Fatalf("healed session runtime = %s, want new runtime %s", sessionRuntimeID, newRuntimeID)
	}

	newDrainReq := newDaemonTokenRequest(http.MethodPost, "/api/daemon/runtimes/"+newRuntimeID+"/agent-inbox/drain", nil, testWorkspaceID, "agent-inbox-heal-new")
	newDrainReq = withURLParam(newDrainReq, "runtimeId", newRuntimeID)
	newDrainRec := httptest.NewRecorder()
	testHandler.DrainAgentInboxByRuntime(newDrainRec, newDrainReq)
	if newDrainRec.Code != http.StatusOK {
		t.Fatalf("new runtime drain: status=%d body=%s", newDrainRec.Code, newDrainRec.Body.String())
	}
	var newDrainResp DrainAgentInboxResponse
	if err := json.Unmarshal(newDrainRec.Body.Bytes(), &newDrainResp); err != nil {
		t.Fatalf("decode new drain response: %v", err)
	}
	if len(newDrainResp.Events) != 1 {
		t.Fatalf("new runtime drain returned %d events, want 1: %s", len(newDrainResp.Events), newDrainRec.Body.String())
	}
	if newDrainResp.Events[0].ID != eventID || newDrainResp.Events[0].AgentID != agentID {
		t.Fatalf("new drain event = %+v, want healed event %s for agent %s", newDrainResp.Events[0], eventID, agentID)
	}
}

// TestUpdateAgent_ReassignsClaimableInboxEventsOnRuntimeMove covers the
// eager UpdateAgent path that complements lease-time heal.
func TestUpdateAgent_ReassignsClaimableInboxEventsOnRuntimeMove(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	agentName := "inbox-update-heal-" + uuid.NewString()[:8]

	// task #(machine-lock, 2026-08-02): old and new runtimes must share a
	// daemon_id (same computer) — handlerTestRuntimeID's daemon_id is NULL,
	// its own unshareable machine, so the agent starts on a runtime that has
	// a real daemon_id instead.
	daemonID := "inbox-update-heal-daemon-" + uuid.NewString()
	oldRuntimeID := seedMachineLockedRuntime(t, daemonID, "Inbox Update Heal Old Runtime")
	agentID := createHandlerTestAgentOnRuntime(t, agentName, oldRuntimeID)
	newRuntimeID := seedMachineLockedRuntime(t, daemonID, "Inbox Update Heal Runtime")

	channelID := seedChannelForTest(t, "agent-inbox-update-heal-"+uuid.NewString(), testUserID)
	if _, err := testPool.Exec(ctx, `
		INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id)
		VALUES ($1, $2, 'agent', $3)
		ON CONFLICT DO NOTHING`, channelID, testWorkspaceID, agentID); err != nil {
		t.Fatalf("seed agent member: %v", err)
	}
	eventID := createProductInboxEventForRuntime(t, oldRuntimeID, agentID, channelID)

	body := map[string]any{"runtime_id": newRuntimeID}
	w := httptest.NewRecorder()
	req := withURLParam(newRequest(http.MethodPatch, "/api/agents/"+agentID, body), "id", agentID)
	testHandler.UpdateAgent(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("UpdateAgent: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var eventRuntimeID, agentRuntimeID string
	if err := testPool.QueryRow(ctx, `
		SELECT e.runtime_id, a.runtime_id
		FROM agent_inbox_event e
		JOIN agent a ON a.id = e.agent_id
		WHERE e.id = $1`, eventID).Scan(&eventRuntimeID, &agentRuntimeID); err != nil {
		t.Fatalf("load post-update runtimes: %v", err)
	}
	if agentRuntimeID != newRuntimeID {
		t.Fatalf("agent runtime = %s, want %s", agentRuntimeID, newRuntimeID)
	}
	if eventRuntimeID != newRuntimeID {
		t.Fatalf("inbox event runtime = %s, want %s (old=%s)", eventRuntimeID, newRuntimeID, oldRuntimeID)
	}
}
