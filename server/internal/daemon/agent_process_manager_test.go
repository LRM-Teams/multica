package daemon

import (
	"testing"
	"time"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestAgentProcessManagerIdempotentStartQueueAndRecovery(t *testing.T) {
	now := time.Date(2026, time.August, 6, 12, 0, 0, 0, time.UTC)
	var transitions []agentLifecycleTransition
	manager := newAgentProcessManager(1, func() time.Time { return now }, func(transition agentLifecycleTransition) {
		transitions = append(transitions, transition)
	})
	manager.newID = sequentialIDs()

	first, err := manager.Start(agentProcessStartRequest{AgentID: "agent-a", RuntimeID: "runtime-1", StartDispatchID: "dispatch-a"})
	if err != nil {
		t.Fatalf("Start(first): %v", err)
	}
	if first.QueueState != protocol.AgentStartQueueStarting {
		t.Fatalf("first queue state = %q, want starting", first.QueueState)
	}
	replayed, err := manager.Start(agentProcessStartRequest{AgentID: "agent-a", RuntimeID: "runtime-1", StartDispatchID: "dispatch-a"})
	if err != nil || replayed != first {
		t.Fatalf("replayed start = %+v, %v; want cached %+v", replayed, err, first)
	}

	queued, err := manager.Start(agentProcessStartRequest{AgentID: "agent-b", RuntimeID: "runtime-1", StartDispatchID: "dispatch-b"})
	if err != nil {
		t.Fatalf("Start(queued): %v", err)
	}
	if queued.QueueState != protocol.AgentStartQueueQueued || queued.QueueDepth != 1 {
		t.Fatalf("queued receipt = %+v, want queued depth 1", queued)
	}

	callback := agentProcessCallback{AgentID: "agent-a", LaunchID: first.LaunchID, ProcessInstanceID: "process-a-1"}
	if err := manager.ProcessSpawned(callback); err != nil {
		t.Fatalf("ProcessSpawned: %v", err)
	}
	if err := manager.RuntimeReady(callback); err != nil {
		t.Fatalf("RuntimeReady: %v", err)
	}
	if err := manager.ProcessExited(callback, true); err != nil {
		t.Fatalf("ProcessExited(recover): %v", err)
	}
	recovered, ok := manager.Snapshot("agent-a")
	if !ok || recovered.LaunchID != first.LaunchID || recovered.QueueState != protocol.AgentStartQueueStarting || recovered.ProcessInstanceID != "" {
		t.Fatalf("recovered snapshot = %+v, exists=%v", recovered, ok)
	}

	if err := manager.Stop(agentProcessCallback{AgentID: "agent-a", LaunchID: first.LaunchID}); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	promoted, ok := manager.Snapshot("agent-b")
	if !ok || promoted.QueueState != protocol.AgentStartQueueStarting {
		t.Fatalf("queued Agent was not promoted: %+v, exists=%v", promoted, ok)
	}
	assertSingleEnterAndClose(t, transitions)
}

func TestAgentProcessManagerFencesStaleCallbacksAndCreatesNewLaunches(t *testing.T) {
	manager := newAgentProcessManager(2, time.Now, nil)
	manager.newID = sequentialIDs()
	first, err := manager.Start(agentProcessStartRequest{AgentID: "agent-a", RuntimeID: "runtime-1", StartDispatchID: "dispatch-a"})
	if err != nil {
		t.Fatalf("Start(first): %v", err)
	}
	process := agentProcessCallback{AgentID: "agent-a", LaunchID: first.LaunchID, ProcessInstanceID: "process-a-1"}
	if err := manager.ProcessSpawned(process); err != nil {
		t.Fatalf("ProcessSpawned: %v", err)
	}
	second, err := manager.Restart(agentProcessCallback{AgentID: "agent-a", LaunchID: first.LaunchID}, agentProcessStartRequest{AgentID: "agent-a", RuntimeID: "runtime-1", StartDispatchID: "dispatch-b"})
	if err != nil {
		t.Fatalf("Restart: %v", err)
	}
	if second.LaunchID == first.LaunchID {
		t.Fatal("explicit restart reused launch ID")
	}
	if err := manager.RuntimeReady(process); err == nil {
		t.Fatal("stale old-process callback was accepted")
	}
	if err := manager.Stop(agentProcessCallback{AgentID: "agent-a", LaunchID: first.LaunchID}); err == nil {
		t.Fatal("stale old-launch stop was accepted")
	}
}

func TestAgentProcessManagerReassignsToNewRuntimeWithNewLaunch(t *testing.T) {
	manager := newAgentProcessManager(2, time.Now, nil)
	manager.newID = sequentialIDs()
	first, err := manager.Start(agentProcessStartRequest{AgentID: "agent-a", RuntimeID: "runtime-1", StartDispatchID: "dispatch-a"})
	if err != nil {
		t.Fatalf("Start(first): %v", err)
	}
	second, err := manager.Start(agentProcessStartRequest{AgentID: "agent-a", RuntimeID: "runtime-2", StartDispatchID: "dispatch-b"})
	if err != nil {
		t.Fatalf("Start(reassigned): %v", err)
	}
	if second.LaunchID == first.LaunchID {
		t.Fatal("Runner reassignment reused launch ID")
	}
	if second.QueueState != protocol.AgentStartQueueStarting {
		t.Fatalf("reassigned queue state = %q, want starting", second.QueueState)
	}
}

func TestAgentProcessManagerReadinessPoliciesAndActivationAreIndependent(t *testing.T) {
	manager := newAgentProcessManager(1, time.Now, nil)
	manager.newID = sequentialIDs()
	accepted, err := manager.Start(agentProcessStartRequest{AgentID: "agent-a", RuntimeID: "runtime-1", StartDispatchID: "dispatch-a", ReadinessPolicy: agentRuntimeReadinessInitialTurn, DeliveryMode: agentInitialDeliveryStdin})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	callback := agentProcessCallback{AgentID: "agent-a", LaunchID: accepted.LaunchID, ProcessInstanceID: "process-a-1"}
	if err := manager.ProcessSpawned(callback); err != nil {
		t.Fatalf("ProcessSpawned: %v", err)
	}
	if err := manager.ObserveRuntimeEvidence(callback, "session_initialized"); err == nil {
		t.Fatal("session initialization incorrectly established initial-turn readiness")
	}
	if err := manager.ObserveRuntimeEvidence(callback, "initial_turn_progress"); err != nil {
		t.Fatalf("initial turn progress did not establish readiness: %v", err)
	}
	state, ok := manager.Snapshot("agent-a")
	if !ok || state.QueueState != protocol.AgentStartQueueStarting {
		t.Fatalf("activation wait must remain distinct from ready: %+v, exists=%v", state, ok)
	}
	if err := manager.InitialActivationDelivered(callback); err != nil {
		t.Fatalf("InitialActivationDelivered: %v", err)
	}
	state, ok = manager.Snapshot("agent-a")
	if !ok || state.QueueState != protocol.AgentStartQueueRunning {
		t.Fatalf("delivered activation did not advance run: %+v, exists=%v", state, ok)
	}
}

func sequentialIDs() func() string {
	ids := []string{"launch-a", "transition-a", "process-a", "transition-b", "launch-b", "transition-c", "launch-c", "transition-d", "launch-d"}
	index := 0
	return func() string {
		if index >= len(ids) {
			return "id-overflow"
		}
		id := ids[index]
		index++
		return id
	}
}

func assertSingleEnterAndClose(t *testing.T, transitions []agentLifecycleTransition) {
	t.Helper()
	entered := make(map[string]int)
	closed := make(map[string]int)
	for _, transition := range transitions {
		switch transition.Event {
		case "enter":
			entered[transition.StateInstanceID]++
		case "close":
			closed[transition.StateInstanceID]++
		default:
			t.Fatalf("unexpected transition event %+v", transition)
		}
	}
	for id, count := range entered {
		if count != 1 || closed[id] > 1 {
			t.Fatalf("transition %s enters=%d closes=%d, want one enter and at most one close", id, count, closed[id])
		}
	}
}
