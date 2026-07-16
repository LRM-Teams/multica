package handler

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func setupBoundRuntimeAgent(t *testing.T, provider string) (agentID, runtimeID string) {
	t.Helper()
	ctx := context.Background()

	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_runtime (
			workspace_id, name, runtime_mode, provider, status,
			device_info, metadata, owner_id, last_seen_at
		)
		VALUES ($1, $2, 'local', $3, 'online', '', '{}'::jsonb, $4, now())
		RETURNING id
	`, testWorkspaceID, "Env dispatch "+provider+" runtime", provider, testUserID).Scan(&runtimeID); err != nil {
		t.Fatalf("create %s runtime: %v", provider, err)
	}

	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent (
			workspace_id, name, description, runtime_mode, runtime_config,
			runtime_id, visibility, max_concurrent_tasks, owner_id
		)
		VALUES ($1, $2, '', 'local', '{}'::jsonb, $3, 'private', 1, $4)
		RETURNING id
	`, testWorkspaceID, "Env dispatch "+provider+" agent", runtimeID, testUserID).Scan(&agentID); err != nil {
		t.Fatalf("create %s agent: %v", provider, err)
	}

	t.Cleanup(func() {
		testPool.Exec(ctx, `DELETE FROM agent WHERE id = $1`, agentID)
		testPool.Exec(ctx, `DELETE FROM agent_runtime WHERE id = $1`, runtimeID)
	})
	return agentID, runtimeID
}

func TestPrecreateAgentRuntimeUsesBoundRuntimeProvider(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	agentID, _ := setupBoundRuntimeAgent(t, "pi")

	a := &envDispatchDepsAdapter{h: testHandler}
	runtimeID, _, err := a.PrecreateAgentRuntime(ctx, testWorkspaceID, testUserID, agentID)
	if err != nil {
		t.Fatalf("PrecreateAgentRuntime: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(ctx, `DELETE FROM agent_runtime WHERE id = $1`, runtimeID) })

	var provider string
	if err := testPool.QueryRow(ctx, `SELECT provider FROM agent_runtime WHERE id = $1`, runtimeID).Scan(&provider); err != nil {
		t.Fatalf("read precreated runtime: %v", err)
	}
	if provider != "pi" {
		t.Fatalf("provider = %q, want pi", provider)
	}
}

func TestPrecreateAgentRuntimeRejectsNonPiBoundRuntime(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	agentID, _ := setupBoundRuntimeAgent(t, "codex")

	a := &envDispatchDepsAdapter{h: testHandler}
	_, _, err := a.PrecreateAgentRuntime(context.Background(), testWorkspaceID, testUserID, agentID)
	if err == nil || !strings.Contains(err.Error(), "requires pi") {
		t.Fatalf("error = %v, want pi validation", err)
	}
}

// setupSquadIssueFixture seeds the entities the issue-path squad dispatch
// touches: a workspace agent that leads a squad, the squad itself (leader_id
// -> that agent), and an issue in the workspace. It returns the leader agent
// id, the squad id, and the issue id. Cleanup is registered via t.Cleanup.
//
// Mirrors the squad/leader fixture pattern in squad_assign_trigger_test.go and
// the issue-insert pattern in handler_test.go.
func setupSquadIssueFixture(t *testing.T) (leaderAgentID, squadID, issueID string) {
	t.Helper()
	ctx := context.Background()

	// A workspace agent with a runtime — eligible to lead a squad.
	leaderAgentID = createHandlerTestAgent(t, "Env Dispatch Squad Leader", nil)

	// A squad led by that agent.
	if err := testPool.QueryRow(ctx, `
		INSERT INTO squad (workspace_id, name, description, leader_id, creator_id)
		VALUES ($1, $2, '', $3, $4)
		RETURNING id
	`, testWorkspaceID, "Env Dispatch Squad", leaderAgentID, testUserID).Scan(&squadID); err != nil {
		t.Fatalf("create squad: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(ctx, `DELETE FROM squad WHERE id = $1`, squadID) })

	// A workspace-scoped issue number that does not collide with existing rows.
	var number int
	if err := testPool.QueryRow(ctx, `
		UPDATE workspace
		SET issue_counter = GREATEST(issue_counter, (SELECT COALESCE(MAX(number), 0) FROM issue WHERE workspace_id = $1)) + 1
		WHERE id = $1 RETURNING issue_counter
	`, testWorkspaceID).Scan(&number); err != nil {
		t.Fatalf("next issue number: %v", err)
	}

	if err := testPool.QueryRow(ctx, `
		INSERT INTO issue (workspace_id, title, status, priority, creator_type, creator_id, number, position)
		VALUES ($1, 'env-dispatch squad issue', 'todo', 'none', 'member', $2, $3, 0)
		RETURNING id
	`, testWorkspaceID, testUserID, number).Scan(&issueID); err != nil {
		t.Fatalf("create issue: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(ctx, `DELETE FROM agent_task_queue WHERE issue_id = $1`, issueID)
		testPool.Exec(ctx, `DELETE FROM issue WHERE id = $1`, issueID)
	})

	return leaderAgentID, squadID, issueID
}

// TestDispatch_EnqueueAgentRunIssueSquadSetsAssigneeLeaderAndActor verifies that an
// issue-path dispatch with a squad_id (a) stamps the issue as
// assignee_type='squad'/assignee_id=squad and (b) enqueues the squad LEADER's
// task with is_leader_task=true.
func TestDispatch_EnqueueAgentRunIssueSquadSetsAssigneeLeaderAndActor(t *testing.T) {
	ctx := context.Background()
	leaderAgentID, squadID, issueID := setupSquadIssueFixture(t)

	a := &envDispatchDepsAdapter{h: testHandler}
	runID, err := a.EnqueueAgentRun(ctx, testWorkspaceID, testUserID, "", squadID, issueID, "", "sandbox-issue", "", "", 0)
	if err != nil {
		t.Fatalf("EnqueueAgentRun squad issue: %v", err)
	}
	if runID == "" {
		t.Fatal("expected a leader task run id")
	}

	var assigneeType, assigneeID string
	if err := testPool.QueryRow(ctx,
		`SELECT assignee_type, assignee_id FROM issue WHERE id = $1`, issueID,
	).Scan(&assigneeType, &assigneeID); err != nil {
		t.Fatalf("read issue assignee: %v", err)
	}
	if assigneeType != "squad" || assigneeID != squadID {
		t.Errorf("issue assignee = (%s,%s), want (squad,%s)", assigneeType, assigneeID, squadID)
	}

	var isLeader bool
	var taskAgent string
	var ctxJSON []byte
	if err := testPool.QueryRow(ctx,
		`SELECT is_leader_task, agent_id, context FROM agent_task_queue WHERE id = $1`, runID,
	).Scan(&isLeader, &taskAgent, &ctxJSON); err != nil {
		t.Fatalf("read task: %v", err)
	}
	var taskContext struct {
		EphemeralSandbox struct {
			SandboxInstanceID string `json:"sandbox_instance_id"`
			ActorUserID       string `json:"actor_user_id"`
		} `json:"ephemeral_sandbox"`
	}
	if err := json.Unmarshal(ctxJSON, &taskContext); err != nil {
		t.Fatalf("unmarshal issue task context %q: %v", string(ctxJSON), err)
	}
	if taskContext.EphemeralSandbox.SandboxInstanceID != "sandbox-issue" {
		t.Errorf("sandbox_instance_id = %q, want sandbox-issue", taskContext.EphemeralSandbox.SandboxInstanceID)
	}
	if taskContext.EphemeralSandbox.ActorUserID != testUserID {
		t.Errorf("actor_user_id = %q, want %q", taskContext.EphemeralSandbox.ActorUserID, testUserID)
	}
	if !isLeader {
		t.Error("squad issue dispatch must set is_leader_task=true")
	}
	if taskAgent != leaderAgentID {
		t.Errorf("task agent = %s, want leader %s", taskAgent, leaderAgentID)
	}
}
