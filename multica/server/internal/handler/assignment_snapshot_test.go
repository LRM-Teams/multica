package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/service"
)

func TestAssignmentSnapshotFreezesStableFieldsAndUsesClaimTimeStatus(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	runtimeID := createClaimReclaimRuntime(t, ctx, "assignment snapshot runtime")
	agentID, issueID := createClaimReclaimAgentAndIssue(t, ctx, runtimeID, "assignment snapshot")
	if _, err := testPool.Exec(ctx, `
		UPDATE issue
		SET assignee_type = 'agent',
		    assignee_id = $2,
		    title = 'Frozen title',
		    description = 'Frozen description',
		    status = 'todo',
		    acceptance_criteria = '["Frozen AC"]'::jsonb,
		    metadata = '{"lane":"backend"}'::jsonb
		WHERE id = $1
	`, issueID, agentID); err != nil {
		t.Fatalf("seed assigned issue: %v", err)
	}
	for _, content := range []string{"first comment", "second comment"} {
		if _, err := testPool.Exec(ctx, `
			INSERT INTO comment (issue_id, workspace_id, author_type, author_id, content, type)
			VALUES ($1, $2, 'member', $3, $4, 'comment')
		`, issueID, testWorkspaceID, testUserID, content); err != nil {
			t.Fatalf("seed comment: %v", err)
		}
	}

	issue, err := testHandler.Queries.GetIssue(ctx, parseUUID(issueID))
	if err != nil {
		t.Fatalf("load issue: %v", err)
	}
	event, err := testHandler.TaskService.EnqueueTaskForIssue(ctx, issue)
	if err != nil {
		t.Fatalf("enqueue assignment: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM agent_inbox_event WHERE id = $1`, event.ID)
	})
	if event.IssueID != issue.ID || event.AgentID != issue.AssigneeID || event.TriggerCommentID.Valid {
		t.Fatalf("enqueue identity changed: event=%#v issue=%#v", event, issue)
	}

	persisted, found, err := service.IssueAssignmentSnapshotFromContext(event.Context)
	if err != nil || !found {
		t.Fatalf("decode persisted snapshot: found=%v err=%v", found, err)
	}
	if persisted.Status != "" {
		t.Fatalf("persisted snapshot froze status %q", persisted.Status)
	}
	if persisted.Title != "Frozen title" || persisted.CommentCount != 2 {
		t.Fatalf("persisted snapshot = %#v", persisted)
	}

	if _, err := testPool.Exec(ctx, `
		UPDATE issue
		SET title = 'Edited after enqueue',
		    description = 'Edited after enqueue',
		    status = 'done',
		    acceptance_criteria = '["Edited AC"]'::jsonb,
		    metadata = '{"lane":"frontend"}'::jsonb
		WHERE id = $1
	`, issueID); err != nil {
		t.Fatalf("edit issue after enqueue: %v", err)
	}
	if _, err := testPool.Exec(ctx, `
		INSERT INTO comment (issue_id, workspace_id, author_type, author_id, content, type)
		VALUES ($1, $2, 'member', $3, 'third comment', 'comment')
	`, issueID, testWorkspaceID, testUserID); err != nil {
		t.Fatalf("add post-enqueue comment: %v", err)
	}

	w := httptest.NewRecorder()
	req := newDaemonTokenRequest(http.MethodPost, "/api/daemon/runtimes/"+runtimeID+"/claim", nil, testWorkspaceID, "assignment-snapshot-claim")
	req = withURLParam(req, "runtimeId", runtimeID)
	claimTaskThroughInboxForTest(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("claim task: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var response struct {
		Task *AgentTaskResponse `json:"task"`
	}
	if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
		t.Fatalf("decode claim response: %v", err)
	}
	if response.Task == nil || response.Task.AssignmentSnapshot == nil {
		t.Fatalf("claim response missing assignment snapshot: %s", w.Body.String())
	}
	snapshot := response.Task.AssignmentSnapshot
	if snapshot.Status != "done" {
		t.Fatalf("claim status = %q, want current done", snapshot.Status)
	}
	if snapshot.Title != "Frozen title" || snapshot.CommentCount != 2 {
		t.Fatalf("claim snapshot did not preserve enqueue state: %#v", snapshot)
	}
	if snapshot.Description == nil || *snapshot.Description != "Frozen description" {
		t.Fatalf("claim description = %#v", snapshot.Description)
	}
	if len(snapshot.AcceptanceCriteria) != 1 || snapshot.AcceptanceCriteria[0] != "Frozen AC" {
		t.Fatalf("claim acceptance criteria = %#v", snapshot.AcceptanceCriteria)
	}
	if snapshot.Metadata["lane"] != "backend" {
		t.Fatalf("claim metadata = %#v", snapshot.Metadata)
	}
	if response.Task.ThreadName != "Frozen title" {
		t.Fatalf("thread name = %q, want frozen title", response.Task.ThreadName)
	}
}

func TestCommentTriggerDoesNotCopyAssignmentSnapshot(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	runtimeID := createClaimReclaimRuntime(t, ctx, "comment snapshot runtime")
	agentID, issueID := createClaimReclaimAgentAndIssue(t, ctx, runtimeID, "comment snapshot")
	if _, err := testPool.Exec(ctx, `
		UPDATE issue SET assignee_type = 'agent', assignee_id = $2 WHERE id = $1
	`, issueID, agentID); err != nil {
		t.Fatalf("assign issue: %v", err)
	}
	var commentID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO comment (issue_id, workspace_id, author_type, author_id, content, type)
		VALUES ($1, $2, 'member', $3, 'Trigger body stays canonical', 'comment')
		RETURNING id
	`, issueID, testWorkspaceID, testUserID).Scan(&commentID); err != nil {
		t.Fatalf("seed trigger comment: %v", err)
	}
	issue, err := testHandler.Queries.GetIssue(ctx, parseUUID(issueID))
	if err != nil {
		t.Fatalf("load issue: %v", err)
	}
	event, err := testHandler.TaskService.EnqueueTaskForIssue(ctx, issue, parseUUID(commentID))
	if err != nil {
		t.Fatalf("enqueue comment trigger: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM agent_inbox_event WHERE id = $1`, event.ID)
	})
	if !event.TriggerCommentID.Valid || event.TriggerCommentID != parseUUID(commentID) {
		t.Fatalf("trigger identity changed: %#v", event.TriggerCommentID)
	}
	if _, found, err := service.IssueAssignmentSnapshotFromContext(event.Context); err != nil || found {
		t.Fatalf("comment-trigger assignment snapshot: found=%v err=%v context=%s", found, err, event.Context)
	}
}

func TestAssignmentSnapshotFailsClosedBeforeEnqueue(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	runtimeID := createClaimReclaimRuntime(t, ctx, "invalid snapshot runtime")
	agentID, issueID := createClaimReclaimAgentAndIssue(t, ctx, runtimeID, "invalid snapshot")
	if _, err := testPool.Exec(ctx, `
		UPDATE issue SET assignee_type = 'agent', assignee_id = $2 WHERE id = $1
	`, issueID, agentID); err != nil {
		t.Fatalf("assign issue: %v", err)
	}
	issue, err := testHandler.Queries.GetIssue(ctx, parseUUID(issueID))
	if err != nil {
		t.Fatalf("load issue: %v", err)
	}

	invalid := issue
	invalid.Metadata = []byte(`not-json`)
	if _, err := testHandler.TaskService.EnqueueTaskForIssue(ctx, invalid); err == nil {
		t.Fatal("invalid read-model metadata was enqueued")
	}

	wrongWorkspace := issue
	wrongWorkspace.WorkspaceID = pgtype.UUID{Bytes: [16]byte{1}, Valid: true}
	if _, err := testHandler.TaskService.EnqueueTaskForIssue(ctx, wrongWorkspace); err == nil {
		t.Fatal("cross-workspace issue/agent assignment was enqueued")
	}

	var count int
	if err := testPool.QueryRow(ctx, `
		SELECT count(*) FROM agent_inbox_event
		WHERE issue_id = $1 AND agent_id = $2
	`, issueID, agentID).Scan(&count); err != nil {
		t.Fatalf("count inbox events: %v", err)
	}
	if count != 0 {
		t.Fatalf("fail-closed enqueue created %d event(s)", count)
	}
}
