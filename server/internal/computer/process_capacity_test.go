package computer

import (
	"testing"
	"time"
)

func TestProcessCapacityIsMachineWideAndPromotesFIFO(t *testing.T) {
	capacity := NewProcessCapacity(1)
	first, admitted := capacity.Acquire(ProcessCapacityRequest{WorkspaceID: "workspace-a", AgentID: "agent-a", RuntimeID: "runtime-a", LaunchID: "launch-a"})
	if !admitted {
		t.Fatal("first Computer capacity request was not admitted")
	}
	promoted := make(chan ProcessCapacityGrant, 1)
	second, admitted := capacity.Acquire(ProcessCapacityRequest{
		WorkspaceID: "workspace-b", AgentID: "agent-b", RuntimeID: "runtime-b", LaunchID: "launch-b",
		Waiter: func(grant ProcessCapacityGrant) { promoted <- grant },
	})
	if admitted {
		t.Fatal("second Binding bypassed the machine-wide process cap")
	}
	capacity.Release(first)
	select {
	case grant := <-promoted:
		if grant != second || !capacity.Active(grant) {
			t.Fatalf("promoted grant = %+v active=%v, want %+v", grant, capacity.Active(grant), second)
		}
	case <-time.After(time.Second):
		t.Fatal("Computer capacity did not promote the queued sibling Binding")
	}
}
