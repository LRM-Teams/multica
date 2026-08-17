package memorygraph

import (
	"path/filepath"
	"testing"

	"github.com/google/uuid"
)

func TestDirForScopeCanonicalLayout(t *testing.T) {
	root := t.TempDir()
	ws, pid, cid := uuid.NewString(), uuid.NewString(), uuid.NewString()

	projDir, err := DirForScope(root, ws, GraphDirKindProject, pid)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(root, ws, "memory_graph", "projects", pid); projDir != want {
		t.Errorf("project dir = %q, want %q", projDir, want)
	}
	chanDir, err := DirForScope(root, ws, GraphDirKindChannel, cid)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(root, ws, "memory_graph", "channels", cid); chanDir != want {
		t.Errorf("channel dir = %q, want %q", chanDir, want)
	}
}

func TestDirForScopeRejectsNonUUIDAndTraversal(t *testing.T) {
	root := t.TempDir()
	ws := uuid.NewString()
	for _, bad := range []string{"", "..", "../../etc", "not-a-uuid", ws + "/../" + ws} {
		if _, err := DirForScope(root, ws, GraphDirKindProject, bad); err == nil {
			t.Errorf("DirForScope(ownerID=%q) must fail closed", bad)
		}
	}
	if _, err := DirForScope(root, "../escape", GraphDirKindProject, uuid.NewString()); err == nil {
		t.Error("DirForScope must reject a non-UUID workspace id")
	}
}

func TestEnsureScopedDirCreatesWithIdentityAndFailsClosedOnMismatch(t *testing.T) {
	root := t.TempDir()
	ws, pid := uuid.NewString(), uuid.NewString()

	dir, err := EnsureScopedDir(root, ws, GraphDirKindProject, pid)
	if err != nil {
		t.Fatal(err)
	}
	id, err := ReadGraphIdentity(dir)
	if err != nil {
		t.Fatal(err)
	}
	if id != (GraphIdentity{WorkspaceID: ws, Kind: "project", OwnerID: pid}) {
		t.Errorf("identity = %+v", id)
	}
	// Second call is idempotent.
	if _, err := EnsureScopedDir(root, ws, GraphDirKindProject, pid); err != nil {
		t.Fatal(err)
	}
	// Identity mismatch fails closed.
	err = VerifyGraphIdentity(dir, GraphIdentity{WorkspaceID: ws, Kind: "project", OwnerID: uuid.NewString()})
	if err == nil {
		t.Error("VerifyGraphIdentity must fail closed on owner mismatch")
	}
}
