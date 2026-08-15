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

func TestCreateNoteRetrospectiveEmptyWindowStillCreatesNote(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	// Far-future day: no facts. Must still 201 with JSON arrays (not null) so FE
	// schema parse keeps page.id — otherwise UI shows a false "failed" toast.
	rec := httptest.NewRecorder()
	testHandler.CreateNoteRetrospective(rec, newRequest(http.MethodPost, "/api/notes/retrospectives", map[string]any{
		"window":   "day",
		"date":     "2099-01-01",
		"timezone": "UTC",
		"sources":  []string{"issue_activity", "touched_notes", "agent_runs"},
	}))
	if rec.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var raw map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&raw); err != nil {
		t.Fatalf("decode: %v", err)
	}
	page, _ := raw["page"].(map[string]any)
	if page == nil || page["id"] == nil || page["id"] == "" {
		t.Fatalf("missing page.id: %#v", raw)
	}
	for _, key := range []string{"sources_used", "sources_empty", "sources_skipped", "layers_used", "child_pages_used"} {
		v, ok := raw[key]
		if !ok || v == nil {
			t.Fatalf("%s must be present as array, got %#v", key, raw[key])
		}
		if _, isArr := v.([]any); !isArr {
			t.Fatalf("%s = %#v, want JSON array", key, v)
		}
	}
	empty, _ := raw["sources_empty"].([]any)
	if len(empty) < 3 {
		t.Fatalf("sources_empty = %#v, want all three empty", empty)
	}
	if n, _ := raw["fact_count"].(float64); n != 0 {
		t.Fatalf("fact_count = %v, want 0", raw["fact_count"])
	}
}

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
	for _, heading := range []string{"## 亲手", "## 委派 Agent", "## 仅相关"} {
		if !strings.Contains(resp.Page.Content, heading) {
			t.Fatalf("missing attribution heading %q: %s", heading, resp.Page.Content)
		}
	}
	if !strings.Contains(resp.Page.Content, "## 亲手") || !strings.Contains(resp.Page.Content, "mention://issue/"+issueID) {
		t.Fatalf("hands-on section should include issue: %s", resp.Page.Content)
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

func TestCreateNoteRetrospectiveBucketsAttribution(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	now := time.Now().UTC()
	day := now.Format("2006-01-02")

	handsIssue, _ := createIssueForNoteRefTest(t, testWorkspaceID, "Hands issue "+uuid.NewString())
	delegatedIssue, _ := createIssueForNoteRefTest(t, testWorkspaceID, "Delegated issue "+uuid.NewString())
	relatedIssue, _ := createIssueForNoteRefTest(t, testWorkspaceID, "Related issue "+uuid.NewString())

	agentID := createHandlerTestAgent(t, "Retro Agent "+uuid.NewString()[:8], nil)
	otherUserID := createWorkspaceMemberUser(t, "Retro Other", "retro-other-"+uuid.NewString()+"@multica.test")

	if _, err := testPool.Exec(ctx, `
INSERT INTO activity_log (workspace_id, issue_id, actor_type, actor_id, action, details, created_at)
VALUES
  ($1, $2, 'member', $3, 'status_changed', '{"from":"todo","to":"done"}'::jsonb, $6),
  ($1, $4, 'member', $3, 'assignee_changed', jsonb_build_object('to_type','agent','to_id',$5::text), $6 + interval '10 seconds'),
  ($1, $4, 'agent', $5::uuid, 'status_changed', '{"from":"todo","to":"in_progress"}'::jsonb, $6 + interval '20 seconds'),
  ($1, $7, 'member', $8::uuid, 'status_changed', '{"from":"todo","to":"in_progress"}'::jsonb, $6 + interval '30 seconds')
`, testWorkspaceID, handsIssue, testUserID, delegatedIssue, agentID, now.Add(-2*time.Minute), relatedIssue, otherUserID); err != nil {
		t.Fatalf("insert activities: %v", err)
	}

	rec := httptest.NewRecorder()
	testHandler.CreateNoteRetrospective(rec, newRequest(http.MethodPost, "/api/notes/retrospectives", map[string]any{
		"window":   "day",
		"date":     day,
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
	content := resp.Page.Content

	handsIdx := strings.Index(content, "## 亲手")
	delegatedIdx := strings.Index(content, "## 委派 Agent")
	relatedIdx := strings.Index(content, "## 仅相关")
	if handsIdx < 0 || delegatedIdx < 0 || relatedIdx < 0 || !(handsIdx < delegatedIdx && delegatedIdx < relatedIdx) {
		t.Fatalf("attribution headings order wrong: hands=%d delegated=%d related=%d\n%s", handsIdx, delegatedIdx, relatedIdx, content)
	}

	handsSection := content[handsIdx:delegatedIdx]
	delegatedSection := content[delegatedIdx:relatedIdx]
	relatedSection := content[relatedIdx:]

	if !strings.Contains(handsSection, "mention://issue/"+handsIssue) {
		t.Fatalf("hands section missing hands issue: %s", handsSection)
	}
	if strings.Contains(handsSection, "mention://issue/"+relatedIssue) {
		t.Fatalf("related issue leaked into hands: %s", handsSection)
	}
	if !strings.Contains(delegatedSection, "mention://issue/"+delegatedIssue) {
		t.Fatalf("delegated section missing issue: %s", delegatedSection)
	}
	if !strings.Contains(delegatedSection, "mention://agent/"+agentID) {
		t.Fatalf("delegated section missing agent mention: %s", delegatedSection)
	}
	if !strings.Contains(relatedSection, "mention://issue/"+relatedIssue) {
		t.Fatalf("related section missing issue: %s", relatedSection)
	}
	if strings.Contains(relatedSection, "mention://issue/"+handsIssue) {
		t.Fatalf("hands issue leaked into related: %s", relatedSection)
	}
}

func TestCreateNoteRetrospectiveWeekUsesLayeredSummaries(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	// Keep this window disjoint from current-day retrospective tests. Those
	// tests intentionally create reusable day pages in the shared fixture
	// workspace, which must not silently change this test from synthesis to
	// reuse based on execution order.
	anchor := time.Date(2040, time.January, 4, 0, 0, 0, 0, time.UTC)
	dayLabel := anchor.Format("2006-01-02")

	issueID, _ := createIssueForNoteRefTest(t, testWorkspaceID, "Week layered "+uuid.NewString())
	if _, err := testPool.Exec(ctx, `
INSERT INTO activity_log (workspace_id, issue_id, actor_type, actor_id, action, details, created_at)
VALUES
  ($1, $2, 'member', $3, 'status_changed', '{"from":"todo","to":"done"}'::jsonb, $4),
  ($1, $2, 'member', $3, 'status_changed', '{"from":"done","to":"todo"}'::jsonb, $4 + interval '1 hour')
`, testWorkspaceID, issueID, testUserID, anchor.Add(10*time.Hour)); err != nil {
		t.Fatalf("insert activities: %v", err)
	}

	rec := httptest.NewRecorder()
	testHandler.CreateNoteRetrospective(rec, newRequest(http.MethodPost, "/api/notes/retrospectives", map[string]any{
		"window":   "week",
		"date":     dayLabel,
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
	if resp.Composition != noteRetrospectiveCompositionLayered {
		t.Fatalf("composition = %q", resp.Composition)
	}
	if len(resp.LayersUsed) == 0 || resp.LayersUsed[0] != "day" {
		t.Fatalf("layers_used = %v", resp.LayersUsed)
	}
	if !strings.Contains(resp.Page.Content, "## 分层说明") || !strings.Contains(resp.Page.Content, "合成日摘要") {
		t.Fatalf("expected layered week content: %s", resp.Page.Content)
	}
	for _, line := range strings.Split(resp.Page.Content, "\n") {
		if strings.TrimSpace(line) == "## 亲手" {
			t.Fatalf("week should not dump top-level day raw attribution sections: %s", resp.Page.Content)
		}
	}
	if !strings.Contains(resp.Page.Content, "mention://issue/"+issueID) {
		t.Fatalf("digest missing issue mention: %s", resp.Page.Content)
	}
}

func TestCreateNoteRetrospectiveWeekReusesExistingDayNote(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	anchor := time.Now().UTC()
	for anchor.Weekday() != time.Wednesday {
		anchor = anchor.AddDate(0, 0, -1)
	}
	dayLabel := anchor.Format("2006-01-02")
	dayTitle := "回顾 " + dayLabel

	folderID, err := testHandler.ensureNoteRetrospectiveFolder(ctx, parseUUID(testWorkspaceID), parseUUID(testUserID))
	if err != nil {
		t.Fatalf("folder: %v", err)
	}
	var dayPageID string
	if err := testPool.QueryRow(ctx, `
INSERT INTO note_page (workspace_id, parent_id, owner_user_id, title, content, sort_key, created_by, updated_by)
VALUES ($1, $2, $3, $4, $5, lpad((extract(epoch from now()) * 1000000)::bigint::text, 20, '0'), $3, $3)
RETURNING id`,
		testWorkspaceID, folderID, testUserID, dayTitle,
		"# "+dayTitle+"\n\n## 亲手\n\n- preexisting day summary line\n",
	).Scan(&dayPageID); err != nil {
		t.Fatalf("seed day note: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(ctx, `DELETE FROM note_page WHERE id = $1`, dayPageID)
	})

	issueID, _ := createIssueForNoteRefTest(t, testWorkspaceID, "Reuse day "+uuid.NewString())
	if _, err := testPool.Exec(ctx, `
INSERT INTO activity_log (workspace_id, issue_id, actor_type, actor_id, action, details, created_at)
VALUES ($1, $2, 'member', $3, 'status_changed', '{"from":"todo","to":"done"}'::jsonb, $4)`,
		testWorkspaceID, issueID, testUserID, anchor.Add(8*time.Hour)); err != nil {
		t.Fatalf("insert activity: %v", err)
	}

	rec := httptest.NewRecorder()
	testHandler.CreateNoteRetrospective(rec, newRequest(http.MethodPost, "/api/notes/retrospectives", map[string]any{
		"window":   "week",
		"date":     dayLabel,
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
	if len(resp.ChildPagesUsed) == 0 || resp.ChildPagesUsed[0] != dayPageID {
		t.Fatalf("child_pages_used = %v want %s", resp.ChildPagesUsed, dayPageID)
	}
	if !strings.Contains(resp.Page.Content, "复用已有日回顾") || !strings.Contains(resp.Page.Content, "preexisting day summary line") {
		t.Fatalf("expected reused day excerpt: %s", resp.Page.Content)
	}
}

func TestCreateNoteRetrospectiveIncludesAgentRuns(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	now := time.Now().UTC()
	day := now.Format("2006-01-02")

	agentID := createHandlerTestAgent(t, "Retro Run Agent "+uuid.NewString()[:8], nil)
	var runID string
	if err := testPool.QueryRow(ctx, `
INSERT INTO agent_inbox_event (
  workspace_id, agent_id, runtime_id, status, priority, reason, requires_wake,
  initiator_user_id, trigger_summary, terminal_outcome, completed_at, created_at
)
VALUES ($1, $2, $3, 'acked', 0, 'issue', false, $4, $5, 'completed', $6, $6)
RETURNING id`,
		testWorkspaceID, agentID, testRuntimeID, testUserID,
		"Summarize standup notes", now.Add(-time.Minute),
	).Scan(&runID); err != nil {
		t.Fatalf("insert run: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(ctx, `DELETE FROM note_page_run_ref WHERE run_id = $1`, runID)
		_, _ = testPool.Exec(ctx, `DELETE FROM agent_inbox_event WHERE id = $1`, runID)
	})

	rec := httptest.NewRecorder()
	testHandler.CreateNoteRetrospective(rec, newRequest(http.MethodPost, "/api/notes/retrospectives", map[string]any{
		"window":   "day",
		"date":     day,
		"timezone": "UTC",
		"sources":  []string{"agent_runs"},
	}))
	if rec.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var resp createNoteRetrospectiveResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !containsNoteRetrospectiveSource(resp.SourcesUsed, noteRetrospectiveSourceRuns) {
		t.Fatalf("sources_used = %v", resp.SourcesUsed)
	}
	if resp.FactCount < 1 {
		t.Fatalf("fact_count = %d", resp.FactCount)
	}
	if !strings.Contains(resp.Page.Content, "mention://run/"+runID) {
		t.Fatalf("missing run mention: %s", resp.Page.Content)
	}
	if !strings.Contains(resp.Page.Content, "Summarize standup notes") {
		t.Fatalf("missing trigger summary: %s", resp.Page.Content)
	}
	if strings.Contains(resp.Page.Content, "未接入") {
		t.Fatalf("stale unwired copy: %s", resp.Page.Content)
	}

	var refCount int
	if err := testPool.QueryRow(ctx, `
SELECT COUNT(*) FROM note_page_run_ref WHERE page_id = $1 AND run_id = $2`,
		resp.Page.ID, runID).Scan(&refCount); err != nil {
		t.Fatalf("ref count: %v", err)
	}
	if refCount != 1 {
		t.Fatalf("run refs = %d, want 1", refCount)
	}
}
