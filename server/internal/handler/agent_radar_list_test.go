package handler

import (
	"context"
	"testing"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func TestListAgentRadarActionsByRunsBatchesAndGroups(t *testing.T) {
	ctx := context.Background()
	agentID := createHandlerTestAgent(t, "radar-list-batch", nil)

	var firstRunID, secondRunID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_radar_run (workspace_id, agent_id, trigger_kind, trigger_ref, status, cooldown_key, context_summary)
		VALUES ($1, $2, 'manual', 'first', 'succeeded', 'test-first', 'first')
		RETURNING id`, testWorkspaceID, agentID).Scan(&firstRunID); err != nil {
		t.Fatalf("insert first radar run: %v", err)
	}
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_radar_run (workspace_id, agent_id, trigger_kind, trigger_ref, status, cooldown_key, context_summary)
		VALUES ($1, $2, 'manual', 'second', 'succeeded', 'test-second', 'second')
		RETURNING id`, testWorkspaceID, agentID).Scan(&secondRunID); err != nil {
		t.Fatalf("insert second radar run: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(ctx, `DELETE FROM agent_radar_run WHERE id = ANY($1::uuid[])`, []string{firstRunID, secondRunID})
	})

	for _, in := range []struct{ runID, dedupe string }{{firstRunID, "a"}, {firstRunID, "b"}, {secondRunID, "c"}} {
		if _, err := testPool.Exec(ctx, `
			INSERT INTO agent_radar_action (radar_run_id, workspace_id, agent_id, action_type, status, risk_level, confidence, dedupe_key, target_kind, reason, evidence, payload)
			VALUES ($1, $2, $3, 'no_action', 'proposed', 'low', 'high', $4, 'none', 'test', '[]'::jsonb, '{}'::jsonb)`, in.runID, testWorkspaceID, agentID, in.dedupe); err != nil {
			t.Fatalf("insert radar action %s: %v", in.dedupe, err)
		}
	}

	runs, err := testHandler.Queries.ListAgentRadarRunsByAgent(ctx, db.ListAgentRadarRunsByAgentParams{WorkspaceID: parseUUID(testWorkspaceID), AgentID: parseUUID(agentID), Limit: 20})
	if err != nil {
		t.Fatalf("list radar runs: %v", err)
	}
	grouped, err := testHandler.listAgentRadarActionsByRuns(ctx, runs)
	if err != nil {
		t.Fatalf("list actions by runs: %v", err)
	}
	if got := len(grouped[firstRunID]); got != 2 {
		t.Fatalf("first run actions = %d, want 2", got)
	}
	if got := len(grouped[secondRunID]); got != 1 {
		t.Fatalf("second run actions = %d, want 1", got)
	}

	empty, err := testHandler.listAgentRadarActionsByRuns(ctx, nil)
	if err != nil {
		t.Fatalf("empty batch errored: %v", err)
	}
	if len(empty) != 0 {
		t.Fatalf("empty batch returned %d groups, want 0", len(empty))
	}
}
