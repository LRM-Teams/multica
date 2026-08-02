package handler

import (
	"context"
	"testing"
)

// TestQueriesListAgentRuntimes_ScansAtLeastOneRealRow pins the fix for a
// live production bug found 2026-08-02 while reviewing task #81's PR #1817:
// ListAgentRuntimes' generated query text (SELECT ..., offline_reason,
// starting_since — 18 columns) had drifted ahead of its Scan() call (which
// stopped at OfflineReason — 17 destinations) ever since #1802 merged
// starting_since. pgx returns "number of field descriptions must equal
// number of destinations, got 18 and 17" for ANY row — but a test that only
// asserts "no error" against an empty result set (zero rows never reaches
// Scan() at all) would stay green forever regardless of what Scan() lists.
// This test seeds a real row and asserts the query returns it, specifically
// to close that blind spot (Nash's finding, same review).
func TestQueriesListAgentRuntimes_ScansAtLeastOneRealRow(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	runtimeID := handlerTestRuntimeID(t)

	rows, err := testHandler.Queries.ListAgentRuntimes(ctx, parseUUID(testWorkspaceID))
	if err != nil {
		t.Fatalf("ListAgentRuntimes: %v", err)
	}
	if len(rows) == 0 {
		t.Fatal("ListAgentRuntimes returned 0 rows — this test seeds a fixture runtime, a zero-row result means the fixture setup broke, not that the query is safe to assume works")
	}
	found := false
	for _, rt := range rows {
		if uuidToString(rt.ID) == runtimeID {
			found = true
		}
	}
	if !found {
		t.Fatalf("ListAgentRuntimes did not include the fixture runtime %s among %d rows", runtimeID, len(rows))
	}
}
