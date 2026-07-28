package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
)

func TestListChannelSourceIssuesProjectsOnlyCurrentGroupAnchors(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	suffix := uuid.NewString()
	visibleChannelID := seedChannelForTest(t, "channel-issues-visible-"+suffix, testUserID)
	hiddenOwner := createWorkspaceMemberUser(t, "Channel Issues Hidden Owner", "channel-issues-hidden-"+suffix+"@multica.test")
	hiddenChannelID := seedChannelForTest(t, "channel-issues-hidden-"+suffix, hiddenOwner)

	createIssue := func(title, status, assigneeID string) string {
		t.Helper()
		var number int32
		if err := testPool.QueryRow(ctx, `
			UPDATE workspace
			SET issue_counter = GREATEST(issue_counter, (SELECT COALESCE(MAX(number), 0) FROM issue WHERE workspace_id = $1)) + 1
			WHERE id = $1
			RETURNING issue_counter`, testWorkspaceID).Scan(&number); err != nil {
			t.Fatalf("allocate issue number: %v", err)
		}
		var issueID string
		if err := testPool.QueryRow(ctx, `
			INSERT INTO issue (
				workspace_id, title, status, priority, assignee_type, assignee_id,
				creator_type, creator_id, position, number
			) VALUES ($1, $2, $3, 'none',
				CASE WHEN $4 <> '' THEN 'member' ELSE NULL END,
				NULLIF($4, '')::uuid,
				'member', $5, 0, $6)
			RETURNING id`, testWorkspaceID, title, status, assigneeID, testUserID, number).Scan(&issueID); err != nil {
			t.Fatalf("create issue %q: %v", title, err)
		}
		t.Cleanup(func() { _, _ = testPool.Exec(context.Background(), `DELETE FROM issue WHERE id = $1`, issueID) })
		return issueID
	}
	anchor := func(issueID, channelID string) {
		t.Helper()
		var messageID string
		if err := testPool.QueryRow(ctx, `
			INSERT INTO channel_message (channel_id, workspace_id, author_type, author_id, author_name, content, source, thread_id, trigger_depth)
			VALUES ($1, $2, 'user', $3, 'Source User', 'Issue source message', 'multica', $4, 0)
			RETURNING id`, channelID, testWorkspaceID, testUserID, "channel-issues-thread-"+uuid.NewString()).Scan(&messageID); err != nil {
			t.Fatalf("seed source message: %v", err)
		}
		if _, err := testPool.Exec(ctx, `
			INSERT INTO issue_source_message (issue_id, workspace_id, channel_id, message_id)
			VALUES ($1, $2, $3, $4)`, issueID, testWorkspaceID, channelID, messageID); err != nil {
			t.Fatalf("anchor issue: %v", err)
		}
	}

	visibleTodoID := createIssue("channel-issues-visible-todo-"+suffix, "todo", testUserID)
	visibleDoneID := createIssue("channel-issues-visible-done-"+suffix, "done", "")
	hiddenTodoID := createIssue("channel-issues-hidden-todo-"+suffix, "todo", "")
	_ = createIssue("channel-issues-unanchored-"+suffix, "todo", "")
	anchor(visibleTodoID, visibleChannelID)
	anchor(visibleDoneID, visibleChannelID)
	anchor(hiddenTodoID, hiddenChannelID)

	list := func(channelID, query string) (int, ChannelIssuesResponse) {
		t.Helper()
		path := fmt.Sprintf("/api/channels/%s/issues?%s", channelID, query)
		req := withURLParam(newRequest(http.MethodGet, path, nil), "channelId", channelID)
		req = withChannelTestWorkspaceCtx(t, req, testUserID)
		rec := httptest.NewRecorder()
		testHandler.ListChannelSourceIssues(rec, req)
		var response ChannelIssuesResponse
		if rec.Code == http.StatusOK {
			if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
				t.Fatalf("decode channel issues: %v", err)
			}
		}
		return rec.Code, response
	}

	code, response := list(visibleChannelID, "limit=1")
	if code != http.StatusOK || response.Total != 2 || len(response.Issues) != 1 {
		t.Fatalf("visible projection = code %d, total %d, issues %#v; want 200/2/1", code, response.Total, response.Issues)
	}
	if got := response.Issues[0].ID; got != visibleTodoID && got != visibleDoneID {
		t.Fatalf("visible projection returned unrelated issue %s", got)
	}

	code, response = list(visibleChannelID, "status=todo")
	if code != http.StatusOK || response.Total != 1 || len(response.Issues) != 1 || response.Issues[0].ID != visibleTodoID {
		t.Fatalf("status projection = code %d, total %d, issues %#v; want visible todo only", code, response.Total, response.Issues)
	}

	code, response = list(visibleChannelID, "assignee_id="+testUserID)
	if code != http.StatusOK || response.Total != 1 || len(response.Issues) != 1 || response.Issues[0].ID != visibleTodoID {
		t.Fatalf("assignee projection = code %d, total %d, issues %#v; want assigned visible todo only", code, response.Total, response.Issues)
	}

	if code, _ = list(hiddenChannelID, ""); code != http.StatusForbidden {
		t.Fatalf("hidden group = %d, want 403", code)
	}
}
