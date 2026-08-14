package daemon

import (
	"testing"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

// These tests intentionally create different Workspace Runners but one Daemon
// pool. They protect the architectural rule that capacity is a machine fact,
// while queue receipts and launch fencing remain Runner-local facts.
func TestManagedCapacityMultiWorkspaceFIFOAndRunnerRemoval(t *testing.T) {
	d := New(Config{DaemonID: "daemon-1", MaxAgentProcesses: 1}, nil)
	first, err := d.newWorkspaceRunner("workspace-a")
	if err != nil {
		t.Fatal(err)
	}
	second, err := d.newWorkspaceRunner("workspace-b")
	if err != nil {
		t.Fatal(err)
	}
	third, err := d.newWorkspaceRunner("workspace-c")
	if err != nil {
		t.Fatal(err)
	}

	firstAck := startManagedForCapacityTest(t, first, "agent-a", "runtime-a", "dispatch-a")
	secondAck := startManagedForCapacityTest(t, second, "agent-b", "runtime-b", "dispatch-b")
	thirdAck := startManagedForCapacityTest(t, third, "agent-c", "runtime-c", "dispatch-c")
	if secondAck.QueueState != protocol.AgentStartQueueQueued || thirdAck.QueueState != protocol.AgentStartQueueQueued {
		t.Fatalf("independent Runners bypassed global cap: second=%+v third=%+v", secondAck, thirdAck)
	}

	// Runner removal cancels its queued launch before the preceding release.
	second.processes.Close()
	if err := first.processes.Stop(agentProcessCallback{AgentID: "agent-a", LaunchID: firstAck.LaunchID}); err != nil {
		t.Fatalf("Stop(first): %v", err)
	}
	waitForProcessQueueState(t, third.processes, "agent-c", protocol.AgentStartQueueStarting)
	if _, found := second.processes.Snapshot("agent-b"); found {
		t.Fatal("removed Runner retained its queued launch")
	}
}

func TestManagedCapacityQueuedLaunchRequiresFormalStopAndCrashReplacementFence(t *testing.T) {
	d := New(Config{DaemonID: "daemon-1", WorkspacesRoot: t.TempDir(), MaxAgentProcesses: 1}, nil)
	for _, runtime := range []Runtime{
		{ID: "runtime-a", WorkspaceID: "workspace-a"},
		{ID: "runtime-b", WorkspaceID: "workspace-b"},
		{ID: "runtime-c", WorkspaceID: "workspace-c"},
	} {
		d.mu.Lock()
		d.runtimeIndex[runtime.ID] = runtime
		d.mu.Unlock()
	}
	first, _ := attachTestWorkspaceRunner(t, d, "workspace-a", nil)
	second, _ := attachTestWorkspaceRunner(t, d, "workspace-b", nil)
	third, _ := attachTestWorkspaceRunner(t, d, "workspace-c", nil)
	firstAck := startManagedForCapacityTest(t, first, "agent-a", "runtime-a", "dispatch-a")
	secondAck := startManagedForCapacityTest(t, second, "agent-b", "runtime-b", "dispatch-b")
	if secondAck.QueueState != protocol.AgentStartQueueQueued {
		t.Fatalf("second launch=%+v, want queued", secondAck)
	}
	if launch, found := second.processes.Snapshot("agent-b"); !found || launch.LaunchID != secondAck.LaunchID {
		t.Fatalf("queued launch disappeared before formal stop: %+v found=%v", launch, found)
	}
	if err := second.processes.Stop(agentProcessCallback{AgentID: "agent-b", LaunchID: secondAck.LaunchID}); err != nil {
		t.Fatalf("formal stop of queued launch: %v", err)
	}
	thirdAck := startManagedForCapacityTest(t, third, "agent-c", "runtime-c", "dispatch-c")
	if thirdAck.QueueState != protocol.AgentStartQueueQueued {
		t.Fatalf("third launch=%+v, want queued", thirdAck)
	}

	process := agentProcessCallback{AgentID: "agent-a", LaunchID: firstAck.LaunchID, ProcessInstanceID: "process-a"}
	if err := first.processes.ProcessSpawned(process); err != nil {
		t.Fatalf("ProcessSpawned(first): %v", err)
	}
	if err := first.processes.ProcessExited(process, false); err != nil {
		t.Fatalf("ProcessExited(first): %v", err)
	}
	waitForProcessQueueState(t, third.processes, "agent-c", protocol.AgentStartQueueStarting)
	if err := third.processes.Stop(agentProcessCallback{AgentID: "agent-c", LaunchID: thirdAck.LaunchID}); err != nil {
		t.Fatalf("Stop(replacement): %v", err)
	}
	if err := third.processes.ProcessSpawned(agentProcessCallback{AgentID: "agent-c", LaunchID: thirdAck.LaunchID, ProcessInstanceID: "late-process"}); err == nil {
		t.Fatal("stale process callback revived a stopped replacement launch")
	}
}

func startManagedForCapacityTest(t *testing.T, runner *WorkspaceRunner, agentID, runtimeID, dispatchID string) protocol.AgentStartAckPayload {
	t.Helper()
	ack, err := runner.processes.Start(agentProcessStartRequest{AgentID: agentID, RuntimeID: runtimeID, LaunchID: dispatchID, StartDispatchID: dispatchID + "-dispatch"})
	if err != nil {
		t.Fatalf("Start(%s): %v", agentID, err)
	}
	return ack
}
