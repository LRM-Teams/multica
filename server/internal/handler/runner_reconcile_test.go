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
		{name: "first setup starts missing agent", desired: []runnerDesiredLaunch{{agentID: "agent-a", runtimeID: "runtime-new", launchID: "launch-new"}}, want: []runnerReconcileAction{{eventType: protocol.EventDaemonAgentStart, payload: protocol.WorkspaceRunnerAgentStartPayload{AgentID: "agent-a", RuntimeID: "runtime-new", LaunchID: "launch-new"}}}},
		{name: "matching reconnect is no-op", desired: []runnerDesiredLaunch{{agentID: "agent-a", runtimeID: "runtime-new", launchID: "launch-new"}}, observed: []runnerObservedLaunch{{agentID: "agent-a", runtimeID: "runtime-new", launchID: "launch-new", status: protocol.AgentStatusActive}}},
		{name: "runtime move stops old then starts new", desired: []runnerDesiredLaunch{{agentID: "agent-a", runtimeID: "runtime-new", launchID: "launch-new"}}, observed: []runnerObservedLaunch{{agentID: "agent-a", runtimeID: "runtime-old", launchID: "launch-old", status: protocol.AgentStatusActive}}, want: []runnerReconcileAction{
			{eventType: protocol.EventDaemonAgentStop, payload: protocol.WorkspaceRunnerAgentStopPayload{AgentID: "agent-a", LaunchID: "launch-old"}},
			{eventType: protocol.EventDaemonAgentStart, payload: protocol.WorkspaceRunnerAgentStartPayload{AgentID: "agent-a", RuntimeID: "runtime-new", LaunchID: "launch-new"}},
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
		hub.HandleWebSocket(w, r, daemonws.ClientIdentity{DaemonID: "daemon-same", WorkspaceID: testWorkspaceID, RuntimeIDs: []string{oldRuntimeID, newRuntimeID}})
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
	h.reconcileConnectedRuntimes(ctx, testWorkspaceID, parseUUID(oldRuntimeID), parseUUID(newRuntimeID))
	want := []protocol.Message{
		{Type: protocol.EventDaemonAgentStop},
		{Type: protocol.EventDaemonAgentStart},
	}
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
	if want[0].Type != protocol.EventDaemonAgentStop || want[1].Type != protocol.EventDaemonAgentStart {
		t.Fatalf("reconcile frames = %q, %q", want[0].Type, want[1].Type)
	}
	var stop protocol.WorkspaceRunnerAgentStopPayload
	var start protocol.WorkspaceRunnerAgentStartPayload
	if err := json.Unmarshal(want[0].Payload, &stop); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(want[1].Payload, &start); err != nil {
		t.Fatal(err)
	}
	if stop.AgentID != agentID || stop.LaunchID != oldLaunchID {
		t.Fatalf("stop payload = %+v", stop)
	}
	if start.AgentID != agentID || start.RuntimeID != newRuntimeID || start.LaunchID != newLaunchID {
		t.Fatalf("start payload = %+v", start)
	}
	if err := conn.SetReadDeadline(time.Now().Add(100 * time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := conn.ReadMessage(); err == nil {
		t.Fatal("same-Computer move emitted a duplicate reconcile sequence")
	}
}
