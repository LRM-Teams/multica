package daemon

import (
	"context"
	"testing"
)

func TestWorkspaceRunnerReconciliationRetainsSiblingsAndCancelsRemovedBinding(t *testing.T) {
	d := New(Config{DaemonID: "daemon-test"}, nil)
	d.workspaces = map[string]*workspaceState{
		"workspace-a": newWorkspaceState("workspace-a", []string{"runtime-a"}),
		"workspace-b": newWorkspaceState("workspace-b", nil),
	}
	started := make(map[string]context.Context)
	d.workspaceRunnerRun = func(runner *WorkspaceRunner, ctx context.Context) { started[runner.config.WorkspaceID] = ctx }
	d.reconcileWorkspaceRunners(context.Background())
	firstA := d.currentWorkspaceRunner("workspace-a")
	firstB := d.currentWorkspaceRunner("workspace-b")
	if firstA == nil || firstB == nil || len(started) != 2 {
		t.Fatalf("initial Binding reconciliation runners=%p,%p starts=%d", firstA, firstB, len(started))
	}

	// Runtime membership is mutable input. Reconciliation must retain both
	// runner objects, including the zero-Runtime Binding.
	d.mu.Lock()
	d.workspaces["workspace-a"] = newWorkspaceState("workspace-a", []string{"runtime-a", "runtime-a2"})
	d.mu.Unlock()
	d.reconcileWorkspaceRunners(context.Background())
	if d.currentWorkspaceRunner("workspace-a") != firstA || d.currentWorkspaceRunner("workspace-b") != firstB || len(started) != 2 {
		t.Fatal("runtime change replaced a stable Workspace Runner")
	}

	d.mu.Lock()
	delete(d.workspaces, "workspace-b")
	d.mu.Unlock()
	d.reconcileWorkspaceRunners(context.Background())
	if d.currentWorkspaceRunner("workspace-a") != firstA {
		t.Fatal("removing sibling Binding replaced unchanged Runner")
	}
	if d.currentWorkspaceRunner("workspace-b") != nil {
		t.Fatal("removed Binding retained its Runner")
	}
	select {
	case <-started["workspace-b"].Done():
	default:
		t.Fatal("removed Binding did not cancel only its Runner")
	}
	select {
	case <-started["workspace-a"].Done():
		t.Fatal("sibling Runner was cancelled")
	default:
	}
}
