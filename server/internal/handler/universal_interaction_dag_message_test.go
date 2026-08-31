package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func cleanupUniversalDAGTask(t *testing.T, taskID string) {
	t.Helper()
	requireDisposableDAGTestSchema(t)
	t.Cleanup(func() {
		ctx := context.Background()
		requireDisposableDAGTestSchema(t)
		_, _ = testPool.Exec(ctx, `TRUNCATE interaction_dag_edge, interaction_dag_edge_sequence, interaction_dag_publish_outbox, interaction_dag_segment, interaction_dag_task_cursor CASCADE`)
		_, _ = testPool.Exec(ctx, `DELETE FROM task_message WHERE task_id = $1`, taskID)
	})
}

func TestUniversalDAGReportTaskMessageBoundary(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	taskID, _ := createChannelCompletionTask(t, "group")
	cleanupUniversalDAGTask(t, taskID)
	req := newDaemonTokenRequest(http.MethodPost, "/api/daemon/tasks/"+taskID+"/messages", TaskMessageBatchRequest{
		Messages: []TaskMessageRequest{
			{Seq: 1, Type: "tool_use", Tool: "read", Input: map[string]any{"filePath": "/tmp/synthetic.txt"}},
			{Seq: 2, Type: "tool_result", Tool: "read", Output: "synthetic result"},
		},
	}, testWorkspaceID, "task3-message-"+uuid.NewString())
	req = withURLParam(req, "taskId", taskID)
	rec := httptest.NewRecorder()
	testHandler.ReportTaskMessages(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("report task messages: status=%d body=%s", rec.Code, rec.Body.String())
	}

	ctx := context.Background()
	var messageCount, segmentCount int
	var openStart, openEnd int32
	if err := testPool.QueryRow(ctx, `SELECT count(*) FROM task_message WHERE task_id = $1`, taskID).Scan(&messageCount); err != nil {
		t.Fatalf("count task messages: %v", err)
	}
	if err := testPool.QueryRow(ctx, `
		SELECT open_start_seq, open_end_seq
		FROM interaction_dag_task_cursor
		WHERE workspace_id = $1 AND agent_run_id = $2`, testWorkspaceID, taskID).Scan(&openStart, &openEnd); err != nil {
		t.Fatalf("load universal DAG cursor: %v", err)
	}
	if err := testPool.QueryRow(ctx, `SELECT count(*) FROM interaction_dag_segment WHERE workspace_id = $1 AND agent_run_id = $2`, testWorkspaceID, taskID).Scan(&segmentCount); err != nil {
		t.Fatalf("count universal DAG segments: %v", err)
	}
	if messageCount != 2 || openStart != 1 || openEnd != 2 || segmentCount != 0 {
		t.Fatalf("canonical boundary messages/start/end/segments=%d/%d/%d/%d, want 2/1/2/0", messageCount, openStart, openEnd, segmentCount)
	}
}

func TestUniversalDAGIssueCommentBoundaryAndRollback(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	issueRec := httptest.NewRecorder()
	issueReq := newRequest(http.MethodPost, "/api/issues?workspace_id="+testWorkspaceID, map[string]any{
		"title": "Task 3 issue comment " + uuid.NewString(),
	})
	testHandler.CreateIssue(issueRec, issueReq)
	if issueRec.Code != http.StatusCreated {
		t.Fatalf("create issue: status=%d body=%s", issueRec.Code, issueRec.Body.String())
	}
	var issue IssueResponse
	if err := json.Unmarshal(issueRec.Body.Bytes(), &issue); err != nil {
		t.Fatalf("decode issue: %v", err)
	}
	agentID := createHandlerTestAgent(t, "Task3 Comment "+uuid.NewString()[:8], nil)
	taskID := createHandlerTestTaskForAgentOnIssue(t, agentID, issue.ID)
	cleanupUniversalDAGTask(t, taskID)

	create := func(content string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		req := newRequest(http.MethodPost, "/api/issues/"+issue.ID+"/comments", map[string]any{"content": content})
		req.Header.Set("X-Agent-ID", agentID)
		req.Header.Set("X-Task-ID", taskID)
		req = withURLParam(req, "id", issue.ID)
		testHandler.CreateComment(rec, req)
		return rec
	}

	success := create("synthetic canonical issue comment")
	if success.Code != http.StatusCreated {
		t.Fatalf("create agent comment: status=%d body=%s", success.Code, success.Body.String())
	}
	var comment CommentResponse
	if err := json.Unmarshal(success.Body.Bytes(), &comment); err != nil {
		t.Fatalf("decode comment: %v", err)
	}
	ctx := context.Background()
	var messageCount, segmentCount, outboxCount int
	var actionID string
	if err := testPool.QueryRow(ctx, `SELECT count(*) FROM task_message WHERE task_id=$1`, taskID).Scan(&messageCount); err != nil {
		t.Fatalf("count canonical comment messages: %v", err)
	}
	if err := testPool.QueryRow(ctx, `
		SELECT count(*), COALESCE(max(canonical_action_id::text), '')
		FROM interaction_dag_segment
		WHERE workspace_id=$1 AND agent_run_id=$2 AND close_action_kind='message'`, testWorkspaceID, taskID).Scan(&segmentCount, &actionID); err != nil {
		t.Fatalf("load canonical comment segment: %v", err)
	}
	if err := testPool.QueryRow(ctx, `
		SELECT count(*) FROM interaction_dag_publish_outbox o
		JOIN interaction_dag_segment s ON s.workspace_id=o.workspace_id AND s.segment_id=o.segment_id
		WHERE s.workspace_id=$1 AND s.agent_run_id=$2`, testWorkspaceID, taskID).Scan(&outboxCount); err != nil {
		t.Fatalf("count canonical comment outbox: %v", err)
	}
	if messageCount != 1 || segmentCount != 1 || outboxCount != 1 || actionID != comment.ID {
		t.Fatalf("comment task-message/segment/outbox/action=%d/%d/%d/%q, want 1/1/1/%q", messageCount, segmentCount, outboxCount, actionID, comment.ID)
	}

	originalDAG := testHandler.TaskService.UniversalDAG
	testHandler.TaskService.UniversalDAG = nil
	t.Cleanup(func() { testHandler.TaskService.UniversalDAG = originalDAG })
	failedContent := "synthetic rollback comment " + uuid.NewString()
	failed := create(failedContent)
	testHandler.TaskService.UniversalDAG = originalDAG
	if failed.Code != http.StatusInternalServerError {
		t.Fatalf("comment with failed universal boundary: status=%d body=%s, want 500", failed.Code, failed.Body.String())
	}
	var durableCommentCount int
	if err := testPool.QueryRow(ctx, `SELECT count(*) FROM comment WHERE issue_id=$1 AND content=$2`, issue.ID, failedContent).Scan(&durableCommentCount); err != nil {
		t.Fatalf("count rolled-back comment: %v", err)
	}
	if durableCommentCount != 0 {
		t.Fatalf("rolled-back comment count=%d, want 0", durableCommentCount)
	}
}

func TestUniversalDAGTaskServiceVisibleTerminalOutputMatrix(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()

	t.Run("completion fallback issue comment", func(t *testing.T) {
		issueRec := httptest.NewRecorder()
		issueReq := newRequest(http.MethodPost, "/api/issues?workspace_id="+testWorkspaceID, map[string]any{"title": "Task3 completion " + uuid.NewString()})
		testHandler.CreateIssue(issueRec, issueReq)
		if issueRec.Code != http.StatusCreated {
			t.Fatalf("create completion issue: status=%d body=%s", issueRec.Code, issueRec.Body.String())
		}
		var issue IssueResponse
		if err := json.Unmarshal(issueRec.Body.Bytes(), &issue); err != nil {
			t.Fatal(err)
		}
		agentID := createHandlerTestAgent(t, "Task3 Completion "+uuid.NewString()[:8], nil)
		taskID := createHandlerTestTaskForAgentOnIssue(t, agentID, issue.ID)
		cleanupUniversalDAGTask(t, taskID)
		result, _ := json.Marshal(protocol.TaskCompletedPayload{Type: "message", Output: "synthetic completion fallback"})
		if _, err := testHandler.TaskService.CompleteTask(ctx, parseUUID(taskID), result, "", ""); err != nil {
			t.Fatalf("complete issue task: %v", err)
		}
		var commentID string
		if err := testPool.QueryRow(ctx, `SELECT id FROM comment WHERE issue_id=$1 AND author_type='agent' AND content=$2`, issue.ID, "synthetic completion fallback").Scan(&commentID); err != nil {
			t.Fatalf("load completion fallback comment: %v", err)
		}
		var segments int
		if err := testPool.QueryRow(ctx, `SELECT count(*) FROM interaction_dag_segment WHERE workspace_id=$1 AND agent_run_id=$2 AND canonical_action_id=$3`, testWorkspaceID, taskID, commentID).Scan(&segments); err != nil {
			t.Fatalf("count completion fallback segment: %v", err)
		}
		if segments != 1 {
			t.Fatalf("completion fallback segments=%d, want 1", segments)
		}
	})

	t.Run("failure assistant chat", func(t *testing.T) {
		taskID, _ := createChannelCompletionTask(t, "group")
		cleanupUniversalDAGTask(t, taskID)
		if _, err := testHandler.TaskService.FailTask(ctx, parseUUID(taskID), "synthetic terminal failure", "", "", "agent_error"); err != nil {
			t.Fatalf("fail chat task: %v", err)
		}
		var messageID string
		if err := testPool.QueryRow(ctx, `SELECT id FROM chat_message WHERE task_id=$1 AND role='assistant' AND failure_reason='agent_error'`, taskID).Scan(&messageID); err != nil {
			t.Fatalf("load failure assistant message: %v", err)
		}
		var segments int
		if err := testPool.QueryRow(ctx, `SELECT count(*) FROM interaction_dag_segment WHERE workspace_id=$1 AND agent_run_id=$2 AND canonical_action_id=$3`, testWorkspaceID, taskID, messageID).Scan(&segments); err != nil {
			t.Fatalf("count failure assistant segment: %v", err)
		}
		if segments != 1 {
			t.Fatalf("failure assistant segments=%d, want 1", segments)
		}
	})

	t.Run("cancellation partial transcript", func(t *testing.T) {
		taskID, _ := createChannelCompletionTask(t, "group")
		cleanupUniversalDAGTask(t, taskID)
		req := newDaemonTokenRequest(http.MethodPost, "/api/daemon/tasks/"+taskID+"/messages", TaskMessageBatchRequest{Messages: []TaskMessageRequest{{Seq: 1, Type: "text", Content: "synthetic partial transcript"}}}, testWorkspaceID, "task3-cancel-"+uuid.NewString())
		req = withURLParam(req, "taskId", taskID)
		rec := httptest.NewRecorder()
		testHandler.ReportTaskMessages(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("report cancellation transcript: status=%d body=%s", rec.Code, rec.Body.String())
		}
		result, err := testHandler.TaskService.CancelTaskWithResult(ctx, parseUUID(taskID))
		if err != nil {
			t.Fatalf("cancel task: %v", err)
		}
		if result.Task.TerminalOutcome.String != "cancelled" {
			t.Fatalf("cancel terminal outcome=%q, want cancelled", result.Task.TerminalOutcome.String)
		}
		var messageID string
		if err := testPool.QueryRow(ctx, `SELECT id FROM chat_message WHERE task_id=$1 AND role='assistant'`, taskID).Scan(&messageID); err != nil {
			t.Fatalf("load cancellation assistant message: %v", err)
		}
		var segments int
		if err := testPool.QueryRow(ctx, `SELECT count(*) FROM interaction_dag_segment WHERE workspace_id=$1 AND agent_run_id=$2 AND canonical_action_id=$3`, testWorkspaceID, taskID, messageID).Scan(&segments); err != nil {
			t.Fatalf("count cancellation visible segment: %v", err)
		}
		if segments != 1 {
			t.Fatalf("cancellation visible segments=%d, want 1", segments)
		}
	})
}
