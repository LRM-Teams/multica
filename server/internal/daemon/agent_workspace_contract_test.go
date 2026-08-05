package daemon

import (
	"testing"

	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/internal/agentworkspace"
	"github.com/multica-ai/multica/server/internal/daemon/execenv"
)

func TestCanonicalAgentWorkspaceLayoutMatchesExistingAgentRoot(t *testing.T) {
	cfg := Config{WorkspacesRoot: t.TempDir()}
	workspaceID := uuid.NewString()
	agentID := uuid.NewString()

	want := agentworkspace.Root(cfg.WorkspacesRoot, workspaceID, agentID)
	got := execenv.PredictAgentRootDir(cfg.WorkspacesRoot, workspaceID, agentID)
	if got != want {
		t.Fatalf("D2 agent root %q diverges from existing agent root %q", got, want)
	}
}

func TestCanonicalAgentWorkspaceIsTheWorkingDirectory(t *testing.T) {
	layout, err := execenv.ResolveAgentWorkspaceLayout(t.TempDir(), uuid.NewString(), uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	if layout.AgentRoot == "" {
		t.Fatal("agent root is empty")
	}
}
