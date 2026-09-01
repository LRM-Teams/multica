package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/multica-ai/multica/server/internal/daemonws"
	"github.com/multica-ai/multica/server/internal/events"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

type fakeRunnerPresenceSource struct {
	current map[string]bool
}

func (f fakeRunnerPresenceSource) IsCurrentWorkspaceDaemon(daemonID, workspaceID, daemonInstanceID string) bool {
	return f.current[daemonID+"/"+workspaceID+"/"+daemonInstanceID]
}

func TestProjectRunnerLaunchPresenceIsOnlineOnlyWhenActive(t *testing.T) {
	h := Handler{RunnerPresenceSource: fakeRunnerPresenceSource{current: map[string]bool{
		"computer-1/workspace-1/instance-1": true,
	}}}
	cases := []struct {
		name   string
		launch *runnerLaunchPresence
		want   string
	}{
		{name: "nil launch", want: AgentPresenceOffline},
		{
			name: "accepted current Runner",
			launch: &runnerLaunchPresence{
				daemonID: "computer-1", daemonInstanceID: "instance-1", status: "accepted",
			},
			want: AgentPresenceOffline,
		},
		{
			name: "inactive current Runner",
			launch: &runnerLaunchPresence{
				daemonID: "computer-1", daemonInstanceID: "instance-1", status: protocol.AgentStatusInactive,
			},
			want: AgentPresenceOffline,
		},
		{
			name: "active current Runner",
			launch: &runnerLaunchPresence{
				daemonID: "computer-1", daemonInstanceID: "instance-1", status: protocol.AgentStatusActive,
			},
			want: AgentPresenceOnline,
		},
		{
			name: "active stale Runner",
			launch: &runnerLaunchPresence{
				daemonID: "computer-1", daemonInstanceID: "other-instance", status: protocol.AgentStatusActive,
			},
			want: AgentPresenceOffline,
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			got := h.projectRunnerLaunchPresence("workspace-1", test.launch)
			if got != test.want {
				t.Fatalf("Presence = %q, want %q", got, test.want)
			}
		})
	}
}

func TestGetAgentPresenceReturnsFullWorkspaceRosterFromRunnerManagementTruth(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	activeID := createHandlerTestAgent(t, "presence-active-"+uuid.NewString()[:8], nil)
	acceptedID := createHandlerTestAgent(t, "presence-accepted-"+uuid.NewString()[:8], nil)
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
	if _, err := testPool.Exec(ctx, `UPDATE agent SET archived_at = now() WHERE id = $1`, archivedID); err != nil {
		t.Fatal(err)
	}

	h := *testHandler
	h.runnerObservations = newRunnerObservationStore()
	h.RunnerPresenceSource = fakeRunnerPresenceSource{current: map[string]bool{
		"daemon-1/" + testWorkspaceID + "/instance-1": true,
	}}
	h.observations().putStatus(testWorkspaceID, "daemon-1", "instance-1", activeID, runtimeID, protocol.AgentStatusActive)
	h.observations().putStatus(testWorkspaceID, "daemon-1", "instance-1", acceptedID, runtimeID, "accepted")
	h.observations().putStatus(testWorkspaceID, "daemon-1", "instance-1", inactiveID, runtimeID, protocol.AgentStatusInactive)
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
	if byAgent[acceptedID] != AgentPresenceOffline {
		t.Fatalf("accepted current launch = %q, want offline", byAgent[acceptedID])
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
	h.runnerObservations = newRunnerObservationStore()
	h.Bus = bus
	h.RunnerPresenceSource = fakeRunnerPresenceSource{current: map[string]bool{
		"daemon-1/" + testWorkspaceID + "/instance-1": true,
	}}
	identity := daemonws.ClientIdentity{DaemonID: "daemon-1", WorkspaceID: testWorkspaceID}
	writeStatus := func(status string) {
		t.Helper()
		raw, err := json.Marshal(protocol.AgentStatusPayload{AgentID: agentID, Status: status})
		if err != nil {
			t.Fatal(err)
		}
		if err := h.HandleWorkspaceDaemonFrame(context.Background(), identity, "instance-1", protocol.EventAgentStatus, raw); err != nil {
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

func TestRunnerStartAcknowledgementAndSessionPersistForCurrentConnection(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	agentID := createHandlerTestAgent(t, "runner-launch-"+uuid.NewString()[:8], nil)
	h := *testHandler
	h.runnerObservations = newRunnerObservationStore()
	bus := events.New()
	var presencePayloads []AgentPresenceRealtimePayload
	bus.Subscribe(protocol.EventAgentPresence, func(event events.Event) {
		presencePayloads = append(presencePayloads, event.Payload.(AgentPresenceRealtimePayload))
	})
	h.Bus = bus
	h.RunnerPresenceSource = fakeRunnerPresenceSource{current: map[string]bool{
		"daemon-1/" + testWorkspaceID + "/instance-1": true,
	}}
	identity := daemonws.ClientIdentity{DaemonID: "daemon-1", WorkspaceID: testWorkspaceID}
	start := protocol.AgentStartAckPayload{
		AgentID: agentID, QueueState: protocol.AgentStartQueueQueued, QueueDepth: 2, QueueAgeMS: 15,
	}
	raw, err := json.Marshal(start)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.HandleWorkspaceDaemonFrame(ctx, identity, "instance-1", protocol.EventAgentStartAck, raw); err != nil {
		t.Fatalf("persist Runner start acknowledgement: %v", err)
	}
	if len(presencePayloads) != 0 {
		t.Fatalf("start acknowledgement Presence payloads = %+v, want none (ACK is not Online)", presencePayloads)
	}
	session := protocol.AgentSessionPayload{AgentID: agentID, ProviderSessionID: "provider-session-1", TurnID: "turn-1", RuntimeGeneration: 3}
	raw, err = json.Marshal(session)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.HandleWorkspaceDaemonFrame(ctx, identity, "instance-1", protocol.EventAgentSession, raw); err != nil {
		t.Fatalf("persist Runner session: %v", err)
	}
	obs, ok := h.observations().get(testWorkspaceID, agentID)
	if !ok || obs.status != "accepted" || obs.sessionID != "provider-session-1" {
		t.Fatalf("observed Runner launch=%+v ok=%v", obs, ok)
	}
	var persistedSessionID string
	if err := testPool.QueryRow(ctx, `
		SELECT provider_session_id FROM agent WHERE id = $1
	`, agentID).Scan(&persistedSessionID); err != nil || persistedSessionID != session.ProviderSessionID {
		t.Fatalf("Agent provider session = %q, %v", persistedSessionID, err)
	}
	active, err := json.Marshal(protocol.AgentStatusPayload{AgentID: agentID, Status: protocol.AgentStatusActive})
	if err != nil {
		t.Fatal(err)
	}
	if err := h.HandleWorkspaceDaemonFrame(ctx, identity, "instance-1", protocol.EventAgentStatus, active); err != nil {
		t.Fatalf("persist Runner active status: %v", err)
	}
	if len(presencePayloads) != 1 || presencePayloads[0].AgentID != agentID || presencePayloads[0].Presence != AgentPresenceOnline {
		t.Fatalf("active Presence payloads = %+v, want one online transition", presencePayloads)
	}
	stale := session
	stale.ProviderSessionID = "provider-session-stale"
	raw, _ = json.Marshal(stale)
	if err := h.HandleWorkspaceDaemonFrame(ctx, identity, "stale-instance", protocol.EventAgentSession, raw); err == nil {
		t.Fatal("session from stale daemon instance was accepted")
	}
}

func TestRunnerStartAcknowledgementPresenceStaysOfflineUntilActive(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	agentID := createHandlerTestAgent(t, "runner-ack-presence-"+uuid.NewString()[:8], nil)
	h := *testHandler
	h.runnerObservations = newRunnerObservationStore()
	bus := events.New()
	var presencePayloads []AgentPresenceRealtimePayload
	bus.Subscribe(protocol.EventAgentPresence, func(event events.Event) {
		presencePayloads = append(presencePayloads, event.Payload.(AgentPresenceRealtimePayload))
	})
	h.Bus = bus
	h.RunnerPresenceSource = fakeRunnerPresenceSource{current: map[string]bool{
		"daemon-1/" + testWorkspaceID + "/instance-1": true,
	}}
	identity := daemonws.ClientIdentity{DaemonID: "daemon-1", WorkspaceID: testWorkspaceID}
	start := protocol.AgentStartAckPayload{
		AgentID: agentID, QueueState: protocol.AgentStartQueueStarting, QueueDepth: 1, QueueAgeMS: 5,
	}
	raw, err := json.Marshal(start)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.HandleWorkspaceDaemonFrame(ctx, identity, "instance-1", protocol.EventAgentStartAck, raw); err != nil {
		t.Fatalf("persist Runner start acknowledgement: %v", err)
	}
	if len(presencePayloads) != 0 {
		t.Fatalf("ACK Presence payloads = %+v, want none", presencePayloads)
	}
	if got := agentPresenceFromHTTP(t, h, agentID); got != AgentPresenceOffline {
		t.Fatalf("Presence after ACK = %q, want offline", got)
	}

	active, err := json.Marshal(protocol.AgentStatusPayload{AgentID: agentID, Status: protocol.AgentStatusActive})
	if err != nil {
		t.Fatal(err)
	}
	if err := h.HandleWorkspaceDaemonFrame(ctx, identity, "instance-1", protocol.EventAgentStatus, active); err != nil {
		t.Fatalf("persist Runner active status: %v", err)
	}
	if len(presencePayloads) != 1 || presencePayloads[0].AgentID != agentID || presencePayloads[0].Presence != AgentPresenceOnline {
		t.Fatalf("active Presence payloads = %+v, want one online", presencePayloads)
	}
	if got := agentPresenceFromHTTP(t, h, agentID); got != AgentPresenceOnline {
		t.Fatalf("Presence after active = %q, want online", got)
	}
}

func agentPresenceFromHTTP(t *testing.T, h Handler, agentID string) string {
	t.Helper()
	rec := httptest.NewRecorder()
	req := newRequestAs(testUserID, http.MethodGet, "/api/agents/presence", nil)
	req.Header.Set("X-Workspace-ID", testWorkspaceID)
	h.GetAgentPresence(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GetAgentPresence status=%d body=%s", rec.Code, rec.Body.String())
	}
	var response AgentPresenceResponse
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatal(err)
	}
	for _, item := range response.Items {
		if item.AgentID == agentID {
			return item.Presence
		}
	}
	t.Fatalf("GetAgentPresence omitted Agent %s", agentID)
	return ""
}

func TestWorkspaceDaemonReconcileUsesAgentDesiredRuntimeWithoutHeartbeatRedrive(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	daemonID := "daemon-runner-dispatch-" + uuid.NewString()[:8]
	runtimeID := seedMachineLockedRuntime(t, daemonID, "runner heartbeat no redrive")
	agentID := createHandlerTestAgentOnRuntime(t, "runner-dispatch-"+uuid.NewString()[:8], runtimeID)
	hub := daemonws.NewHub()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hub.HandleWebSocket(w, r, daemonws.ClientIdentity{DaemonID: daemonID, WorkspaceID: testWorkspaceID, RuntimeIDs: []string{runtimeID}})
	}))
	defer server.Close()
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	readyPayload, _ := json.Marshal(protocol.WorkspaceReadyPayload{
		WorkspaceID: testWorkspaceID, DaemonInstanceID: "instance-1", ActiveCapabilities: []string{protocol.DaemonCapabilityWorkspaceDaemonAgentProcess},
	})
	ready, _ := json.Marshal(protocol.Message{Type: protocol.EventWorkspaceDaemonReady, Payload: readyPayload})
	if err := conn.WriteMessage(websocket.TextMessage, ready); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for hub.WorkspaceDaemonConnectionCount(daemonID, testWorkspaceID) != 1 {
		if time.Now().After(deadline) {
			t.Fatal("Runner did not become ready")
		}
		time.Sleep(time.Millisecond)
	}
	h := *testHandler
	h.DaemonHub = hub
	identity := daemonws.ClientIdentity{DaemonID: daemonID, WorkspaceID: testWorkspaceID, RuntimeIDs: []string{runtimeID}}
	if err := h.reconcileWorkspaceDaemonLaunches(ctx, identity); err != nil {
		t.Fatalf("reconcile WorkspaceDaemon launch: %v", err)
	}
	conn.SetReadDeadline(time.Now().Add(time.Second))
	for {
		_, raw, err := conn.ReadMessage()
		if err != nil {
			t.Fatal(err)
		}
		var frame protocol.Message
		if err := json.Unmarshal(raw, &frame); err != nil || frame.Type != protocol.EventDaemonAgentStart {
			t.Fatalf("Runner launch frame=%+v err=%v", frame, err)
		}
		var start protocol.AgentStartPayload
		if err := json.Unmarshal(frame.Payload, &start); err != nil {
			t.Fatal(err)
		}
		if start.AgentID != agentID {
			continue
		}
		if start.RuntimeID != runtimeID {
			t.Fatalf("Runner launch payload=%+v", start)
		}
		break
	}

	if _, err := h.HandleDaemonWSHeartbeat(ctx, identity, protocol.DaemonHeartbeatRequestPayload{RuntimeID: runtimeID}); err != nil {
		t.Fatalf("handle WorkspaceDaemon heartbeat: %v", err)
	}
	if err := conn.SetReadDeadline(time.Now().Add(100 * time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := conn.ReadMessage(); err == nil {
		t.Fatal("Runtime heartbeat re-drove an already-dispatched WorkspaceDaemon start")
	}
}

func TestPendingAgentLifecycleOperationDoesNotDispatchParallelRunnerStop(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	daemonID := "daemon-runner-lifecycle-" + uuid.NewString()[:8]
	runtimeID := seedMachineLockedRuntime(t, daemonID, "runner lifecycle")
	agentID := createHandlerTestAgentOnRuntime(t, "runner-lifecycle-"+uuid.NewString()[:8], runtimeID)
	operationID := uuid.NewString()
	h := *testHandler
	h.agentRestarts = newAgentRestartStore()
	if _, ok := h.restarts().begin(activeAgentRestartState{
		operationID: operationID,
		workspaceID: testWorkspaceID,
		agentID:     agentID,
		runtimeID:   runtimeID,
		computerID:  daemonID,
		storageKind: agentRestartStorageRestart,
		step:        agentRestartStepStopping,
	}); !ok {
		t.Fatal("seed in-flight restart")
	}
	hub := daemonws.NewHub()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hub.HandleWebSocket(w, r, daemonws.ClientIdentity{DaemonID: daemonID, WorkspaceID: testWorkspaceID, RuntimeIDs: []string{runtimeID}})
	}))
	defer server.Close()
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	readyPayload, _ := json.Marshal(protocol.WorkspaceReadyPayload{
		WorkspaceID: testWorkspaceID, DaemonInstanceID: "instance-1", ActiveCapabilities: []string{protocol.DaemonCapabilityWorkspaceDaemonAgentProcess},
	})
	ready, _ := json.Marshal(protocol.Message{Type: protocol.EventWorkspaceDaemonReady, Payload: readyPayload})
	if err := conn.WriteMessage(websocket.TextMessage, ready); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for hub.WorkspaceDaemonConnectionCount(daemonID, testWorkspaceID) != 1 ||
		!hub.WorkspaceDaemonSupportsCapability(daemonID, testWorkspaceID, protocol.DaemonCapabilityWorkspaceDaemonAgentProcess) {
		if time.Now().After(deadline) {
			t.Fatal("Runner did not become ready")
		}
		time.Sleep(time.Millisecond)
	}
	h.runnerObservations = newRunnerObservationStore()
	h.DaemonHub = hub
	h.observations().putStatus(testWorkspaceID, daemonID, "instance-1", agentID, runtimeID, protocol.AgentStatusActive)
	identity := daemonws.ClientIdentity{DaemonID: daemonID, WorkspaceID: testWorkspaceID, RuntimeIDs: []string{runtimeID}}
	if err := h.reconcileWorkspaceDaemonLaunches(ctx, identity); err != nil {
		t.Fatalf("reconcile while lifecycle stop is active: %v", err)
	}
	conn.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
	if _, raw, err := conn.ReadMessage(); err == nil {
		var frame protocol.Message
		_ = json.Unmarshal(raw, &frame)
		t.Fatalf("lifecycle operation dispatched a parallel Runner frame: %+v", frame)
	}
}

func TestRunnerDisconnectFencesExactInstanceAndPublishesOnce(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	agentID := createHandlerTestAgent(t, "presence-disconnect-"+uuid.NewString()[:8], nil)
	runtimeID := handlerTestRuntimeID(t)

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
	h.runnerObservations = newRunnerObservationStore()
	h.Bus = bus
	h.observations().putStatus(testWorkspaceID, "daemon-1", "replacement", agentID, runtimeID, protocol.AgentStatusActive)
	identity := daemonws.ClientIdentity{DaemonID: "daemon-1", WorkspaceID: testWorkspaceID}

	if err := h.HandleWorkspaceDaemonDisconnect(ctx, identity, "old-instance"); err != nil {
		t.Fatal(err)
	}
	obs, ok := h.observations().get(testWorkspaceID, agentID)
	if !ok || obs.status != protocol.AgentStatusActive || obs.daemonInstanceID != "replacement" {
		t.Fatalf("late old disconnect changed replacement observation=%+v ok=%v", obs, ok)
	}

	if err := h.HandleWorkspaceDaemonDisconnect(ctx, identity, "replacement"); err != nil {
		t.Fatal(err)
	}
	if err := h.HandleWorkspaceDaemonDisconnect(ctx, identity, "replacement"); err != nil {
		t.Fatal(err)
	}
	if _, ok := h.observations().get(testWorkspaceID, agentID); ok {
		t.Fatal("current disconnect left a live observation")
	}

	mu.Lock()
	defer mu.Unlock()
	if len(payloads) != 1 || payloads[0].AgentID != agentID || payloads[0].Presence != AgentPresenceOffline {
		t.Fatalf("disconnect Presence payloads=%+v, want one exact Offline", payloads)
	}
}

func TestWorkspaceDaemonReadyAndDisconnectPublishComputerStatus(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	daemonID := uuid.NewString()
	daemonInstanceID := "instance-1"
	identity := daemonws.ClientIdentity{DaemonID: daemonID, WorkspaceID: testWorkspaceID}
	bus := events.New()
	var payloads []map[string]any
	bus.Subscribe(protocol.EventComputerStatus, func(event events.Event) {
		payload, ok := event.Payload.(map[string]any)
		if !ok {
			t.Errorf("payload type = %T", event.Payload)
			return
		}
		payloads = append(payloads, payload)
	})
	h := *testHandler
	h.Bus = bus
	h.DaemonHub = daemonws.NewHub()
	h.runnerObservations = newRunnerObservationStore()
	h.RunnerPresenceSource = fakeRunnerPresenceSource{current: map[string]bool{
		daemonID + "/" + testWorkspaceID + "/" + daemonInstanceID: true,
	}}
	ready, err := json.Marshal(protocol.WorkspaceReadyPayload{
		WorkspaceID:      testWorkspaceID,
		DaemonInstanceID: daemonInstanceID,
	})
	if err != nil {
		t.Fatal(err)
	}

	if err := h.HandleWorkspaceDaemonFrame(ctx, identity, daemonInstanceID, protocol.EventWorkspaceDaemonReady, ready); err != nil {
		t.Fatal(err)
	}
	if err := h.HandleWorkspaceDaemonDisconnect(ctx, identity, daemonInstanceID); err != nil {
		t.Fatal(err)
	}
	if len(payloads) != 2 {
		t.Fatalf("Computer status payloads=%+v, want Ready and disconnect events", payloads)
	}
	for index, payload := range payloads {
		if payload["computer_id"] != daemonID {
			t.Fatalf("Computer status payload=%+v, want computer_id=%s", payload, daemonID)
		}
		wantStatus := "disconnected"
		if index == 0 {
			wantStatus = "connected"
		}
		if payload["status"] != wantStatus {
			t.Fatalf("Computer status payload[%d]=%+v, want status=%q", index, payload, wantStatus)
		}
		changedAt, ok := payload["changed_at"].(string)
		if !ok {
			t.Fatalf("Computer status payload[%d]=%+v, want changed_at", index, payload)
		}
		if _, err := time.Parse(time.RFC3339Nano, changedAt); err != nil {
			t.Fatalf("Computer status changed_at=%q: %v", changedAt, err)
		}
	}
}
