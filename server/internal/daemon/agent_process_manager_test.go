package daemon

import (
	"testing"
	"time"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestAgentProcessManagerIdempotentStartAndRecovery(t *testing.T) {
	now := time.Date(2026, time.August, 6, 12, 0, 0, 0, time.UTC)
	var transitions []agentLifecycleTransition
	manager := newAgentProcessManager(func() time.Time { return now }, func(transition agentLifecycleTransition) {
		transitions = append(transitions, transition)
	})
	manager.newID = sequentialIDs()

	first, err := manager.Start(agentProcessStartRequest{AgentID: "agent-a", RuntimeID: "runtime-1"})
	if err != nil {
		t.Fatalf("Start(first): %v", err)
	}
	if first.QueueState != protocol.AgentStartQueueStarting {
		t.Fatalf("first queue state = %q, want starting", first.QueueState)
	}
	replayed, err := manager.Start(agentProcessStartRequest{AgentID: "agent-a", RuntimeID: "runtime-1"})
	if err != nil || replayed != first {
		t.Fatalf("replayed start = %+v, %v; want cached %+v", replayed, err, first)
	}
	if first.AgentInstanceID == "" {
		t.Fatalf("start acceptance = %+v, want a local Agent instance identity", first)
	}
	if replayed, err := manager.startWithDisposition(agentProcessStartRequest{AgentID: "agent-a", RuntimeID: "runtime-1"}); err != nil || !replayed.Replayed {
		t.Fatalf("same-Runtime start = %+v, %v; want the current instance replay", replayed, err)
	}
	if _, err := manager.Start(agentProcessStartRequest{AgentID: "agent-a", RuntimeID: "runtime-other"}); err == nil {
		t.Fatal("same Agent was allowed to start on another Runtime without Stop")
	}

	second, err := manager.Start(agentProcessStartRequest{AgentID: "agent-b", RuntimeID: "runtime-1"})
	if err != nil {
		t.Fatalf("Start(second): %v", err)
	}
	if second.QueueState != protocol.AgentStartQueueStarting {
		t.Fatalf("second start = %+v, want starting", second)
	}

	callback := agentProcessCallback{AgentID: "agent-a", AgentInstanceID: first.AgentInstanceID, ProcessInstanceID: "process-a-1"}
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
	if !ok || recovered.AgentInstanceID != first.AgentInstanceID || recovered.QueueState != protocol.AgentStartQueueStarting || recovered.ProcessInstanceID != "" {
		t.Fatalf("recovered snapshot = %+v, exists=%v", recovered, ok)
	}

	if err := manager.Stop(agentProcessCallback{AgentID: "agent-a", AgentInstanceID: first.AgentInstanceID}); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	current, ok := manager.Snapshot("agent-b")
	if !ok || current.QueueState != protocol.AgentStartQueueStarting {
		t.Fatalf("second Agent changed unexpectedly: %+v, exists=%v", current, ok)
	}
	assertSingleEnterAndClose(t, transitions)
}

func TestAgentProcessManagerFailedStartCanBeRetriedWithSameDispatch(t *testing.T) {
	manager := newAgentProcessManager(time.Now, nil)
	request := agentProcessStartRequest{AgentID: "agent-a", RuntimeID: "runtime-1"}
	first, err := manager.startWithDisposition(request)
	if err != nil {
		t.Fatalf("first start: %v", err)
	}
	if first.Replayed {
		t.Fatal("first start was unexpectedly replayed")
	}

	manager.completeFailedManagedStart(agentProcessCallback{AgentID: request.AgentID, AgentInstanceID: first.Acceptance.AgentInstanceID})

	retry, err := manager.startWithDisposition(request)
	if err != nil {
		t.Fatalf("retry start: %v", err)
	}
	if retry.Replayed {
		t.Fatal("failed start retained an ACK-only replay receipt")
	}
	if retry.Acceptance.AgentID != request.AgentID || retry.Acceptance.AgentInstanceID == first.Acceptance.AgentInstanceID {
		t.Fatalf("retry acceptance = %+v, want a new local Agent instance", retry.Acceptance)
	}
}

func TestAgentProcessManagerProviderFailureCanBeRetriedWithSameDispatch(t *testing.T) {
	manager := newAgentProcessManager(time.Now, nil)
	request := agentProcessStartRequest{AgentID: "agent-a", RuntimeID: "runtime-1"}
	first, err := manager.startWithDisposition(request)
	if err != nil {
		t.Fatalf("first start: %v", err)
	}
	callback := agentProcessCallback{AgentID: request.AgentID, AgentInstanceID: first.Acceptance.AgentInstanceID, ProcessInstanceID: "process-a"}
	if err := manager.ProcessSpawned(callback); err != nil {
		t.Fatalf("process spawned: %v", err)
	}
	if err := manager.RuntimeReady(callback); err != nil {
		t.Fatalf("runtime ready: %v", err)
	}
	if !manager.failManagedProcess(callback, nil) {
		t.Fatal("provider failure did not claim the managed process")
	}

	retry, err := manager.startWithDisposition(request)
	if err != nil {
		t.Fatalf("retry start: %v", err)
	}
	if retry.Replayed || retry.Acceptance.AgentInstanceID == first.Acceptance.AgentInstanceID {
		t.Fatalf("retry result = %+v, want a fresh local Agent instance", retry)
	}
}

func TestAgentProcessManagerStopAllowsNewAgentInstance(t *testing.T) {
	manager := newAgentProcessManager(time.Now, nil)
	firstRequest := agentProcessStartRequest{AgentID: "agent-a", RuntimeID: "runtime-1"}
	first, err := manager.Start(firstRequest)
	if err != nil {
		t.Fatalf("first start: %v", err)
	}
	if err := manager.Stop(agentProcessCallback{AgentID: firstRequest.AgentID, AgentInstanceID: first.AgentInstanceID}); err != nil {
		t.Fatalf("stop: %v", err)
	}

	secondRequest := agentProcessStartRequest{AgentID: "agent-a", RuntimeID: "runtime-1"}
	second, err := manager.Start(secondRequest)
	if err != nil {
		t.Fatalf("second start: %v", err)
	}
	if second.AgentInstanceID == first.AgentInstanceID {
		t.Fatalf("second start = %+v, want a new local instance after Stop", second)
	}
}

func TestAgentProcessManagerStopThenStartFencesStaleCallbacks(t *testing.T) {
	manager := newAgentProcessManager(time.Now, nil)
	manager.newID = sequentialIDs()
	first, err := manager.Start(agentProcessStartRequest{AgentID: "agent-a", RuntimeID: "runtime-1"})
	if err != nil {
		t.Fatalf("Start(first): %v", err)
	}
	process := agentProcessCallback{AgentID: "agent-a", AgentInstanceID: first.AgentInstanceID, ProcessInstanceID: "process-a-1"}
	if err := manager.ProcessSpawned(process); err != nil {
		t.Fatalf("ProcessSpawned: %v", err)
	}
	if err := manager.Stop(agentProcessCallback{AgentID: "agent-a", AgentInstanceID: first.AgentInstanceID}); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	second, err := manager.Start(agentProcessStartRequest{AgentID: "agent-a", RuntimeID: "runtime-1"})
	if err != nil {
		t.Fatalf("Start(second): %v", err)
	}
	if second.AgentInstanceID == first.AgentInstanceID {
		t.Fatal("replacement start reused Agent instance ID")
	}
	if err := manager.RuntimeReady(process); err == nil {
		t.Fatal("stale old-process callback was accepted")
	}
	if err := manager.Stop(agentProcessCallback{AgentID: "agent-a", AgentInstanceID: first.AgentInstanceID}); err == nil {
		t.Fatal("stale old-launch stop was accepted")
	}
}

func TestAgentProcessManagerSameRuntimeStartReplaysWithoutRebinding(t *testing.T) {
	manager := newAgentProcessManager(time.Now, nil)
	first, err := manager.startWithDisposition(agentProcessStartRequest{AgentID: "agent-a", RuntimeID: "runtime-1"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if first.Acceptance.QueueState != protocol.AgentStartQueueStarting {
		t.Fatalf("queue = %q, want starting", first.Acceptance.QueueState)
	}
	second, err := manager.startWithDisposition(agentProcessStartRequest{AgentID: "agent-a", RuntimeID: "runtime-1"})
	if err != nil {
		t.Fatalf("same-Runtime Start: %v", err)
	}
	if !second.Replayed {
		t.Fatalf("same-Runtime Start = %+v, want replay", second)
	}
	if second.Acceptance.AgentInstanceID != first.Acceptance.AgentInstanceID {
		t.Fatalf("replayed instance = %q, want existing instance %q", second.Acceptance.AgentInstanceID, first.Acceptance.AgentInstanceID)
	}
	snapshot, ok := manager.Snapshot("agent-a")
	if !ok || snapshot.AgentInstanceID != first.Acceptance.AgentInstanceID || snapshot.ProcessInstanceID != "" {
		t.Fatalf("snapshot = %+v, exists=%v; want the original launch still starting", snapshot, ok)
	}
	if snapshot.QueueState != protocol.AgentStartQueueStarting {
		t.Fatalf("live queue = %q, want starting", snapshot.QueueState)
	}
}

func TestAgentProcessManagerStoppingRejectsAcceptedStartReplayUntilSettled(t *testing.T) {
	manager := newAgentProcessManager(time.Now, nil)
	request := agentProcessStartRequest{AgentID: "agent-a", RuntimeID: "runtime-1"}
	accepted, err := manager.Start(request)
	if err != nil {
		t.Fatal(err)
	}
	callback := agentProcessCallback{AgentID: request.AgentID, AgentInstanceID: accepted.AgentInstanceID}
	if _, _, found, err := manager.beginManagedStop(callback); err != nil || !found {
		t.Fatalf("begin managed stop found=%v err=%v", found, err)
	}
	if _, err := manager.Start(request); err == nil {
		t.Fatal("accepted start replay crossed active stopping ownership")
	}
	manager.completeManagedStart(callback)
	manager.completeManagedStop(callback)
	replacement := agentProcessStartRequest{AgentID: "agent-a", RuntimeID: "runtime-1"}
	_, err = manager.Start(replacement)
	if err != nil {
		t.Fatal(err)
	}
}

func TestAgentProcessManagerStopCannotOvertakeActivePublication(t *testing.T) {
	manager := newAgentProcessManager(time.Now, nil)
	start, err := manager.Start(agentProcessStartRequest{AgentID: "agent-a", RuntimeID: "runtime-1"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	callback := agentProcessCallback{AgentID: "agent-a", AgentInstanceID: start.AgentInstanceID}
	publicationEntered := make(chan struct{})
	releasePublication := make(chan struct{})
	publicationDone := make(chan error, 1)
	go func() {
		publicationDone <- manager.publishManagedStart(callback, func() error {
			close(publicationEntered)
			<-releasePublication
			return nil
		})
	}()
	<-publicationEntered

	stopDone := make(chan error, 1)
	go func() {
		_, _, _, err := manager.beginManagedStop(callback)
		stopDone <- err
	}()
	select {
	case err := <-stopDone:
		t.Fatalf("Stop overtook Active publication: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	close(releasePublication)
	if err := <-publicationDone; err != nil {
		t.Fatalf("publish managed start: %v", err)
	}
	if err := <-stopDone; err != nil {
		t.Fatalf("begin managed stop: %v", err)
	}
}

func TestAgentProcessManagerStopWaitsForIdleRestoreStartupOwner(t *testing.T) {
	manager := newAgentProcessManager(time.Now, nil)
	residency := newAgentResidencyStore(nil)
	residency.rememberLaunch("agent-a", "runtime-1", "idle-1")
	residency.rememberIdle("agent-a", "runtime-1", "idle-1")
	if err := manager.RestoreIdle("agent-a", "runtime-1", "idle-1", residency); err != nil {
		t.Fatal(err)
	}
	current, found := manager.Snapshot("agent-a")
	if !found {
		t.Fatal("idle restore did not create an Agent instance")
	}
	if restored, ok := residency.get("agent-a"); !ok || restored.agentInstanceID != current.AgentInstanceID {
		t.Fatalf("idle residency = %+v, found %v; want replacement instance %s", restored, ok, current.AgentInstanceID)
	}
	callback := agentProcessCallback{AgentID: "agent-a", AgentInstanceID: current.AgentInstanceID}
	_, startupDone, found, err := manager.beginManagedStop(callback)
	if err != nil || !found {
		t.Fatalf("begin idle-restore stop found=%v err=%v", found, err)
	}
	select {
	case <-startupDone:
		t.Fatal("idle restore closed its startup fence before the owner settled")
	default:
	}
	manager.completeManagedStart(callback)
	select {
	case <-startupDone:
	case <-time.After(time.Second):
		t.Fatal("idle restore startup fence did not close after owner settlement")
	}
}

func TestAgentProcessManagerMissingLaunchStopDoesNotBlockIdleRestore(t *testing.T) {
	manager := newAgentProcessManager(time.Now, nil)
	if _, _, found, err := manager.beginManagedStop(agentProcessCallback{AgentID: "agent-a", AgentInstanceID: "stale-instance"}); err != nil || found {
		t.Fatalf("missing Stop found=%v err=%v", found, err)
	}
	residency := newAgentResidencyStore(nil)
	residency.rememberLaunch("agent-a", "runtime-1", "idle-1")
	residency.rememberIdle("agent-a", "runtime-1", "idle-1")
	if err := manager.RestoreIdle("agent-a", "runtime-1", "idle-1", residency); err != nil {
		t.Fatalf("missing stale Stop blocked restore: %v", err)
	}
}

func TestAgentProcessManagerFailedStartupSettlesBeforeLaunchDisappears(t *testing.T) {
	manager := newAgentProcessManager(time.Now, nil)
	request := agentProcessStartRequest{AgentID: "agent-a", RuntimeID: "runtime-1"}
	accepted, err := manager.Start(request)
	if err != nil {
		t.Fatal(err)
	}
	callback := agentProcessCallback{AgentID: request.AgentID, AgentInstanceID: accepted.AgentInstanceID}
	startupDone, found := manager.managedStartupDone(callback)
	if !found {
		t.Fatal("accepted start has no startup owner")
	}
	manager.completeFailedManagedStart(callback)
	select {
	case <-startupDone:
	case <-time.After(time.Second):
		t.Fatal("failed startup owner did not settle")
	}
	if snapshot, ok := manager.Snapshot(request.AgentID); ok {
		t.Fatalf("failed launch remained visible after startup settlement: %+v", snapshot)
	}
}

func TestAgentProcessManagerRequiresStopBeforeRuntimeReassignment(t *testing.T) {
	manager := newAgentProcessManager(time.Now, nil)
	manager.newID = sequentialIDs()
	first, err := manager.Start(agentProcessStartRequest{AgentID: "agent-a", RuntimeID: "runtime-1"})
	if err != nil {
		t.Fatalf("Start(first): %v", err)
	}
	if _, err := manager.Start(agentProcessStartRequest{AgentID: "agent-a", RuntimeID: "runtime-2"}); err == nil {
		t.Fatal("cross-Runtime start bypassed explicit stop")
	}
	if err := manager.Stop(agentProcessCallback{AgentID: "agent-a", AgentInstanceID: first.AgentInstanceID}); err != nil {
		t.Fatalf("Stop(first): %v", err)
	}
	second, err := manager.Start(agentProcessStartRequest{AgentID: "agent-a", RuntimeID: "runtime-2"})
	if err != nil {
		t.Fatalf("Start(after stop): %v", err)
	}
	if second.QueueState != protocol.AgentStartQueueStarting {
		t.Fatalf("replacement queue state = %q, want starting", second.QueueState)
	}
}

func TestAgentProcessManagerReadinessPoliciesAndActivationAreIndependent(t *testing.T) {
	manager := newAgentProcessManager(time.Now, nil)
	manager.newID = sequentialIDs()
	accepted, err := manager.Start(agentProcessStartRequest{AgentID: "agent-a", RuntimeID: "runtime-1", ReadinessPolicy: agentRuntimeReadinessInitialTurn, DeliveryMode: agentInitialDeliveryStdin})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	callback := agentProcessCallback{AgentID: "agent-a", AgentInstanceID: accepted.AgentInstanceID, ProcessInstanceID: "process-a-1"}
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
