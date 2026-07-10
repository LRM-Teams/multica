package radar

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
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

func TestContextBuilderAssignedIssuesUsesWorkspacePrefixAndNumber(t *testing.T) {
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		dbURL = "postgres://multica:multica@localhost:5432/multica?sslmode=disable"
	}
	pool, err := pgxpool.New(t.Context(), dbURL)
	if err != nil {
		t.Skipf("database unavailable: %v", err)
	}
	defer pool.Close()
	if err := pool.Ping(t.Context()); err != nil {
		t.Skipf("database unavailable: %v", err)
	}

	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")
	var workspaceID, runtimeID, agentID string
	if err := pool.QueryRow(t.Context(), `
		INSERT INTO workspace (name, slug, issue_prefix)
		VALUES ($1, $2, 'RAD')
		RETURNING id
	`, "Radar Test", "radar-"+suffix).Scan(&workspaceID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(t.Context(), `DELETE FROM workspace WHERE id = $1`, workspaceID)
	})
	if err := pool.QueryRow(t.Context(), `
		INSERT INTO agent_runtime (workspace_id, name, runtime_mode, provider, status, device_info, metadata)
		VALUES ($1, 'Radar Runtime', 'local', 'claude', 'online', '', '{}'::jsonb)
		RETURNING id
	`, workspaceID).Scan(&runtimeID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(t.Context(), `
		INSERT INTO agent (workspace_id, name, display_name, runtime_mode, runtime_config, runtime_id, visibility, max_concurrent_tasks, owner_id)
		VALUES ($1, 'radar-agent', 'Radar Agent', 'local', '{}'::jsonb, $2, 'workspace', 1, NULL)
		RETURNING id
	`, workspaceID, runtimeID).Scan(&agentID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), `
		INSERT INTO issue (workspace_id, number, title, status, priority, assignee_type, assignee_id, creator_type, position)
		VALUES ($1, 7, 'Investigate radar', 'todo', 'none', 'agent', $2, 'member', 0)
	`, workspaceID, agentID); err != nil {
		t.Fatal(err)
	}

	ctx, err := NewContextBuilder(pool, "").Build(t.Context(), workspaceID, agentID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(ctx.Markdown, "RAD-7 Investigate radar [todo]") {
		t.Fatalf("assigned issue missing from context:\n%s", ctx.Markdown)
	}
}
