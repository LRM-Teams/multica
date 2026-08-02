package handler

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
)

// TestListAgentRuntimeConnectivityByIDs_UnwiredColumnArrivesForFree is task
// #84's self-contained acceptance test: sqlc.embed(agent_runtime) must return
// the whole agent_runtime row, including columns attachAgentRuntimeNames's
// old hand-listed SELECT never selected and no Go code here scans for
// explicitly. `provider` is a real, pre-existing column that was never part
// of that old column list — proof that a column reaches this query's result
// the moment it exists on the table, without any SELECT/scan edit, is proof
// the same holds for any column added after this PR (the acceptance bar
// asked for: "add a column, don't touch scan code, it shows up").
func TestListAgentRuntimeConnectivityByIDs_UnwiredColumnArrivesForFree(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	_, runtimeID := createAgentHealthFixture(t, "online", time.Now(), time.Now())

	rows, err := testHandler.Queries.ListAgentRuntimeConnectivityByIDs(context.Background(), []pgtype.UUID{parseUUID(runtimeID)})
	if err != nil {
		t.Fatalf("ListAgentRuntimeConnectivityByIDs: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1", len(rows))
	}
	if got := rows[0].AgentRuntime.Provider; got != "health-test" {
		t.Fatalf("AgentRuntime.Provider = %q, want %q — sqlc.embed must return every agent_runtime column, not just the ones a caller happens to read today", got, "health-test")
	}
}
