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
	"github.com/multica-ai/multica/server/internal/daemonws"
)

func TestCreateNotePeriodBriefRejectsEmptyCollectors(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	agentID := createHandlerTestAgent(t, "Period Brief No Collector "+uuid.NewString()[:8], nil)

	rec := httptest.NewRecorder()
	req := newRequest(http.MethodPost, "/api/notes/period-briefs", map[string]any{
		"window":   "day",
		"date":     time.Now().UTC().Format("2006-01-02"),
		"timezone": "UTC",
		"agent_id": agentID,
	})
	testHandler.CreateNotePeriodBrief(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("missing collectors = %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "collector_agent_ids is required") {
		t.Fatalf("expected collector_agent_ids required, got %s", rec.Body.String())
	}
}

func TestCreateNotePeriodBriefAllowsMemberWithCollectors(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	memberID := createRuntimeLocalSkillTestMember(t, "member")
	agentID := createHandlerTestAgent(t, "Period Brief Member Agent "+uuid.NewString()[:8], nil)
	collectorID := createHandlerTestAgent(t, "Period Brief Collector "+uuid.NewString()[:8], nil)

	rec := httptest.NewRecorder()
	req := newRequestAsUser(memberID, http.MethodPost, "/api/notes/period-briefs", map[string]any{
		"window":              "day",
		"date":                time.Now().UTC().Format("2006-01-02"),
		"timezone":            "UTC",
		"agent_id":            agentID,
		"collector_agent_ids": []string{collectorID},
	})
	testHandler.CreateNotePeriodBrief(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("member with collectors = %d: %s", rec.Code, rec.Body.String())
	}
	var resp createNotePeriodBriefResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.CollectorAgentIDs) != 1 || resp.CollectorAgentIDs[0] != collectorID {
		t.Fatalf("collector_agent_ids = %#v, want [%s]", resp.CollectorAgentIDs, collectorID)
	}
}

func TestCreateNotePeriodBriefDispatchesWithDisabledDigest(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	daemonID, hub, conn := setupComputerWorkDigestLiveBinding(t, testUserID)
	go serveComputerWorkJournalFixture(t, conn, daemonID)

	agentID := createHandlerTestAgent(t, "Period Brief Agent "+uuid.NewString()[:8], nil)
	collectorID := createHandlerTestAgent(t, "Period Brief Collector "+uuid.NewString()[:8], nil)
	local := *testHandler
	local.DaemonHub = hub

	day := time.Now().UTC().Format("2006-01-02")
	rec := httptest.NewRecorder()
	local.CreateNotePeriodBrief(rec, newRequest(http.MethodPost, "/api/notes/period-briefs", map[string]any{
		"window":              "day",
		"date":                day,
		"timezone":            "UTC",
		"agent_id":            agentID,
		"collector_agent_ids": []string{collectorID},
	}))
	if rec.Code != http.StatusCreated {
		t.Fatalf("period brief = %d: %s", rec.Code, rec.Body.String())
	}
	var resp createNotePeriodBriefResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.CollectorAgentIDs) != 1 || resp.CollectorAgentIDs[0] != collectorID {
		t.Fatalf("collector_agent_ids = %#v", resp.CollectorAgentIDs)
	}
	if resp.Page.ID == "" || !strings.Contains(resp.Page.Title, "工作介绍") {
		t.Fatalf("page = %#v", resp.Page)
	}
	if resp.Job.ID == "" || resp.Job.PageID != resp.Page.ID || resp.Job.Status != "dispatched" {
		t.Fatalf("job = %#v", resp.Job)
	}
	if resp.Job.TaskID == nil || resp.Job.ChannelID == nil {
		t.Fatalf("job missing task/channel: %#v", resp.Job)
	}
	if !containsNoteRetrospectiveSource(resp.SourcesEmpty, notePeriodBriefSourceJournal) {
		t.Fatalf("sources_empty = %v, want machine_work_journal when journal disabled", resp.SourcesEmpty)
	}
	if !strings.Contains(resp.Page.Content, "disabled: true") {
		t.Fatalf("draft missing disabled digest marker: %s", resp.Page.Content)
	}
	if resp.Page.ParentID == nil || *resp.Page.ParentID == "" {
		t.Fatalf("draft must be under 工作介绍/: %#v", resp.Page)
	}
	folderID := *resp.Page.ParentID

	if resp.Job.ChannelMessageID == nil {
		t.Fatal("expected channel_message_id")
	}
	var partsRaw []byte
	if err := testPool.QueryRow(context.Background(), `
SELECT parts FROM channel_message WHERE id = $1`, *resp.Job.ChannelMessageID).Scan(&partsRaw); err != nil {
		t.Fatalf("load channel parts: %v", err)
	}
	var parts []map[string]any
	if err := json.Unmarshal(partsRaw, &parts); err != nil {
		t.Fatalf("unmarshal parts: %v", err)
	}
	foundBrief := false
	for _, part := range parts {
		if part["type"] != "note_brief" {
			continue
		}
		foundBrief = true
		if part["ref_id"] != folderID {
			t.Fatalf("note_brief sticky ref_id = %v, want folder %s (not draft %s)", part["ref_id"], folderID, resp.Page.ID)
		}
		if part["ref_id"] == resp.Page.ID {
			t.Fatalf("note_brief must not sticky the draft page")
		}
	}
	if !foundBrief {
		t.Fatalf("expected note_brief part: %s", partsRaw)
	}

	var wake map[string]any
	var contextRaw []byte
	if err := testPool.QueryRow(context.Background(), `
SELECT context FROM agent_inbox_event WHERE id = $1`, *resp.Job.TaskID).Scan(&contextRaw); err != nil {
		t.Fatalf("load wake context: %v", err)
	}
	if err := json.Unmarshal(contextRaw, &wake); err != nil {
		t.Fatalf("unmarshal wake: %v", err)
	}
	prompt, _ := wake["prompt"].(string)
	if !strings.Contains(prompt, "<facts>") || !strings.Contains(prompt, "<digest>") {
		t.Fatalf("wake prompt missing facts/digest partitions: %s", prompt)
	}
	if !strings.Contains(prompt, "--note-write --note-page-id "+folderID) {
		t.Fatalf("wake prompt must require note-write to folder: %s", prompt)
	}
	if !strings.Contains(prompt, "Never pass the draft page id ("+resp.Page.ID+")") {
		t.Fatalf("wake prompt must forbid draft write target: %s", prompt)
	}
	if !strings.Contains(prompt, "disabled: true") && !strings.Contains(prompt, "disabled:‹") {
		// escaped angle brackets shouldn't apply to "disabled: true"
		if !strings.Contains(prompt, "disabled") {
			t.Fatalf("wake prompt missing disabled digest: %s", prompt)
		}
	}
	digestInner := extractBetween(t, prompt, "<digest>\n", "\n</digest>")
	if !strings.Contains(digestInner, "disabled: true") {
		t.Fatalf("digest partition missing disabled marker: %s", digestInner)
	}
}

func TestCreateNotePeriodBriefDispatchesWithHarvestedDigest(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	daemonID, hub, conn := setupComputerWorkDigestLiveBinding(t, testUserID)
	go serveComputerWorkJournalFixture(t, conn, daemonID)

	agentID := createHandlerTestAgent(t, "Period Brief On Agent "+uuid.NewString()[:8], nil)
	collectorID := createHandlerTestAgent(t, "Period Brief Harvest Collector "+uuid.NewString()[:8], nil)
	local := *testHandler
	local.DaemonHub = hub

	enable := httptest.NewRecorder()
	local.PatchComputerWorkJournal(enable, computerWorkJournalRequest(testUserID, daemonID, true))
	if enable.Code != http.StatusOK {
		t.Fatalf("enable journal = %d: %s", enable.Code, enable.Body.String())
	}

	day := time.Date(2026, time.August, 10, 12, 0, 0, 0, time.UTC).Format("2006-01-02")
	rec := httptest.NewRecorder()
	local.CreateNotePeriodBrief(rec, newRequest(http.MethodPost, "/api/notes/period-briefs", map[string]any{
		"window":              "day",
		"date":                day,
		"timezone":            "UTC",
		"agent_id":            agentID,
		"collector_agent_ids": []string{collectorID},
	}))
	if rec.Code != http.StatusCreated {
		t.Fatalf("period brief = %d: %s", rec.Code, rec.Body.String())
	}
	var resp createNotePeriodBriefResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !containsNoteRetrospectiveSource(resp.SourcesUsed, notePeriodBriefSourceJournal) {
		t.Fatalf("sources_used = %v", resp.SourcesUsed)
	}
	if !strings.Contains(resp.Page.Content, "/home/owner/code/app") {
		t.Fatalf("draft missing harvest root: %s", resp.Page.Content)
	}

	var contextRaw []byte
	if err := testPool.QueryRow(context.Background(), `
SELECT context FROM agent_inbox_event WHERE id = $1`, *resp.Job.TaskID).Scan(&contextRaw); err != nil {
		t.Fatalf("load wake: %v", err)
	}
	var wake map[string]any
	_ = json.Unmarshal(contextRaw, &wake)
	prompt, _ := wake["prompt"].(string)
	if !strings.Contains(prompt, "wire SSO login") {
		t.Fatalf("wake prompt missing commit subject: %s", prompt)
	}
}

func TestCreateNotePeriodBriefSurvivesOfflineComputer(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	_ = setupComputerWorkDigestOwner(t, testUserID)
	agentID := createHandlerTestAgent(t, "Period Brief Offline Agent "+uuid.NewString()[:8], nil)
	collectorID := createHandlerTestAgent(t, "Period Brief Offline Collector "+uuid.NewString()[:8], nil)
	local := *testHandler
	local.DaemonHub = daemonws.NewHub()

	rec := httptest.NewRecorder()
	local.CreateNotePeriodBrief(rec, newRequest(http.MethodPost, "/api/notes/period-briefs", map[string]any{
		"window":              "day",
		"date":                time.Now().UTC().Format("2006-01-02"),
		"timezone":            "UTC",
		"agent_id":            agentID,
		"collector_agent_ids": []string{collectorID},
	}))
	if rec.Code != http.StatusCreated {
		t.Fatalf("offline computer should still dispatch: %d %s", rec.Code, rec.Body.String())
	}
	var resp createNotePeriodBriefResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !containsNoteRetrospectiveSource(resp.SourcesEmpty, notePeriodBriefSourceJournal) {
		t.Fatalf("sources_empty = %v", resp.SourcesEmpty)
	}
	if !strings.Contains(resp.Page.Content, "computer_offline") {
		t.Fatalf("draft should record offline fetch: %s", resp.Page.Content)
	}
}
