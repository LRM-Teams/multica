package daemon

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestValidateWorkspaceStatePathRejectsSymlinkedWorkspace(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink permissions vary on Windows")
	}
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "workspace-1")); err != nil {
		t.Fatalf("create workspace symlink: %v", err)
	}
	if err := validateWorkspaceStatePath(root, "workspace-1"); err == nil {
		t.Fatal("symlinked workspace path was accepted")
	}
}

func TestValidateStatePathPartRejectsTraversal(t *testing.T) {
	for _, value := range []string{"", ".", "..", "../workspace", "/workspace", `workspace\agent`} {
		if err := validateStatePathPart("workspace", value); err == nil {
			t.Fatalf("invalid path component %q was accepted", value)
		}
	}
}
