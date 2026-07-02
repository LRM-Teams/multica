package handler

import (
	"context"
	"testing"
)

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

// TestEnqueueAgentRun_IssueSquad_SetsAssigneeAndLeaderTask verifies that an
// issue-path dispatch with a squad_id (a) stamps the issue as
// assignee_type='squad'/assignee_id=squad and (b) enqueues the squad LEADER's
// task with is_leader_task=true.
func TestEnqueueAgentRun_IssueSquad_SetsAssigneeAndLeaderTask(t *testing.T) {
	ctx := context.Background()
	leaderAgentID, squadID, issueID := setupSquadIssueFixture(t)

	a := &envDispatchDepsAdapter{h: testHandler}
	runID, err := a.EnqueueAgentRun(ctx, testWorkspaceID, "", squadID, issueID, "", "", 0)
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
	if err := testPool.QueryRow(ctx,
		`SELECT is_leader_task, agent_id FROM agent_task_queue WHERE id = $1`, runID,
	).Scan(&isLeader, &taskAgent); err != nil {
		t.Fatalf("read task: %v", err)
	}
	if !isLeader {
		t.Error("squad issue dispatch must set is_leader_task=true")
	}
	if taskAgent != leaderAgentID {
		t.Errorf("task agent = %s, want leader %s", taskAgent, leaderAgentID)
	}
}
