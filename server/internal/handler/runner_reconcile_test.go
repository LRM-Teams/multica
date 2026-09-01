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

func TestReduceRunnerLaunchesCrossDaemonMoveDoesNotWaitForOldInactive(t *testing.T) {
	oldActions := reduceRunnerLaunches(nil, []runnerObservedLaunch{{
		agentID: "agent-a", runtimeID: "runtime-old", status: protocol.AgentStatusActive,
	}})
	targetActions := reduceRunnerLaunches([]runnerDesiredLaunch{{
		agentID: "agent-a", runtimeID: "runtime-new",
	}}, nil)

	wantOld := []runnerReconcileAction{{eventType: protocol.EventDaemonAgentStop, payload: protocol.AgentStopPayload{AgentID: "agent-a"}}}
	wantTarget := []runnerReconcileAction{{eventType: protocol.EventDaemonAgentStart, payload: protocol.AgentStartPayload{AgentID: "agent-a", RuntimeID: "runtime-new"}}}
	if !reflect.DeepEqual(oldActions, wantOld) {
		t.Fatalf("old daemon actions = %#v, want %#v", oldActions, wantOld)
	}
	if !reflect.DeepEqual(targetActions, wantTarget) {
		t.Fatalf("target daemon actions = %#v, want %#v", targetActions, wantTarget)
	}
}

func TestReduceRunnerLaunchesSameDaemonMoveWaitsForInactive(t *testing.T) {
	desired := []runnerDesiredLaunch{{agentID: "agent-a", runtimeID: "runtime-new"}}
	activeOld := []runnerObservedLaunch{{agentID: "agent-a", runtimeID: "runtime-old", status: protocol.AgentStatusActive}}
	wantStop := []runnerReconcileAction{{eventType: protocol.EventDaemonAgentStop, payload: protocol.AgentStopPayload{AgentID: "agent-a"}}}
	if got := reduceRunnerLaunches(desired, activeOld); !reflect.DeepEqual(got, wantStop) {
		t.Fatalf("actions before inactive = %#v, want %#v", got, wantStop)
	}
	wantStart := []runnerReconcileAction{{eventType: protocol.EventDaemonAgentStart, payload: protocol.AgentStartPayload{AgentID: "agent-a", RuntimeID: "runtime-new"}}}
	if got := reduceRunnerLaunches(desired, nil); !reflect.DeepEqual(got, wantStart) {
		t.Fatalf("actions after inactive = %#v, want %#v", got, wantStart)
	}
}

func TestReadyProcessSnapshotSuppressesReconnectStartBurst(t *testing.T) {
	store := newRunnerObservationStore()
	store.replaceInstance("workspace", "daemon", "instance", []runnerObservedAgent{
		{
			workspaceID: "workspace", daemonID: "daemon", daemonInstanceID: "instance",
			agentID: "agent-a", runtimeID: "runtime-a", status: protocol.AgentStatusActive,
		},
		{
			workspaceID: "workspace", daemonID: "daemon", daemonInstanceID: "instance",
			agentID: "agent-b", runtimeID: "runtime-b", status: runnerObservedStatusManagedStarting,
		},
	})
	observed := make([]runnerObservedLaunch, 0)
	for _, process := range store.listInstance("workspace", "daemon", "instance") {
		observed = append(observed, runnerObservedLaunch{
			agentID: process.agentID, runtimeID: process.runtimeID, status: process.status,
		})
	}
	desired := []runnerDesiredLaunch{
		{agentID: "agent-a", runtimeID: "runtime-a"},
		{agentID: "agent-b", runtimeID: "runtime-b"},
	}
	if actions := reduceRunnerLaunches(desired, observed); len(actions) != 0 {
		t.Fatalf("reconnect snapshot produced duplicate launch actions: %#v", actions)
	}
}
