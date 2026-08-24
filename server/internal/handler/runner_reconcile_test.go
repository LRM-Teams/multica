package handler

import (
	"reflect"
	"testing"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestReduceRunnerLaunchesConvergesDesiredAndObservedState(t *testing.T) {
	tests := []struct {
		name     string
		desired  []runnerDesiredLaunch
		observed []runnerObservedLaunch
		want     []runnerReconcileAction
	}{
		{name: "starts missing Agent", desired: []runnerDesiredLaunch{{agentID: "agent-a", runtimeID: "runtime-new"}}, want: []runnerReconcileAction{{eventType: protocol.EventDaemonAgentStart, payload: protocol.AgentStartPayload{AgentID: "agent-a", RuntimeID: "runtime-new"}}}},
		{name: "matching active Agent is unchanged", desired: []runnerDesiredLaunch{{agentID: "agent-a", runtimeID: "runtime-new"}}, observed: []runnerObservedLaunch{{agentID: "agent-a", runtimeID: "runtime-new", status: protocol.AgentStatusActive}}},
		{name: "starting is not active residency", desired: []runnerDesiredLaunch{{agentID: "agent-a", runtimeID: "runtime-new"}}, observed: []runnerObservedLaunch{{agentID: "agent-a", runtimeID: "runtime-new", status: "accepted"}}, want: []runnerReconcileAction{{eventType: protocol.EventDaemonAgentStart, payload: protocol.AgentStartPayload{AgentID: "agent-a", RuntimeID: "runtime-new"}}}},
		{name: "Runtime change stops old Runtime first", desired: []runnerDesiredLaunch{{agentID: "agent-a", runtimeID: "runtime-new"}}, observed: []runnerObservedLaunch{{agentID: "agent-a", runtimeID: "runtime-old", status: protocol.AgentStatusActive}}, want: []runnerReconcileAction{{eventType: protocol.EventDaemonAgentStop, payload: protocol.AgentStopPayload{AgentID: "agent-a"}}}},
		{name: "start preserves provider session", desired: []runnerDesiredLaunch{{agentID: "agent-a", runtimeID: "runtime-new", sessionID: "provider-session"}}, want: []runnerReconcileAction{{eventType: protocol.EventDaemonAgentStart, payload: protocol.AgentStartPayload{AgentID: "agent-a", RuntimeID: "runtime-new", Config: protocol.AgentStartConfig{SessionID: "provider-session"}}}}},
		{name: "removed Agent stops stale residency", observed: []runnerObservedLaunch{{agentID: "agent-a", runtimeID: "runtime-old", status: protocol.AgentStatusActive}}, want: []runnerReconcileAction{{eventType: protocol.EventDaemonAgentStop, payload: protocol.AgentStopPayload{AgentID: "agent-a"}}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := reduceRunnerLaunches(test.desired, test.observed); !reflect.DeepEqual(got, test.want) {
				t.Fatalf("actions = %#v, want %#v", got, test.want)
			}
		})
	}
}
