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

func TestCreateNotePeriodBriefRejectsNonComputerOwner(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	memberID := createRuntimeLocalSkillTestMember(t, "member")
	agentID := createHandlerTestAgent(t, "Period Brief Member Agent "+uuid.NewString()[:8], nil)

	rec := httptest.NewRecorder()
	req := newRequestAsUser(memberID, http.MethodPost, "/api/notes/period-briefs", map[string]any{
		"window":   "day",
		"date":     time.Now().UTC().Format("2006-01-02"),
		"timezone": "UTC",
		"agent_id": agentID,
	})
	testHandler.CreateNotePeriodBrief(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("non-owner period brief = %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "computer owner required") {
		t.Fatalf("expected computer owner required, got %s", rec.Body.String())
	}
}

func TestCreateNotePeriodBriefDispatchesWithDisabledDigest(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	daemonID, hub, conn := setupComputerWorkDigestLiveBinding(t, testUserID)
	go serveComputerWorkJournalFixture(t, conn, daemonID)

	agentID := createHandlerTestAgent(t, "Period Brief Agent "+uuid.NewString()[:8], nil)
	local := *testHandler
	local.DaemonHub = hub

	day := time.Now().UTC().Format("2006-01-02")
	rec := httptest.NewRecorder()
	local.CreateNotePeriodBrief(rec, newRequest(http.MethodPost, "/api/notes/period-briefs", map[string]any{
		"window":   "day",
		"date":     day,
		"timezone": "UTC",
		"agent_id": agentID,
	}))
	if rec.Code != http.StatusCreated {
		t.Fatalf("period brief = %d: %s", rec.Code, rec.Body.String())
	}
	var resp createNotePeriodBriefResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
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
		"window":   "day",
		"date":     day,
		"timezone": "UTC",
		"agent_id": agentID,
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
	local := *testHandler
	local.DaemonHub = daemonws.NewHub()

	rec := httptest.NewRecorder()
	local.CreateNotePeriodBrief(rec, newRequest(http.MethodPost, "/api/notes/period-briefs", map[string]any{
		"window":   "day",
		"date":     time.Now().UTC().Format("2006-01-02"),
		"timezone": "UTC",
		"agent_id": agentID,
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
