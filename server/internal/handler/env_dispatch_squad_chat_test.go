package handler

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// setupSquadChatFixture seeds the entities the chat-path squad dispatch
// touches: a workspace agent that leads a squad, the squad itself (leader_id
// -> that agent), and a chat session bound to that leader agent (runtime_id
// resolved from the agent, mirroring the production CreateChatSession query).
// It returns the leader agent id, the squad id, and the chat session id.
// Cleanup is registered via t.Cleanup.
//
// Mirrors the squad/leader fixture pattern in squad_assign_trigger_test.go /
// env_dispatch_squad_issue_test.go and the chat-session insert pattern in
// daemon_test.go.
func setupSquadChatFixture(t *testing.T) (leaderAgentID, squadID, chatSessionID string) {
	t.Helper()
	ctx := context.Background()

	// A workspace agent with a runtime — eligible to lead a squad.
	leaderAgentID = createHandlerTestAgent(t, "Env Dispatch Chat Squad Leader", nil)

	// A squad led by that agent.
	if err := testPool.QueryRow(ctx, `
		INSERT INTO squad (workspace_id, name, description, leader_id, creator_id)
		VALUES ($1, $2, '', $3, $4)
		RETURNING id
	`, testWorkspaceID, "Env Dispatch Chat Squad", leaderAgentID, testUserID).Scan(&squadID); err != nil {
		t.Fatalf("create squad: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(ctx, `DELETE FROM squad WHERE id = $1`, squadID) })

	// A chat session bound to the leader agent; runtime_id resolved from the
	// agent exactly like CreateChatSession does in production.
	if err := testPool.QueryRow(ctx, `
		INSERT INTO chat_session (workspace_id, agent_id, creator_id, title, runtime_id)
		VALUES ($1, $2, $3, 'env-dispatch squad chat', (SELECT runtime_id FROM agent WHERE id = $2))
		RETURNING id
	`, testWorkspaceID, leaderAgentID, testUserID).Scan(&chatSessionID); err != nil {
		t.Fatalf("create chat session: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(ctx, `DELETE FROM chat_session WHERE id = $1`, chatSessionID) })

	return leaderAgentID, squadID, chatSessionID
}

// TestEnqueueAgentRun_ChatSquad_StampsSquadHint verifies that a chat-path
// dispatch with a squad_id (a) stamps the created chat task's context JSONB
// with {"squad_id": <squad>} and (b) enqueues the squad LEADER's task.
func TestEnqueueAgentRun_ChatSquad_StampsSquadHint(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	leaderAgentID, squadID, chatSessionID := setupSquadChatFixture(t)

	a := &envDispatchDepsAdapter{h: testHandler}
	runID, err := a.EnqueueAgentRun(ctx, testWorkspaceID, "", squadID, "", chatSessionID, "", "", 0)
	if err != nil {
		t.Fatalf("EnqueueAgentRun squad chat: %v", err)
	}
	if runID == "" {
		t.Fatal("expected a leader chat task run id")
	}
	t.Cleanup(func() { testPool.Exec(ctx, `DELETE FROM agent_task_queue WHERE id = $1`, runID) })

	var ctxJSON []byte
	var taskAgent string
	if err := testPool.QueryRow(ctx,
		`SELECT context, agent_id FROM agent_task_queue WHERE id = $1`, runID,
	).Scan(&ctxJSON, &taskAgent); err != nil {
		t.Fatalf("read chat task: %v", err)
	}
	var c struct {
		SquadID string `json:"squad_id"`
	}
	if err := json.Unmarshal(ctxJSON, &c); err != nil {
		t.Fatalf("unmarshal chat task context %q: %v", string(ctxJSON), err)
	}
	if c.SquadID != squadID {
		t.Errorf("chat task context squad_id = %q, want %q", c.SquadID, squadID)
	}
	if taskAgent != leaderAgentID {
		t.Errorf("chat task agent = %s, want leader %s", taskAgent, leaderAgentID)
	}
}

// setupSquadChatClaimFixture seeds a dedicated runtime + squad-leader agent
// bound to it, a squad led by that agent, a chat session with an unanswered
// user message, and a queued chat task whose context carries the squad_id
// hint. It returns the runtime id and the queued chat task id so a test can
// drive the daemon claim path and assert the injected briefing.
func setupSquadChatClaimFixture(t *testing.T) (runtimeID, taskID string) {
	t.Helper()
	ctx := context.Background()

	runtimeID = createClaimReclaimRuntime(t, ctx, "Chat squad briefing runtime")

	var leaderAgentID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent (
			workspace_id, name, description, runtime_mode, runtime_config,
			runtime_id, visibility, max_concurrent_tasks, owner_id,
			instructions, custom_env, custom_args, mcp_config
		)
		VALUES ($1, $2, '', 'cloud', '{}'::jsonb, $3, 'private', 1, $4, '', '{}'::jsonb, '[]'::jsonb, NULL)
		RETURNING id
	`, testWorkspaceID, "Chat squad briefing leader", runtimeID, testUserID).Scan(&leaderAgentID); err != nil {
		t.Fatalf("create leader agent: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(ctx, `DELETE FROM agent WHERE id = $1`, leaderAgentID) })

	var squadID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO squad (workspace_id, name, description, leader_id, creator_id)
		VALUES ($1, $2, '', $3, $4)
		RETURNING id
	`, testWorkspaceID, "Chat squad briefing squad", leaderAgentID, testUserID).Scan(&squadID); err != nil {
		t.Fatalf("create squad: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(ctx, `DELETE FROM squad WHERE id = $1`, squadID) })

	var chatSessionID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO chat_session (workspace_id, agent_id, creator_id, title, runtime_id)
		VALUES ($1, $2, $3, 'env-dispatch squad chat claim', $4)
		RETURNING id
	`, testWorkspaceID, leaderAgentID, testUserID, runtimeID).Scan(&chatSessionID); err != nil {
		t.Fatalf("create chat session: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(ctx, `DELETE FROM chat_session WHERE id = $1`, chatSessionID) })

	if _, err := testPool.Exec(ctx, `
		INSERT INTO chat_message (chat_session_id, role, content)
		VALUES ($1, 'user', 'please coordinate the squad on this task')
	`, chatSessionID); err != nil {
		t.Fatalf("insert chat prompt: %v", err)
	}

	ctxJSON, err := json.Marshal(map[string]string{"squad_id": squadID})
	if err != nil {
		t.Fatalf("marshal context: %v", err)
	}
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_task_queue (agent_id, runtime_id, chat_session_id, status, priority, context)
		VALUES ($1, $2, $3, 'queued', 2, $4)
		RETURNING id
	`, leaderAgentID, runtimeID, chatSessionID, ctxJSON).Scan(&taskID); err != nil {
		t.Fatalf("create queued chat task: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(ctx, `DELETE FROM agent_task_queue WHERE id = $1`, taskID) })

	return runtimeID, taskID
}

// TestClaimTaskByRuntime_ChatSquad_InjectsLeaderBriefing verifies that when
// the daemon claim path handles a chat task whose context carries a squad_id
// and the claiming agent is that squad's leader, the squad-leader briefing
// (Operating Protocol) is appended to the response agent's Instructions.
func TestClaimTaskByRuntime_ChatSquad_InjectsLeaderBriefing(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	runtimeID, taskID := setupSquadChatClaimFixture(t)

	task, body := claimTaskByRuntimeForTest(t, runtimeID)
	if task == nil {
		t.Fatalf("expected queued chat task %s to be claimed, got nil: %s", taskID, body)
	}
	if task.ID != taskID {
		t.Fatalf("claimed task id = %s, want %s", task.ID, taskID)
	}
	// A distinctive heading from squadOperatingProtocol must appear in the
	// claim response (inside resp.Agent.Instructions).
	if !strings.Contains(body, "Squad Operating Protocol") {
		t.Fatalf("expected squad leader briefing in claim response, got: %s", body)
	}
}
