package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/internal/daemonws"
	"github.com/multica-ai/multica/server/internal/events"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

type fakeRunnerPresenceSource struct {
	current map[string]bool
}

func (f fakeRunnerPresenceSource) IsCurrentWorkspaceRunner(daemonID, workspaceID, daemonInstanceID string) bool {
	return f.current[daemonID+"/"+workspaceID+"/"+daemonInstanceID]
}

func TestGetAgentPresenceReturnsFullWorkspaceRosterFromRunnerManagementTruth(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	activeID := createHandlerTestAgent(t, "presence-active-"+uuid.NewString()[:8], nil)
	inactiveID := createHandlerTestAgent(t, "presence-inactive-"+uuid.NewString()[:8], nil)
	missingID := createHandlerTestAgent(t, "presence-missing-"+uuid.NewString()[:8], nil)
	archivedID := createHandlerTestAgent(t, "presence-archived-"+uuid.NewString()[:8], nil)
	hiddenID := createHandlerTestAgent(t, "presence-hidden-"+uuid.NewString()[:8], nil)
	runtimeID := handlerTestRuntimeID(t)
	var fleetID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO research_fleet (workspace_id) VALUES ($1)
		ON CONFLICT (workspace_id) DO UPDATE SET workspace_id = EXCLUDED.workspace_id
		RETURNING id`, testWorkspaceID).Scan(&fleetID); err != nil {
		t.Fatal(err)
	}
	if _, err := testPool.Exec(ctx, `
		INSERT INTO research_fleet_member (workspace_id, fleet_id, agent_id, role, status)
		VALUES ($1, $2, $3, $4, 'active')`, testWorkspaceID, fleetID, hiddenID, "presence-"+uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM research_fleet_member WHERE agent_id = $1`, hiddenID)
	})
	if _, err := testPool.Exec(ctx, `
		INSERT INTO agent_activity_launch (workspace_id, agent_id, runtime_id, daemon_id, daemon_instance_id, launch_id, status)
		VALUES
			($1, $2, $4, 'daemon-1', 'instance-1', 'launch-active', 'active'),
			($1, $3, $4, 'daemon-1', 'instance-1', 'launch-inactive', 'inactive')`,
		testWorkspaceID, activeID, inactiveID, runtimeID); err != nil {
		t.Fatal(err)
	}
	if _, err := testPool.Exec(ctx, `UPDATE agent SET archived_at = now() WHERE id = $1`, archivedID); err != nil {
		t.Fatal(err)
	}

	h := *testHandler
	h.RunnerPresenceSource = fakeRunnerPresenceSource{current: map[string]bool{
		"daemon-1/" + testWorkspaceID + "/instance-1": true,
	}}
	rec := httptest.NewRecorder()
	req := newRequestAs(testUserID, http.MethodGet, "/api/agents/presence", nil)
	req.Header.Set("X-Workspace-ID", testWorkspaceID)
	h.GetAgentPresence(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s want 200", rec.Code, rec.Body.String())
	}
	var response AgentPresenceResponse
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatal(err)
	}
	byAgent := make(map[string]string, len(response.Items))
	for _, item := range response.Items {
		if _, duplicate := byAgent[item.AgentID]; duplicate {
			t.Fatalf("duplicate Agent Presence row for %s", item.AgentID)
		}
		byAgent[item.AgentID] = item.Presence
	}
	if byAgent[activeID] != AgentPresenceOnline {
		t.Fatalf("active current launch = %q, want online", byAgent[activeID])
	}
	if byAgent[inactiveID] != AgentPresenceOffline || byAgent[missingID] != AgentPresenceOffline {
		t.Fatalf("inactive=%q missing=%q, want offline/offline", byAgent[inactiveID], byAgent[missingID])
	}
	if _, ok := byAgent[archivedID]; ok {
		t.Fatal("archived Agent leaked into Presence roster")
	}
	if byAgent[hiddenID] != AgentPresenceOffline {
		t.Fatalf("directory-hidden Agent Presence = %q, want offline row", byAgent[hiddenID])
	}

	agentReq := newRequestAs(testUserID, http.MethodGet, "/api/agents/presence", nil)
	agentReq.Header.Set("X-Workspace-ID", testWorkspaceID)
	agentReq = withAgentPrincipal(agentReq, activeID, testWorkspaceID, testUserID)
	agentRec := httptest.NewRecorder()
	h.GetAgentPresence(agentRec, agentReq)
	if agentRec.Code != http.StatusForbidden {
		t.Fatalf("agent principal status=%d body=%s want 403", agentRec.Code, agentRec.Body.String())
	}
}

func TestGetAgentPresenceRequiresHumanWorkspaceMembership(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	unauthenticated := httptest.NewRecorder()
	req := newRequest(http.MethodGet, "/api/agents/presence", nil)
	req.Header.Del("X-User-ID")
	req.Header.Set("X-Workspace-ID", testWorkspaceID)
	testHandler.GetAgentPresence(unauthenticated, req)
	if unauthenticated.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status=%d body=%s want 401", unauthenticated.Code, unauthenticated.Body.String())
	}

	var foreignWorkspaceID string
	suffix := uuid.NewString()[:8]
	if err := testPool.QueryRow(context.Background(), `
		INSERT INTO workspace (name, slug, description, issue_prefix)
		VALUES ($1, $2, 'Presence authorization fixture', 'PRS')
		RETURNING id`, "presence-foreign-"+suffix, "presence-foreign-"+suffix).Scan(&foreignWorkspaceID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM workspace WHERE id = $1`, foreignWorkspaceID)
	})

	foreign := httptest.NewRecorder()
	foreignReq := newRequestAs(testUserID, http.MethodGet, "/api/agents/presence", nil)
	foreignReq.Header.Set("X-Workspace-ID", foreignWorkspaceID)
	testHandler.GetAgentPresence(foreign, foreignReq)
	if foreign.Code != http.StatusNotFound {
		t.Fatalf("cross-workspace status=%d body=%s want 404", foreign.Code, foreign.Body.String())
	}
}

func TestRunnerStatusPublishesPresenceOnlyOnSemanticChange(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	agentID := createHandlerTestAgent(t, "presence-events-"+uuid.NewString()[:8], nil)
	bus := events.New()
	var mu sync.Mutex
	var payloads []AgentPresenceRealtimePayload
	bus.Subscribe(protocol.EventAgentPresence, func(event events.Event) {
		payload, ok := event.Payload.(AgentPresenceRealtimePayload)
		if !ok {
			t.Errorf("payload type = %T", event.Payload)
			return
		}
		mu.Lock()
		payloads = append(payloads, payload)
		mu.Unlock()
	})
	h := *testHandler
	h.Bus = bus
	h.RunnerPresenceSource = fakeRunnerPresenceSource{current: map[string]bool{
		"daemon-1/" + testWorkspaceID + "/instance-1": true,
	}}
	identity := daemonws.ClientIdentity{DaemonID: "daemon-1", WorkspaceID: testWorkspaceID}
	writeStatus := func(status string) {
		t.Helper()
		raw, err := json.Marshal(protocol.AgentStatusPayload{AgentID: agentID, LaunchID: "launch-1", Status: status})
		if err != nil {
			t.Fatal(err)
		}
		if err := h.HandleWorkspaceRunnerFrame(context.Background(), identity, "instance-1", protocol.EventAgentStatus, raw); err != nil {
			t.Fatal(err)
		}
	}

	writeStatus(protocol.AgentStatusActive)
	writeStatus(protocol.AgentStatusActive)
	writeStatus(protocol.AgentStatusInactive)

	mu.Lock()
	defer mu.Unlock()
	if len(payloads) != 2 {
		t.Fatalf("Presence event count=%d payloads=%+v, want two semantic changes", len(payloads), payloads)
	}
	if payloads[0].AgentID != agentID || payloads[0].Presence != AgentPresenceOnline || payloads[1].Presence != AgentPresenceOffline {
		t.Fatalf("Presence payloads=%+v", payloads)
	}
}

func TestRunnerDisconnectFencesExactInstanceAndPublishesOnce(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	agentID := createHandlerTestAgent(t, "presence-disconnect-"+uuid.NewString()[:8], nil)
	runtimeID := handlerTestRuntimeID(t)
	if _, err := testPool.Exec(ctx, `
		INSERT INTO agent_activity_launch (
			workspace_id, agent_id, runtime_id, daemon_id, daemon_instance_id, launch_id, status
		) VALUES ($1, $2, $3, 'daemon-1', 'replacement', 'launch-new', 'active')`,
		testWorkspaceID, agentID, runtimeID); err != nil {
		t.Fatal(err)
	}

	bus := events.New()
	var mu sync.Mutex
	var payloads []AgentPresenceRealtimePayload
	bus.Subscribe(protocol.EventAgentPresence, func(event events.Event) {
		payload, ok := event.Payload.(AgentPresenceRealtimePayload)
		if !ok {
			t.Errorf("payload type = %T", event.Payload)
			return
		}
		mu.Lock()
		payloads = append(payloads, payload)
		mu.Unlock()
	})
	h := *testHandler
	h.Bus = bus
	identity := daemonws.ClientIdentity{DaemonID: "daemon-1", WorkspaceID: testWorkspaceID}

	if err := h.HandleWorkspaceRunnerDisconnect(ctx, identity, "old-instance"); err != nil {
		t.Fatal(err)
	}
	var status string
	if err := testPool.QueryRow(ctx, `SELECT status FROM agent_activity_launch WHERE workspace_id = $1 AND agent_id = $2`, testWorkspaceID, agentID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != protocol.AgentStatusActive {
		t.Fatalf("late old disconnect changed replacement status to %q", status)
	}

	if err := h.HandleWorkspaceRunnerDisconnect(ctx, identity, "replacement"); err != nil {
		t.Fatal(err)
	}
	if err := h.HandleWorkspaceRunnerDisconnect(ctx, identity, "replacement"); err != nil {
		t.Fatal(err)
	}
	if err := testPool.QueryRow(ctx, `SELECT status FROM agent_activity_launch WHERE workspace_id = $1 AND agent_id = $2`, testWorkspaceID, agentID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != protocol.AgentStatusInactive {
		t.Fatalf("current disconnect status=%q want inactive", status)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(payloads) != 1 || payloads[0].AgentID != agentID || payloads[0].Presence != AgentPresenceOffline {
		t.Fatalf("disconnect Presence payloads=%+v, want one exact Offline", payloads)
	}
}
