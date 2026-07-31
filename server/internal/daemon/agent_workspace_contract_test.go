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

// allDormantD2Calls is the full set of D2 calls that stay inert until D6's
// hard cutover — everything except the one explicitly-approved exception
// below.
var allDormantD2Calls = []string{
	"PredictAgentRootDir(",
	"ProvisionAgentWorkspace(",
	"ProvisionAgentTurn(",
	"MaterializeAgentRepo(",
	"CleanupAgentTurn(",
	"RemoveAgentWorkspace(",
}

// dormantD2CallsByFile lets one specific, product-approved call jump the D6
// gate early per file, instead of only being able to allow it everywhere or
// nowhere. health.go's ProvisionAgentWorkspace() call is the sole exception:
// Frank explicitly approved persistent repo/code checkouts ahead of D6 hard
// cutover (2026-07-31, #prj-daemon) — see repoCheckoutHandler's doc comment.
// Every other dormant D2 call (ProvisionAgentTurn, MaterializeAgentRepo,
// CleanupAgentTurn, RemoveAgentWorkspace — and ProvisionAgentWorkspace
// anywhere other than this one call site) still needs the full D6 cutover
// before use.
func dormantD2CallsForFile(fileName string) []string {
	if fileName != "health.go" {
		return allDormantD2Calls
	}
	calls := make([]string, 0, len(allDormantD2Calls))
	for _, call := range allDormantD2Calls {
		if call == "ProvisionAgentWorkspace(" {
			continue
		}
		calls = append(calls, call)
	}
	return calls
}

func TestD2WorkspaceContractsRemainInertUntilHardCutover(t *testing.T) {
	for _, fileName := range []string{"daemon.go", "health.go"} {
		raw, err := os.ReadFile(fileName)
		if err != nil {
			t.Fatalf("read %s: %v", fileName, err)
		}
		source := string(raw)
		for _, dormantCall := range dormantD2CallsForFile(fileName) {
			if strings.Contains(source, dormantCall) {
				t.Errorf("%s activates dormant D2 call %q before D6", fileName, dormantCall)
			}
		}
	}
}
