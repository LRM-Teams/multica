package handler

import (
	"context"
	"testing"

	"github.com/multica-ai/multica/server/internal/memorygraph"
)

func TestGraphMemoryLegacyMigrationPreservesTTTDisabledProfile(t *testing.T) {
	if testPool == nil {
		t.Skip("handler test database unavailable")
	}
	ctx := context.Background()
	workspaceID := createGraphMemoryTestWorkspace(t)
	mustGraphMemoryMember(t, workspaceID, "owner")
	const savedK = 7
	if _, err := testPool.Exec(ctx, `
		INSERT INTO graph_memory_profile (workspace_id, memory_type, ttt_enabled, explore_agents)
		VALUES ($1, 'graph', false, $2)`, workspaceID.String(), savedK); err != nil {
		t.Fatal(err)
	}

	store := memorygraph.NewStore(t.TempDir())
	if err := store.Init(); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendQueryLog("legacy", &memorygraph.QueryLogEntry{Query: "legacy", Version: 1, JudgeDone: true, JudgeScore: 0.5}); err != nil {
		t.Fatal(err)
	}
	if _, err := memorygraph.MigrateLegacyQueryLogs(store); err != nil {
		t.Fatal(err)
	}

	var (
		tttEnabled bool
		exploreK   int
	)
	if err := testPool.QueryRow(ctx, `SELECT ttt_enabled, explore_agents FROM graph_memory_profile WHERE workspace_id = $1`, workspaceID.String()).Scan(&tttEnabled, &exploreK); err != nil {
		t.Fatal(err)
	}
	if tttEnabled || exploreK != savedK {
		t.Fatalf("profile after legacy migration = ttt_enabled=%v explore_agents=%d, want false/%d", tttEnabled, exploreK, savedK)
	}
}
