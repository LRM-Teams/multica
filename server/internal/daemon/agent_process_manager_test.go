package daemon

import (
	"testing"
	"time"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestAgentProcessManagerIdempotentStartQueueAndRecovery(t *testing.T) {
	now := time.Date(2026, time.August, 6, 12, 0, 0, 0, time.UTC)
	var transitions []agentLifecycleTransition
	manager := newAgentProcessManager("workspace-1", newTestProcessAdmission(1), func() time.Time { return now }, func(transition agentLifecycleTransition) {
		transitions = append(transitions, transition)
	})
	manager.newID = sequentialIDs()

	first, err := manager.Start(agentProcessStartRequest{AgentID: "agent-a", RuntimeID: "runtime-1", LaunchID: "launch-a", StartDispatchID: "dispatch-a"})
	if err != nil {
		t.Fatalf("Start(first): %v", err)
	}
	if first.QueueState != protocol.AgentStartQueueStarting {
		t.Fatalf("first queue state = %q, want starting", first.QueueState)
	}
	replayed, err := manager.Start(agentProcessStartRequest{AgentID: "agent-a", RuntimeID: "runtime-1", LaunchID: "launch-a", StartDispatchID: "dispatch-a"})
	if err != nil || replayed != first {
		t.Fatalf("replayed start = %+v, %v; want cached %+v", replayed, err, first)
	}
	if first.LaunchID != "launch-a" || first.StartDispatchID != "dispatch-a" {
		t.Fatalf("start identities = %+v, want separate launch and dispatch ids", first)
	}
	if _, err := manager.Start(agentProcessStartRequest{AgentID: "agent-a", RuntimeID: "runtime-other", LaunchID: "launch-a", StartDispatchID: "dispatch-a"}); err == nil {
		t.Fatal("conflicting reuse of accepted start dispatch was allowed")
	}

	queued, err := manager.Start(agentProcessStartRequest{AgentID: "agent-b", RuntimeID: "runtime-1", LaunchID: "launch-b", StartDispatchID: "dispatch-b"})
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
	manager := newAgentProcessManager("workspace-1", newCanonicalAgentRuntimePool().managedProcessAdmission(), time.Now, nil)
	manager.newID = sequentialIDs()
	first, err := manager.Start(agentProcessStartRequest{AgentID: "agent-a", RuntimeID: "runtime-1", LaunchID: "dispatch-a", StartDispatchID: "dispatch-a" + "-dispatch"})
	if err != nil {
		t.Fatalf("Start(first): %v", err)
	}
	process := agentProcessCallback{AgentID: "agent-a", LaunchID: first.LaunchID, ProcessInstanceID: "process-a-1"}
	if err := manager.ProcessSpawned(process); err != nil {
		t.Fatalf("ProcessSpawned: %v", err)
	}
	second, err := manager.Restart(agentProcessCallback{AgentID: "agent-a", LaunchID: first.LaunchID}, agentProcessStartRequest{AgentID: "agent-a", RuntimeID: "runtime-1", LaunchID: "dispatch-b", StartDispatchID: "dispatch-b" + "-dispatch"})
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

func TestAgentProcessManagerRejectsRevokedCapacityGrant(t *testing.T) {
	admission := newTestProcessAdmission(1)
	manager := newAgentProcessManager("workspace-1", admission, time.Now, nil)
	manager.newID = sequentialIDs()
	accepted, err := manager.Start(agentProcessStartRequest{AgentID: "agent-a", RuntimeID: "runtime-1", LaunchID: "dispatch-a", StartDispatchID: "dispatch-a" + "-dispatch"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	manager.mu.Lock()
	grant := manager.agents["agent-a"].capacityGrant
	manager.mu.Unlock()
	admission.Cancel(grant)
	if err := manager.ProcessSpawned(agentProcessCallback{AgentID: "agent-a", LaunchID: accepted.LaunchID, ProcessInstanceID: "process-a"}); err == nil {
		t.Fatal("ProcessSpawned accepted a revoked capacity grant")
	}
}

func TestAgentProcessManagerSharesCanonicalCapacityAcrossRunners(t *testing.T) {
	pool := newCanonicalAgentRuntimePool()
	pool.setMaxAgentProcesses(1)
	first := newAgentProcessManager("workspace-a", pool.managedProcessAdmission(), time.Now, nil)
	second := newAgentProcessManager("workspace-b", pool.managedProcessAdmission(), time.Now, nil)

	firstAck, err := first.Start(agentProcessStartRequest{AgentID: "agent-a", RuntimeID: "runtime-a", LaunchID: "dispatch-a", StartDispatchID: "dispatch-a" + "-dispatch"})
	if err != nil || firstAck.QueueState != protocol.AgentStartQueueStarting {
		t.Fatalf("Start(first) = %+v, %v; want starting", firstAck, err)
	}
	secondAck, err := second.Start(agentProcessStartRequest{AgentID: "agent-b", RuntimeID: "runtime-b", LaunchID: "dispatch-b", StartDispatchID: "dispatch-b" + "-dispatch"})
	if err != nil || secondAck.QueueState != protocol.AgentStartQueueQueued {
		t.Fatalf("Start(second) = %+v, %v; want globally queued", secondAck, err)
	}

	if err := first.Stop(agentProcessCallback{AgentID: "agent-a", LaunchID: firstAck.LaunchID}); err != nil {
		t.Fatalf("Stop(first): %v", err)
	}
	waitForProcessQueueState(t, second, "agent-b", protocol.AgentStartQueueStarting)
	pool.mu.Lock()
	grants := len(pool.managedProcessGrants)
	pending := len(pool.pendingManagedProcesses)
	pool.mu.Unlock()
	if grants != 1 || pending != 0 {
		t.Fatalf("global capacity after release: grants=%d pending=%d, want 1/0", grants, pending)
	}
}

func TestAgentProcessManagerCancelsGloballyQueuedLaunch(t *testing.T) {
	pool := newCanonicalAgentRuntimePool()
	pool.setMaxAgentProcesses(1)
	first := newAgentProcessManager("workspace-a", pool.managedProcessAdmission(), time.Now, nil)
	second := newAgentProcessManager("workspace-b", pool.managedProcessAdmission(), time.Now, nil)
	third := newAgentProcessManager("workspace-c", pool.managedProcessAdmission(), time.Now, nil)

	firstAck, _ := first.Start(agentProcessStartRequest{AgentID: "agent-a", RuntimeID: "runtime-a", LaunchID: "dispatch-a", StartDispatchID: "dispatch-a" + "-dispatch"})
	secondAck, err := second.Start(agentProcessStartRequest{AgentID: "agent-b", RuntimeID: "runtime-b", LaunchID: "dispatch-b", StartDispatchID: "dispatch-b" + "-dispatch"})
	if err != nil || secondAck.QueueState != protocol.AgentStartQueueQueued {
		t.Fatalf("Start(second) = %+v, %v; want queued", secondAck, err)
	}
	if err := second.Stop(agentProcessCallback{AgentID: "agent-b", LaunchID: secondAck.LaunchID}); err != nil {
		t.Fatalf("Stop(queued): %v", err)
	}
	thirdAck, err := third.Start(agentProcessStartRequest{AgentID: "agent-c", RuntimeID: "runtime-c", LaunchID: "dispatch-c", StartDispatchID: "dispatch-c" + "-dispatch"})
	if err != nil || thirdAck.QueueState != protocol.AgentStartQueueQueued {
		t.Fatalf("Start(third) = %+v, %v; want queued", thirdAck, err)
	}
	if err := first.Stop(agentProcessCallback{AgentID: "agent-a", LaunchID: firstAck.LaunchID}); err != nil {
		t.Fatalf("Stop(first): %v", err)
	}
	waitForProcessQueueState(t, third, "agent-c", protocol.AgentStartQueueStarting)
	if _, found := second.Snapshot("agent-b"); found {
		t.Fatal("cancelled queued launch was retained")
	}
}

func TestManagedCapacityCountsResidentProcessAndEvictsOnlyIdle(t *testing.T) {
	pool := newCanonicalAgentRuntimePool()
	pool.setMaxAgentProcesses(1)
	probe := &canonicalRuntimeFactoryProbe{}
	lease := acquireResident(t, pool, probe, "agent-a", "runtime-a", nil)
	lease.release(true) // resident but idle: safe capacity eviction candidate.

	manager := newAgentProcessManager("workspace-b", pool.managedProcessAdmission(), time.Now, nil)
	ack, err := manager.Start(agentProcessStartRequest{AgentID: "agent-b", RuntimeID: "runtime-b", LaunchID: "dispatch-b", StartDispatchID: "dispatch-b" + "-dispatch"})
	if err != nil || ack.QueueState != protocol.AgentStartQueueStarting {
		t.Fatalf("managed Start = %+v, %v; want starting after idle eviction", ack, err)
	}
	if pool.agentHasLiveForTest("agent-a") {
		t.Fatal("idle resident survived a managed-capacity admission")
	}
	if got := pool.EvictForCapTotal(); got != 1 {
		t.Fatalf("idle evictions = %d, want 1", got)
	}
}

func waitForProcessQueueState(t *testing.T, manager *agentProcessManager, agentID, want string) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if snapshot, ok := manager.Snapshot(agentID); ok && snapshot.QueueState == want {
			return
		}
		time.Sleep(time.Millisecond)
	}
	snapshot, ok := manager.Snapshot(agentID)
	t.Fatalf("queue state for %s = %+v, exists=%v; want %q", agentID, snapshot, ok, want)
}

func TestAgentProcessManagerRequiresStopBeforeRuntimeReassignment(t *testing.T) {
	manager := newAgentProcessManager("workspace-1", newCanonicalAgentRuntimePool().managedProcessAdmission(), time.Now, nil)
	manager.newID = sequentialIDs()
	first, err := manager.Start(agentProcessStartRequest{AgentID: "agent-a", RuntimeID: "runtime-1", LaunchID: "dispatch-a", StartDispatchID: "dispatch-a" + "-dispatch"})
	if err != nil {
		t.Fatalf("Start(first): %v", err)
	}
	if _, err := manager.Start(agentProcessStartRequest{AgentID: "agent-a", RuntimeID: "runtime-2", LaunchID: "dispatch-b", StartDispatchID: "dispatch-b" + "-dispatch"}); err == nil {
		t.Fatal("cross-Runtime start bypassed explicit stop")
	}
	if err := manager.Stop(agentProcessCallback{AgentID: "agent-a", LaunchID: first.LaunchID}); err != nil {
		t.Fatalf("Stop(first): %v", err)
	}
	second, err := manager.Start(agentProcessStartRequest{AgentID: "agent-a", RuntimeID: "runtime-2", LaunchID: "dispatch-b", StartDispatchID: "dispatch-b" + "-dispatch"})
	if err != nil {
		t.Fatalf("Start(after stop): %v", err)
	}
	if second.QueueState != protocol.AgentStartQueueStarting {
		t.Fatalf("replacement queue state = %q, want starting", second.QueueState)
	}
}

func TestAgentProcessManagerReadinessPoliciesAndActivationAreIndependent(t *testing.T) {
	manager := newAgentProcessManager("workspace-1", newCanonicalAgentRuntimePool().managedProcessAdmission(), time.Now, nil)
	manager.newID = sequentialIDs()
	accepted, err := manager.Start(agentProcessStartRequest{AgentID: "agent-a", RuntimeID: "runtime-1", LaunchID: "dispatch-a", StartDispatchID: "dispatch-a" + "-dispatch", ReadinessPolicy: agentRuntimeReadinessInitialTurn, DeliveryMode: agentInitialDeliveryStdin})
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

type testProcessAdmission struct {
	limit  int
	active map[string]agentProcessCapacityGrant
}

func newTestProcessAdmission(limit int) *testProcessAdmission {
	return &testProcessAdmission{limit: limit, active: make(map[string]agentProcessCapacityGrant)}
}

func (a *testProcessAdmission) Acquire(request agentProcessCapacityRequest) (agentProcessCapacityGrant, bool) {
	if grant, ok := a.active[request.LaunchID]; ok {
		return grant, true
	}
	if a.limit > 0 && len(a.active) >= a.limit {
		return agentProcessCapacityGrant{}, false
	}
	grant := agentProcessCapacityGrant{ID: request.LaunchID, LaunchID: request.LaunchID}
	a.active[grant.LaunchID] = grant
	return grant, true
}

func (a *testProcessAdmission) Cancel(grant agentProcessCapacityGrant) { a.Release(grant) }

func (a *testProcessAdmission) Release(grant agentProcessCapacityGrant) {
	if current, ok := a.active[grant.LaunchID]; ok && current == grant {
		delete(a.active, grant.LaunchID)
	}
}

func (a *testProcessAdmission) Active(grant agentProcessCapacityGrant) bool {
	current, ok := a.active[grant.LaunchID]
	return ok && current == grant
}
