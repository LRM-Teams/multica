package handler

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/internal/researchrun"
)

func TestResearchV6AgentLifecycleCreateAgentUsesMembershipGeneration(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	templateAgentID := createHandlerTestAgent(t, "v6-runtime-template-"+uuid.NewString()[:8], nil)
	tx, err := testPool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin research fixture transaction: %v", err)
	}
	defer tx.Rollback(ctx)
	var sessionID string
	if err = tx.QueryRow(ctx, `
		INSERT INTO research_session (
			workspace_id, created_by, title, goal, status, orchestrator_version
		) VALUES ($1::uuid, $2::uuid, $3, $4, 'running', 'research-run-v6')
		RETURNING id::text
	`, testWorkspaceID, testUserID, "V6 runtime adapter fixture", "Create a V6 agent from an active team template").Scan(&sessionID); err != nil {
		t.Fatalf("create research session: %v", err)
	}
	if _, err = tx.Exec(ctx, `SELECT research_ensure_run_session_passport($1::uuid, $2::uuid)`, testWorkspaceID, sessionID); err != nil {
		t.Fatalf("ensure research session passport: %v", err)
	}
	if _, err = tx.Exec(ctx, `
		INSERT INTO research_team_membership (
			workspace_id, session_id, agent_id, membership_generation,
			mission_prompt, mission_hash, mission_revision, state
		) VALUES (
			$1::uuid, $2::uuid, $3::uuid, 1,
			'template mission', 'sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa', 1, 'idle'
		)
	`, testWorkspaceID, sessionID, templateAgentID); err != nil {
		t.Fatalf("create research team membership: %v", err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatalf("commit research fixture transaction: %v", err)
	}

	idempotencyKey := "v6-create-agent-test:" + uuid.NewString()
	var createdAgentID string
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM research_v6_runtime_effect WHERE workspace_id=$1::uuid AND effect_kind='create_agent' AND idempotency_key=$2`, testWorkspaceID, idempotencyKey)
		if createdAgentID != "" {
			_, _ = testPool.Exec(context.Background(), `DELETE FROM agent WHERE id=$1::uuid`, createdAgentID)
		}
		_, _ = testPool.Exec(context.Background(), `DELETE FROM research_session WHERE id=$1::uuid`, sessionID)
	})

	adapter := &researchV6AgentLifecycleAdapter{handler: testHandler}
	createdAgentID, err = adapter.CreateAgent(ctx, testWorkspaceID, idempotencyKey, researchrun.V6AgentSpec{
		Name:          "V6 source scout",
		Capability:    "Find primary sources",
		MissionPrompt: "Gather primary evidence and preserve lineage.",
		ModelConfig:   json.RawMessage(`{"model":"test-v6-model","thinking_level":"high"}`),
	})
	if err != nil {
		t.Fatalf("create V6 agent: %v", err)
	}

	var displayName, model, thinkingLevel, runtimeID string
	if err = testPool.QueryRow(ctx, `
		SELECT display_name, model, thinking_level, runtime_id::text
		FROM agent WHERE id=$1::uuid
	`, createdAgentID).Scan(&displayName, &model, &thinkingLevel, &runtimeID); err != nil {
		t.Fatalf("load created V6 agent: %v", err)
	}
	if displayName != "V6 source scout" || model != "test-v6-model" || thinkingLevel != "high" || runtimeID == "" {
		t.Fatalf("created V6 agent = display:%q model:%q thinking:%q runtime:%q", displayName, model, thinkingLevel, runtimeID)
	}
}
