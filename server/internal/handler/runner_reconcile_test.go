package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/multica-ai/multica/server/internal/daemonws"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestReduceRunnerLaunchesConvergesRaftDesiredAndRunningState(t *testing.T) {
	tests := []struct {
		name     string
		desired  []runnerDesiredLaunch
		observed []runnerObservedLaunch
		want     []runnerReconcileAction
	}{
		{name: "first setup starts missing agent", desired: []runnerDesiredLaunch{{agentID: "agent-a", runtimeID: "runtime-new", launchID: "launch-new", startDispatchID: "dispatch-new"}}, want: []runnerReconcileAction{{eventType: protocol.EventDaemonAgentStart, payload: protocol.WorkspaceRunnerAgentStartPayload{AgentID: "agent-a", RuntimeID: "runtime-new", LaunchID: "launch-new", StartDispatchID: "dispatch-new"}}}},
		{name: "matching reconnect is no-op", desired: []runnerDesiredLaunch{{agentID: "agent-a", runtimeID: "runtime-new", launchID: "launch-new"}}, observed: []runnerObservedLaunch{{agentID: "agent-a", runtimeID: "runtime-new", launchID: "launch-new", status: protocol.AgentStatusActive}}},
		{name: "accepted start is not residency", desired: []runnerDesiredLaunch{{agentID: "agent-a", runtimeID: "runtime-new", launchID: "launch-new", startDispatchID: "dispatch-new"}}, observed: []runnerObservedLaunch{{agentID: "agent-a", runtimeID: "runtime-new", launchID: "launch-new", status: "accepted"}}, want: []runnerReconcileAction{{eventType: protocol.EventDaemonAgentStart, payload: protocol.WorkspaceRunnerAgentStartPayload{AgentID: "agent-a", RuntimeID: "runtime-new", LaunchID: "launch-new", StartDispatchID: "dispatch-new"}}}},
		{name: "runtime move stops old before a later reconcile starts new", desired: []runnerDesiredLaunch{{agentID: "agent-a", runtimeID: "runtime-new", launchID: "launch-new", startDispatchID: "dispatch-new"}}, observed: []runnerObservedLaunch{{agentID: "agent-a", runtimeID: "runtime-old", launchID: "launch-old", status: protocol.AgentStatusActive}}, want: []runnerReconcileAction{
			{eventType: protocol.EventDaemonAgentStop, payload: protocol.WorkspaceRunnerAgentStopPayload{AgentID: "agent-a", LaunchID: "launch-old"}},
		}},
		{name: "removed agent stops stale residency", observed: []runnerObservedLaunch{{agentID: "agent-a", runtimeID: "runtime-old", launchID: "launch-old", status: protocol.AgentStatusActive}}, want: []runnerReconcileAction{{eventType: protocol.EventDaemonAgentStop, payload: protocol.WorkspaceRunnerAgentStopPayload{AgentID: "agent-a", LaunchID: "launch-old"}}}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := reduceRunnerLaunches(tc.desired, tc.observed); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("actions = %#v, want %#v", got, tc.want)
			}
		})
	}
}

func TestRunnerLaunchProjectionRotatesOnlyOnPlacementEpoch(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	firstRuntimeID := seedMachineLockedRuntime(t, "computer-projection", "projection-first")
	secondRuntimeID := seedMachineLockedRuntime(t, "computer-projection", "projection-second")
	agentID := createHandlerTestAgentOnRuntime(t, "runner-projection", firstRuntimeID)
	read := func() (string, string, bool) {
		var launchID, dispatchID string
		err := testPool.QueryRow(ctx, `SELECT launch_id::text, start_dispatch_id::text FROM agent_runner_launch_projection WHERE agent_id = $1`, agentID).Scan(&launchID, &dispatchID)
		return launchID, dispatchID, err == nil
	}
	firstLaunch, firstDispatch, found := read()
	if !found || firstLaunch == "" || firstDispatch == "" || firstLaunch == firstDispatch {
		t.Fatalf("initial projection launch=%q dispatch=%q found=%v", firstLaunch, firstDispatch, found)
	}
	if _, err := testPool.Exec(ctx, `UPDATE agent SET description = description || ' unchanged-placement' WHERE id = $1`, agentID); err != nil {
		t.Fatal(err)
	}
	stableLaunch, stableDispatch, _ := read()
	if stableLaunch != firstLaunch || stableDispatch != firstDispatch {
		t.Fatalf("ordinary update rotated projection: (%s,%s) -> (%s,%s)", firstLaunch, firstDispatch, stableLaunch, stableDispatch)
	}
	if _, err := testPool.Exec(ctx, `UPDATE agent SET runtime_id = $2 WHERE id = $1`, agentID, secondRuntimeID); err != nil {
		t.Fatal(err)
	}
	secondLaunch, secondDispatch, found := read()
	if !found || secondLaunch == firstLaunch || secondDispatch == firstDispatch || secondLaunch == secondDispatch {
		t.Fatalf("runtime move projection launch=%q dispatch=%q", secondLaunch, secondDispatch)
	}
	if _, err := testPool.Exec(ctx, `UPDATE agent SET archived_at = now() WHERE id = $1`, agentID); err != nil {
		t.Fatal(err)
	}
	if _, _, found := read(); found {
		t.Fatal("archived Agent retained desired launch projection")
	}
}

func TestReconcileConnectedRuntimesDeduplicatesSameComputerMove(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	oldRuntimeID := seedMachineLockedRuntime(t, "daemon-same", "runner-old")
	newRuntimeID := seedMachineLockedRuntime(t, "daemon-same", "runner-new")
	agentID := createHandlerTestAgentOnRuntime(t, "runner-move-dedup", oldRuntimeID)
	var oldLaunchID string
	if err := testPool.QueryRow(ctx, `SELECT launch_id::text FROM agent_runner_launch_projection WHERE agent_id = $1`, agentID).Scan(&oldLaunchID); err != nil {
		t.Fatal(err)
	}
	if _, err := testPool.Exec(ctx, `UPDATE agent SET runtime_id = $2 WHERE id = $1`, agentID, newRuntimeID); err != nil {
		t.Fatal(err)
	}
	var newLaunchID string
	if err := testPool.QueryRow(ctx, `SELECT launch_id::text FROM agent_runner_launch_projection WHERE agent_id = $1`, agentID).Scan(&newLaunchID); err != nil {
		t.Fatal(err)
	}
	if _, err := testPool.Exec(ctx, `
		INSERT INTO agent_activity_launch (
			workspace_id, agent_id, runtime_id, daemon_id, daemon_instance_id, launch_id, status
		) VALUES ($1, $2, $3, 'daemon-same', 'instance-same', $4, 'active')`, testWorkspaceID, agentID, oldRuntimeID, oldLaunchID); err != nil {
		t.Fatal(err)
	}

	hub := daemonws.NewHub()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hub.HandleWebSocket(w, r, daemonws.ClientIdentity{DaemonID: "daemon-same", WorkspaceID: testWorkspaceID})
	}))
	defer server.Close()
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	readyPayload, _ := json.Marshal(protocol.WorkspaceRunnerReadyPayload{
		WorkspaceID: testWorkspaceID, DaemonInstanceID: "instance-same", ActiveCapabilities: []string{protocol.DaemonCapabilityWorkspaceRunnerAttachment},
	})
	ready, _ := json.Marshal(protocol.Message{Type: protocol.EventWorkspaceRunnerReady, Payload: readyPayload})
	if err := conn.WriteMessage(websocket.TextMessage, ready); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for hub.WorkspaceRunnerConnectionCount("daemon-same", testWorkspaceID) != 1 {
		if time.Now().After(deadline) {
			t.Fatal("Runner did not become ready")
		}
		time.Sleep(time.Millisecond)
	}

	h := *testHandler
	h.DaemonHub = hub
	h.RunnerPresenceSource = hub
	h.reconcileConnectedRuntimes(ctx, testWorkspaceID, parseUUID(oldRuntimeID), parseUUID(newRuntimeID))
	want := []protocol.Message{{Type: protocol.EventDaemonAgentStop}}
	for i := range want {
		if err := conn.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
			t.Fatal(err)
		}
		_, raw, err := conn.ReadMessage()
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(raw, &want[i]); err != nil {
			t.Fatal(err)
		}
	}
	if want[0].Type != protocol.EventDaemonAgentStop {
		t.Fatalf("reconcile frame = %q", want[0].Type)
	}
	var stop protocol.WorkspaceRunnerAgentStopPayload
	if err := json.Unmarshal(want[0].Payload, &stop); err != nil {
		t.Fatal(err)
	}
	if stop.AgentID != agentID || stop.LaunchID != oldLaunchID {
		t.Fatalf("stop payload = %+v", stop)
	}
	inactive, _ := json.Marshal(protocol.AgentStatusPayload{AgentID: agentID, LaunchID: oldLaunchID, Status: protocol.AgentStatusInactive})
	if err := h.HandleWorkspaceRunnerFrame(ctx, daemonws.ClientIdentity{DaemonID: "daemon-same", WorkspaceID: testWorkspaceID}, "instance-same", protocol.EventAgentStatus, inactive); err != nil {
		t.Fatalf("record matching inactive: %v", err)
	}
	if err := conn.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	_, raw, err := conn.ReadMessage()
	if err != nil {
		t.Fatal(err)
	}
	var startMessage protocol.Message
	if err := json.Unmarshal(raw, &startMessage); err != nil {
		t.Fatal(err)
	}
	var start protocol.WorkspaceRunnerAgentStartPayload
	if startMessage.Type != protocol.EventDaemonAgentStart || json.Unmarshal(startMessage.Payload, &start) != nil {
		t.Fatalf("post-inactive frame = %+v", startMessage)
	}
	if start.AgentID != agentID || start.RuntimeID != newRuntimeID || start.LaunchID != newLaunchID || start.StartDispatchID == "" {
		t.Fatalf("replacement start payload = %+v", start)
	}
}
