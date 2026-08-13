package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestCreateNoteRetrospectiveAggregatesIssueAndNoteFacts(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()

	touched := createNotePageForAITest(t, "Touched brief "+uuid.NewString())
	issueID, number := createIssueForNoteRefTest(t, testWorkspaceID, "Retro issue "+uuid.NewString())
	identifier := ""
	_ = number

	now := time.Now().UTC()
	if _, err := testPool.Exec(ctx, `
UPDATE note_page SET updated_at = $2 WHERE id = $1`, touched, now.Add(-time.Minute)); err != nil {
		t.Fatalf("touch note: %v", err)
	}
	if _, err := testPool.Exec(ctx, `
INSERT INTO activity_log (workspace_id, issue_id, actor_type, actor_id, action, details, created_at)
VALUES ($1, $2, 'member', $3, 'status_changed', '{"from":"todo","to":"done"}'::jsonb, $4)`,
		testWorkspaceID, issueID, testUserID, now.Add(-30*time.Second)); err != nil {
		t.Fatalf("insert activity: %v", err)
	}

	day := now.Format("2006-01-02")
	rec := httptest.NewRecorder()
	testHandler.CreateNoteRetrospective(rec, newRequest(http.MethodPost, "/api/notes/retrospectives", map[string]any{
		"window":   "day",
		"date":     day,
		"timezone": "UTC",
		"sources":  []string{"issue_activity", "touched_notes"},
	}))
	if rec.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var resp createNoteRetrospectiveResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Page.ID == "" || resp.Page.Title == "" {
		t.Fatalf("page = %#v", resp.Page)
	}
	if !strings.Contains(resp.Page.Title, "回顾") {
		t.Fatalf("title = %q", resp.Page.Title)
	}
	if resp.FactCount < 2 {
		t.Fatalf("fact_count = %d, want >= 2; used=%v empty=%v", resp.FactCount, resp.SourcesUsed, resp.SourcesEmpty)
	}
	if !strings.Contains(resp.Page.Content, "mention://issue/"+issueID) {
		t.Fatalf("content missing issue mention: %s", resp.Page.Content)
	}
	_ = identifier
	if !strings.Contains(resp.Page.Content, touched) {
		t.Fatalf("content missing touched note id: %s", resp.Page.Content)
	}

	var parentTitle string
	if err := testPool.QueryRow(ctx, `
SELECT p.title FROM note_page c
JOIN note_page p ON p.id = c.parent_id
WHERE c.id = $1`, resp.Page.ID).Scan(&parentTitle); err != nil {
		t.Fatalf("parent: %v", err)
	}
	if parentTitle != noteRetrospectiveFolderTitle {
		t.Fatalf("parent title = %q", parentTitle)
	}

	var refCount int
	if err := testPool.QueryRow(ctx, `
SELECT COUNT(*) FROM note_page_issue_ref WHERE page_id = $1 AND issue_id = $2`,
		resp.Page.ID, issueID).Scan(&refCount); err != nil {
		t.Fatalf("ref count: %v", err)
	}
	if refCount != 1 {
		t.Fatalf("issue refs = %d, want 1", refCount)
	}
}

func TestCreateNoteRetrospectiveSkipsDisabledSources(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	createNotePageForAITest(t, "Should not appear "+uuid.NewString())

	rec := httptest.NewRecorder()
	testHandler.CreateNoteRetrospective(rec, newRequest(http.MethodPost, "/api/notes/retrospectives", map[string]any{
		"window":   "day",
		"date":     time.Now().UTC().Format("2006-01-02"),
		"timezone": "UTC",
		"sources":  []string{"issue_activity"},
	}))
	if rec.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var resp createNoteRetrospectiveResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	joined := strings.Join(resp.SourcesSkipped, ",")
	if !strings.Contains(joined, "touched_notes") {
		t.Fatalf("sources_skipped = %v", resp.SourcesSkipped)
	}
	if strings.Contains(resp.Page.Content, "Should not appear") {
		t.Fatalf("disabled source leaked into content: %s", resp.Page.Content)
	}
}
