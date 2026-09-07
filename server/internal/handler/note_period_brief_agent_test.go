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

func createNoteBubbleSession(t *testing.T, agentID, pageID string) string {
	t.Helper()
	var sessionID string
	if err := testPool.QueryRow(context.Background(), `
INSERT INTO chat_session (workspace_id, agent_id, creator_id, title)
VALUES ($1, $2, $3, 'note bubble')
RETURNING id`, testWorkspaceID, agentID, testUserID).Scan(&sessionID); err != nil {
		t.Fatalf("create bubble session: %v", err)
	}
	if _, err := testPool.Exec(context.Background(), `
UPDATE chat_session SET context_note_page_id = $2 WHERE id = $1`, sessionID, pageID); err != nil {
		t.Fatalf("bind bubble session: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM chat_session WHERE id = $1`, sessionID)
	})
	return sessionID
}

func TestStartAgentNotePeriodBriefDispatchesCollectors(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	prevWait := notePeriodBriefCollectorMaxWait
	notePeriodBriefCollectorMaxWait = 2 * time.Second
	prevBG := notePeriodBriefFinishInBackground
	notePeriodBriefFinishInBackground = false
	t.Cleanup(func() {
		notePeriodBriefCollectorMaxWait = prevWait
		notePeriodBriefFinishInBackground = prevBG
	})

	sourcePageID := insertPeriodBriefFixtureDraft(t, "Agent start page")
	assistantID := createHandlerTestAgent(t, "Notes Assistant Start "+uuid.NewString()[:8], nil)
	sessionID := createNoteBubbleSession(t, assistantID, sourcePageID)
	collectorA := createPeriodBriefCollectorTestAgent(t, "Laptop A")
	injectPeriodBriefCollectorPackMarkdown(t, collectorA, "## Work groups\n\n### Ready\n- injected pack\n")

	day := time.Now().UTC().Format("2006-01-02")
	req := withAgentCredentialPrincipal(
		newRequest(http.MethodPost, "/api/agent/notes/period-briefs/start", map[string]any{
			"chat_session_id":      sessionID,
			"context_note_page_id": sourcePageID,
			"window":               "day",
			"date":                 day,
			"timezone":             "UTC",
			"collector_agent_ids":  []string{collectorA},
		}),
		assistantID, testWorkspaceID, testUserID,
	)
	rec := httptest.NewRecorder()
	testHandler.StartAgentNotePeriodBrief(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("start = %d: %s", rec.Code, rec.Body.String())
	}
	var resp startAgentNotePeriodBriefResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !resp.Dispatched || resp.Status != "dispatched" || resp.RunID == "" || resp.ChatSessionID != sessionID {
		t.Fatalf("start response = %+v", resp)
	}

	var runCount int
	if err := testPool.QueryRow(context.Background(), `
SELECT count(*) FROM note_period_brief_run WHERE id = $1 AND chat_session_id = $2`,
		resp.RunID, sessionID).Scan(&runCount); err != nil {
		t.Fatalf("load run: %v", err)
	}
	if runCount != 1 {
		t.Fatalf("run count = %d", runCount)
	}
}

func TestStartAgentNotePeriodBriefRejectsOtherAgentSession(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	sourcePageID := insertPeriodBriefFixtureDraft(t, "Other agent page")
	assistantID := createHandlerTestAgent(t, "Notes Assistant Other "+uuid.NewString()[:8], nil)
	intruderID := createHandlerTestAgent(t, "Intruder "+uuid.NewString()[:8], nil)
	sessionID := createNoteBubbleSession(t, assistantID, sourcePageID)
	collectorA := createPeriodBriefCollectorTestAgent(t, "Laptop B")

	req := withAgentCredentialPrincipal(
		newRequest(http.MethodPost, "/api/agent/notes/period-briefs/start", map[string]any{
			"chat_session_id":      sessionID,
			"context_note_page_id": sourcePageID,
			"window":               "week",
			"timezone":             "UTC",
			"collector_agent_ids":  []string{collectorA},
		}),
		intruderID, testWorkspaceID, testUserID,
	)
	rec := httptest.NewRecorder()
	testHandler.StartAgentNotePeriodBrief(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("intruder start = %d, want 404: %s", rec.Code, rec.Body.String())
	}
}

func TestStartAgentNotePeriodBriefRejectsEmptyCollectors(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	sourcePageID := insertPeriodBriefFixtureDraft(t, "Empty collectors page")
	assistantID := createHandlerTestAgent(t, "Notes Assistant Empty "+uuid.NewString()[:8], nil)
	sessionID := createNoteBubbleSession(t, assistantID, sourcePageID)

	req := withAgentCredentialPrincipal(
		newRequest(http.MethodPost, "/api/agent/notes/period-briefs/start", map[string]any{
			"chat_session_id":      sessionID,
			"context_note_page_id": sourcePageID,
			"window":               "week",
			"timezone":             "UTC",
			"collector_agent_ids":  []string{},
		}),
		assistantID, testWorkspaceID, testUserID,
	)
	rec := httptest.NewRecorder()
	testHandler.StartAgentNotePeriodBrief(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("empty collectors = %d, want 400: %s", rec.Code, rec.Body.String())
	}
}

func TestInsertAgentNotePeriodBriefInsertsOnIssuingPage(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	sourcePageID := insertPeriodBriefFixtureDraft(t, "Issuing")
	if _, err := testPool.Exec(context.Background(), `
UPDATE note_page SET content = 'Keep me' WHERE id = $1`, sourcePageID); err != nil {
		t.Fatalf("seed source: %v", err)
	}
	draftID := insertPeriodBriefFixtureDraft(t, "awaiting draft")
	if _, err := testPool.Exec(context.Background(), `
UPDATE note_page SET content = '## Brief\n\nDone.' WHERE id = $1`, draftID); err != nil {
		t.Fatalf("seed draft: %v", err)
	}
	folderID := insertPeriodBriefFixtureDraft(t, "工作介绍")
	assistantID := createHandlerTestAgent(t, "Insert Agent "+uuid.NewString()[:8], nil)
	sessionID := createNoteBubbleSession(t, assistantID, sourcePageID)
	runID := insertPeriodBriefFixtureRun(t, sourcePageID, folderID, assistantID, draftID, "awaiting_confirm", time.Now())
	if _, err := testPool.Exec(context.Background(), `
UPDATE note_period_brief_run SET chat_session_id = $2 WHERE id = $1`, runID, sessionID); err != nil {
		t.Fatalf("bind run session: %v", err)
	}

	req := withURLParam(withAgentCredentialPrincipal(
		newRequest(http.MethodPost, "/api/agent/notes/period-briefs/"+runID+"/insert", map[string]any{
			"mode":           "append",
			"target_page_id": sourcePageID,
		}),
		assistantID, testWorkspaceID, testUserID,
	), "runId", runID)
	rec := httptest.NewRecorder()
	testHandler.InsertAgentNotePeriodBrief(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("insert issuing = %d: %s", rec.Code, rec.Body.String())
	}
	var resp insertAgentNotePeriodBriefResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Status != "inserted" || resp.Mode != "append" || resp.PageID != sourcePageID {
		t.Fatalf("insert response = %+v", resp)
	}

	var content string
	if err := testPool.QueryRow(context.Background(), `
SELECT content FROM note_page WHERE id = $1`, sourcePageID).Scan(&content); err != nil {
		t.Fatalf("load source: %v", err)
	}
	if !strings.Contains(content, "Keep me") || !strings.Contains(content, "## ") {
		t.Fatalf("issuing page should keep body and gain a section:\n%s", content)
	}
}

func TestInsertAgentNotePeriodBriefNeedsConfirmOffIssuingPage(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	sourcePageID := insertPeriodBriefFixtureDraft(t, "Issuing")
	targetPageID := insertPeriodBriefFixtureDraft(t, "Other note")
	if _, err := testPool.Exec(context.Background(), `
UPDATE note_page SET content = 'Keep me' WHERE id = $1`, targetPageID); err != nil {
		t.Fatalf("seed target: %v", err)
	}
	draftID := insertPeriodBriefFixtureDraft(t, "awaiting draft")
	folderID := insertPeriodBriefFixtureDraft(t, "工作介绍")
	assistantID := createHandlerTestAgent(t, "Insert Confirm "+uuid.NewString()[:8], nil)
	sessionID := createNoteBubbleSession(t, assistantID, sourcePageID)
	runID := insertPeriodBriefFixtureRun(t, sourcePageID, folderID, assistantID, draftID, "awaiting_confirm", time.Now())
	if _, err := testPool.Exec(context.Background(), `
UPDATE note_period_brief_run SET chat_session_id = $2 WHERE id = $1`, runID, sessionID); err != nil {
		t.Fatalf("bind run session: %v", err)
	}

	req := withURLParam(withAgentCredentialPrincipal(
		newRequest(http.MethodPost, "/api/agent/notes/period-briefs/"+runID+"/insert", map[string]any{
			"mode":           "append",
			"target_page_id": targetPageID,
		}),
		assistantID, testWorkspaceID, testUserID,
	), "runId", runID)
	rec := httptest.NewRecorder()
	testHandler.InsertAgentNotePeriodBrief(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("needs confirm = %d: %s", rec.Code, rec.Body.String())
	}
	var resp insertAgentNotePeriodBriefResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Status != "needs_confirm" || resp.PageID != targetPageID {
		t.Fatalf("confirm response = %+v", resp)
	}

	var targetContent string
	if err := testPool.QueryRow(context.Background(), `
SELECT content FROM note_page WHERE id = $1`, targetPageID).Scan(&targetContent); err != nil {
		t.Fatalf("load target: %v", err)
	}
	if targetContent != "Keep me" {
		t.Fatalf("other page must stay unchanged: %q", targetContent)
	}

	var parts json.RawMessage
	if err := testPool.QueryRow(context.Background(), `
SELECT parts FROM chat_message
WHERE chat_session_id = $1 AND parts::text LIKE '%period_brief_insert_propose%'
ORDER BY created_at DESC LIMIT 1`, sessionID).Scan(&parts); err != nil {
		t.Fatalf("propose part: %v", err)
	}
	if !strings.Contains(string(parts), targetPageID) || !strings.Contains(string(parts), runID) {
		t.Fatalf("propose parts = %s", parts)
	}
}

func TestInsertAgentNotePeriodBriefRejectsCollector(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	sourcePageID := insertPeriodBriefFixtureDraft(t, "Issuing")
	draftID := insertPeriodBriefFixtureDraft(t, "awaiting draft")
	folderID := insertPeriodBriefFixtureDraft(t, "工作介绍")
	assistantID := createHandlerTestAgent(t, "Insert Owner "+uuid.NewString()[:8], nil)
	collectorID := createHandlerTestAgent(t, "Collector "+uuid.NewString()[:8], nil)
	runID := insertPeriodBriefFixtureRun(t, sourcePageID, folderID, assistantID, draftID, "awaiting_confirm", time.Now())

	req := withURLParam(withAgentCredentialPrincipal(
		newRequest(http.MethodPost, "/api/agent/notes/period-briefs/"+runID+"/insert", map[string]any{
			"mode":           "append",
			"target_page_id": sourcePageID,
		}),
		collectorID, testWorkspaceID, testUserID,
	), "runId", runID)
	rec := httptest.NewRecorder()
	testHandler.InsertAgentNotePeriodBrief(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("collector insert = %d, want 404: %s", rec.Code, rec.Body.String())
	}
}
