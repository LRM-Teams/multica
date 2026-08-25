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
	foreignTemplateAgentID := createHandlerTestAgent(t, "v6-runtime-foreign-template-"+uuid.NewString()[:8], nil)
	if _, err := testPool.Exec(ctx, `
		UPDATE agent
		SET model='foreign-run-model', thinking_level='low'
		WHERE id=$1::uuid
	`, foreignTemplateAgentID); err != nil {
		t.Fatalf("configure foreign V6 runtime template: %v", err)
	}
	templateAgentID := createHandlerTestAgent(t, "v6-runtime-template-"+uuid.NewString()[:8], nil)
	if _, err := testPool.Exec(ctx, `
		UPDATE agent
		SET model='template-v6-model', thinking_level='high',
		    mcp_config='{"servers":{"template":{"command":"template-mcp"}}}'::jsonb
		WHERE id=$1::uuid
	`, templateAgentID); err != nil {
		t.Fatalf("configure V6 runtime template: %v", err)
	}
	tx, err := testPool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin research fixture transaction: %v", err)
	}
	defer tx.Rollback(ctx)
	var foreignSessionID string
	if err = tx.QueryRow(ctx, `
		INSERT INTO research_session (
			workspace_id, created_by, title, goal, status, orchestrator_version
		) VALUES ($1::uuid, $2::uuid, $3, $4, 'running', 'research-run-v6')
		RETURNING id::text
	`, testWorkspaceID, testUserID, "Foreign V6 runtime adapter fixture", "Must not supply another Run's template").Scan(&foreignSessionID); err != nil {
		t.Fatalf("create foreign research session: %v", err)
	}
	if _, err = tx.Exec(ctx, `SELECT research_ensure_run_session_passport($1::uuid, $2::uuid)`, testWorkspaceID, foreignSessionID); err != nil {
		t.Fatalf("ensure foreign research session passport: %v", err)
	}
	if _, err = tx.Exec(ctx, `
		INSERT INTO research_team_membership (
			workspace_id, session_id, agent_id, membership_generation,
			mission_prompt, mission_hash, mission_revision, state
		) VALUES (
			$1::uuid, $2::uuid, $3::uuid, 1,
			'foreign template mission', 'sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb', 1, 'idle'
		)
	`, testWorkspaceID, foreignSessionID, foreignTemplateAgentID); err != nil {
		t.Fatalf("create foreign research team membership: %v", err)
	}
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
			$1::uuid, $2::uuid, $3::uuid, 2,
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
		_, _ = testPool.Exec(context.Background(), `DELETE FROM research_session WHERE id=ANY($1::uuid[])`, []string{sessionID, foreignSessionID})
	})

	adapter := &researchV6AgentLifecycleAdapter{handler: testHandler}
	createdAgentID, err = adapter.CreateAgent(ctx, testWorkspaceID, sessionID, idempotencyKey, researchrun.V6AgentSpec{
		Name:          "V6 source scout",
		Capability:    "Find primary sources",
		MissionPrompt: "Gather primary evidence and preserve lineage.",
		ModelConfig:   json.RawMessage(`{"model":"default","thinking_level":"high"}`),
		ToolConfig:    json.RawMessage(`{"allowed_tools":["web_search","read"]}`),
	})
	if err != nil {
		t.Fatalf("create V6 agent: %v", err)
	}

	var displayName, model, thinkingLevel, runtimeID, mcpConfig string
	if err = testPool.QueryRow(ctx, `
		SELECT display_name, model, thinking_level, runtime_id::text, mcp_config::text
		FROM agent WHERE id=$1::uuid
	`, createdAgentID).Scan(&displayName, &model, &thinkingLevel, &runtimeID, &mcpConfig); err != nil {
		t.Fatalf("load created V6 agent: %v", err)
	}
	if displayName != "V6 source scout" || model != "template-v6-model" || thinkingLevel != "high" || runtimeID == "" || mcpConfig != `{"servers": {"template": {"command": "template-mcp"}}}` {
		t.Fatalf("created V6 agent = display:%q model:%q thinking:%q runtime:%q mcp:%q", displayName, model, thinkingLevel, runtimeID, mcpConfig)
	}
}

// Director-generated idempotency keys (e.g. "create_agent.cross_validator.v1")
// repeat across runs. Receipts must be run-scoped: the same key redelivered in
// one run converges on one agent, while a different run mints a fresh agent
// instead of adopting the previous run's mission-stale one.
func TestResearchV6AgentLifecycleCreateAgentScopesReceiptsByRun(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	templateAgentID := createHandlerTestAgent(t, "v6-run-scope-template-"+uuid.NewString()[:8], nil)

	// Session insert and passport creation must share one transaction: the
	// deferred research_session_artifact_passport_guard fires at commit.
	newRunSession := func(title string) string {
		t.Helper()
		tx, err := testPool.Begin(ctx)
		if err != nil {
			t.Fatalf("begin session fixture transaction: %v", err)
		}
		defer tx.Rollback(ctx)
		var sessionID string
		if err := tx.QueryRow(ctx, `
			INSERT INTO research_session (
				workspace_id, created_by, title, goal, status, orchestrator_version
			) VALUES ($1::uuid, $2::uuid, $3, $4, 'running', 'research-run-v6')
			RETURNING id::text
		`, testWorkspaceID, testUserID, title, "Receipt run-scoping fixture").Scan(&sessionID); err != nil {
			t.Fatalf("create research session: %v", err)
		}
		if _, err := tx.Exec(ctx, `SELECT research_ensure_run_session_passport($1::uuid, $2::uuid)`, testWorkspaceID, sessionID); err != nil {
			t.Fatalf("ensure research session passport: %v", err)
		}
		if err := tx.Commit(ctx); err != nil {
			t.Fatalf("commit session fixture transaction: %v", err)
		}
		return sessionID
	}
	runA := newRunSession("V6 receipt scope run A")
	runB := newRunSession("V6 receipt scope run B")
	if _, err := testPool.Exec(ctx, `
		INSERT INTO research_team_membership (
			workspace_id, session_id, agent_id, membership_generation,
			mission_prompt, mission_hash, mission_revision, state
		) VALUES (
			$1::uuid, $2::uuid, $3::uuid, 1,
			'template mission', 'sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa', 1, 'idle'
		), (
			$1::uuid, $4::uuid, $3::uuid, 1,
			'template mission', 'sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb', 1, 'idle'
		)
	`, testWorkspaceID, runA, templateAgentID, runB); err != nil {
		t.Fatalf("create run-scoped template memberships: %v", err)
	}

	idempotencyKey := "create_agent.cross_validator.v1-" + uuid.NewString()[:8]
	var createdAgentIDs []string
	t.Cleanup(func() {
		cleanupCtx := context.Background()
		_, _ = testPool.Exec(cleanupCtx, `DELETE FROM research_v6_runtime_effect WHERE workspace_id=$1::uuid AND idempotency_key=$2`, testWorkspaceID, idempotencyKey)
		for _, agentID := range createdAgentIDs {
			_, _ = testPool.Exec(cleanupCtx, `DELETE FROM agent WHERE id=$1::uuid`, agentID)
		}
		_, _ = testPool.Exec(cleanupCtx, `DELETE FROM research_session WHERE id=ANY($1::uuid[])`, []string{runA, runB})
	})

	adapter := &researchV6AgentLifecycleAdapter{handler: testHandler}
	spec := researchrun.V6AgentSpec{Name: "交叉验证员", Capability: "cross_validator", MissionPrompt: "Run A mission"}

	firstID, err := adapter.CreateAgent(ctx, testWorkspaceID, runA, idempotencyKey, spec)
	if err != nil {
		t.Fatalf("create agent in run A: %v", err)
	}
	createdAgentIDs = append(createdAgentIDs, firstID)

	redeliveredID, err := adapter.CreateAgent(ctx, testWorkspaceID, runA, idempotencyKey, spec)
	if err != nil {
		t.Fatalf("redeliver create agent in run A: %v", err)
	}
	if redeliveredID != firstID {
		t.Fatalf("same-run redelivery minted a new agent: first=%s redelivered=%s", firstID, redeliveredID)
	}

	specB := spec
	specB.MissionPrompt = "Run B mission"
	otherRunID, err := adapter.CreateAgent(ctx, testWorkspaceID, runB, idempotencyKey, specB)
	if err != nil {
		t.Fatalf("create agent in run B: %v", err)
	}
	createdAgentIDs = append(createdAgentIDs, otherRunID)
	if otherRunID == firstID {
		t.Fatalf("run B adopted run A's agent %s for key %q", firstID, idempotencyKey)
	}

	var instructions string
	if err := testPool.QueryRow(ctx, `SELECT instructions FROM agent WHERE id=$1::uuid`, otherRunID).Scan(&instructions); err != nil {
		t.Fatalf("load run B agent: %v", err)
	}
	if instructions != "Run B mission" {
		t.Fatalf("run B agent carries stale instructions %q", instructions)
	}
}
