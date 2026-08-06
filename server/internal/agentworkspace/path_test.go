package agentworkspace

import (
	"path/filepath"
	"testing"
)

func TestCanonicalLayout(t *testing.T) {
	root := filepath.Join("tmp", "multica", "workspaces")
	if got, want := DefaultWorkspacesRoot("/home/alice"), filepath.Join("/home/alice", ".multica", "workspaces"); got != want {
		t.Fatalf("DefaultWorkspacesRoot() = %q, want %q", got, want)
	}
	if got, want := Root(root, "workspace-1", "agent-1"), filepath.Join(root, "workspace-1", "agents", "agent-1"); got != want {
		t.Fatalf("Root() = %q, want %q", got, want)
	}
	if got, want := RootRelPath("workspace-1", "agent-1"), "workspace-1/agents/agent-1"; got != want {
		t.Fatalf("RootRelPath() = %q, want %q", got, want)
	}
}

func TestIDsFromRelPath(t *testing.T) {
	workspaceID, agentID, ok := IDsFromRelPath("workspace-1/agents/agent-1/notes/work-log.md")
	if !ok || workspaceID != "workspace-1" || agentID != "agent-1" {
		t.Fatalf("IDsFromRelPath() = (%q, %q, %t)", workspaceID, agentID, ok)
	}
	if _, _, ok := IDsFromRelPath("workspace-1/tasks/task-1"); ok {
		t.Fatal("IDsFromRelPath accepted a non-Agent path")
	}
}
