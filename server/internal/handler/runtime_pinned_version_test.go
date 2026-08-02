package handler

import (
	"context"
	"testing"
	"time"
)

// TestAttachAgentRuntimeNames_PinnedRuntimeShowsPinnedVersion is the wiring
// check for the primary agent-list/detail endpoint (GET /agents, GetAgent),
// which goes through attachAgentRuntimeNames's own hand-rolled raw-SQL query
// rather than a sqlc-generated `SELECT *`. Written before the fix landed in
// this same PR (task #81 / #1801-#1803's "done but unreachable" lesson: a
// new agent_runtime column being correctly modeled means nothing if this
// specific hand-written SELECT never selects it) — confirm-broken by
// stripping pinned_version from the SELECT/scan/assignment and rerunning
// this test; it must fail before this PR and pass after.
func TestAttachAgentRuntimeNames_PinnedRuntimeShowsPinnedVersion(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	agentID, runtimeID := createAgentHealthFixture(t, "online", time.Now(), time.Now())
	if _, err := testPool.Exec(context.Background(), `
		UPDATE agent_runtime SET pinned_version = '0.3.85' WHERE id = $1
	`, runtimeID); err != nil {
		t.Fatalf("seed pinned_version: %v", err)
	}

	resps := []AgentResponse{{ID: agentID, RuntimeID: runtimeID, Status: "idle"}}
	testHandler.attachAgentRuntimeNames(context.Background(), resps)

	if resps[0].RuntimePinnedVersion == nil || *resps[0].RuntimePinnedVersion != "0.3.85" {
		t.Fatalf("RuntimePinnedVersion = %v, want \"0.3.85\" — the agent-list endpoint must surface agent_runtime.pinned_version",
			resps[0].RuntimePinnedVersion)
	}
}

// TestAttachAgentRuntimeNames_UnpinnedRuntimeOmitsPinnedVersion is the
// negative case: a runtime with no pin must not show a pinned_version, and
// the JSON field must be omitted (omitempty), not present-but-null — the
// UI's "affordance disappears if the field is simply absent" contract
// (API Response Compatibility rule) depends on the field's presence itself
// meaning "pinned."
func TestAttachAgentRuntimeNames_UnpinnedRuntimeOmitsPinnedVersion(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	agentID, runtimeID := createAgentHealthFixture(t, "online", time.Now(), time.Now())

	resps := []AgentResponse{{ID: agentID, RuntimeID: runtimeID, Status: "idle"}}
	testHandler.attachAgentRuntimeNames(context.Background(), resps)

	if resps[0].RuntimePinnedVersion != nil {
		t.Fatalf("RuntimePinnedVersion = %v, want nil for an unpinned runtime", *resps[0].RuntimePinnedVersion)
	}
}
