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

func TestPeriodBriefPackHasHarvest(t *testing.T) {
	t.Parallel()
	empty := "# 采集包\n\n## Highlights\n- No in-window commits, dirty git trees, or non-git files were found.\n"
	if periodBriefPackHasHarvest(empty) {
		t.Fatal("empty-scan pack must not count as harvest")
	}
	if periodBriefPackHasHarvest("") {
		t.Fatal("blank pack must not count as harvest")
	}
	ready := "# 采集包\n\n## Highlights\n- D:/pi/legal_downloads/henan.txt was modified in-window\n"
	if !periodBriefPackHasHarvest(ready) {
		t.Fatal("ready pack must count as harvest")
	}
	mixed := "# 采集包\n\n## Highlights\n- Multica notes in-window work\n- docker_image_for_docker carry dirty files whose mtimes are all **outside** the window — listed as idle/dirty pre-window, no in-window claims made from them.\n"
	if !periodBriefPackHasHarvest(mixed) {
		t.Fatal("a real highlight must count as harvest even if a later bullet mentions no in-window")
	}
}

func TestLooksLikePeriodBriefPlanAsk(t *testing.T) {
	t.Parallel()
	for _, text := range []string{
		"写汇报",
		"帮我写个周报",
		"重新写一个汇报",
		"下面重新采集，重新给我写一个汇报",
		"重新采集",
		"再采集一遍 windows",
		"re-collect both machines",
	} {
		if !looksLikePeriodBriefPlanAsk(text) {
			t.Fatalf("%q must match the plan-ask intercept", text)
		}
	}
	for _, text := range []string{
		"把 windows 也写进去",
		"把那台去掉再整理",
		"这段笔记的标题怎么改",
	} {
		if looksLikePeriodBriefPlanAsk(text) {
			t.Fatalf("%q must not match the plan-ask intercept", text)
		}
	}
}

func TestPeriodBriefDirectOpenAsk(t *testing.T) {
	t.Parallel()
	if !periodBriefDirectOpenAsk("写汇报") {
		t.Fatal("exact 写汇报 must open the card without soft-confirm")
	}
	if !periodBriefDirectOpenAsk("  写汇报  ") {
		t.Fatal("trimmed 写汇报 must open the card")
	}
	for _, text := range []string{"帮我写汇报", "是", "重新采集", "写汇报吧"} {
		if periodBriefDirectOpenAsk(text) {
			t.Fatalf("%q must not skip soft-confirm", text)
		}
	}
}

func TestPeriodBriefPackMachineIdentityReadsRuntimeLine(t *testing.T) {
	t.Parallel()
	host, osFamily := periodBriefPackMachineIdentity("## Runtime\n- hostname / env: web-01 / Linux\n")
	if host != "web-01" || osFamily != "linux" {
		t.Fatalf("linux identity = %q %q", host, osFamily)
	}
	host, osFamily = periodBriefPackMachineIdentity("## Runtime\n- hostname / env: office-pc / MINGW64_NT-10.0-26200\n")
	if host != "office-pc" || osFamily != "windows" {
		t.Fatalf("windows identity = %q %q", host, osFamily)
	}
	host, osFamily = periodBriefPackMachineIdentity("## Runtime\n- hostname / env: buildbox / Debian\n")
	if host != "buildbox" || osFamily != "linux" {
		t.Fatalf("debian identity = %q %q", host, osFamily)
	}
}

func TestFormatPeriodBriefSessionMaterialsBoardIsReadOnly(t *testing.T) {
	t.Parallel()
	got := formatPeriodBriefSessionMaterialsBoard([]periodBriefSessionMaterial{
		{AgentID: "win-1", Label: "采集 · Pi (office-pc)", Hostname: "office-pc", OS: "windows", WindowLabel: "2026-W36", InLatestRun: false},
		{AgentID: "linux-1", Label: "采集 · Pi (web-01)", Hostname: "web-01", OS: "linux", WindowLabel: "2026-W36", InLatestRun: true},
	})
	for _, want := range []string{
		"<period_brief_session_materials>",
		"name: 采集 · Pi (office-pc)",
		"os: windows",
		"hostname: office-pc",
		"in_latest_run: no",
		"name: 采集 · Pi (web-01)",
		"os: linux",
		"hostname: web-01",
		"in_latest_run: yes",
		"os, hostname, or collector name",
		"must NOT use these packs",
		"explicitly asks",
		"Do not call start",
		"写汇报",
		"开始采集",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("board missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "<period_brief_resynth/>") {
		t.Fatalf("board still teaches resynth fence:\n%s", got)
	}
	if strings.Contains(got, "collector pack") || strings.Contains(got, "## Highlights") {
		t.Fatalf("board leaked pack bodies:\n%s", got)
	}
}

func TestCreateNotePeriodBriefSecondStartReWalksSelectedComputers(t *testing.T) {
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

	var sourcePageID string
	if err := testPool.QueryRow(context.Background(), `
INSERT INTO note_page (workspace_id, owner_user_id, title, content, sort_key, created_by, updated_by)
VALUES ($1, $2, $3, '', lpad((extract(epoch from now()) * 1000000)::bigint::text, 20, '0'), $2, $2)
RETURNING id`, testWorkspaceID, testUserID, "Carry page "+uuid.NewString()[:8]).Scan(&sourcePageID); err != nil {
		t.Fatalf("create source page: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM note_period_brief_run WHERE source_page_id = $1`, sourcePageID)
		_, _ = testPool.Exec(context.Background(), `DELETE FROM note_page WHERE id = $1`, sourcePageID)
	})

	synthID := createHandlerTestAgent(t, "Carry Synth "+uuid.NewString()[:8], nil)
	ubuntu := createPeriodBriefCollectorTestAgent(t, "Carry Ubuntu")
	lijian := createPeriodBriefCollectorTestAgent(t, "Carry Lijian")
	injectPeriodBriefCollectorPackMarkdown(t, ubuntu, periodBriefHarvestPack("ubuntu-first"))
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
	if firstResp.ChatSessionID == "" {
		t.Fatal("first create should bind a bubble session")
	}

	injectPeriodBriefCollectorPackMarkdown(t, ubuntu, periodBriefHarvestPack("ubuntu-second"))
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
		t.Fatalf("second create = %d: %s", second.Code, second.Body.String())
	}
	var secondResp createNotePeriodBriefResponse
	if err := json.NewDecoder(second.Body).Decode(&secondResp); err != nil {
		t.Fatalf("decode second: %v", err)
	}
	if strings.Contains(secondResp.Page.Content, "lijian-cnc") {
		t.Fatalf("a computer off this plan must not feed the official brief: %s", secondResp.Page.Content)
	}
	if strings.Contains(secondResp.Page.Content, "ubuntu-first") {
		t.Fatalf("second start must re-walk ubuntu, not keep the first pack: %s", secondResp.Page.Content)
	}
	if !strings.Contains(secondResp.Page.Content, "ubuntu-second") {
		t.Fatalf("second start must use the new ubuntu harvest: %s", secondResp.Page.Content)
	}
	if strings.Contains(secondResp.Page.Content, "source: carried") {
		t.Fatalf("second start must not carry session packs: %s", secondResp.Page.Content)
	}
	if len(secondResp.CollectorJobs) != 1 || secondResp.CollectorJobs[0].AgentID != ubuntu {
		t.Fatalf("second start must collect the selected computer, jobs=%#v", secondResp.CollectorJobs)
	}
}

func TestCreateNotePeriodBriefSecondStartReWalksEveryComputer(t *testing.T) {
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

	var sourcePageID string
	if err := testPool.QueryRow(context.Background(), `
INSERT INTO note_page (workspace_id, owner_user_id, title, content, sort_key, created_by, updated_by)
VALUES ($1, $2, $3, '', lpad((extract(epoch from now()) * 1000000)::bigint::text, 20, '0'), $2, $2)
RETURNING id`, testWorkspaceID, testUserID, "Missing page "+uuid.NewString()[:8]).Scan(&sourcePageID); err != nil {
		t.Fatalf("create source page: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM note_period_brief_run WHERE source_page_id = $1`, sourcePageID)
		_, _ = testPool.Exec(context.Background(), `DELETE FROM note_page WHERE id = $1`, sourcePageID)
	})

	synthID := createHandlerTestAgent(t, "Missing Synth "+uuid.NewString()[:8], nil)
	ubuntu := createPeriodBriefCollectorTestAgent(t, "Missing Ubuntu")
	lijian := createPeriodBriefCollectorTestAgent(t, "Missing Lijian")
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
		t.Fatalf("second create = %d: %s", second.Code, second.Body.String())
	}
	var secondResp createNotePeriodBriefResponse
	if err := json.NewDecoder(second.Body).Decode(&secondResp); err != nil {
		t.Fatalf("decode second: %v", err)
	}
	if strings.Contains(secondResp.Page.Content, "ubuntu-first") {
		t.Fatalf("second start must re-walk ubuntu, not keep the first pack: %s", secondResp.Page.Content)
	}
	if !strings.Contains(secondResp.Page.Content, "ubuntu-second") {
		t.Fatalf("ubuntu must be collected again: %s", secondResp.Page.Content)
	}
	if !strings.Contains(secondResp.Page.Content, "lijian-cnc") {
		t.Fatalf("lijian must be collected: %s", secondResp.Page.Content)
	}
	if strings.Contains(secondResp.Page.Content, "source: carried") {
		t.Fatalf("second start must not carry session packs: %s", secondResp.Page.Content)
	}
	if len(secondResp.CollectorJobs) != 2 {
		t.Fatalf("second start must collect every selected computer, jobs=%#v", secondResp.CollectorJobs)
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

func TestCreateNotePeriodBriefDoesNotCarryOtherSessionPacks(t *testing.T) {
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

	var sourcePageID string
	if err := testPool.QueryRow(context.Background(), `
INSERT INTO note_page (workspace_id, owner_user_id, title, content, sort_key, created_by, updated_by)
VALUES ($1, $2, $3, '', lpad((extract(epoch from now()) * 1000000)::bigint::text, 20, '0'), $2, $2)
RETURNING id`, testWorkspaceID, testUserID, "Slate page "+uuid.NewString()[:8]).Scan(&sourcePageID); err != nil {
		t.Fatalf("create source page: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM note_period_brief_run WHERE source_page_id = $1`, sourcePageID)
		_, _ = testPool.Exec(context.Background(), `DELETE FROM note_page WHERE id = $1`, sourcePageID)
	})

	synthID := createHandlerTestAgent(t, "Slate Synth "+uuid.NewString()[:8], nil)
	ubuntu := createPeriodBriefCollectorTestAgent(t, "Slate Ubuntu")
	lijian := createPeriodBriefCollectorTestAgent(t, "Slate Lijian")
	injectPeriodBriefCollectorPackMarkdown(t, ubuntu, periodBriefHarvestPack("ubuntu-first"))
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

	second := httptest.NewRecorder()
	testHandler.CreateNotePeriodBrief(second, newRequest(http.MethodPost, "/api/notes/period-briefs", map[string]any{
		"window":               "day",
		"date":                 day,
		"timezone":             "UTC",
		"agent_id":             synthID,
		"collector_agent_ids":  []string{ubuntu},
		"context_note_page_id": sourcePageID,
	}))
	if second.Code != http.StatusCreated {
		t.Fatalf("second create = %d: %s", second.Code, second.Body.String())
	}
	var secondResp createNotePeriodBriefResponse
	if err := json.NewDecoder(second.Body).Decode(&secondResp); err != nil {
		t.Fatalf("decode second: %v", err)
	}
	if strings.Contains(secondResp.Page.Content, "lijian-cnc") {
		t.Fatalf("new bubble session must not steal the other session's lijian pack: %s", secondResp.Page.Content)
	}
}

func TestCreateNotePeriodBriefDoesNotReuseSessionHarvests(t *testing.T) {
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

	sourcePageID := insertPeriodBriefFixtureDraft(t, "Harvest source")
	synthID := createHandlerTestAgent(t, "Harvest Synth "+uuid.NewString()[:8], nil)
	sessionID := createHandlerTestChatSession(t, synthID)
	if _, err := testPool.Exec(context.Background(), `
UPDATE chat_session SET context_note_page_id = $1 WHERE id = $2`, sourcePageID, sessionID); err != nil {
		t.Fatalf("bind session: %v", err)
	}
	ubuntu := createPeriodBriefCollectorTestAgent(t, "Harvest Ubuntu")
	lijian := createPeriodBriefCollectorTestAgent(t, "Harvest Lijian")
	folderID, err := testHandler.ensureNotePeriodBriefFolder(context.Background(), parseUUID(testWorkspaceID), parseUUID(testUserID))
	if err != nil {
		t.Fatalf("folder: %v", err)
	}
	draftID := insertPeriodBriefFixtureDraft(t, "prior draft")
	runID := insertPeriodBriefFixtureRun(t, sourcePageID, uuidToString(folderID), synthID, draftID, "done", time.Now())
	if _, err := testPool.Exec(context.Background(), `
UPDATE note_period_brief_run
SET chat_session_id = $1, collectors = $2::jsonb
WHERE id = $3`, sessionID, periodBriefCollectorsJSON(t, map[string]string{
		ubuntu: periodBriefHarvestPack("ubuntu-old"),
		lijian: periodBriefHarvestPack("lijian-old"),
	}), runID); err != nil {
		t.Fatalf("bind collectors: %v", err)
	}
	injectPeriodBriefCollectorPackMarkdown(t, ubuntu, periodBriefHarvestPack("ubuntu-fresh"))
	injectPeriodBriefCollectorPackMarkdown(t, lijian, periodBriefHarvestPack("lijian-fresh"))

	rec := httptest.NewRecorder()
	testHandler.CreateNotePeriodBrief(rec, newRequest(http.MethodPost, "/api/notes/period-briefs", map[string]any{
		"window":               "day",
		"date":                 time.Now().UTC().Format("2006-01-02"),
		"timezone":             "UTC",
		"agent_id":             synthID,
		"collector_agent_ids":  []string{ubuntu, lijian},
		"context_note_page_id": sourcePageID,
		"chat_session_id":      sessionID,
		"from_chat":            true,
	}))
	if rec.Code != http.StatusCreated {
		t.Fatalf("start = %d: %s", rec.Code, rec.Body.String())
	}
	var resp createNotePeriodBriefResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.CollectorJobs) != 2 {
		t.Fatalf("start must walk both computers, jobs=%#v", resp.CollectorJobs)
	}
	if strings.Contains(resp.Page.Content, "ubuntu-old") || strings.Contains(resp.Page.Content, "lijian-old") {
		t.Fatalf("session harvests must not skip collect: %s", resp.Page.Content)
	}
	if !strings.Contains(resp.Page.Content, "ubuntu-fresh") || !strings.Contains(resp.Page.Content, "lijian-fresh") {
		t.Fatalf("start draft missing new harvests: %s", resp.Page.Content)
	}
}

func TestCreateNotePeriodBriefHarvestsReturnBeforeSynthesizerWrite(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	prevBG := notePeriodBriefFinishInBackground
	prevWait := notePeriodBriefSynthWriteMaxWait
	prevCollect := notePeriodBriefCollectorMaxWait
	notePeriodBriefFinishInBackground = true
	notePeriodBriefSynthWriteMaxWait = time.Minute
	notePeriodBriefCollectorMaxWait = 2 * time.Second
	t.Cleanup(func() {
		notePeriodBriefFinishInBackground = prevBG
		notePeriodBriefSynthWriteMaxWait = prevWait
		notePeriodBriefCollectorMaxWait = prevCollect
	})

	sourcePageID := insertPeriodBriefFixtureDraft(t, "Harvest source bg")
	synthID := createHandlerTestAgent(t, "Harvest Synth BG "+uuid.NewString()[:8], nil)
	sessionID := createHandlerTestChatSession(t, synthID)
	if _, err := testPool.Exec(context.Background(), `
UPDATE chat_session SET context_note_page_id = $1 WHERE id = $2`, sourcePageID, sessionID); err != nil {
		t.Fatalf("bind session: %v", err)
	}
	ubuntu := createPeriodBriefCollectorTestAgent(t, "Harvest Ubuntu BG")
	folderID, err := testHandler.ensureNotePeriodBriefFolder(context.Background(), parseUUID(testWorkspaceID), parseUUID(testUserID))
	if err != nil {
		t.Fatalf("folder: %v", err)
	}
	draftID := insertPeriodBriefFixtureDraft(t, "prior draft bg")
	runID := insertPeriodBriefFixtureRun(t, sourcePageID, uuidToString(folderID), synthID, draftID, "done", time.Now())
	if _, err := testPool.Exec(context.Background(), `
UPDATE note_period_brief_run
SET chat_session_id = $1, collectors = $2::jsonb
WHERE id = $3`, sessionID, periodBriefCollectorsJSON(t, map[string]string{
		ubuntu: periodBriefHarvestPack("ubuntu-bg"),
	}), runID); err != nil {
		t.Fatalf("bind collectors: %v", err)
	}
	injectPeriodBriefCollectorPackMarkdown(t, ubuntu, periodBriefHarvestPack("ubuntu-bg"))

	started := time.Now()
	rec := httptest.NewRecorder()
	testHandler.CreateNotePeriodBrief(rec, newRequest(http.MethodPost, "/api/notes/period-briefs", map[string]any{
		"window":               "day",
		"date":                 time.Now().UTC().Format("2006-01-02"),
		"timezone":             "UTC",
		"agent_id":             synthID,
		"collector_agent_ids":  []string{ubuntu},
		"context_note_page_id": sourcePageID,
		"chat_session_id":      sessionID,
		"from_chat":            true,
	}))
	if rec.Code != http.StatusCreated {
		t.Fatalf("start = %d: %s", rec.Code, rec.Body.String())
	}
	if elapsed := time.Since(started); elapsed > 3*time.Second {
		t.Fatalf("start held the HTTP request for %s; it must return after dispatch", elapsed)
	}
}

func TestCreateNotePeriodBriefSettlesOrphanThenRegenerates(t *testing.T) {
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
	sourcePageID := insertPeriodBriefFixtureDraft(t, "Harvest orphan source")
	synthID := createHandlerTestAgent(t, "Harvest Orphan "+uuid.NewString()[:8], nil)
	sessionID := createHandlerTestChatSession(t, synthID)
	if _, err := testPool.Exec(context.Background(), `
UPDATE chat_session SET context_note_page_id = $1 WHERE id = $2`, sourcePageID, sessionID); err != nil {
		t.Fatalf("bind session: %v", err)
	}
	ubuntu := createPeriodBriefCollectorTestAgent(t, "Harvest Orphan Ubuntu")
	folderID, err := testHandler.ensureNotePeriodBriefFolder(context.Background(), parseUUID(testWorkspaceID), parseUUID(testUserID))
	if err != nil {
		t.Fatalf("folder: %v", err)
	}
	draftID := insertPeriodBriefFixtureDraft(t, "orphan draft")
	runID := insertPeriodBriefFixtureRun(t, sourcePageID, uuidToString(folderID), synthID, draftID, "synthesizing", time.Now().Add(-time.Minute))
	if _, err := testPool.Exec(context.Background(), `
UPDATE note_period_brief_run
SET chat_session_id = $1, collectors = $2::jsonb
WHERE id = $3`, sessionID, periodBriefCollectorsJSON(t, map[string]string{
		ubuntu: periodBriefHarvestPack("ubuntu-orphan"),
	}), runID); err != nil {
		t.Fatalf("bind collectors: %v", err)
	}
	plantPeriodBriefFolderNoteWrite(t, synthID, uuidToString(folderID), "# 工作介绍 orphan\n\nMerged ubuntu and lijian.")
	injectPeriodBriefCollectorPackMarkdown(t, ubuntu, periodBriefHarvestPack("ubuntu-next"))

	rec := httptest.NewRecorder()
	testHandler.CreateNotePeriodBrief(rec, newRequest(http.MethodPost, "/api/notes/period-briefs", map[string]any{
		"window":               "day",
		"date":                 time.Now().UTC().Format("2006-01-02"),
		"timezone":             "UTC",
		"agent_id":             synthID,
		"collector_agent_ids":  []string{ubuntu},
		"context_note_page_id": sourcePageID,
		"chat_session_id":      sessionID,
		"from_chat":            true,
	}))
	if rec.Code != http.StatusCreated {
		t.Fatalf("start = %d: %s", rec.Code, rec.Body.String())
	}
	var status string
	if err := testPool.QueryRow(context.Background(), `
SELECT status FROM note_period_brief_run WHERE id = $1`, runID).Scan(&status); err != nil {
		t.Fatalf("load orphan status: %v", err)
	}
	if status != "awaiting_confirm" {
		t.Fatalf("orphan status = %s, want awaiting_confirm", status)
	}
}

func periodBriefHarvestPack(token string) string {
	return "# 采集包\n\n## Highlights\n- " + token + " in-window work\n\n## Work groups\n\n### Demo\n- why: same project\n- items:\n  - " + token + "\n"
}

func periodBriefCollectorsJSON(t *testing.T, packs map[string]string) string {
	t.Helper()
	refs := make([]notePeriodBriefCollectorRef, 0, len(packs))
	for agentID, markdown := range packs {
		refs = append(refs, notePeriodBriefCollectorRef{
			AgentID:      agentID,
			WindowLabel:  "2026-W36",
			PackMarkdown: markdown,
		})
	}
	raw, err := json.Marshal(refs)
	if err != nil {
		t.Fatalf("marshal collectors: %v", err)
	}
	return string(raw)
}
