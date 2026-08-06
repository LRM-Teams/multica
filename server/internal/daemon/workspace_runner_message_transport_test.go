package daemon

import "testing"

func TestWorkspaceRunnerMessageTransportFencesReplacedConnection(t *testing.T) {
	d := New(Config{}, nil)
	d.messageCoordinatorMu.Lock()
	d.messageRuntimeIDs["agent-1"] = "runtime-1"
	d.messageCoordinatorMu.Unlock()
	d.mu.Lock()
	d.runtimeIndex["runtime-1"] = Runtime{ID: "runtime-1", WorkspaceID: "workspace-1"}
	d.mu.Unlock()

	var first, second int
	firstGeneration := d.attachWorkspaceRunnerMessageTransport("workspace-1", func(string, any) error {
		first++
		return nil
	})
	secondGeneration := d.attachWorkspaceRunnerMessageTransport("workspace-1", func(string, any) error {
		second++
		return nil
	})
	d.detachWorkspaceRunnerMessageTransport("workspace-1", firstGeneration)
	if !d.sendAgentMessageRunnerFrame("agent-1", "agent:recovery:request", map[string]any{"request": 1}) {
		t.Fatal("current Runner transport did not receive Message frame")
	}
	if first != 0 || second != 1 {
		t.Fatalf("Message transport delivery first=%d second=%d, want 0/1", first, second)
	}
	d.detachWorkspaceRunnerMessageTransport("workspace-1", secondGeneration)
	if d.sendAgentMessageRunnerFrame("agent-1", "agent:recovery:request", nil) {
		t.Fatal("detached Runner transport accepted Message frame")
	}
}
