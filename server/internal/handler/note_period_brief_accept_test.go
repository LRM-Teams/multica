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

func periodBriefAcceptSetup(t *testing.T) {
	t.Helper()
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
}

func insertNoteBubbleUserMessage(t *testing.T, sessionID, content string) {
	t.Helper()
	if _, err := testPool.Exec(context.Background(), `
INSERT INTO chat_message (chat_session_id, role, content) VALUES ($1, 'user', $2)`, sessionID, content); err != nil {
		t.Fatalf("insert user speech: %v", err)
	}
}

// TestAcceptSpokenWeeklyReportCreatesRun: 「帮我写个周报」 is spoken intent;
// after the assistant fills the session plan, start creates a run (not a grey card).
func TestAcceptSpokenWeeklyReportCreatesRun(t *testing.T) {
	periodBriefAcceptSetup(t)
	if !looksLikePeriodBriefRequest("帮我写个周报") {
		t.Fatal("「帮我写个周报」 must be a 写汇报 ask")
	}

	pageID := insertPeriodBriefFixtureDraft(t, "Accept weekly")
	assistantID := createHandlerTestAgent(t, "Accept Weekly "+uuid.NewString()[:8], nil)
	sessionID := createNoteBubbleSession(t, assistantID, pageID)
	collector := createPeriodBriefCollectorTestAgent(t, "Accept Weekly Laptop")
	injectPeriodBriefCollectorPackMarkdown(t, collector, periodBriefHarvestPack("weekly-pack"))

	put := withAgentCredentialPrincipal(
		newRequest(http.MethodPut, "/api/agent/notes/period-briefs/plan", map[string]any{
			"chat_session_id":     sessionID,
			"window":              "week",
			"date":                time.Now().UTC().Format("2006-01-02"),
			"collector_agent_ids": []string{collector},
		}),
		assistantID, testWorkspaceID, testUserID,
	)
	putRec := httptest.NewRecorder()
	testHandler.PutAgentNotePeriodBriefPlan(putRec, put)
	if putRec.Code != http.StatusOK {
		t.Fatalf("put plan = %d: %s", putRec.Code, putRec.Body.String())
	}

	start := withAgentCredentialPrincipal(
		newRequest(http.MethodPost, "/api/agent/notes/period-briefs/start", map[string]any{
			"chat_session_id":      sessionID,
			"context_note_page_id": pageID,
			"timezone":             "UTC",
		}),
		assistantID, testWorkspaceID, testUserID,
	)
	startRec := httptest.NewRecorder()
	testHandler.StartAgentNotePeriodBrief(startRec, start)
	if startRec.Code != http.StatusCreated {
		t.Fatalf("start = %d: %s", startRec.Code, startRec.Body.String())
	}
	var resp startAgentNotePeriodBriefResponse
	if err := json.NewDecoder(startRec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !resp.Dispatched || resp.RunID == "" {
		t.Fatalf("spoken start must create a run, got %+v", resp)
	}
	var runCount int
	if err := testPool.QueryRow(context.Background(), `
SELECT count(*) FROM note_period_brief_run WHERE id = $1 AND chat_session_id = $2`,
		resp.RunID, sessionID).Scan(&runCount); err != nil {
		t.Fatalf("load run: %v", err)
	}
	if runCount != 1 {
		t.Fatalf("run count = %d, want a real run not an empty card", runCount)
	}
}

// TestAcceptCardStartAndSpeechStartShareThePlan: clicking Start and saying
// 「开始」 both omit window/collectors and use the same session plan.
func TestAcceptCardStartAndSpeechStartShareThePlan(t *testing.T) {
	periodBriefAcceptSetup(t)
	day := time.Now().UTC().Format("2006-01-02")

	cardPage := insertPeriodBriefFixtureDraft(t, "Accept card start")
	cardAgent := createHandlerTestAgent(t, "Accept Card "+uuid.NewString()[:8], nil)
	cardSession := createNoteBubbleSession(t, cardAgent, cardPage)
	cardCollector := createPeriodBriefCollectorTestAgent(t, "Accept Card Laptop")
	injectPeriodBriefCollectorPackMarkdown(t, cardCollector, periodBriefHarvestPack("card-pack"))

	putCard := newRequest(http.MethodPut, "/api/notes/period-briefs/plan", map[string]any{
		"chat_session_id":     cardSession,
		"window":              "day",
		"date":                day,
		"collector_agent_ids": []string{cardCollector},
	})
	putCardRec := httptest.NewRecorder()
	testHandler.PutNotePeriodBriefPlan(putCardRec, putCard)
	if putCardRec.Code != http.StatusOK {
		t.Fatalf("card put plan = %d: %s", putCardRec.Code, putCardRec.Body.String())
	}
	cardStart := newRequest(http.MethodPost, "/api/notes/period-briefs", map[string]any{
		"agent_id":             cardAgent,
		"context_note_page_id": cardPage,
		"chat_session_id":      cardSession,
		"from_chat":            true,
		"timezone":             "UTC",
	})
	cardRec := httptest.NewRecorder()
	testHandler.CreateNotePeriodBrief(cardRec, cardStart)
	if cardRec.Code != http.StatusCreated {
		t.Fatalf("card start = %d: %s", cardRec.Code, cardRec.Body.String())
	}
	var cardResp createNotePeriodBriefResponse
	if err := json.NewDecoder(cardRec.Body).Decode(&cardResp); err != nil {
		t.Fatalf("decode card: %v", err)
	}
	if len(cardResp.CollectorAgentIDs) != 1 || cardResp.CollectorAgentIDs[0] != cardCollector {
		t.Fatalf("card start collectors = %#v", cardResp.CollectorAgentIDs)
	}

	speechPage := insertPeriodBriefFixtureDraft(t, "Accept speech start")
	speechAgent := createHandlerTestAgent(t, "Accept Speech "+uuid.NewString()[:8], nil)
	speechSession := createNoteBubbleSession(t, speechAgent, speechPage)
	speechCollector := createPeriodBriefCollectorTestAgent(t, "Accept Speech Laptop")
	injectPeriodBriefCollectorPackMarkdown(t, speechCollector, periodBriefHarvestPack("speech-pack"))

	putSpeech := withAgentCredentialPrincipal(
		newRequest(http.MethodPut, "/api/agent/notes/period-briefs/plan", map[string]any{
			"chat_session_id":     speechSession,
			"window":              "day",
			"date":                day,
			"collector_agent_ids": []string{speechCollector},
		}),
		speechAgent, testWorkspaceID, testUserID,
	)
	putSpeechRec := httptest.NewRecorder()
	testHandler.PutAgentNotePeriodBriefPlan(putSpeechRec, putSpeech)
	if putSpeechRec.Code != http.StatusOK {
		t.Fatalf("speech put plan = %d: %s", putSpeechRec.Code, putSpeechRec.Body.String())
	}
	speechStart := withAgentCredentialPrincipal(
		newRequest(http.MethodPost, "/api/agent/notes/period-briefs/start", map[string]any{
			"chat_session_id":      speechSession,
			"context_note_page_id": speechPage,
			"timezone":             "UTC",
		}),
		speechAgent, testWorkspaceID, testUserID,
	)
	speechRec := httptest.NewRecorder()
	testHandler.StartAgentNotePeriodBrief(speechRec, speechStart)
	if speechRec.Code != http.StatusCreated {
		t.Fatalf("speech start = %d: %s", speechRec.Code, speechRec.Body.String())
	}
	var speechResp startAgentNotePeriodBriefResponse
	if err := json.NewDecoder(speechRec.Body).Decode(&speechResp); err != nil {
		t.Fatalf("decode speech: %v", err)
	}
	if !speechResp.Dispatched || speechResp.RunID == "" {
		t.Fatalf("speech start must dispatch a run, got %+v", speechResp)
	}

	var cardRun, speechRun int
	if err := testPool.QueryRow(context.Background(), `
SELECT count(*) FROM note_period_brief_run WHERE chat_session_id = $1`, cardSession).Scan(&cardRun); err != nil {
		t.Fatalf("count card runs: %v", err)
	}
	if err := testPool.QueryRow(context.Background(), `
SELECT count(*) FROM note_period_brief_run WHERE chat_session_id = $1`, speechSession).Scan(&speechRun); err != nil {
		t.Fatalf("count speech runs: %v", err)
	}
	if cardRun != 1 || speechRun != 1 {
		t.Fatalf("card runs=%d speech runs=%d, both starts must create a run", cardRun, speechRun)
	}
}

// TestAcceptDeadLockDoesNotBlockTheNextStart: a synthesizing orphan with no
// live worker must not 409 the next start.
func TestAcceptDeadLockDoesNotBlockTheNextStart(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	prevBG := notePeriodBriefFinishInBackground
	notePeriodBriefFinishInBackground = true
	t.Cleanup(func() { notePeriodBriefFinishInBackground = prevBG })

	pageID := insertPeriodBriefFixtureDraft(t, "Accept dead lock")
	synthID := createHandlerTestAgent(t, "Accept Dead "+uuid.NewString()[:8], nil)
	folderID, err := testHandler.ensureNotePeriodBriefFolder(context.Background(), parseUUID(testWorkspaceID), parseUUID(testUserID))
	if err != nil {
		t.Fatalf("folder: %v", err)
	}
	draftID := insertPeriodBriefFixtureDraft(t, "accept dead draft")
	_ = insertPeriodBriefFixtureRun(t, pageID, uuidToString(folderID), synthID, draftID, "synthesizing", time.Now().Add(-time.Minute))
	collector := createPeriodBriefCollectorTestAgent(t, "Accept Dead Collector")

	rec := httptest.NewRecorder()
	testHandler.CreateNotePeriodBrief(rec, newRequest(http.MethodPost, "/api/notes/period-briefs", map[string]any{
		"window":               "day",
		"date":                 time.Now().UTC().Format("2006-01-02"),
		"timezone":             "UTC",
		"agent_id":             synthID,
		"collector_agent_ids":  []string{collector},
		"context_note_page_id": pageID,
	}))
	if rec.Code != http.StatusCreated {
		t.Fatalf("next start after dead lock = %d: %s", rec.Code, rec.Body.String())
	}
}

// TestAcceptAddingAnotherComputerPostsInsertCard: 「把另一台也写进去」
// re-walks every selected computer and still posts the insert card.
func TestAcceptAddingAnotherComputerPostsInsertCard(t *testing.T) {
	periodBriefAcceptSetup(t)

	sourcePageID := insertPeriodBriefFixtureDraft(t, "Accept add computer")
	synthID := createHandlerTestAgent(t, "Accept Add Synth "+uuid.NewString()[:8], nil)
	ubuntu := createPeriodBriefCollectorTestAgent(t, "Accept Ubuntu")
	lijian := createPeriodBriefCollectorTestAgent(t, "Accept Lijian")
	injectPeriodBriefCollectorPackMarkdown(t, ubuntu, periodBriefHarvestPack("ubuntu-first"))

	day := time.Now().UTC().Format("2006-01-02")
	first := httptest.NewRecorder()
	testHandler.CreateNotePeriodBrief(first, newRequest(http.MethodPost, "/api/notes/period-briefs", map[string]any{
		"window":               "day",
		"date":                 day,
		"timezone":             "UTC",
		"agent_id":             synthID,
		"collector_agent_ids":  []string{ubuntu},
		"context_note_page_id": sourcePageID,
	}))
	if first.Code != http.StatusCreated {
		t.Fatalf("first create = %d: %s", first.Code, first.Body.String())
	}
	var firstResp createNotePeriodBriefResponse
	if err := json.NewDecoder(first.Body).Decode(&firstResp); err != nil {
		t.Fatalf("decode first: %v", err)
	}

	injectPeriodBriefCollectorPackMarkdown(t, ubuntu, periodBriefHarvestPack("ubuntu-second"))
	injectPeriodBriefCollectorPackMarkdown(t, lijian, periodBriefHarvestPack("lijian-cnc"))
	second := httptest.NewRecorder()
	testHandler.CreateNotePeriodBrief(second, newRequest(http.MethodPost, "/api/notes/period-briefs", map[string]any{
		"window":               "day",
		"date":                 day,
		"timezone":             "UTC",
		"agent_id":             synthID,
		"collector_agent_ids":  []string{ubuntu, lijian},
		"context_note_page_id": sourcePageID,
		"chat_session_id":      firstResp.ChatSessionID,
		"from_chat":            true,
	}))
	if second.Code != http.StatusCreated {
		t.Fatalf("add computer = %d: %s", second.Code, second.Body.String())
	}
	var secondResp createNotePeriodBriefResponse
	if err := json.NewDecoder(second.Body).Decode(&secondResp); err != nil {
		t.Fatalf("decode second: %v", err)
	}
	if len(secondResp.CollectorJobs) != 2 {
		t.Fatalf("start must re-walk both selected computers, jobs=%#v", secondResp.CollectorJobs)
	}
	gotJobs := map[string]struct{}{}
	for _, job := range secondResp.CollectorJobs {
		gotJobs[job.AgentID] = struct{}{}
	}
	if _, ok := gotJobs[ubuntu]; !ok {
		t.Fatalf("missing ubuntu job: %#v", secondResp.CollectorJobs)
	}
	if _, ok := gotJobs[lijian]; !ok {
		t.Fatalf("missing lijian job: %#v", secondResp.CollectorJobs)
	}
	if !strings.Contains(secondResp.Page.Content, "ubuntu-second") || !strings.Contains(secondResp.Page.Content, "lijian-cnc") {
		t.Fatalf("draft missing both harvests: %s", secondResp.Page.Content)
	}
	if strings.Contains(secondResp.Page.Content, "ubuntu-first") {
		t.Fatalf("prior ubuntu pack must not be reused: %s", secondResp.Page.Content)
	}

	var resultParts []byte
	if err := testPool.QueryRow(context.Background(), `
SELECT parts::text
FROM chat_message
WHERE chat_session_id = $1 AND role = 'assistant' AND content LIKE '汇报稿整理完成了%'
ORDER BY created_at DESC
LIMIT 1`, secondResp.ChatSessionID).Scan(&resultParts); err != nil {
		t.Fatalf("load insert card: %v", err)
	}
	if got := periodBriefPartTypes(t, resultParts); !got["note_brief"] || !got["period_brief_insert"] {
		t.Fatalf("adding a computer must post the result + insert card: %s types=%v", resultParts, got)
	}
}

// TestAcceptPartialHarvestWritesFromReadyPacks: 「再采集 windows 然后整合」
// still re-walks every selected computer; when one fails with no pack but
// another is ready, the platform writes a new official brief from ready packs.
func TestAcceptPartialHarvestWritesFromReadyPacks(t *testing.T) {
	periodBriefAcceptSetup(t)

	sourcePageID := insertPeriodBriefFixtureDraft(t, "Accept partial harvest")
	synthID := createHandlerTestAgent(t, "Accept Partial Synth "+uuid.NewString()[:8], nil)
	ubuntu := createPeriodBriefCollectorTestAgent(t, "Accept Partial Ubuntu")
	lijian := createPeriodBriefCollectorTestAgent(t, "Accept Partial Lijian")
	injectPeriodBriefCollectorPackMarkdown(t, ubuntu, periodBriefHarvestPack("ubuntu-first"))

	day := time.Now().UTC().Format("2006-01-02")
	first := httptest.NewRecorder()
	testHandler.CreateNotePeriodBrief(first, newRequest(http.MethodPost, "/api/notes/period-briefs", map[string]any{
		"window":               "day",
		"date":                 day,
		"timezone":             "UTC",
		"agent_id":             synthID,
		"collector_agent_ids":  []string{ubuntu},
		"context_note_page_id": sourcePageID,
	}))
	if first.Code != http.StatusCreated {
		t.Fatalf("first create = %d: %s", first.Code, first.Body.String())
	}
	var firstResp createNotePeriodBriefResponse
	if err := json.NewDecoder(first.Body).Decode(&firstResp); err != nil {
		t.Fatalf("decode first: %v", err)
	}

	injectPeriodBriefCollectorPackMarkdown(t, ubuntu, periodBriefHarvestPack("ubuntu-second"))
	failPeriodBriefCollectorWithoutPack(t, lijian, "No API key configured")
	second := httptest.NewRecorder()
	testHandler.CreateNotePeriodBrief(second, newRequest(http.MethodPost, "/api/notes/period-briefs", map[string]any{
		"window":               "day",
		"date":                 day,
		"timezone":             "UTC",
		"agent_id":             synthID,
		"collector_agent_ids":  []string{ubuntu, lijian},
		"context_note_page_id": sourcePageID,
		"chat_session_id":      firstResp.ChatSessionID,
		"from_chat":            true,
	}))
	if second.Code != http.StatusCreated {
		t.Fatalf("add computer = %d: %s", second.Code, second.Body.String())
	}
	var secondResp createNotePeriodBriefResponse
	if err := json.NewDecoder(second.Body).Decode(&secondResp); err != nil {
		t.Fatalf("decode second: %v", err)
	}
	if len(secondResp.CollectorJobs) != 2 {
		t.Fatalf("start must re-walk both selected computers, jobs=%#v", secondResp.CollectorJobs)
	}

	var resultCount int
	if err := testPool.QueryRow(context.Background(), `
SELECT count(*)
FROM chat_message
WHERE chat_session_id = $1 AND role = 'assistant' AND content LIKE '汇报稿整理完成了%'`,
		secondResp.ChatSessionID).Scan(&resultCount); err != nil {
		t.Fatalf("count result cards: %v", err)
	}
	if resultCount != 2 {
		t.Fatalf("partial ready harvest must post a new official brief, result cards=%d", resultCount)
	}
	var status string
	if err := testPool.QueryRow(context.Background(), `
SELECT status FROM note_period_brief_run WHERE chat_session_id = $1 ORDER BY created_at DESC LIMIT 1`,
		secondResp.ChatSessionID).Scan(&status); err != nil {
		t.Fatalf("load latest run: %v", err)
	}
	if status != "awaiting_confirm" && status != "done" && status != "synthesizing" {
		t.Fatalf("partial ready harvest status = %s, want synthesizing/awaiting_confirm/done", status)
	}
}

// TestAcceptDroppingAComputerPostsInsertCard: 「把 windows 去掉再整理」
// re-walks only the remaining selected computers and still posts the insert card.
func TestAcceptDroppingAComputerPostsInsertCard(t *testing.T) {
	periodBriefAcceptSetup(t)

	sourcePageID := insertPeriodBriefFixtureDraft(t, "Accept drop computer")
	synthID := createHandlerTestAgent(t, "Accept Drop Synth "+uuid.NewString()[:8], nil)
	ubuntu := createPeriodBriefCollectorTestAgent(t, "Accept Drop Ubuntu")
	lijian := createPeriodBriefCollectorTestAgent(t, "Accept Drop Lijian")
	injectPeriodBriefCollectorPackMarkdown(t, ubuntu, periodBriefHarvestPack("ubuntu-keep"))
	injectPeriodBriefCollectorPackMarkdown(t, lijian, periodBriefHarvestPack("lijian-cnc"))

	day := time.Now().UTC().Format("2006-01-02")
	first := httptest.NewRecorder()
	testHandler.CreateNotePeriodBrief(first, newRequest(http.MethodPost, "/api/notes/period-briefs", map[string]any{
		"window":               "day",
		"date":                 day,
		"timezone":             "UTC",
		"agent_id":             synthID,
		"collector_agent_ids":  []string{ubuntu, lijian},
		"context_note_page_id": sourcePageID,
	}))
	if first.Code != http.StatusCreated {
		t.Fatalf("first create = %d: %s", first.Code, first.Body.String())
	}
	var firstResp createNotePeriodBriefResponse
	if err := json.NewDecoder(first.Body).Decode(&firstResp); err != nil {
		t.Fatalf("decode first: %v", err)
	}

	injectPeriodBriefCollectorPackMarkdown(t, ubuntu, periodBriefHarvestPack("ubuntu-only"))
	second := httptest.NewRecorder()
	testHandler.CreateNotePeriodBrief(second, newRequest(http.MethodPost, "/api/notes/period-briefs", map[string]any{
		"window":               "day",
		"date":                 day,
		"timezone":             "UTC",
		"agent_id":             synthID,
		"collector_agent_ids":  []string{ubuntu},
		"context_note_page_id": sourcePageID,
		"chat_session_id":      firstResp.ChatSessionID,
		"from_chat":            true,
	}))
	if second.Code != http.StatusCreated {
		t.Fatalf("drop computer = %d: %s", second.Code, second.Body.String())
	}
	var secondResp createNotePeriodBriefResponse
	if err := json.NewDecoder(second.Body).Decode(&secondResp); err != nil {
		t.Fatalf("decode second: %v", err)
	}
	if len(secondResp.CollectorJobs) != 1 || secondResp.CollectorJobs[0].AgentID != ubuntu {
		t.Fatalf("drop must re-walk only the remaining computer, jobs=%#v", secondResp.CollectorJobs)
	}
	if !strings.Contains(secondResp.Page.Content, "ubuntu-only") {
		t.Fatalf("remaining computer must be re-harvested: %s", secondResp.Page.Content)
	}
	if strings.Contains(secondResp.Page.Content, "lijian-cnc") || strings.Contains(secondResp.Page.Content, "ubuntu-keep") {
		t.Fatalf("dropped / prior harvest leaked: %s", secondResp.Page.Content)
	}

	var resultParts []byte
	if err := testPool.QueryRow(context.Background(), `
SELECT parts::text
FROM chat_message
WHERE chat_session_id = $1 AND role = 'assistant' AND content LIKE '汇报稿整理完成了%'
ORDER BY created_at DESC
LIMIT 1`, secondResp.ChatSessionID).Scan(&resultParts); err != nil {
		t.Fatalf("load insert card: %v", err)
	}
	if got := periodBriefPartTypes(t, resultParts); !got["note_brief"] || !got["period_brief_insert"] {
		t.Fatalf("dropping a computer must post the result + insert card: %s types=%v", resultParts, got)
	}
}

func acceptInsertReadyRun(t *testing.T, sourcePageID, folderID, assistantID, sessionID string) string {
	t.Helper()
	draftID := insertPeriodBriefFixtureDraft(t, "accept insert draft")
	if _, err := testPool.Exec(context.Background(), `
UPDATE note_page SET content = '## Brief\n\nDone.' WHERE id = $1`, draftID); err != nil {
		t.Fatalf("seed draft: %v", err)
	}
	runID := insertPeriodBriefFixtureRun(t, sourcePageID, folderID, assistantID, draftID, "awaiting_confirm", time.Now())
	if _, err := testPool.Exec(context.Background(), `
UPDATE note_period_brief_run SET chat_session_id = $2 WHERE id = $1`, runID, sessionID); err != nil {
		t.Fatalf("bind run: %v", err)
	}
	return runID
}

func acceptAgentInsert(t *testing.T, assistantID, runID, mode, targetPageID string) insertAgentNotePeriodBriefResponse {
	t.Helper()
	req := withURLParam(withAgentCredentialPrincipal(
		newRequest(http.MethodPost, "/api/agent/notes/period-briefs/"+runID+"/insert", map[string]any{
			"mode":           mode,
			"target_page_id": targetPageID,
		}),
		assistantID, testWorkspaceID, testUserID,
	), "runId", runID)
	rec := httptest.NewRecorder()
	testHandler.InsertAgentNotePeriodBrief(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("insert %s = %d: %s", mode, rec.Code, rec.Body.String())
	}
	var resp insertAgentNotePeriodBriefResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode insert %s: %v", mode, err)
	}
	return resp
}

// TestAcceptInsertUsesTheInsertTool: 「插到这篇下面 / 做成子笔记」 is the insert
// tool; a non-issuing page stays needs_confirm.
func TestAcceptInsertUsesTheInsertTool(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	sourcePageID := insertPeriodBriefFixtureDraft(t, "Accept issuing")
	if _, err := testPool.Exec(context.Background(), `
UPDATE note_page SET content = 'Keep issuing' WHERE id = $1`, sourcePageID); err != nil {
		t.Fatalf("seed source: %v", err)
	}
	otherPageID := insertPeriodBriefFixtureDraft(t, "Accept other")
	if _, err := testPool.Exec(context.Background(), `
UPDATE note_page SET content = 'Keep other' WHERE id = $1`, otherPageID); err != nil {
		t.Fatalf("seed other: %v", err)
	}
	folderID := insertPeriodBriefFixtureDraft(t, "工作介绍")
	assistantID := createHandlerTestAgent(t, "Accept Insert "+uuid.NewString()[:8], nil)
	sessionID := createNoteBubbleSession(t, assistantID, sourcePageID)

	appendResp := acceptAgentInsert(t, assistantID, acceptInsertReadyRun(t, sourcePageID, folderID, assistantID, sessionID), "append", sourcePageID)
	if appendResp.Status != "inserted" || appendResp.Mode != "append" {
		t.Fatalf("append issuing = %+v", appendResp)
	}
	var sourceContent string
	if err := testPool.QueryRow(context.Background(), `
SELECT content FROM note_page WHERE id = $1`, sourcePageID).Scan(&sourceContent); err != nil {
		t.Fatalf("load source: %v", err)
	}
	if !strings.Contains(sourceContent, "Keep issuing") || !strings.Contains(sourceContent, "## ") {
		t.Fatalf("append must keep the page body and add the brief:\n%s", sourceContent)
	}

	childResp := acceptAgentInsert(t, assistantID, acceptInsertReadyRun(t, sourcePageID, folderID, assistantID, sessionID), "child", sourcePageID)
	if childResp.Status != "inserted" || childResp.Mode != "child" {
		t.Fatalf("child insert = %+v", childResp)
	}
	var childParent string
	if err := testPool.QueryRow(context.Background(), `
SELECT parent_id::text FROM note_page
WHERE parent_id = $1 AND deleted_at IS NULL
ORDER BY created_at DESC LIMIT 1`, sourcePageID).Scan(&childParent); err != nil {
		t.Fatalf("load child: %v", err)
	}
	if childParent != sourcePageID {
		t.Fatalf("child parent = %s, want issuing page %s", childParent, sourcePageID)
	}

	otherResp := acceptAgentInsert(t, assistantID, acceptInsertReadyRun(t, sourcePageID, folderID, assistantID, sessionID), "append", otherPageID)
	if otherResp.Status != "needs_confirm" || otherResp.PageID != otherPageID {
		t.Fatalf("non-issuing insert must wait for the human, got %+v", otherResp)
	}
	var otherContent string
	if err := testPool.QueryRow(context.Background(), `
SELECT content FROM note_page WHERE id = $1`, otherPageID).Scan(&otherContent); err != nil {
		t.Fatalf("load other: %v", err)
	}
	if otherContent != "Keep other" {
		t.Fatalf("other page must stay unchanged: %q", otherContent)
	}
}

// TestAcceptSecondStartReWalksReadyHarvests: a later start on the same
// session always walks every selected computer, even when a pack exists.
func TestAcceptSecondStartReWalksReadyHarvests(t *testing.T) {
	periodBriefAcceptSetup(t)
	sourcePageID := insertPeriodBriefFixtureDraft(t, "Accept rewalk")
	synthID := createHandlerTestAgent(t, "Accept Rewalk Synth "+uuid.NewString()[:8], nil)
	ubuntu := createPeriodBriefCollectorTestAgent(t, "Accept Rewalk Ubuntu")
	lijian := createPeriodBriefCollectorTestAgent(t, "Accept Rewalk Lijian")
	injectPeriodBriefCollectorPackMarkdown(t, ubuntu, periodBriefHarvestPack("ubuntu-first"))
	injectPeriodBriefCollectorPackMarkdown(t, lijian, periodBriefHarvestPack("lijian-first"))

	day := time.Now().UTC().Format("2006-01-02")
	first := httptest.NewRecorder()
	testHandler.CreateNotePeriodBrief(first, newRequest(http.MethodPost, "/api/notes/period-briefs", map[string]any{
		"window":               "day",
		"date":                 day,
		"timezone":             "UTC",
		"agent_id":             synthID,
		"collector_agent_ids":  []string{ubuntu, lijian},
		"context_note_page_id": sourcePageID,
	}))
	if first.Code != http.StatusCreated {
		t.Fatalf("first create = %d: %s", first.Code, first.Body.String())
	}
	var firstResp createNotePeriodBriefResponse
	if err := json.NewDecoder(first.Body).Decode(&firstResp); err != nil {
		t.Fatalf("decode first: %v", err)
	}
	if len(firstResp.CollectorJobs) != 2 {
		t.Fatalf("first jobs = %#v, want both computers", firstResp.CollectorJobs)
	}

	injectPeriodBriefCollectorPackMarkdown(t, ubuntu, periodBriefHarvestPack("ubuntu-second"))
	injectPeriodBriefCollectorPackMarkdown(t, lijian, periodBriefHarvestPack("lijian-second"))
	second := httptest.NewRecorder()
	testHandler.CreateNotePeriodBrief(second, newRequest(http.MethodPost, "/api/notes/period-briefs", map[string]any{
		"window":               "day",
		"date":                 day,
		"timezone":             "UTC",
		"agent_id":             synthID,
		"collector_agent_ids":  []string{ubuntu, lijian},
		"context_note_page_id": sourcePageID,
		"chat_session_id":      firstResp.ChatSessionID,
		"from_chat":            true,
	}))
	if second.Code != http.StatusCreated {
		t.Fatalf("second = %d: %s", second.Code, second.Body.String())
	}
	var secondResp createNotePeriodBriefResponse
	if err := json.NewDecoder(second.Body).Decode(&secondResp); err != nil {
		t.Fatalf("decode second: %v", err)
	}
	if len(secondResp.CollectorJobs) != 2 {
		t.Fatalf("second start must re-walk both computers, jobs=%#v", secondResp.CollectorJobs)
	}
	gotJobs := map[string]struct{}{}
	for _, job := range secondResp.CollectorJobs {
		gotJobs[job.AgentID] = struct{}{}
	}
	if _, ok := gotJobs[ubuntu]; !ok {
		t.Fatalf("second start missing ubuntu job: %#v", secondResp.CollectorJobs)
	}
	if _, ok := gotJobs[lijian]; !ok {
		t.Fatalf("second start missing lijian job: %#v", secondResp.CollectorJobs)
	}
	if !strings.Contains(secondResp.Page.Content, "ubuntu-second") || !strings.Contains(secondResp.Page.Content, "lijian-second") {
		t.Fatalf("second start missing new harvests: %s", secondResp.Page.Content)
	}
	if strings.Contains(secondResp.Page.Content, "ubuntu-first") || strings.Contains(secondResp.Page.Content, "lijian-first") {
		t.Fatalf("second start must not keep the prior packs: %s", secondResp.Page.Content)
	}
}

// TestAcceptSatelliteSeedsTheSameSpokenPath: the FAB satellite is 「写汇报」,
// the same ask as typing it — not a parallel POST.
func TestAcceptSatelliteSeedsTheSameSpokenPath(t *testing.T) {
	if !looksLikePeriodBriefRequest("写汇报") {
		t.Fatal("satellite copy 写汇报 must be the same 写汇报 ask")
	}
}
