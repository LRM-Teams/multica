package handler

import (
	"context"
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/internal/service"
)

// Per-Goal execution budget: concurrency past the cap defers silently (the
// recovery scan re-dispatches once a slot frees), and an Issue past the
// attempt ceiling stops consuming Runs and surfaces one deduplicated
// budget_exhausted controller event for the manager to act on.
func TestGoalExecutionBudgetBoundsConcurrencyAndBreaksRetryLoops(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()

	runtimeID := createClaimReclaimRuntime(t, ctx, "goal budget runtime "+uuid.NewString()[:8])
	var agentID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent (workspace_id, name, description, runtime_mode, runtime_config, runtime_id, max_concurrent_tasks, owner_id, model)
		VALUES ($1, $2, '', 'cloud', '{}'::jsonb, $3, 32, $4, 'composer-1.5')
		RETURNING id`, testWorkspaceID, "goal budget worker "+uuid.NewString()[:8], runtimeID, testUserID).Scan(&agentID); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(ctx, `DELETE FROM agent WHERE id = $1`, agentID) })

	channelID := seedChannelForTest(t, "goal-budget-"+uuid.NewString()[:8])
	var goalID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO channel_goal (workspace_id, channel_id, title, objective, success_criteria, created_by_type, created_by_id, updated_by_type, updated_by_id)
		VALUES ($1, $2, 'Budget Goal', 'Bound spend', '["bounded"]', 'user', $3, 'user', $3)
		RETURNING id`, testWorkspaceID, channelID, testUserID).Scan(&goalID); err != nil {
		t.Fatalf("create goal: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(ctx, `DELETE FROM channel_goal WHERE id = $1`, goalID) })

	createGoalIssue := func(name string) string {
		var issueID string
		if err := testPool.QueryRow(ctx, `
			INSERT INTO issue (workspace_id, title, status, priority, creator_id, creator_type,
			                   assignee_type, assignee_id, channel_goal_id, goal_required,
			                   acceptance_criteria, number, position)
			VALUES ($1, $2, 'todo', 'none', $3, 'member', 'agent', $4, $5, true, '["done means done"]',
			        (SELECT COALESCE(MAX(number), 92649) + 1 FROM issue WHERE workspace_id = $1), 0)
			RETURNING id`, testWorkspaceID, name, testUserID, agentID, goalID).Scan(&issueID); err != nil {
			t.Fatalf("create goal issue: %v", err)
		}
		t.Cleanup(func() { testPool.Exec(ctx, `DELETE FROM issue WHERE id = $1`, issueID) })
		return issueID
	}

	reconcile := func(issueID string) service.IssueExecutionReconcileOutcome {
		t.Helper()
		issue, err := testHandler.Queries.GetIssue(ctx, parseUUID(issueID))
		if err != nil {
			t.Fatalf("load issue: %v", err)
		}
		outcome, err := testHandler.IssueExecution.Reconcile(ctx, issue.WorkspaceID, issue.ID, service.IssueExecutionReconcileOptions{
			TriggerKind: "test_goal_budget",
		})
		if err != nil {
			t.Fatalf("reconcile: %v", err)
		}
		return outcome
	}

	// Fill the Goal's concurrency budget of 8, then confirm the ninth defers
	// without a claim.
	issueIDs := make([]string, 0, 9)
	for i := 0; i < 9; i++ {
		issueIDs = append(issueIDs, createGoalIssue(fmt.Sprintf("budget issue %d", i)))
	}
	for i := 0; i < 8; i++ {
		if outcome := reconcile(issueIDs[i]); !outcome.Dispatch {
			t.Fatalf("issue %d under budget did not dispatch", i)
		}
	}
	deferred := reconcile(issueIDs[8])
	if deferred.Dispatch {
		t.Fatal("ninth concurrent goal issue dispatched past the concurrency budget")
	}
	var claims int
	if err := testPool.QueryRow(ctx, `
		SELECT count(*) FROM active_issue_execution claim
		JOIN issue ON issue.id = claim.issue_id
		WHERE issue.channel_goal_id = $1`, goalID).Scan(&claims); err != nil {
		t.Fatal(err)
	}
	if claims != 8 {
		t.Fatalf("active claims = %d, want exactly the budget of 8", claims)
	}

	// Free one slot the way a finished Run does, then confirm the deferred
	// Issue dispatches on the next reconcile (in production the 5s recovery
	// scan issues that call).
	if _, err := testPool.Exec(ctx, `
		DELETE FROM active_issue_execution WHERE issue_id = $1`, parseUUID(issueIDs[0])); err != nil {
		t.Fatal(err)
	}
	if _, err := testPool.Exec(ctx, `
		UPDATE issue SET status = 'done' WHERE id = $1`, parseUUID(issueIDs[0])); err != nil {
		t.Fatal(err)
	}
	if outcome := reconcile(issueIDs[8]); !outcome.Dispatch {
		t.Fatal("deferred issue did not dispatch after a budget slot freed")
	}

	// Attempt ceiling: a runaway Issue stops consuming Runs and surfaces one
	// pending budget_exhausted event, deduplicated across repeated scans.
	runawayID := createGoalIssue("budget runaway issue")
	if _, err := testPool.Exec(ctx, `
		UPDATE issue SET execution_attempt_sequence = 50 WHERE id = $1`, parseUUID(runawayID)); err != nil {
		t.Fatal(err)
	}
	if outcome := reconcile(runawayID); outcome.Dispatch {
		t.Fatal("issue past the attempt ceiling still dispatched")
	}
	countEvents := func() int {
		var pending int
		if err := testPool.QueryRow(ctx, `
			SELECT count(*) FROM goal_controller_event
			WHERE goal_id = $1 AND event_kind = 'budget_exhausted'
			  AND source_id = $2 AND status = 'pending'`, goalID, parseUUID(runawayID)).Scan(&pending); err != nil {
			t.Fatal(err)
		}
		return pending
	}
	if got := countEvents(); got != 1 {
		t.Fatalf("budget_exhausted pending events = %d, want 1", got)
	}
	if outcome := reconcile(runawayID); outcome.Dispatch {
		t.Fatal("attempt-capped issue dispatched on the second scan")
	}
	if got := countEvents(); got != 1 {
		t.Fatalf("budget_exhausted events after rescan = %d, want dedup to hold at 1", got)
	}
}
