package daemon

import (
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// deadResidentLease acquires a resident slot for agentID/runtimeID backed by
// a confirmed-dead-on-demand test backend, releases it back to the pool
// idle, and returns the backend so the caller can trigger a "detected dead"
// pass via checkResidentLiveness.
func deadResidentLease(t *testing.T, pool *canonicalAgentRuntimePool, agentID, runtimeID string) *canonicalRuntimeLivenessTestBackend {
	t.Helper()
	backend := &canonicalRuntimeLivenessTestBackend{}
	backend.setLiveness(false, true) // known dead
	identity, err := newCanonicalAgentRuntimeIdentity(canonicalAgentRuntimeIdentityParams{
		AgentID: agentID, RuntimeID: runtimeID, Provider: "opencode",
		Executable: "/usr/local/bin/opencode", WorkDir: "/var/lib/multica/" + agentID,
	})
	if err != nil {
		t.Fatalf("identity for %s/%s: %v", agentID, runtimeID, err)
	}
	lease, err := pool.acquire(canonicalAgentRuntimeAcquireRequest{
		Identity: identity, Factory: newLivenessFactory(backend),
	})
	if err != nil {
		t.Fatalf("acquire %s/%s: %v", agentID, runtimeID, err)
	}
	lease.release(true)
	return backend
}

// startManagedLaunch registers an APM launch for agentID/runtimeID on runner
// and marks it Running (spawned + ready), mirroring a resident provider
// process that is actually up before it dies.
func startManagedLaunch(t *testing.T, runner *workspaceSession, agentID, runtimeID string) {
	t.Helper()
	if _, err := runner.processes.Start(agentProcessStartRequest{
		AgentID: agentID, RuntimeID: runtimeID,
		LaunchID: "test-launch-" + agentID, StartDispatchID: "test-launch-" + agentID + "-dispatch",
	}); err != nil {
		t.Fatalf("register APM launch for %s: %v", agentID, err)
	}
	markTestLaunchRunning(t, runner, agentID)
}

// TestResidentProcessDeathClearsAPMRunningState is the invariant this change
// exists for (LRM-1571): once the liveness sweep detects a dead resident
// process, agentProcessManager must stop reporting that launch as Running —
// otherwise the local launch layer believes the launch is alive long after
// the server has already been told it crashed, and the capacity grant it
// holds is never released. Before this change, onResidentRuntimeCrash never
// touched runner.processes at all, so this must fail red on the
// pre-implementation code.
func TestResidentProcessDeathClearsAPMRunningState(t *testing.T) {
	d := New(Config{}, testLogger())
	d.mu.Lock()
	d.runtimeIndex["runtime-1"] = Runtime{ID: "runtime-1", WorkspaceID: "workspace-1"}
	d.mu.Unlock()

	runner, _ := attachTestWorkspaceDaemon(t, d, "workspace-1", nil)
	startManagedLaunch(t, runner, "agent-1", "runtime-1")

	if snap, ok := runner.processes.Snapshot("agent-1"); !ok || snap.QueueState != protocol.AgentStartQueueRunning {
		t.Fatalf("precondition: APM launch not Running before crash: %+v ok=%v", snap, ok)
	}

	deadResidentLease(t, d.canonicalRuntimes, "agent-1", "runtime-1")
	d.canonicalRuntimes.checkResidentLiveness(time.Now())

	snap, ok := runner.processes.Snapshot("agent-1")
	if ok && snap.QueueState == protocol.AgentStartQueueRunning {
		t.Fatalf("APM still reports the launch Running after its resident process died: %+v", snap)
	}
}

// TestResidentProcessExitedBackoffCapReleasesCapacityAndPromotes pins task
// #42②'s retry-cap contract as routed through APM: crashes at or under the
// cap keep the launch (APM recreates lazily on the next acquire), a crash
// that pushes the count over the cap releases the launch's capacity grant
// and promotes the next queued launch.
func TestResidentProcessExitedBackoffCapReleasesCapacityAndPromotes(t *testing.T) {
	d := New(Config{}, testLogger())
	d.canonicalRuntimes.setMaxAgentProcesses(1)
	d.mu.Lock()
	d.runtimeIndex["runtime-1"] = Runtime{ID: "runtime-1", WorkspaceID: "workspace-1"}
	d.runtimeIndex["runtime-2"] = Runtime{ID: "runtime-2", WorkspaceID: "workspace-1"}
	d.mu.Unlock()

	// The retired-launch route (over the retry cap) must publish
	// AgentStatusInactive the same way a mid-turn provider failure does — the
	// kept-launch route (under the cap) must publish nothing. Capture every
	// Inactive status frame sent on this Runner's connection to pin both
	// halves of that contract, and that it is published exactly once (no
	// double teardown between failManagedRuntime's stopLocked and a second
	// ProcessExited call).
	var statusMu sync.Mutex
	var inactiveStatuses []protocol.AgentStatusPayload
	send := func(eventType string, payload any) error {
		if eventType != protocol.EventAgentStatus {
			return nil
		}
		status, ok := payload.(protocol.AgentStatusPayload)
		if !ok || status.Status != protocol.AgentStatusInactive {
			return nil
		}
		statusMu.Lock()
		inactiveStatuses = append(inactiveStatuses, status)
		statusMu.Unlock()
		return nil
	}

	runner, _ := attachTestWorkspaceDaemon(t, d, "workspace-1", send)
	startManagedLaunch(t, runner, "agent-1", "runtime-1")

	// A second launch competing for the same (capped at 1) capacity — it
	// must be admitted as Queued behind agent-1's live grant.
	if _, err := runner.processes.Start(agentProcessStartRequest{
		AgentID: "agent-2", RuntimeID: "runtime-2",
		LaunchID: "test-launch-agent-2", StartDispatchID: "test-launch-agent-2-dispatch",
	}); err != nil {
		t.Fatalf("register APM launch for agent-2: %v", err)
	}
	if snap, ok := runner.processes.Snapshot("agent-2"); !ok || snap.QueueState != protocol.AgentStartQueueQueued {
		t.Fatalf("precondition: agent-2 must be Queued behind the capacity cap: %+v ok=%v", snap, ok)
	}

	now := time.Now()
	// residentCrashRetryCap crashes within the window are recoverable: the
	// launch is kept (re-Starting) and never releases capacity, so agent-2
	// stays Queued throughout. Re-spawn after each kept crash so the next
	// crash detects a fresh process instance, mirroring the real lazy
	// recreate on the next acquire().
	for attempt := 1; attempt <= residentCrashRetryCap; attempt++ {
		d.canonicalRuntimes.emitResidentProcessEvent(residentProcessEvent{
			AgentID: "agent-1", RuntimeID: "runtime-1", Kind: residentProcessExited,
			Provider: "opencode", At: now.Add(time.Duration(attempt) * time.Second),
		})
		snap, ok := runner.processes.Snapshot("agent-1")
		if !ok {
			t.Fatalf("crash %d: agent-1 launch was dropped under the retry cap", attempt)
		}
		if snap.QueueState == protocol.AgentStartQueueRunning {
			t.Fatalf("crash %d: agent-1 launch still reports Running", attempt)
		}
		if snap2, ok2 := runner.processes.Snapshot("agent-2"); !ok2 || snap2.QueueState != protocol.AgentStartQueueQueued {
			t.Fatalf("crash %d: agent-2 must remain Queued under the cap: %+v ok=%v", attempt, snap2, ok2)
		}
		startManagedLaunch(t, runner, "agent-1", "runtime-1")
	}

	statusMu.Lock()
	underCapInactiveCount := len(inactiveStatuses)
	statusMu.Unlock()
	if underCapInactiveCount != 0 {
		t.Fatalf("under the retry cap the launch is kept, not retired — no Inactive status should have been published: %+v", inactiveStatuses)
	}

	launchBeforeRetire, ok := runner.processes.Snapshot("agent-1")
	if !ok {
		t.Fatal("precondition: agent-1 launch must still exist before the over-cap crash")
	}

	// One more crash exceeds the cap: agent-1's launch is retired (dropped,
	// capacity released, agent-2 promoted off the queue), and — unlike a
	// kept-launch crash — that retirement must be reported outward exactly
	// like a mid-turn provider failure is.
	d.canonicalRuntimes.emitResidentProcessEvent(residentProcessEvent{
		AgentID: "agent-1", RuntimeID: "runtime-1", Kind: residentProcessExited,
		Provider: "opencode", At: now.Add(time.Duration(residentCrashRetryCap+1) * time.Second),
	})

	if _, ok := runner.processes.Snapshot("agent-1"); ok {
		t.Fatal("agent-1 launch should have been dropped once it exceeded the retry cap")
	}
	snap2, ok2 := runner.processes.Snapshot("agent-2")
	if !ok2 || snap2.QueueState == protocol.AgentStartQueueQueued {
		t.Fatalf("agent-2 should have been promoted off the queue once agent-1 released capacity: %+v ok=%v", snap2, ok2)
	}

	statusMu.Lock()
	defer statusMu.Unlock()
	if len(inactiveStatuses) != 1 {
		t.Fatalf("expected exactly one Inactive status for the retired launch (no double teardown), got %d: %+v",
			len(inactiveStatuses), inactiveStatuses)
	}
	if inactiveStatuses[0].AgentID != "agent-1" || inactiveStatuses[0].LaunchID != launchBeforeRetire.LaunchID {
		t.Fatalf("Inactive status published for the wrong agent/launch: %+v, want agent-1/%s",
			inactiveStatuses[0], launchBeforeRetire.LaunchID)
	}
}

// TestResidentProcessEventFanOutOrderAndPanicRecovery pins the resident
// process event bus's delivery contract: subscribers run sequentially in
// registration order, and a panicking subscriber is recovered so later
// subscribers still receive the event.
func TestResidentProcessEventFanOutOrderAndPanicRecovery(t *testing.T) {
	pool := newCanonicalAgentRuntimePool()

	var mu sync.Mutex
	var order []string
	pool.subscribeResidentProcess(func(residentProcessEvent) {
		mu.Lock()
		order = append(order, "first")
		mu.Unlock()
	})
	pool.subscribeResidentProcess(func(residentProcessEvent) {
		panic("boom: second subscriber always panics")
	})
	pool.subscribeResidentProcess(func(residentProcessEvent) {
		mu.Lock()
		order = append(order, "third")
		mu.Unlock()
	})

	pool.emitResidentProcessEvent(residentProcessEvent{
		AgentID: "agent-1", RuntimeID: "runtime-1", Kind: residentProcessExited, At: time.Now(),
	})

	mu.Lock()
	defer mu.Unlock()
	if len(order) != 2 || order[0] != "first" || order[1] != "third" {
		t.Fatalf("delivery order = %v, want [first third] (panic must not stop later subscribers)", order)
	}
}

// TestResidentProcessEventRoutingIsolatedByWorkspace pins that a resident
// process event for a runtime in one workspace never reaches another
// workspace's Runner/APM — resolveManagedLaunch's runtimeIndex lookup keeps
// routing scoped to the event's own runtime.
func TestResidentProcessEventRoutingIsolatedByWorkspace(t *testing.T) {
	dA := New(Config{WorkspaceID: "workspace-a"}, testLogger())
	dA.mu.Lock()
	dA.runtimeIndex["runtime-a"] = Runtime{ID: "runtime-a", WorkspaceID: "workspace-a"}
	dA.mu.Unlock()
	runnerA, _ := attachTestWorkspaceDaemon(t, dA, "workspace-a", nil)
	startManagedLaunch(t, runnerA, "agent-1", "runtime-a")

	dB := New(Config{WorkspaceID: "workspace-b"}, testLogger())
	dB.mu.Lock()
	dB.runtimeIndex["runtime-b"] = Runtime{ID: "runtime-b", WorkspaceID: "workspace-b"}
	dB.mu.Unlock()
	runnerB, _ := attachTestWorkspaceDaemon(t, dB, "workspace-b", nil)
	startManagedLaunch(t, runnerB, "agent-1", "runtime-b")

	dA.canonicalRuntimes.emitResidentProcessEvent(residentProcessEvent{
		AgentID: "agent-1", RuntimeID: "runtime-a", Kind: residentProcessExited,
		Provider: "opencode", At: time.Now(),
	})

	snapA, okA := runnerA.processes.Snapshot("agent-1")
	if !okA || snapA.QueueState == protocol.AgentStartQueueRunning {
		t.Fatalf("workspace-a launch should have been routed the exited event: %+v ok=%v", snapA, okA)
	}
	snapB, okB := runnerB.processes.Snapshot("agent-1")
	if !okB || snapB.QueueState != protocol.AgentStartQueueRunning {
		t.Fatalf("workspace-b launch must be unaffected by workspace-a's event: %+v ok=%v", snapB, okB)
	}
}
