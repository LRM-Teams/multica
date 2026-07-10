package radar

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestContextBuilderReadsAgentPlanFiles(t *testing.T) {
	root := t.TempDir()
	workspaceID := "workspace-1"
	agentID := "agent-1"
	agentRoot := filepath.Join(root, workspaceID, ".multica", "agents", agentID)
	for path, content := range map[string]string{
		"notes/agent-plan.md": "# Agent Plan\n\n## Watchlist\n- Watch backend auth\n",
		"memory/MEMORY.md":    "# Memory\n\nOwns backend integrations.\n",
		"memory/STATE.md":     "# State\n\nInvestigating GitHub files.\n",
		"notes/work-log.md":   "# Work Log\n\nFixed compose env.\n",
	} {
		full := filepath.Join(agentRoot, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	ctx, err := NewContextBuilder(nil, root).Build(t.Context(), workspaceID, agentID)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Watch backend auth", "Owns backend integrations", "Investigating GitHub files", "Fixed compose env"} {
		if !strings.Contains(ctx.Markdown, want) {
			t.Fatalf("context missing %q:\n%s", want, ctx.Markdown)
		}
	}
}
