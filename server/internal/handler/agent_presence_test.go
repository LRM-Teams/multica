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
	h.runnerActivityCursor = newRunnerActivityCursorStore()
	h.RunnerPresenceSource = fakeRunnerPresenceSource{current: map[string]bool{
		"daemon-1/" + testWorkspaceID + "/instance-1": true,
	}}
	h.observations().putStatus(testWorkspaceID, "daemon-1", "instance-1", activeID, runtimeID, "launch-active", protocol.AgentStatusActive)
	h.observations().putStatus(testWorkspaceID, "daemon-1", "instance-1", acceptedID, runtimeID, "launch-accepted", "accepted")
	h.observations().putStatus(testWorkspaceID, "daemon-1", "instance-1", inactiveID, runtimeID, "launch-inactive", protocol.AgentStatusInactive)
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
	h.runnerActivityCursor = newRunnerActivityCursorStore()
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

func TestRunnerStartAcknowledgementAndSessionPersistOneFencedLaunch(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	agentID := createHandlerTestAgent(t, "runner-launch-"+uuid.NewString()[:8], nil)
	h := *testHandler
	h.runnerObservations = newRunnerObservationStore()
	h.runnerActivityCursor = newRunnerActivityCursorStore()
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
	var launchID, startDispatchID string
	if err := testPool.QueryRow(ctx, `SELECT launch_id::text, start_dispatch_id::text FROM agent_runner_launch_projection WHERE agent_id = $1`, parseUUID(agentID)).Scan(&launchID, &startDispatchID); err != nil {
		t.Fatalf("load desired launch: %v", err)
	}
	start := protocol.AgentStartAckPayload{
		AgentID: agentID, LaunchID: launchID, StartDispatchID: startDispatchID, QueueState: protocol.AgentStartQueueQueued, QueueDepth: 2, QueueAgeMS: 15,
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
	session := protocol.AgentSessionPayload{AgentID: agentID, LaunchID: launchID, ProviderSessionID: "provider-session-1", TurnID: "turn-1", RuntimeGeneration: 3}
	raw, err = json.Marshal(session)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.HandleWorkspaceDaemonFrame(ctx, identity, "instance-1", protocol.EventAgentSession, raw); err != nil {
		t.Fatalf("persist Runner session: %v", err)
	}
	obs, ok := h.observations().get(testWorkspaceID, agentID)
	if !ok || obs.status != "accepted" || obs.launchID != launchID || obs.sessionID != "provider-session-1" {
		t.Fatalf("observed Runner launch=%+v ok=%v", obs, ok)
	}
	var persistedSessionID string
	if err := testPool.QueryRow(ctx, `
		SELECT provider_session_id FROM agent_runner_launch_projection WHERE agent_id = $1
	`, agentID).Scan(&persistedSessionID); err != nil || persistedSessionID != session.ProviderSessionID {
		t.Fatalf("desired launch provider session = %q, %v", persistedSessionID, err)
	}
	active, err := json.Marshal(protocol.AgentStatusPayload{AgentID: agentID, LaunchID: launchID, Status: protocol.AgentStatusActive})
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
	stale.LaunchID, stale.ProviderSessionID = "launch-stale", "provider-session-stale"
	raw, _ = json.Marshal(stale)
	if err := h.HandleWorkspaceDaemonFrame(ctx, identity, "instance-1", protocol.EventAgentSession, raw); err == nil {
		t.Fatal("stale Runner session was accepted")
	}
	staleStatus := protocol.AgentStatusPayload{AgentID: agentID, LaunchID: "launch-stale", Status: protocol.AgentStatusActive}
	raw, _ = json.Marshal(staleStatus)
	if err := h.HandleWorkspaceDaemonFrame(ctx, identity, "instance-1", protocol.EventAgentStatus, raw); err == nil {
		t.Fatal("stale Runner launch status was accepted")
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
	h.runnerActivityCursor = newRunnerActivityCursorStore()
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
	var launchID, startDispatchID string
	if err := testPool.QueryRow(ctx, `SELECT launch_id::text, start_dispatch_id::text FROM agent_runner_launch_projection WHERE agent_id = $1`, parseUUID(agentID)).Scan(&launchID, &startDispatchID); err != nil {
		t.Fatalf("load desired launch: %v", err)
	}
	start := protocol.AgentStartAckPayload{
		AgentID: agentID, LaunchID: launchID, StartDispatchID: startDispatchID,
		QueueState: protocol.AgentStartQueueStarting, QueueDepth: 1, QueueAgeMS: 5,
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

	active, err := json.Marshal(protocol.AgentStatusPayload{AgentID: agentID, LaunchID: launchID, Status: protocol.AgentStatusActive})
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

func TestPendingRunnerLaunchDispatchUsesDesiredLaunchID(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	agentID, runtimeID := createHandlerTestAgent(t, "runner-dispatch-"+uuid.NewString()[:8], nil), handlerTestRuntimeID(t)
	if _, err := testPool.Exec(ctx, `UPDATE agent_runtime SET daemon_id = 'daemon-1' WHERE id = $1`, runtimeID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `UPDATE agent_runtime SET daemon_id = NULL WHERE id = $1`, runtimeID)
	})
	var launchID string
	if err := testPool.QueryRow(ctx, `SELECT launch_id::text FROM agent_runner_launch_projection WHERE agent_id = $1`, agentID).Scan(&launchID); err != nil {
		t.Fatal(err)
	}
	hub := daemonws.NewHub()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hub.HandleWebSocket(w, r, daemonws.ClientIdentity{DaemonID: "daemon-1", WorkspaceID: testWorkspaceID, RuntimeIDs: []string{runtimeID}})
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
	for hub.WorkspaceDaemonConnectionCount("daemon-1", testWorkspaceID) != 1 {
		if time.Now().After(deadline) {
			t.Fatal("Runner did not become ready")
		}
		time.Sleep(time.Millisecond)
	}
	h := *testHandler
	h.DaemonHub = hub
	identity := daemonws.ClientIdentity{DaemonID: "daemon-1", WorkspaceID: testWorkspaceID, RuntimeIDs: []string{runtimeID}}
	if err := h.dispatchPendingRunnerLaunches(ctx, identity); err != nil {
		t.Fatalf("dispatch pending Runner launch: %v", err)
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
		if start.RuntimeID != runtimeID || start.LaunchID != launchID {
			t.Fatalf("Runner launch payload=%+v", start)
		}
		break
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
	var launchID string
	if err := testPool.QueryRow(ctx, `
		SELECT launch_id::text
		FROM agent_runner_launch_projection
		WHERE workspace_id = $1 AND agent_id = $2 AND runtime_id = $3
	`, testWorkspaceID, agentID, runtimeID).Scan(&launchID); err != nil {
		t.Fatal(err)
	}
	operationID := uuid.NewString()
	h := *testHandler
	h.agentRestarts = newAgentRestartStore()
	if _, ok := h.restarts().begin(activeAgentRestartState{
		operationID:  operationID,
		workspaceID:  testWorkspaceID,
		agentID:      agentID,
		runtimeID:    runtimeID,
		computerID:   daemonID,
		storageKind:  agentRestartStorageRestart,
		step:         agentRestartStepStopping,
		stopLaunchID: launchID,
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
	h.runnerActivityCursor = newRunnerActivityCursorStore()
	h.DaemonHub = hub
	h.observations().putStatus(testWorkspaceID, daemonID, "instance-1", agentID, runtimeID, launchID, protocol.AgentStatusActive)
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
	h.runnerActivityCursor = newRunnerActivityCursorStore()
	h.Bus = bus
	h.observations().putStatus(testWorkspaceID, "daemon-1", "replacement", agentID, runtimeID, "launch-new", protocol.AgentStatusActive)
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
