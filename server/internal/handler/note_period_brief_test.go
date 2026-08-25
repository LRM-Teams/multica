package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

func injectPeriodBriefCollectorPackMarkdown(t *testing.T, collectorAgentID, packBody string) {
	t.Helper()
	stop := make(chan struct{})
	done := make(chan struct{})
	t.Cleanup(func() {
		close(stop)
		<-done
	})
	go func() {
		defer close(done)
		for {
			select {
			case <-stop:
				return
			default:
			}
			tag, err := testPool.Exec(context.Background(), `
UPDATE note_period_brief_run
SET collectors = (
  SELECT COALESCE(jsonb_agg(
    CASE WHEN c->>'agent_id' = $1
      THEN c || jsonb_build_object('pack_markdown', $2::text)
      ELSE c
    END
  ), '[]'::jsonb)
  FROM jsonb_array_elements(collectors) AS c
), updated_at = now()
WHERE status = 'collecting'
  AND collectors @> jsonb_build_array(jsonb_build_object('agent_id', $1::text))
  AND EXISTS (
    SELECT 1 FROM jsonb_array_elements(collectors) x
    WHERE x->>'agent_id' = $1 AND COALESCE(x->>'pack_markdown','') = ''
  )`, collectorAgentID, packBody)
			if err == nil && tag.RowsAffected() > 0 {
				return
			}
			time.Sleep(40 * time.Millisecond)
		}
	}()
}

func TestCreateNotePeriodBriefRejectsEmptyCollectors(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	prevBG := notePeriodBriefFinishInBackground
	notePeriodBriefFinishInBackground = false
	t.Cleanup(func() { notePeriodBriefFinishInBackground = prevBG })
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
	prevWait := notePeriodBriefCollectorMaxWait
	notePeriodBriefCollectorMaxWait = 0
	prevBG := notePeriodBriefFinishInBackground
	notePeriodBriefFinishInBackground = false
	t.Cleanup(func() {
		notePeriodBriefCollectorMaxWait = prevWait
		notePeriodBriefFinishInBackground = prevBG
	})

	synthID := createHandlerTestAgent(t, "Period Brief Synth "+uuid.NewString()[:8], nil)
	collectorA := createPeriodBriefCollectorTestAgent(t, "Collector A")
	collectorB := createPeriodBriefCollectorTestAgent(t, "Collector B")

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
	if containsNoteRetrospectiveSource(resp.SourcesUsed, "machine_work_journal") ||
		containsNoteRetrospectiveSource(resp.SourcesEmpty, "machine_work_journal") {
		t.Fatalf("Brief path must not use Host Digest source: used=%v empty=%v", resp.SourcesUsed, resp.SourcesEmpty)
	}
	if !containsNoteRetrospectiveSource(resp.SourcesEmpty, notePeriodBriefSourceCollectors) {
		t.Fatalf("unsettled/empty collectors should mark collectors empty: %v", resp.SourcesEmpty)
	}
	if strings.Contains(resp.Page.Content, "Stub awaiting Agent pack") {
		t.Fatalf("draft must not include collector pack stub body: %s", resp.Page.Content)
	}
	if !strings.Contains(resp.Page.Content, "调用采集 Agent 失败了") {
		t.Fatalf("draft must state collector call failed without a pack: %s", resp.Page.Content)
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
	assertPeriodBriefInboxForceFresh(t, *resp.Job.TaskID)
	for _, job := range resp.CollectorJobs {
		if job.TaskID == nil {
			t.Fatalf("collector job missing task_id: %#v", job)
		}
		assertPeriodBriefInboxForceFresh(t, *job.TaskID)
		var maxAttempts int32
		if err := testPool.QueryRow(context.Background(), `
SELECT max_attempts FROM agent_inbox_event WHERE id = $1`, *job.TaskID).Scan(&maxAttempts); err != nil {
			t.Fatalf("load collector max_attempts: %v", err)
		}
		if maxAttempts != 1 {
			t.Fatalf("collector inbox max_attempts = %d, want 1 (no automatic re-collect)", maxAttempts)
		}
	}
	if resp.Job.ChannelMessageID == nil {
		t.Fatal("expected synthesizer channel_message_id")
	}
	var partsRaw []byte
	if err := testPool.QueryRow(context.Background(), `
SELECT parts FROM channel_message WHERE id = $1`, *resp.Job.ChannelMessageID).Scan(&partsRaw); err != nil {
		t.Fatalf("load synthesizer channel parts: %v", err)
	}
	var parts []map[string]any
	if err := json.Unmarshal(partsRaw, &parts); err != nil {
		t.Fatalf("unmarshal parts: %v", err)
	}
	foundSticky := false
	for _, part := range parts {
		if part["type"] != "note_brief" {
			continue
		}
		foundSticky = true
		if part["ref_id"] != folderID {
			t.Fatalf("sticky note_brief ref_id = %v, want folder %s", part["ref_id"], folderID)
		}
		if part["ref_id"] == resp.Page.ID {
			t.Fatalf("sticky must not point at draft page %s", resp.Page.ID)
		}
	}
	if !foundSticky {
		t.Fatalf("expected note_brief sticky on folder: %s", partsRaw)
	}
}

func TestCreateNotePeriodBriefIncludesReadyCollectorPack(t *testing.T) {
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

	synthID := createHandlerTestAgent(t, "Period Brief Ready Synth "+uuid.NewString()[:8], nil)
	collectorID := createPeriodBriefCollectorTestAgent(t, "Ready Collector")

	packBody := `# 采集包 ready

## Runtime
- local / test-host

## Repos / roots
- /tmp/demo — SSO login path

## Highlights
- wired SSO login

## Work groups

### SSO
- why: same project
- repos/paths: /tmp/demo
- items:
  - wired SSO login

## Unscoped / unclear
- scratch under /tmp
`

	day := time.Now().UTC().Format("2006-01-02")
	injectPeriodBriefCollectorPackMarkdown(t, collectorID, packBody)

	rec := httptest.NewRecorder()
	testHandler.CreateNotePeriodBrief(rec, newRequest(http.MethodPost, "/api/notes/period-briefs", map[string]any{
		"window":              "day",
		"date":                day,
		"timezone":            "UTC",
		"agent_id":            synthID,
		"collector_agent_ids": []string{collectorID},
	}))
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
	var packNotes int
	if err := testPool.QueryRow(context.Background(), `
SELECT count(*) FROM note_page
WHERE owner_user_id = $1 AND title LIKE '采集包%' AND deleted_at IS NULL`, testUserID).Scan(&packNotes); err != nil {
		t.Fatalf("count pack notes: %v", err)
	}
	if packNotes != 0 {
		t.Fatalf("collector packs must not create Notes pages, got %d", packNotes)
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
	if !strings.Contains(packsInner, "## Highlights") {
		t.Fatalf("packs partition missing pack structure: %s", packsInner)
	}
	folderID := ""
	if resp.Page.ParentID != nil {
		folderID = *resp.Page.ParentID
	}
	if folderID == "" || !strings.Contains(prompt, "--note-write --note-page-id "+folderID) {
		t.Fatalf("synthesizer must note-write under folder: %s", prompt)
	}
}

func TestCreateNotePeriodBriefWaitsForCompletedCollectorNoteWriteProposal(t *testing.T) {
	// Legacy name: packs now arrive via submit-pack / run.pack_markdown, not note_write.
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	prevWait := notePeriodBriefCollectorMaxWait
	notePeriodBriefCollectorMaxWait = 3 * time.Second
	prevBG := notePeriodBriefFinishInBackground
	notePeriodBriefFinishInBackground = false
	t.Cleanup(func() {
		notePeriodBriefCollectorMaxWait = prevWait
		notePeriodBriefFinishInBackground = prevBG
	})

	synthID := createHandlerTestAgent(t, "Period Brief Proposal Synth "+uuid.NewString()[:8], nil)
	collectorID := createPeriodBriefCollectorTestAgent(t, "Proposal Collector")

	packBody := "# 采集包 from submit-pack\n\n## Highlights\n- harvested pending proposal\n\n## Work groups\n\n### Demo\n- why: same project\n- items:\n  - harvested pending proposal\n\n## Unscoped / unclear\n- none\n"
	day := time.Now().UTC().Format("2006-01-02")
	injectPeriodBriefCollectorPackMarkdown(t, collectorID, packBody)

	rec := httptest.NewRecorder()
	testHandler.CreateNotePeriodBrief(rec, newRequest(http.MethodPost, "/api/notes/period-briefs", map[string]any{
		"window":              "day",
		"date":                day,
		"timezone":            "UTC",
		"agent_id":            synthID,
		"collector_agent_ids": []string{collectorID},
	}))
	if rec.Code != http.StatusCreated {
		t.Fatalf("period brief = %d: %s", rec.Code, rec.Body.String())
	}
	var resp createNotePeriodBriefResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !containsNoteRetrospectiveSource(resp.SourcesUsed, notePeriodBriefSourceCollectors) {
		t.Fatalf("submit-pack should mark collectors used: %v", resp.SourcesUsed)
	}
	if !strings.Contains(resp.Page.Content, "harvested pending proposal") {
		t.Fatalf("draft missing proposal pack body: %s", resp.Page.Content)
	}
}

func TestCreateNotePeriodBriefHarvestsNoteWriteWhileJobStillRunning(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	prevWait := notePeriodBriefCollectorMaxWait
	notePeriodBriefCollectorMaxWait = 3 * time.Second
	prevBG := notePeriodBriefFinishInBackground
	notePeriodBriefFinishInBackground = false
	t.Cleanup(func() {
		notePeriodBriefCollectorMaxWait = prevWait
		notePeriodBriefFinishInBackground = prevBG
	})

	synthID := createHandlerTestAgent(t, "Period Brief Running Synth "+uuid.NewString()[:8], nil)
	collectorID := createPeriodBriefCollectorTestAgent(t, "Running Collector")
	packBody := "# 采集包 while running\n\n## Highlights\n- harvested before job completed\n\n## Work groups\n\n### Demo\n- why: same project\n- items:\n  - harvested before job completed\n\n## Unscoped / unclear\n- none\n"
	day := time.Now().UTC().Format("2006-01-02")
	injectPeriodBriefCollectorPackMarkdown(t, collectorID, packBody)

	rec := httptest.NewRecorder()
	testHandler.CreateNotePeriodBrief(rec, newRequest(http.MethodPost, "/api/notes/period-briefs", map[string]any{
		"window":              "day",
		"date":                day,
		"timezone":            "UTC",
		"agent_id":            synthID,
		"collector_agent_ids": []string{collectorID},
	}))
	if rec.Code != http.StatusCreated {
		t.Fatalf("period brief = %d: %s", rec.Code, rec.Body.String())
	}
	var resp createNotePeriodBriefResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !strings.Contains(resp.Page.Content, "harvested before job completed") {
		t.Fatalf("draft missing pack while job running: %s", resp.Page.Content)
	}
}

func TestCreateNotePeriodBriefHarvestsNoteWriteAfterFailedTask(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	prevWait := notePeriodBriefCollectorMaxWait
	notePeriodBriefCollectorMaxWait = 3 * time.Second
	prevBG := notePeriodBriefFinishInBackground
	notePeriodBriefFinishInBackground = false
	t.Cleanup(func() {
		notePeriodBriefCollectorMaxWait = prevWait
		notePeriodBriefFinishInBackground = prevBG
	})

	synthID := createHandlerTestAgent(t, "Period Brief Failed Synth "+uuid.NewString()[:8], nil)
	collectorID := createPeriodBriefCollectorTestAgent(t, "Failed Collector")
	packBody := "# 采集包 after failed job\n\n## Highlights\n- harvested after api_invalid_request\n\n## Work groups\n\n### Demo\n- why: same project\n- items:\n  - harvested after api_invalid_request\n\n## Unscoped / unclear\n- none\n"
	day := time.Now().UTC().Format("2006-01-02")

	// Inject pack and mark collector task failed — pack_markdown still wins ready.
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		injected := false
		for {
			select {
			case <-stop:
				return
			default:
			}
			if !injected {
				tag, err := testPool.Exec(context.Background(), `
UPDATE note_period_brief_run
SET collectors = (
  SELECT COALESCE(jsonb_agg(
    CASE WHEN c->>'agent_id' = $1
      THEN c || jsonb_build_object('pack_markdown', $2::text)
      ELSE c
    END
  ), '[]'::jsonb)
  FROM jsonb_array_elements(collectors) AS c
), updated_at = now()
WHERE status = 'collecting'
  AND collectors @> jsonb_build_array(jsonb_build_object('agent_id', $1::text))`, collectorID, packBody)
				if err == nil && tag.RowsAffected() > 0 {
					injected = true
				}
			}
			_, _ = testPool.Exec(context.Background(), `
UPDATE agent_inbox_event e
SET status = 'acked', terminal_outcome = 'failed', failure_reason = 'api_invalid_request',
    started_at = COALESCE(started_at, now()), completed_at = now(), acked_at = now()
FROM note_worker_job j
WHERE j.task_id = e.id AND j.agent_id = $1::uuid AND e.status <> 'acked'`, collectorID)
			if injected {
				return
			}
			time.Sleep(40 * time.Millisecond)
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
	close(stop)
	<-done
	if rec.Code != http.StatusCreated {
		t.Fatalf("period brief = %d: %s", rec.Code, rec.Body.String())
	}
	var resp createNotePeriodBriefResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !strings.Contains(resp.Page.Content, "harvested after api_invalid_request") {
		t.Fatalf("failed job with pack_markdown must still be ready: %s", resp.Page.Content)
	}
}

func assertPeriodBriefInboxForceFresh(t *testing.T, taskID string) {
	t.Helper()
	var fresh bool
	if err := testPool.QueryRow(context.Background(), `
SELECT force_fresh_session FROM agent_inbox_event WHERE id = $1`, taskID).Scan(&fresh); err != nil {
		t.Fatalf("load force_fresh_session for %s: %v", taskID, err)
	}
	if !fresh {
		t.Fatalf("period brief inbox %s must set force_fresh_session", taskID)
	}
}

func TestCreateNotePeriodBriefAllowsMemberWithCollectors(t *testing.T) {
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

	memberID := createRuntimeLocalSkillTestMember(t, "member")
	agentID := createHandlerTestAgent(t, "Period Brief Member Agent "+uuid.NewString()[:8], nil)
	collectorID := createPeriodBriefCollectorTestAgentForOwner(t, "Member Collector", memberID)

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

// Pi/local collectors authenticate with durable agent_credential (no TaskID).
// submit-pack must accept that — Notes page ACL is not the gate.
func TestSubmitAgentNotePeriodBriefPackAllowsAgentCredentialWithoutTaskID(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	prevWait := notePeriodBriefCollectorMaxWait
	notePeriodBriefCollectorMaxWait = 30 * time.Second
	t.Cleanup(func() { notePeriodBriefCollectorMaxWait = prevWait })

	synthID := createHandlerTestAgent(t, "Submit Cred Synth "+uuid.NewString()[:8], nil)
	collectorID := createPeriodBriefCollectorTestAgent(t, "Submit Cred Collector")

	createRec := httptest.NewRecorder()
	testHandler.CreateNotePeriodBrief(createRec, newRequest(http.MethodPost, "/api/notes/period-briefs", map[string]any{
		"window":              "day",
		"date":                time.Now().UTC().Format("2006-01-02"),
		"timezone":            "UTC",
		"agent_id":            synthID,
		"collector_agent_ids": []string{collectorID},
	}))
	if createRec.Code != http.StatusCreated {
		t.Fatalf("create = %d: %s", createRec.Code, createRec.Body.String())
	}
	var created createNotePeriodBriefResponse
	if err := json.NewDecoder(createRec.Body).Decode(&created); err != nil {
		t.Fatalf("decode create: %v", err)
	}
	draftID := created.Page.ID
	if draftID == "" {
		t.Fatal("missing draft page id")
	}

	packBody := "# 采集包 credential submit\n\n## Highlights\n- via agent_credential\n\n## Work groups\n\n### Demo\n- why: same project\n- items:\n  - via agent_credential\n\n## Unscoped / unclear\n- none\n"
	submitReq := withURLParam(withAgentCredentialPrincipal(
		newRequest(http.MethodPost, "/api/agent/notes/period-briefs/"+draftID+"/submit-pack", map[string]any{
			"markdown": packBody,
		}),
		collectorID, testWorkspaceID, testUserID,
	), "draftPageId", draftID)
	submitRec := httptest.NewRecorder()
	testHandler.SubmitAgentNotePeriodBriefPack(submitRec, submitReq)
	if submitRec.Code != http.StatusOK {
		t.Fatalf("submit-pack with agent_credential: expected 200, got %d: %s", submitRec.Code, submitRec.Body.String())
	}

	var stored string
	if err := testPool.QueryRow(context.Background(), `
SELECT c->>'pack_markdown'
FROM note_period_brief_run r,
     jsonb_array_elements(r.collectors) AS c
WHERE r.draft_page_id = $1::uuid
  AND c->>'agent_id' = $2`, draftID, collectorID).Scan(&stored); err != nil {
		t.Fatalf("load pack_markdown: %v", err)
	}
	if !strings.Contains(stored, "via agent_credential") {
		t.Fatalf("pack_markdown = %q", stored)
	}
}

func injectPeriodBriefCollectPlan(t *testing.T, plan notePeriodBriefCollectPlan) {
	t.Helper()
	raw, err := json.Marshal(plan)
	if err != nil {
		t.Fatalf("marshal plan: %v", err)
	}
	stop := make(chan struct{})
	done := make(chan struct{})
	t.Cleanup(func() {
		close(stop)
		<-done
	})
	go func() {
		defer close(done)
		for {
			select {
			case <-stop:
				return
			default:
			}
			tag, err := testPool.Exec(context.Background(), `
UPDATE note_period_brief_run
SET collect_plan = $1::jsonb, updated_at = now()
WHERE status = 'planning'
  AND collect_plan IS NULL`, raw)
			if err == nil && tag.RowsAffected() > 0 {
				return
			}
			time.Sleep(40 * time.Millisecond)
		}
	}()
}

func TestCreateNotePeriodBriefEmptyFocusSkipsPlanner(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	prevWait := notePeriodBriefCollectorMaxWait
	notePeriodBriefCollectorMaxWait = 0
	prevPlan := notePeriodBriefPlannerMaxWait
	notePeriodBriefPlannerMaxWait = 0
	prevBG := notePeriodBriefFinishInBackground
	notePeriodBriefFinishInBackground = false
	t.Cleanup(func() {
		notePeriodBriefCollectorMaxWait = prevWait
		notePeriodBriefPlannerMaxWait = prevPlan
		notePeriodBriefFinishInBackground = prevBG
	})

	synthID := createHandlerTestAgent(t, "Period Brief Synth "+uuid.NewString()[:8], nil)
	collectorA := createPeriodBriefCollectorTestAgent(t, "Collector A")
	day := time.Now().UTC().Format("2006-01-02")
	rec := httptest.NewRecorder()
	testHandler.CreateNotePeriodBrief(rec, newRequest(http.MethodPost, "/api/notes/period-briefs", map[string]any{
		"window":              "day",
		"date":                day,
		"timezone":            "UTC",
		"agent_id":            synthID,
		"collector_agent_ids": []string{collectorA},
		"focus":               "   ",
	}))
	if rec.Code != http.StatusCreated {
		t.Fatalf("period brief = %d: %s", rec.Code, rec.Body.String())
	}
	var resp createNotePeriodBriefResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.CollectorJobs) != 1 {
		t.Fatalf("empty focus must dispatch collectors immediately: %#v", resp.CollectorJobs)
	}
	var userFocus, status string
	if err := testPool.QueryRow(context.Background(), `
SELECT user_focus, status FROM note_period_brief_run WHERE draft_page_id = $1`, resp.Page.ID).Scan(&userFocus, &status); err != nil {
		t.Fatalf("load run: %v", err)
	}
	if userFocus != "" {
		t.Fatalf("blank focus should store empty, got %q", userFocus)
	}
	if status == "planning" {
		t.Fatal("empty focus must not stay in planning")
	}
}

func TestCreateNotePeriodBriefFocusFallsBackWhenPlanMissing(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	prevWait := notePeriodBriefCollectorMaxWait
	notePeriodBriefCollectorMaxWait = 0
	prevPlan := notePeriodBriefPlannerMaxWait
	notePeriodBriefPlannerMaxWait = 0
	prevBG := notePeriodBriefFinishInBackground
	notePeriodBriefFinishInBackground = false
	t.Cleanup(func() {
		notePeriodBriefCollectorMaxWait = prevWait
		notePeriodBriefPlannerMaxWait = prevPlan
		notePeriodBriefFinishInBackground = prevBG
	})

	synthID := createHandlerTestAgent(t, "Period Brief Synth "+uuid.NewString()[:8], nil)
	collectorA := createPeriodBriefCollectorTestAgent(t, "Collector A")
	collectorB := createPeriodBriefCollectorTestAgent(t, "Collector B")
	day := time.Now().UTC().Format("2006-01-02")
	rec := httptest.NewRecorder()
	testHandler.CreateNotePeriodBrief(rec, newRequest(http.MethodPost, "/api/notes/period-briefs", map[string]any{
		"window":              "day",
		"date":                day,
		"timezone":            "UTC",
		"agent_id":            synthID,
		"collector_agent_ids": []string{collectorA, collectorB},
		"focus":               "只整理 ~/multica",
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
	if len(resp.CollectorJobs) != 2 {
		t.Fatalf("missing plan should fall back to all selected collectors: %#v", resp.CollectorJobs)
	}
	var userFocus string
	if err := testPool.QueryRow(context.Background(), `
SELECT user_focus FROM note_period_brief_run WHERE draft_page_id = $1`, resp.Page.ID).Scan(&userFocus); err != nil {
		t.Fatalf("load focus: %v", err)
	}
	if userFocus != "只整理 ~/multica" {
		t.Fatalf("user_focus = %q", userFocus)
	}
}

func TestCreateNotePeriodBriefFocusHonorsCollectPlanSkip(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	prevWait := notePeriodBriefCollectorMaxWait
	notePeriodBriefCollectorMaxWait = 0
	prevPlan := notePeriodBriefPlannerMaxWait
	notePeriodBriefPlannerMaxWait = 3 * time.Second
	prevBG := notePeriodBriefFinishInBackground
	notePeriodBriefFinishInBackground = false
	t.Cleanup(func() {
		notePeriodBriefCollectorMaxWait = prevWait
		notePeriodBriefPlannerMaxWait = prevPlan
		notePeriodBriefFinishInBackground = prevBG
	})

	synthID := createHandlerTestAgent(t, "Period Brief Synth "+uuid.NewString()[:8], nil)
	collectorA := createPeriodBriefCollectorTestAgent(t, "Collector A")
	collectorB := createPeriodBriefCollectorTestAgent(t, "Collector B")
	injectPeriodBriefCollectPlan(t, notePeriodBriefCollectPlan{
		Summary: "laptop only",
		Assignments: []notePeriodBriefCollectAssignment{
			{CollectorAgentID: collectorA, Paths: []string{"/home/jian40/multica"}, Brief: "notes-agent"},
			{CollectorAgentID: collectorB, Skip: true},
		},
	})

	day := time.Now().UTC().Format("2006-01-02")
	rec := httptest.NewRecorder()
	testHandler.CreateNotePeriodBrief(rec, newRequest(http.MethodPost, "/api/notes/period-briefs", map[string]any{
		"window":              "day",
		"date":                day,
		"timezone":            "UTC",
		"agent_id":            synthID,
		"collector_agent_ids": []string{collectorA, collectorB},
		"focus":               "只整理本机 ~/multica",
	}))
	if rec.Code != http.StatusCreated {
		t.Fatalf("period brief = %d: %s", rec.Code, rec.Body.String())
	}
	var resp createNotePeriodBriefResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.CollectorJobs) != 1 {
		t.Fatalf("plan skip should dispatch one collector: %#v", resp.CollectorJobs)
	}
	if resp.CollectorJobs[0].AgentID != collectorA {
		t.Fatalf("dispatched %s, want %s", resp.CollectorJobs[0].AgentID, collectorA)
	}
}

func TestSubmitAgentNotePeriodBriefPackDoesNotClobberSibling(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	synthID := createHandlerTestAgent(t, "Pack Race Synth "+uuid.NewString()[:8], nil)
	collectorA := createPeriodBriefCollectorTestAgent(t, "Pack Race A")
	collectorB := createPeriodBriefCollectorTestAgent(t, "Pack Race B")

	createRec := httptest.NewRecorder()
	testHandler.CreateNotePeriodBrief(createRec, newRequest(http.MethodPost, "/api/notes/period-briefs", map[string]any{
		"window":              "day",
		"date":                time.Now().UTC().Format("2006-01-02"),
		"timezone":            "UTC",
		"agent_id":            synthID,
		"collector_agent_ids": []string{collectorA, collectorB},
	}))
	if createRec.Code != http.StatusCreated {
		t.Fatalf("create = %d: %s", createRec.Code, createRec.Body.String())
	}
	var created createNotePeriodBriefResponse
	if err := json.NewDecoder(createRec.Body).Decode(&created); err != nil {
		t.Fatalf("decode create: %v", err)
	}

	submit := func(agentID, body string) int {
		req := withURLParam(withAgentCredentialPrincipal(
			newRequest(http.MethodPost, "/api/agent/notes/period-briefs/"+created.Page.ID+"/submit-pack", map[string]any{
				"markdown": body,
			}),
			agentID, testWorkspaceID, testUserID,
		), "draftPageId", created.Page.ID)
		rec := httptest.NewRecorder()
		testHandler.SubmitAgentNotePeriodBriefPack(rec, req)
		return rec.Code
	}

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		if code := submit(collectorA, "## Work groups\n\n### A\n- from A\n"); code != http.StatusOK {
			t.Errorf("submit A = %d", code)
		}
	}()
	go func() {
		defer wg.Done()
		if code := submit(collectorB, "## Work groups\n\n### B\n- from B\n"); code != http.StatusOK {
			t.Errorf("submit B = %d", code)
		}
	}()
	wg.Wait()

	var packs []string
	rows, err := testPool.Query(context.Background(), `
SELECT c->>'agent_id', c->>'pack_markdown'
FROM note_period_brief_run r,
     jsonb_array_elements(r.collectors) AS c
WHERE r.draft_page_id = $1::uuid
ORDER BY c->>'agent_id'`, created.Page.ID)
	if err != nil {
		t.Fatalf("load packs: %v", err)
	}
	defer rows.Close()
	got := map[string]string{}
	for rows.Next() {
		var agentID, markdown string
		if err := rows.Scan(&agentID, &markdown); err != nil {
			t.Fatalf("scan pack: %v", err)
		}
		got[agentID] = markdown
		packs = append(packs, agentID)
	}
	if !strings.Contains(got[collectorA], "from A") {
		t.Fatalf("collector A pack lost: %#v", got)
	}
	if !strings.Contains(got[collectorB], "from B") {
		t.Fatalf("collector B pack lost: %#v", got)
	}
	_ = packs
}
