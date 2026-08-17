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

func TestCreateNotePeriodBriefOrchestratesCollectorsThenSynthesizerWithoutDigest(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	prevWait := notePeriodBriefCollectorWaitBudget
	notePeriodBriefCollectorWaitBudget = 0
	t.Cleanup(func() { notePeriodBriefCollectorWaitBudget = prevWait })

	synthID := createHandlerTestAgent(t, "Period Brief Synth "+uuid.NewString()[:8], nil)
	collectorA := createHandlerTestAgent(t, "Period Brief Collector A "+uuid.NewString()[:8], nil)
	collectorB := createHandlerTestAgent(t, "Period Brief Collector B "+uuid.NewString()[:8], nil)

	day := time.Now().UTC().Format("2006-01-02")
	rec := httptest.NewRecorder()
	testHandler.CreateNotePeriodBrief(rec, newRequest(http.MethodPost, "/api/notes/period-briefs", map[string]any{
		"window":              "day",
		"date":                day,
		"timezone":            "UTC",
		"agent_id":            synthID,
		"collector_agent_ids": []string{collectorA, collectorB},
	}))
	if rec.Code != http.StatusCreated {
		t.Fatalf("period brief = %d: %s", rec.Code, rec.Body.String())
	}
	var resp createNotePeriodBriefResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Job.AgentID != synthID {
		t.Fatalf("synthesizer job agent = %s, want %s", resp.Job.AgentID, synthID)
	}
	if len(resp.CollectorAgentIDs) != 2 {
		t.Fatalf("collector_agent_ids = %#v", resp.CollectorAgentIDs)
	}
	if len(resp.CollectorJobs) != 2 {
		t.Fatalf("collector_jobs = %#v", resp.CollectorJobs)
	}
	if containsNoteRetrospectiveSource(resp.SourcesUsed, notePeriodBriefSourceJournal) ||
		containsNoteRetrospectiveSource(resp.SourcesEmpty, notePeriodBriefSourceJournal) {
		t.Fatalf("Brief path must not use Host Digest source: used=%v empty=%v", resp.SourcesUsed, resp.SourcesEmpty)
	}
	if !containsNoteRetrospectiveSource(resp.SourcesEmpty, notePeriodBriefSourceCollectors) {
		t.Fatalf("timed-out stubs should mark collectors empty: %v", resp.SourcesEmpty)
	}
	if strings.Contains(resp.Page.Content, "Machine Work Digest") || strings.Contains(resp.Page.Content, "disabled: true") {
		t.Fatalf("draft must not include Host Digest: %s", resp.Page.Content)
	}
	if !strings.Contains(resp.Page.Content, "## Collector packs") {
		t.Fatalf("draft missing collector packs: %s", resp.Page.Content)
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
	if strings.Contains(prompt, "<digest>") || strings.Contains(prompt, "Machine Work Digest") {
		t.Fatalf("synthesizer wake must not include Host Digest: %s", prompt)
	}
	if !strings.Contains(prompt, "<packs>") || !strings.Contains(prompt, "</packs>") {
		t.Fatalf("synthesizer wake missing packs partition: %s", prompt)
	}
	if !strings.Contains(prompt, "<facts>") {
		t.Fatalf("synthesizer wake missing facts: %s", prompt)
	}
	if !strings.Contains(prompt, "collector packs") {
		t.Fatalf("system contract should mention collector packs: %s", prompt)
	}
	folderID := ""
	if resp.Page.ParentID != nil {
		folderID = *resp.Page.ParentID
	}
	if folderID == "" || !strings.Contains(prompt, "--note-write --note-page-id "+folderID) {
		t.Fatalf("wake must note-write to folder: %s", prompt)
	}
}

func TestCreateNotePeriodBriefIncludesReadyCollectorPack(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	prevWait := notePeriodBriefCollectorWaitBudget
	notePeriodBriefCollectorWaitBudget = 2 * time.Second
	t.Cleanup(func() { notePeriodBriefCollectorWaitBudget = prevWait })

	synthID := createHandlerTestAgent(t, "Period Brief Ready Synth "+uuid.NewString()[:8], nil)
	collectorID := createHandlerTestAgent(t, "Period Brief Ready Collector "+uuid.NewString()[:8], nil)

	// First create to get pack page id is awkward; instead dispatch then
	// simulate by writing pack content during wait via a short path:
	// create request with wait, but update pack after collectors insert.
	// We do a two-step: call Create with wait=0 after manually inserting ready
	// content is hard mid-flight. Simpler: unit-test await helper separately
	// and here only verify ready path by updating pack before await with
	// wait budget and a goroutine.

	day := time.Now().UTC().Format("2006-01-02")
	done := make(chan struct{})
	go func() {
		defer close(done)
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			var pageID string
			err := testPool.QueryRow(context.Background(), `
SELECT id::text FROM note_page
WHERE owner_user_id = $1 AND title LIKE '采集包%' AND content LIKE $2
ORDER BY created_at DESC LIMIT 1`,
				testUserID, "%"+notePeriodBriefCollectorStubMarker+"%").Scan(&pageID)
			if err == nil && pageID != "" {
				_, _ = testPool.Exec(context.Background(), `
UPDATE note_page SET content = $1, updated_at = now() WHERE id = $2`,
					"# 采集包 ready\n\n## Highlights\n- wired SSO login\n", pageID)
				return
			}
			time.Sleep(50 * time.Millisecond)
		}
	}()

	rec := httptest.NewRecorder()
	testHandler.CreateNotePeriodBrief(rec, newRequest(http.MethodPost, "/api/notes/period-briefs", map[string]any{
		"window":              "day",
		"date":                day,
		"timezone":            "UTC",
		"agent_id":            synthID,
		"collector_agent_ids": []string{collectorID},
	}))
	<-done
	if rec.Code != http.StatusCreated {
		t.Fatalf("period brief = %d: %s", rec.Code, rec.Body.String())
	}
	var resp createNotePeriodBriefResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !containsNoteRetrospectiveSource(resp.SourcesUsed, notePeriodBriefSourceCollectors) {
		t.Fatalf("ready pack should mark collectors used: %v", resp.SourcesUsed)
	}
	if !strings.Contains(resp.Page.Content, "wired SSO login") {
		t.Fatalf("draft missing ready pack body: %s", resp.Page.Content)
	}
	var contextRaw []byte
	if err := testPool.QueryRow(context.Background(), `
SELECT context FROM agent_inbox_event WHERE id = $1`, *resp.Job.TaskID).Scan(&contextRaw); err != nil {
		t.Fatalf("load wake: %v", err)
	}
	var wake map[string]any
	_ = json.Unmarshal(contextRaw, &wake)
	prompt, _ := wake["prompt"].(string)
	packsInner := extractBetween(t, prompt, "<packs>\n", "\n</packs>")
	if !strings.Contains(packsInner, "wired SSO login") {
		t.Fatalf("packs partition missing ready content: %s", packsInner)
	}
}

func TestCreateNotePeriodBriefAllowsMemberWithCollectors(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	prevWait := notePeriodBriefCollectorWaitBudget
	notePeriodBriefCollectorWaitBudget = 0
	t.Cleanup(func() { notePeriodBriefCollectorWaitBudget = prevWait })

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
	if len(resp.CollectorJobs) != 1 || resp.CollectorJobs[0].AgentID != collectorID {
		t.Fatalf("collector_jobs = %#v", resp.CollectorJobs)
	}
}
