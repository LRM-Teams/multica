package computer

import (
	"os"
	"path/filepath"
	"testing"
)

func newTestBindings(t *testing.T) (*BindingsStore, string) {
	t.Helper()
	root := t.TempDir()
	return NewBindingsStore(root), root
}

func TestBindingsPersistAndReloadStable(t *testing.T) {
	s, _ := newTestBindings(t)
	s.AddOrRepair(WorkspaceBinding{WorkspaceID: "ws-1", WorkspaceSlug: "alpha", ComputerID: "comp-1", Active: true})

	// Reload through a fresh store instance (simulates restart).
	again := NewBindingsStore(s.root)
	all, err := again.All()
	if err != nil {
		t.Fatalf("All: %v", err)
	}
	if len(all) != 1 || all[0].WorkspaceID != "ws-1" || all[0].WorkspaceSlug != "alpha" {
		t.Fatalf("reload = %+v", all)
	}
}

func TestBindingsAddOrRepairIsIdempotent(t *testing.T) {
	s, _ := newTestBindings(t)
	s.AddOrRepair(WorkspaceBinding{WorkspaceID: "ws-1", WorkspaceSlug: "alpha", Active: true})
	// Repeat for the same immutable workspace_id must repair, not duplicate.
	s.AddOrRepair(WorkspaceBinding{WorkspaceID: "ws-1", WorkspaceSlug: "renamed", ComputerID: "comp-1", Credential: "new-cred", Active: true})

	all, _ := s.All()
	if len(all) != 1 {
		t.Fatalf("AddOrRepair duplicated the binding: %d entries", len(all))
	}
	b, ok, _ := s.Get("ws-1")
	if !ok || b.Credential != "new-cred" || b.WorkspaceSlug != "renamed" {
		t.Fatalf("binding not repaired: %+v (ok=%v)", b, ok)
	}
}

func TestBindingsMultipleSiblingsPreservedOnRemove(t *testing.T) {
	s, _ := newTestBindings(t)
	s.AddOrRepair(WorkspaceBinding{WorkspaceID: "ws-1", WorkspaceSlug: "alpha", Active: true})
	s.AddOrRepair(WorkspaceBinding{WorkspaceID: "ws-2", WorkspaceSlug: "beta", Active: true})

	if err := s.Remove("ws-1"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	all, _ := s.All()
	if len(all) != 1 || all[0].WorkspaceID != "ws-2" {
		t.Fatalf("removing ws-1 disturbed sibling: %+v", all)
	}
	if _, ok, _ := s.Get("ws-1"); ok {
		t.Fatal("removed binding still present")
	}
}

func TestBindingsDoNotDeriveDirsFromSlug(t *testing.T) {
	s, root := newTestBindings(t)
	s.AddOrRepair(WorkspaceBinding{WorkspaceID: "ws-1", WorkspaceSlug: "alpha-team", Active: true})

	// The binding file lives directly under the machine root, NOT under any
	// slug-named directory, and no slug directory is created.
	if filepath.Dir(s.path()) != root {
		t.Fatalf("binding file not at machine root: %s", s.path())
	}
	if _, err := os.Stat(filepath.Join(root, "alpha-team")); !os.IsNotExist(err) {
		t.Fatalf("local state must not be derived from the workspace slug: %v", err)
	}
}

func TestBindingsFilePermissionsAtomic(t *testing.T) {
	s, _ := newTestBindings(t)
	if err := s.AddOrRepair(WorkspaceBinding{WorkspaceID: "ws-1", Active: true}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(s.path())
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("bindings file mode = %o, want 0600", perm)
	}
}

func TestBindingsGetIgnoresInactive(t *testing.T) {
	s, _ := newTestBindings(t)
	s.AddOrRepair(WorkspaceBinding{WorkspaceID: "ws-1", Active: false})
	if _, ok, _ := s.Get("ws-1"); ok {
		t.Fatal("inactive binding reported as active")
	}
}

func TestBindingsInstallValidatesBeforeWriting(t *testing.T) {
	s, _ := newTestBindings(t)
	s.AddOrRepair(WorkspaceBinding{WorkspaceID: "ws-1", WorkspaceSlug: "alpha", Active: true})

	// Invalid request (missing actor) must not mutate existing state.
	bad := BindingRequest{ActorUserID: "", TargetComputerID: "c", TargetWorkspaceID: "ws-2"}
	if err := s.Install(bad, WorkspaceBinding{WorkspaceID: "ws-2", Active: true}); err == nil {
		t.Fatal("Install with invalid request should error")
	}
	all, _ := s.All()
	if len(all) != 1 || all[0].WorkspaceID != "ws-1" {
		t.Fatalf("failed additive install disturbed existing bindings: %+v", all)
	}
}

func TestBindingsInstallAddsSiblingAndRestoreReturnsAllActive(t *testing.T) {
	s, _ := newTestBindings(t)
	ok := BindingRequest{ActorUserID: "u", TargetComputerID: "c", TargetWorkspaceID: "ws-2"}
	if err := s.Install(ok, WorkspaceBinding{WorkspaceID: "ws-2", WorkspaceSlug: "beta", Active: true}); err != nil {
		t.Fatal(err)
	}
	s.AddOrRepair(WorkspaceBinding{WorkspaceID: "ws-1", Active: true})
	s.AddOrRepair(WorkspaceBinding{WorkspaceID: "ws-9", Active: false})

	active, _ := s.AllActive()
	if len(active) != 2 {
		t.Fatalf("AllActive = %d, want 2 (inactive excluded)", len(active))
	}
}

func TestBindingsRemoveValidatedFailsClosed(t *testing.T) {
	s, _ := newTestBindings(t)
	s.AddOrRepair(WorkspaceBinding{WorkspaceID: "ws-1", Active: true})
	req := BindingRequest{ActorUserID: "u", TargetComputerID: "c", TargetWorkspaceID: "missing"}
	if err := s.RemoveValidated(req, "missing"); err == nil {
		t.Fatal("removing a non-existent binding should fail closed")
	}
	all, _ := s.All()
	if len(all) != 1 {
		t.Fatalf("failed removal mutated state: %+v", all)
	}
}
