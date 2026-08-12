package handler

import (
	"reflect"
	"testing"

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
