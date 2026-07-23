package daemon

import (
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/internal/daemon/execenv"
)

func TestCanonicalAgentWorkspaceLayoutMatchesExistingAgentRoot(t *testing.T) {
	cfg := Config{WorkspacesRoot: t.TempDir()}
	workspaceID := uuid.NewString()
	agentID := uuid.NewString()

	want := multicaAgentRoot(cfg, workspaceID, agentID)
	got := execenv.PredictAgentRootDir(cfg.WorkspacesRoot, workspaceID, agentID)
	if got != want {
		t.Fatalf("D2 agent root %q diverges from existing agent root %q", got, want)
	}
}

func TestD2WorkspaceContractsRemainInertUntilHardCutover(t *testing.T) {
	for _, fileName := range []string{"daemon.go", "health.go"} {
		raw, err := os.ReadFile(fileName)
		if err != nil {
			t.Fatalf("read %s: %v", fileName, err)
		}
		source := string(raw)
		for _, dormantCall := range []string{
			"PredictAgentRootDir(",
			"ProvisionAgentWorkspace(",
			"ProvisionAgentTurn(",
			"MaterializeAgentRepo(",
			"CleanupAgentTurn(",
			"RemoveAgentWorkspace(",
		} {
			if strings.Contains(source, dormantCall) {
				t.Errorf("%s activates dormant D2 call %q before D6", fileName, dormantCall)
			}
		}
	}
}
