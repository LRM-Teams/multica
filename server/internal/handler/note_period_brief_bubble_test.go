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

func TestFormatPeriodBriefBubbleUserTurn(t *testing.T) {
	got := formatPeriodBriefBubbleUserTurn("本周", []string{"采集 · Laptop A", "采集 · 云端 · Cloud Box"}, "只整理 ~/multica")
	want := "写汇报\n\n时间：本周\n电脑：采集 · Laptop A、采集 · 云端 · Cloud Box\n\n只整理 ~/multica"
	if got != want {
		t.Fatalf("user turn = %q, want %q", got, want)
	}
}

func TestLooksLikePeriodBriefRequest(t *testing.T) {
	if !looksLikePeriodBriefRequest("帮我写汇报") {
		t.Fatal("expected 写汇报 intent")
	}
	if looksLikePeriodBriefRequest("这段笔记的标题怎么改") {
		t.Fatal("ordinary note chat should not start 写汇报")
	}
}

func TestParsePeriodBriefIntakeWindowAndCollectors(t *testing.T) {
	kind, date, _, _, ok := parsePeriodBriefIntakeWindow("先写本周的", "2026-08-21")
	if !ok || kind != "week" || date != "2026-08-21" {
		t.Fatalf("window = %s %s ok=%v", kind, date, ok)
	}
	owned := []periodBriefOwnedCollector{{ID: "c1", Label: "采集 · Laptop A"}}
	ids, ok := parsePeriodBriefIntakeCollectors("本周，全部", owned)
	if !ok || len(ids) != 1 || ids[0] != "c1" {
		t.Fatalf("collectors = %#v ok=%v", ids, ok)
	}
	if got := periodBriefIntakeFocus("本周，全部", owned); got != "" {
		t.Fatalf("window+computers answer should not be focus, got %q", got)
	}
}

func TestPeriodBriefConfirmDecision(t *testing.T) {
	if periodBriefConfirmDecision("插入") != "yes" {
		t.Fatal("插入 should confirm")
	}
	if periodBriefConfirmDecision("插入笔记下面") != "append" {
		t.Fatal("插入笔记下面 should append")
	}
	if periodBriefConfirmDecision("插入子笔记") != "yes" {
		t.Fatal("插入子笔记 should insert a child")
	}
	if periodBriefConfirmDecision("先不了") != "no" {
		t.Fatal("先不了 should decline")
	}
	if periodBriefConfirmDecision("改一下标题") != "" {
		t.Fatal("ordinary chat should not be treated as confirm")
	}
}

func TestAppendPeriodBriefBelowNote(t *testing.T) {
	got := appendPeriodBriefBelowNote("Existing body", "工作介绍 本周", "Summary here")
	want := "Existing body\n\n## 工作介绍 本周\n\nSummary here"
	if got != want {
		t.Fatalf("append = %q, want %q", got, want)
	}
}

func TestCreateNotePeriodBriefPostsBubbleTranscriptAndInsertsUnderSourcePage(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	prevWait := notePeriodBriefCollectorMaxWait
	notePeriodBriefCollectorMaxWait = 0
	prevBG := notePeriodBriefFinishInBackground
	notePeriodBriefFinishInBackground = false
	t.Cleanup(func() {
		notePeriodBriefCollectorMaxWait = prevWait
		notePeriodBriefFinishInBackground = prevBG
	})

	var sourcePageID string
	if err := testPool.QueryRow(context.Background(), `
INSERT INTO note_page (workspace_id, owner_user_id, title, content, sort_key, created_by, updated_by)
VALUES ($1, $2, $3, '', lpad((extract(epoch from now()) * 1000000)::bigint::text, 20, '0'), $2, $2)
RETURNING id`, testWorkspaceID, testUserID, "Source page "+uuid.NewString()[:8]).Scan(&sourcePageID); err != nil {
		t.Fatalf("create source page: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM note_page WHERE id = $1`, sourcePageID)
	})

	synthID := createHandlerTestAgent(t, "Period Brief Synth "+uuid.NewString()[:8], nil)
	collectorA := createPeriodBriefCollectorTestAgent(t, "Laptop A")

	day := time.Now().UTC().Format("2006-01-02")
	rec := httptest.NewRecorder()
	testHandler.CreateNotePeriodBrief(rec, newRequest(http.MethodPost, "/api/notes/period-briefs", map[string]any{
		"window":               "day",
		"date":                 day,
		"timezone":             "UTC",
		"agent_id":             synthID,
		"collector_agent_ids":  []string{collectorA},
		"context_note_page_id": sourcePageID,
	}))
	if rec.Code != http.StatusCreated {
		t.Fatalf("period brief = %d: %s", rec.Code, rec.Body.String())
	}
	var resp createNotePeriodBriefResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.ChatSessionID == "" {
		t.Fatal("expected chat_session_id on bubble-bound create")
	}

	var joined string
	if err := testPool.QueryRow(context.Background(), `
SELECT string_agg(role || ':' || content, E'\n' ORDER BY created_at, id)
FROM chat_message
WHERE chat_session_id = $1`, resp.ChatSessionID).Scan(&joined); err != nil {
		t.Fatalf("load transcript: %v", err)
	}
	for _, want := range []string{
		"user:写汇报",
		"我将让",
		"先采集信息",
		"我已经将任务分派给",
		"汇报稿整理完成了",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("transcript missing %q:\n%s", want, joined)
		}
	}

	activeRec := httptest.NewRecorder()
	testHandler.GetActiveNotePeriodBrief(activeRec, newRequest(http.MethodGet, "/api/notes/period-briefs/active?page_id="+sourcePageID, nil))
	if activeRec.Code != http.StatusOK {
		t.Fatalf("active = %d: %s", activeRec.Code, activeRec.Body.String())
	}
	var active struct {
		Run *notePeriodBriefActiveResponse `json:"run"`
	}
	if err := json.NewDecoder(activeRec.Body).Decode(&active); err != nil {
		t.Fatalf("decode active: %v", err)
	}
	if active.Run == nil || active.Run.Status != "awaiting_confirm" {
		t.Fatalf("active run = %#v, want awaiting_confirm", active.Run)
	}

	var resultParts []byte
	if err := testPool.QueryRow(context.Background(), `
SELECT parts::text
FROM chat_message
WHERE chat_session_id = $1 AND role = 'assistant' AND content LIKE '汇报稿整理完成了%'
ORDER BY created_at DESC
LIMIT 1`, resp.ChatSessionID).Scan(&resultParts); err != nil {
		t.Fatalf("load result parts: %v", err)
	}
	if got := periodBriefPartTypes(t, resultParts); !got["note_brief"] || !got["period_brief_insert"] {
		t.Fatalf("result parts missing collapsible brief or insert card: %s types=%v", resultParts, got)
	}

	sendReq := newRequest(http.MethodPost, "/api/chat-sessions/"+resp.ChatSessionID+"/messages", map[string]any{
		"content": "插入",
	})
	sendReq = withURLParam(sendReq, "sessionId", resp.ChatSessionID)
	sendReq = withChatTestWorkspaceCtx(t, sendReq)
	sendRec := httptest.NewRecorder()
	testHandler.SendChatMessage(sendRec, sendReq)
	if sendRec.Code != http.StatusCreated {
		t.Fatalf("confirm send = %d: %s", sendRec.Code, sendRec.Body.String())
	}
	var sendResp SendChatMessageResponse
	if err := json.NewDecoder(sendRec.Body).Decode(&sendResp); err != nil {
		t.Fatalf("decode send: %v", err)
	}
	if sendResp.Pending {
		t.Fatal("confirm send should not wake a new agent turn")
	}

	var childTitle, parentID string
	if err := testPool.QueryRow(context.Background(), `
SELECT title, parent_id::text
FROM note_page
WHERE parent_id = $1 AND deleted_at IS NULL
ORDER BY created_at DESC
LIMIT 1`, sourcePageID).Scan(&childTitle, &parentID); err != nil {
		t.Fatalf("load inserted child: %v", err)
	}
	if parentID != sourcePageID {
		t.Fatalf("child parent = %s, want %s", parentID, sourcePageID)
	}
	if !strings.Contains(childTitle, "工作介绍") {
		t.Fatalf("child title = %q", childTitle)
	}
}

func TestPeriodBriefPackAndAppendInsert(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	prevWait := notePeriodBriefCollectorMaxWait
	notePeriodBriefCollectorMaxWait = 0
	prevBG := notePeriodBriefFinishInBackground
	notePeriodBriefFinishInBackground = false
	t.Cleanup(func() {
		notePeriodBriefCollectorMaxWait = prevWait
		notePeriodBriefFinishInBackground = prevBG
	})

	var sourcePageID string
	if err := testPool.QueryRow(context.Background(), `
INSERT INTO note_page (workspace_id, owner_user_id, title, content, sort_key, created_by, updated_by)
VALUES ($1, $2, $3, $4, lpad((extract(epoch from now()) * 1000000)::bigint::text, 20, '0'), $2, $2)
RETURNING id`, testWorkspaceID, testUserID, "Source page "+uuid.NewString()[:8], "Original page body").Scan(&sourcePageID); err != nil {
		t.Fatalf("create source page: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM note_page WHERE id = $1`, sourcePageID)
	})

	synthID := createHandlerTestAgent(t, "Period Brief Append "+uuid.NewString()[:8], nil)
	collectorID := createPeriodBriefCollectorTestAgent(t, "Laptop Pack")

	rec := httptest.NewRecorder()
	testHandler.CreateNotePeriodBrief(rec, newRequest(http.MethodPost, "/api/notes/period-briefs", map[string]any{
		"window":               "day",
		"date":                 time.Now().UTC().Format("2006-01-02"),
		"timezone":             "UTC",
		"agent_id":             synthID,
		"collector_agent_ids":  []string{collectorID},
		"context_note_page_id": sourcePageID,
	}))
	if rec.Code != http.StatusCreated {
		t.Fatalf("period brief = %d: %s", rec.Code, rec.Body.String())
	}
	var created createNotePeriodBriefResponse
	if err := json.NewDecoder(rec.Body).Decode(&created); err != nil {
		t.Fatalf("decode: %v", err)
	}

	packBody := "# 采集包 from bubble\n\n## Highlights\n- harvested pending proposal\n"
	submitReq := withURLParam(withAgentCredentialPrincipal(
		newRequest(http.MethodPost, "/api/agent/notes/period-briefs/"+created.Page.ID+"/submit-pack", map[string]any{
			"markdown": packBody,
		}),
		collectorID, testWorkspaceID, testUserID,
	), "draftPageId", created.Page.ID)
	submitRec := httptest.NewRecorder()
	testHandler.SubmitAgentNotePeriodBriefPack(submitRec, submitReq)
	if submitRec.Code != http.StatusOK {
		t.Fatalf("submit-pack = %d: %s", submitRec.Code, submitRec.Body.String())
	}

	var packParts []byte
	if err := testPool.QueryRow(context.Background(), `
SELECT parts::text
FROM chat_message
WHERE chat_session_id = $1 AND role = 'assistant' AND content LIKE '刚刚收到了%'
ORDER BY created_at DESC
LIMIT 1`, created.ChatSessionID).Scan(&packParts); err != nil {
		t.Fatalf("load pack parts: %v", err)
	}
	if got := periodBriefPartTypes(t, packParts); !got["note_brief"] || !strings.Contains(string(packParts), "harvested pending proposal") {
		t.Fatalf("pack message should carry a collapsible note_brief: %s types=%v", packParts, got)
	}

	_, _ = testPool.Exec(context.Background(), `
UPDATE note_period_brief_run
SET status = 'awaiting_confirm', updated_at = now()
WHERE chat_session_id = $1`, created.ChatSessionID)

	var runID string
	if err := testPool.QueryRow(context.Background(), `
SELECT id::text FROM note_period_brief_run WHERE chat_session_id = $1`, created.ChatSessionID).Scan(&runID); err != nil {
		t.Fatalf("load run: %v", err)
	}

	insertReq := newRequest(http.MethodPost, "/api/notes/period-briefs/"+runID+"/insert", map[string]any{
		"mode": "append",
	})
	insertReq = withURLParam(insertReq, "runId", runID)
	insertRec := httptest.NewRecorder()
	testHandler.InsertNotePeriodBrief(insertRec, insertReq)
	if insertRec.Code != http.StatusOK {
		t.Fatalf("insert append = %d: %s", insertRec.Code, insertRec.Body.String())
	}

	var sourceContent string
	if err := testPool.QueryRow(context.Background(), `
SELECT content FROM note_page WHERE id = $1`, sourcePageID).Scan(&sourceContent); err != nil {
		t.Fatalf("load source: %v", err)
	}
	if !strings.Contains(sourceContent, "Original page body") || !strings.Contains(sourceContent, "## ") {
		t.Fatalf("source should keep original body and gain a section:\n%s", sourceContent)
	}

	again := newRequest(http.MethodPost, "/api/notes/period-briefs/"+runID+"/insert", map[string]any{
		"mode": "child",
	})
	again = withURLParam(again, "runId", runID)
	againRec := httptest.NewRecorder()
	testHandler.InsertNotePeriodBrief(againRec, again)
	if againRec.Code != http.StatusOK {
		t.Fatalf("second insert = %d: %s", againRec.Code, againRec.Body.String())
	}
}

func TestNoteBubbleChatAsksBeforeStartingPeriodBrief(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	prevWait := notePeriodBriefCollectorMaxWait
	notePeriodBriefCollectorMaxWait = 0
	prevBG := notePeriodBriefFinishInBackground
	notePeriodBriefFinishInBackground = false
	t.Cleanup(func() {
		notePeriodBriefCollectorMaxWait = prevWait
		notePeriodBriefFinishInBackground = prevBG
	})

	var sourcePageID string
	if err := testPool.QueryRow(context.Background(), `
INSERT INTO note_page (workspace_id, owner_user_id, title, content, sort_key, created_by, updated_by)
VALUES ($1, $2, $3, '', lpad((extract(epoch from now()) * 1000000)::bigint::text, 20, '0'), $2, $2)
RETURNING id`, testWorkspaceID, testUserID, "Intake page "+uuid.NewString()[:8]).Scan(&sourcePageID); err != nil {
		t.Fatalf("create source page: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM note_page WHERE id = $1`, sourcePageID)
	})

	agentID := createHandlerTestAgent(t, "Notes Assistant "+uuid.NewString()[:8], nil)
	collector := createPeriodBriefCollectorTestAgent(t, "Laptop A")
	sessionID := createHandlerTestChatSession(t, agentID)
	if _, err := testPool.Exec(context.Background(), `
UPDATE chat_session SET context_note_page_id = $2 WHERE id = $1`, sessionID, sourcePageID); err != nil {
		t.Fatalf("bind note page: %v", err)
	}

	ask := sendNoteBubbleChat(t, sessionID, "帮我写汇报")
	if ask.Pending {
		t.Fatal("intake ask should not wake a collector/synthesizer turn")
	}
	var joined string
	if err := testPool.QueryRow(context.Background(), `
SELECT string_agg(content, E'\n' ORDER BY created_at, id)
FROM chat_message WHERE chat_session_id = $1`, sessionID).Scan(&joined); err != nil {
		t.Fatalf("load ask transcript: %v", err)
	}
	if !strings.Contains(joined, "时间") || !strings.Contains(joined, "电脑") {
		t.Fatalf("assistant should ask for time and computers:\n%s", joined)
	}
	var runCount int
	if err := testPool.QueryRow(context.Background(), `
SELECT count(*) FROM note_period_brief_run WHERE chat_session_id = $1`, sessionID).Scan(&runCount); err != nil {
		t.Fatalf("count runs: %v", err)
	}
	if runCount != 0 {
		t.Fatalf("asked before start, runs = %d", runCount)
	}

	start := sendNoteBubbleChat(t, sessionID, "本周，全部")
	if start.Pending {
		t.Fatal("starting from a completed answer should not enqueue a regular chat wake")
	}
	if err := testPool.QueryRow(context.Background(), `
SELECT count(*) FROM note_period_brief_run WHERE chat_session_id = $1`, sessionID).Scan(&runCount); err != nil {
		t.Fatalf("count runs after answer: %v", err)
	}
	if runCount != 1 {
		t.Fatalf("expected one period brief run after the answer, got %d", runCount)
	}
	var after string
	if err := testPool.QueryRow(context.Background(), `
SELECT string_agg(content, E'\n' ORDER BY created_at, id)
FROM chat_message WHERE chat_session_id = $1`, sessionID).Scan(&after); err != nil {
		t.Fatalf("load start transcript: %v", err)
	}
	if !strings.Contains(after, "我将让") {
		t.Fatalf("expected collect progress after start:\n%s", after)
	}
	_ = collector
}

func TestPeriodBriefBubbleResultIgnoresPriorFolderWrite(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	prevWait := notePeriodBriefCollectorMaxWait
	notePeriodBriefCollectorMaxWait = 0
	prevBG := notePeriodBriefFinishInBackground
	notePeriodBriefFinishInBackground = false
	t.Cleanup(func() {
		notePeriodBriefCollectorMaxWait = prevWait
		notePeriodBriefFinishInBackground = prevBG
	})

	var sourcePageID string
	if err := testPool.QueryRow(context.Background(), `
INSERT INTO note_page (workspace_id, owner_user_id, title, content, sort_key, created_by, updated_by)
VALUES ($1, $2, $3, '', lpad((extract(epoch from now()) * 1000000)::bigint::text, 20, '0'), $2, $2)
RETURNING id`, testWorkspaceID, testUserID, "Source page "+uuid.NewString()[:8]).Scan(&sourcePageID); err != nil {
		t.Fatalf("create source page: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM note_page WHERE id = $1`, sourcePageID)
	})

	synthID := createHandlerTestAgent(t, "Period Brief Stale "+uuid.NewString()[:8], nil)
	collectorID := createPeriodBriefCollectorTestAgent(t, "Laptop Stale")
	folderID, err := testHandler.ensureNotePeriodBriefFolder(context.Background(), parseUUID(testWorkspaceID), parseUUID(testUserID))
	if err != nil {
		t.Fatalf("ensure folder: %v", err)
	}
	plantPeriodBriefFolderNoteWrite(t, synthID, uuidToString(folderID), "# OLD BRIEF FROM LAST WEEK\n\nThis must not appear in the next bubble card.")

	rec := httptest.NewRecorder()
	testHandler.CreateNotePeriodBrief(rec, newRequest(http.MethodPost, "/api/notes/period-briefs", map[string]any{
		"window":               "day",
		"date":                 time.Now().UTC().Format("2006-01-02"),
		"timezone":             "UTC",
		"agent_id":             synthID,
		"collector_agent_ids":  []string{collectorID},
		"context_note_page_id": sourcePageID,
	}))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create brief = %d: %s", rec.Code, rec.Body.String())
	}
	var created createNotePeriodBriefResponse
	if err := json.NewDecoder(rec.Body).Decode(&created); err != nil {
		t.Fatalf("decode: %v", err)
	}

	var resultParts []byte
	if err := testPool.QueryRow(context.Background(), `
SELECT parts::text
FROM chat_message
WHERE chat_session_id = $1 AND role = 'assistant' AND content LIKE '汇报稿整理完成了%'
ORDER BY created_at DESC
LIMIT 1`, created.ChatSessionID).Scan(&resultParts); err != nil {
		t.Fatalf("load result parts: %v", err)
	}
	if strings.Contains(string(resultParts), "OLD BRIEF FROM LAST WEEK") {
		t.Fatalf("bubble reused a prior folder note_write as this run's brief: %s", resultParts)
	}

	var runID string
	if err := testPool.QueryRow(context.Background(), `
SELECT id::text FROM note_period_brief_run WHERE chat_session_id = $1
ORDER BY created_at DESC LIMIT 1`, created.ChatSessionID).Scan(&runID); err != nil {
		t.Fatalf("load run: %v", err)
	}
	run, err := testHandler.loadNotePeriodBriefRunByID(context.Background(), parseUUID(testWorkspaceID), parseUUID(testUserID), parseUUID(runID))
	if err != nil {
		t.Fatalf("load run row: %v", err)
	}
	plantPeriodBriefFolderNoteWrite(t, synthID, uuidToString(folderID), "# THIS RUN BRIEF\n\nCurrent synthesizer output.")
	if got := testHandler.loadPeriodBriefSynthesizerWrite(context.Background(), run); !strings.Contains(got, "THIS RUN BRIEF") {
		t.Fatalf("this-run folder write not harvested: %q", got)
	}
}

func plantPeriodBriefFolderNoteWrite(t *testing.T, authorID, folderID, body string) {
	t.Helper()
	var channelID string
	if err := testPool.QueryRow(context.Background(), `
INSERT INTO channel (workspace_id, name, created_by, kind)
VALUES ($1, $2, $3, 'dm')
RETURNING id::text`, testWorkspaceID, "period-brief-stale-"+uuid.NewString()[:8], testUserID).Scan(&channelID); err != nil {
		t.Fatalf("create channel: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM channel_message WHERE channel_id = $1`, channelID)
		_, _ = testPool.Exec(context.Background(), `DELETE FROM channel WHERE id = $1`, channelID)
	})
	parts := `[{"type":"note_write","ref_id":"` + folderID + `"}]`
	if _, err := testPool.Exec(context.Background(), `
INSERT INTO channel_message (channel_id, workspace_id, author_type, author_id, author_name, content, source, parts)
VALUES ($1, $2, 'agent', $3, 'synth', $4, 'multica', $5::jsonb)`,
		channelID, testWorkspaceID, authorID, body, parts); err != nil {
		t.Fatalf("plant note_write: %v", err)
	}
}

func periodBriefPartTypes(t *testing.T, raw []byte) map[string]bool {
	t.Helper()
	var parts []map[string]any
	if err := json.Unmarshal(raw, &parts); err != nil {
		t.Fatalf("decode parts %q: %v", raw, err)
	}
	out := map[string]bool{}
	for _, part := range parts {
		if typ, ok := part["type"].(string); ok {
			out[typ] = true
		}
	}
	return out
}

func sendNoteBubbleChat(t *testing.T, sessionID, content string) SendChatMessageResponse {
	t.Helper()
	req := newRequest(http.MethodPost, "/api/chat-sessions/"+sessionID+"/messages", map[string]any{
		"content": content,
	})
	req = withURLParam(req, "sessionId", sessionID)
	req = withChatTestWorkspaceCtx(t, req)
	rec := httptest.NewRecorder()
	testHandler.SendChatMessage(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("send %q = %d: %s", content, rec.Code, rec.Body.String())
	}
	var resp SendChatMessageResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode send: %v", err)
	}
	return resp
}
