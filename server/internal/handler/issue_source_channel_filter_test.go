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

func TestListIssuesSourceChannelFilter(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	suffix := uuid.NewString()
	visibleChannelID := seedChannelForTest(t, "issue-source-filter-visible-"+suffix, testUserID)
	hiddenChannelID := seedChannelForTest(t, "issue-source-filter-hidden-"+suffix)

	createIssue := func(title, status string) string {
		t.Helper()
		w := httptest.NewRecorder()
		testHandler.CreateIssue(w, newRequest("POST", "/api/issues?workspace_id="+testWorkspaceID, map[string]any{
			"title":    title,
			"status":   status,
			"priority": "medium",
		}))
		if w.Code != http.StatusCreated {
			t.Fatalf("CreateIssue(%q) = %d: %s", title, w.Code, w.Body.String())
		}
		var issue IssueResponse
		if err := json.NewDecoder(w.Body).Decode(&issue); err != nil {
			t.Fatalf("decode created issue: %v", err)
		}
		t.Cleanup(func() { testPool.Exec(context.Background(), `DELETE FROM issue WHERE id = $1`, issue.ID) })
		return issue.ID
	}
	seedMessage := func(channelID string) string {
		t.Helper()
		var messageID string
		if err := testPool.QueryRow(ctx, `
			INSERT INTO channel_message (channel_id, workspace_id, author_type, author_id, author_name, content, source, thread_id, trigger_depth)
			VALUES ($1, $2, 'user', $3, 'Filter Test User', 'Issue filter source message', 'multica', $4, 0)
			RETURNING id`, channelID, testWorkspaceID, testUserID, "issue-source-filter-thread-"+uuid.NewString()).Scan(&messageID); err != nil {
			t.Fatalf("seed source message: %v", err)
		}
		return messageID
	}
	anchor := func(issueID, channelID string) {
		t.Helper()
		if _, err := testPool.Exec(ctx, `
			INSERT INTO issue_source_message (issue_id, workspace_id, channel_id, message_id)
			VALUES ($1, $2, $3, $4)`, issueID, testWorkspaceID, channelID, seedMessage(channelID)); err != nil {
			t.Fatalf("anchor issue %s: %v", issueID, err)
		}
	}

	visibleTodoID := createIssue("source-filter-visible-todo-"+suffix, "todo")
	visibleDoneID := createIssue("source-filter-visible-done-"+suffix, "done")
	hiddenTodoID := createIssue("source-filter-hidden-todo-"+suffix, "todo")
	_ = createIssue("source-filter-unanchored-"+suffix, "todo")
	anchor(visibleTodoID, visibleChannelID)
	anchor(visibleDoneID, visibleChannelID)
	anchor(hiddenTodoID, hiddenChannelID)

	list := func(query string) (int, []IssueResponse, int64) {
		t.Helper()
		w := httptest.NewRecorder()
		testHandler.ListIssues(w, newRequest("GET", fmt.Sprintf("/api/issues?workspace_id=%s&%s", testWorkspaceID, query), nil))
		var response struct {
			Issues []IssueResponse `json:"issues"`
			Total  int64           `json:"total"`
		}
		if w.Code == http.StatusOK {
			if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
				t.Fatalf("decode ListIssues response: %v", err)
			}
		}
		return w.Code, response.Issues, response.Total
	}

	code, issues, total := list("source_channel_id=" + visibleChannelID + "&limit=1")
	if code != http.StatusOK {
		t.Fatalf("source filter = %d", code)
	}
	if len(issues) != 1 || total != 2 {
		t.Fatalf("source filter pagination = %d issues, total %d; want 1/2", len(issues), total)
	}
	if issues[0].ID != visibleTodoID && issues[0].ID != visibleDoneID {
		t.Fatalf("source filter returned unrelated issue %s", issues[0].ID)
	}

	code, issues, total = list("source_channel_id=" + visibleChannelID + "&status=todo")
	if code != http.StatusOK || len(issues) != 1 || total != 1 || issues[0].ID != visibleTodoID {
		t.Fatalf("source+status = code %d, issues %#v, total %d; want visible todo only", code, issues, total)
	}

	code, issues, total = list("source_channel_id=" + visibleChannelID + "&open_only=true")
	if code != http.StatusOK || len(issues) != 1 || total != 1 || issues[0].ID != visibleTodoID {
		t.Fatalf("source+open_only = code %d, issues %#v, total %d; want visible todo only", code, issues, total)
	}

	if code, _, _ = list("source_channel_id=not-a-uuid"); code != http.StatusBadRequest {
		t.Fatalf("invalid source channel UUID = %d, want 400", code)
	}
	if code, _, _ = list("source_channel_id=" + hiddenChannelID); code != http.StatusForbidden {
		t.Fatalf("non-member source channel = %d, want 403", code)
	}
	if code, _, _ = list("source_channel_id=" + uuid.NewString()); code != http.StatusNotFound {
		t.Fatalf("missing source channel = %d, want 404", code)
	}
}
