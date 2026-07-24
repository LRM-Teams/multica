package handler

import (
	"context"
	"testing"
)

func createCommentTriggerPreviewIssue(t *testing.T, title string, assigneeType, assigneeID string) string {
	t.Helper()
	ctx := context.Background()

	var number int
	if err := testPool.QueryRow(ctx, `
		UPDATE workspace
		SET issue_counter = GREATEST(issue_counter, (SELECT COALESCE(MAX(number), 0) FROM issue WHERE workspace_id = $1)) + 1
		WHERE id = $1 RETURNING issue_counter
	`, testWorkspaceID).Scan(&number); err != nil {
		t.Fatalf("next issue number: %v", err)
	}

	var assigneeTypeArg any
	var assigneeIDArg any
	if assigneeType != "" {
		assigneeTypeArg = assigneeType
	}
	if assigneeID != "" {
		assigneeIDArg = assigneeID
	}

	var issueID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO issue (workspace_id, creator_type, creator_id, title, assignee_type, assignee_id, number)
		VALUES ($1, 'member', $2, $3, $4, $5, $6)
		RETURNING id
	`, testWorkspaceID, testUserID, title, assigneeTypeArg, assigneeIDArg, number).Scan(&issueID); err != nil {
		t.Fatalf("create issue: %v", err)
	}

	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM agent_inbox_event WHERE issue_id = $1`, issueID)
		testPool.Exec(context.Background(), `DELETE FROM comment WHERE issue_id = $1`, issueID)
		testPool.Exec(context.Background(), `DELETE FROM issue WHERE id = $1`, issueID)
	})

	return issueID
}
