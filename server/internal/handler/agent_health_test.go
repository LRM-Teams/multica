package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func TestGetAgentHealth_MapsRuntimeAndHealthEvents(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	agentID, runtimeID := createAgentHealthFixture(t, "online", time.Now().Add(-20*time.Second), time.Now().Add(-15*time.Second))
	eventID := createAgentHealthEvent(t, agentID, runtimeID, agentHealthEventTransportRecover, agentHealthStateRecovered, "transport_reconnected")

	w := httptest.NewRecorder()
	testHandler.GetAgentHealth(w, withURLParam(newRequest("GET", "/api/agents/"+agentID+"/health", nil), "id", agentID))
	if w.Code != http.StatusOK {
		t.Fatalf("GetAgentHealth: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp AgentHealthResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Summary.AgentID != agentID {
		t.Fatalf("summary agent_id = %q, want %q", resp.Summary.AgentID, agentID)
	}
	if resp.Summary.RuntimeID == nil || *resp.Summary.RuntimeID != runtimeID {
		t.Fatalf("summary runtime_id = %#v, want %s", resp.Summary.RuntimeID, runtimeID)
	}
	if resp.Summary.State != agentHealthStateRecovered {
		t.Fatalf("summary state = %q, want %q", resp.Summary.State, agentHealthStateRecovered)
	}
	if len(resp.Events) == 0 {
		t.Fatalf("expected health events")
	}
	if resp.Events[0].ID != eventID {
		t.Fatalf("first event id = %q, want persisted event %q", resp.Events[0].ID, eventID)
	}
	if resp.Events[0].Type != agentHealthEventTransportRecover || resp.Events[0].StateAfter != agentHealthStateRecovered {
		t.Fatalf("first event = %s/%s, want recovered transport event", resp.Events[0].Type, resp.Events[0].StateAfter)
	}
}

func TestGetAgentHealth_OfflineAgesIntoReconnecting(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	agentID, _ := createAgentHealthFixture(t, "offline", time.Now().Add(-15*time.Minute), time.Now().Add(-10*time.Minute))

	w := httptest.NewRecorder()
	testHandler.GetAgentHealth(w, withURLParam(newRequest("GET", "/api/agents/"+agentID+"/health", nil), "id", agentID))
	if w.Code != http.StatusOK {
		t.Fatalf("GetAgentHealth: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp AgentHealthResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Summary.State != agentHealthStateReconnecting {
		t.Fatalf("summary state = %q, want %q", resp.Summary.State, agentHealthStateReconnecting)
	}
	if len(resp.Events) == 0 || resp.Events[0].Type != agentHealthEventProbeTimeout {
		t.Fatalf("expected synthetic probe-timeout event, got %+v", resp.Events)
	}
}

func TestGetAgentHealth_PrivateAgentForbidsPlainMember(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	agentID, ownerID, memberID := privateAgentTestFixture(t)

	w := httptest.NewRecorder()
	testHandler.GetAgentHealth(w, withURLParam(newRequestAs(ownerID, "GET", "/api/agents/"+agentID+"/health", nil), "id", agentID))
	if w.Code != http.StatusOK {
		t.Fatalf("GetAgentHealth as owner: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	w = httptest.NewRecorder()
	testHandler.GetAgentHealth(w, withURLParam(newRequestAs(memberID, "GET", "/api/agents/"+agentID+"/health", nil), "id", agentID))
	if w.Code != http.StatusForbidden {
		t.Fatalf("GetAgentHealth as plain member: expected 403, got %d: %s", w.Code, w.Body.String())
	}
}

func TestAgentHealthEventStateMapping(t *testing.T) {
	tests := []struct {
		eventType string
		state     string
	}{
		{agentHealthEventServerPing, agentHealthStateOnline},
		{agentHealthEventLivenessProbe, agentHealthStateSuspectedDisconnect},
		{agentHealthEventProbeTimeout, agentHealthStateReconnecting},
		{agentHealthEventTransportRecover, agentHealthStateRecovered},
	}
	for _, tt := range tests {
		if got := agentHealthEventState(tt.eventType); got != tt.state {
			t.Fatalf("agentHealthEventState(%q) = %q, want %q", tt.eventType, got, tt.state)
		}
	}
}

func TestAgentHealthMissingRuntimeSummary_OfflineEmptyState(t *testing.T) {
	agent := dbAgentForHealthTest(t)
	summary := agentHealthMissingRuntimeSummary(agent)
	if summary.AgentID != uuidToString(agent.ID) {
		t.Fatalf("summary agent_id = %q, want %q", summary.AgentID, uuidToString(agent.ID))
	}
	if summary.State != agentHealthStateOffline || summary.ReasonCode != "runtime_missing" {
		t.Fatalf("missing runtime summary = %+v", summary)
	}
	if summary.RuntimeID != nil {
		t.Fatalf("missing runtime should return null runtime_id, got %#v", summary.RuntimeID)
	}
}

func createAgentHealthFixture(t *testing.T, status string, lastSeen, updatedAt time.Time) (agentID, runtimeID string) {
	t.Helper()
	if err := testPool.QueryRow(context.Background(), `
		INSERT INTO agent_runtime (
			workspace_id, daemon_id, name, runtime_mode, provider,
			status, device_info, metadata, last_seen_at, updated_at
		)
		VALUES ($1, NULL, $2, 'cloud', 'health-test',
			$3, '', '{}'::jsonb, $4, $5)
		RETURNING id
	`, testWorkspaceID, "health-runtime-"+randomID(), status, lastSeen, updatedAt).Scan(&runtimeID); err != nil {
		t.Fatalf("create health runtime: %v", err)
	}
	if err := testPool.QueryRow(context.Background(), `
		INSERT INTO agent (
			workspace_id, name, description, runtime_mode, runtime_config,
			runtime_id, visibility, max_concurrent_tasks, owner_id,
			instructions, custom_env, custom_args, mcp_config
		)
		VALUES ($1, $2, '', 'cloud', '{}'::jsonb, $3, 'workspace', 1, $4, '', '{}'::jsonb, '[]'::jsonb, '{}'::jsonb)
		RETURNING id
	`, testWorkspaceID, "health-agent-"+randomID(), runtimeID, testUserID).Scan(&agentID); err != nil {
		t.Fatalf("create health agent: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM agent_activity_event WHERE agent_id = $1`, agentID)
		testPool.Exec(context.Background(), `DELETE FROM agent WHERE id = $1`, agentID)
		testPool.Exec(context.Background(), `DELETE FROM agent_runtime WHERE id = $1`, runtimeID)
	})
	return agentID, runtimeID
}

func createAgentHealthEvent(t *testing.T, agentID, runtimeID, eventType, state, reasonCode string) string {
	t.Helper()
	var eventID string
	details := map[string]any{"state_after": state}
	raw, err := json.Marshal(details)
	if err != nil {
		t.Fatalf("marshal event details: %v", err)
	}
	if err := testPool.QueryRow(context.Background(), `
		INSERT INTO agent_activity_event (
			workspace_id, agent_id, runtime_id, event_kind, event_type, severity,
			target_kind, target_id, reason_code, message, details
		)
		VALUES ($1, $2, $3, 'lifecycle', $4, 'info', 'agent', $2, $5, 'health event', $6::jsonb)
		RETURNING id
	`, testWorkspaceID, agentID, runtimeID, eventType, reasonCode, string(raw)).Scan(&eventID); err != nil {
		t.Fatalf("create health event: %v", err)
	}
	return eventID
}

func dbAgentForHealthTest(t *testing.T) db.Agent {
	t.Helper()
	var agent db.Agent
	agent.ID = parseUUID("11111111-1111-1111-1111-111111111111")
	return agent
}
