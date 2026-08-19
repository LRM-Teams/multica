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

## Unscoped / unclear
- scratch under /tmp
`

	day := time.Now().UTC().Format("2006-01-02")
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-stop:
				return
			default:
			}
			_, _ = testPool.Exec(context.Background(), `
UPDATE note_page
SET content = $1, updated_at = now()
WHERE owner_user_id = $2
  AND title LIKE '采集包%'
  AND length(trim(content)) = 0
  AND deleted_at IS NULL`,
				packBody, testUserID)
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

	packBody := "# 采集包 from note_write\n\n## Highlights\n- harvested pending proposal\n"
	day := time.Now().UTC().Format("2006-01-02")
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-stop:
				return
			default:
			}
			var jobID, taskID, channelID, pageID string
			err := testPool.QueryRow(context.Background(), `
SELECT j.id::text, j.task_id::text, j.channel_id::text, j.page_id::text
FROM note_worker_job j
JOIN note_page p ON p.id = j.page_id
WHERE j.agent_id = $1
  AND p.title LIKE '采集包%'
  AND length(trim(p.content)) = 0
ORDER BY j.created_at DESC
LIMIT 1`, collectorID).Scan(&jobID, &taskID, &channelID, &pageID)
			if err == nil && jobID != "" && channelID != "" && pageID != "" && taskID != "" {
				parts, _ := json.Marshal([]map[string]any{{
					"type":   "note_write",
					"ref_id": pageID,
					"text":   packBody,
				}})
				var msgID string
				insErr := testPool.QueryRow(context.Background(), `
INSERT INTO channel_message (channel_id, workspace_id, author_type, author_id, author_name, content, parts, source)
VALUES ($1::uuid, $2::uuid, 'agent', $3::uuid, 'collector', $4, $5::jsonb, 'multica')
RETURNING id::text`,
					channelID, testWorkspaceID, collectorID, packBody, string(parts)).Scan(&msgID)
				if insErr != nil {
					t.Logf("insert note_write proposal: %v", insErr)
					time.Sleep(40 * time.Millisecond)
					continue
				}
				if _, err := testPool.Exec(context.Background(), `
UPDATE agent_inbox_event
SET status = 'acked', terminal_outcome = 'completed', started_at = now(), completed_at = now(), acked_at = now()
WHERE id = $1::uuid`, taskID); err != nil {
					t.Logf("complete collector task: %v", err)
					time.Sleep(40 * time.Millisecond)
					continue
				}
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
	if !containsNoteRetrospectiveSource(resp.SourcesUsed, notePeriodBriefSourceCollectors) {
		t.Fatalf("pending note_write after complete should mark collectors used: %v", resp.SourcesUsed)
	}
	if !strings.Contains(resp.Page.Content, "harvested pending proposal") {
		t.Fatalf("draft missing proposal pack body: %s", resp.Page.Content)
	}
	var contextRaw []byte
	if err := testPool.QueryRow(context.Background(), `
SELECT context FROM agent_inbox_event WHERE id = $1`, *resp.Job.TaskID).Scan(&contextRaw); err != nil {
		t.Fatalf("load wake: %v", err)
	}
	var wake map[string]any
	_ = json.Unmarshal(contextRaw, &wake)
	prompt, _ := wake["prompt"].(string)
	if !strings.Contains(prompt, "harvested pending proposal") {
		t.Fatalf("synthesizer packs missing proposal: %s", prompt)
	}
}

// Regression: collectors often emit --note-write while the Note Worker job is
// still projected as "running". Synthesis must harvest that proposal instead
// of waiting for completed (and timing out to empty packs).
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

	synthID := createHandlerTestAgent(t, "Period Brief Running Harvest Synth "+uuid.NewString()[:8], nil)
	collectorID := createPeriodBriefCollectorTestAgent(t, "Running Harvest Collector")

	packBody := "# 采集包 while running\n\n## Highlights\n- harvested before job completed\n"
	day := time.Now().UTC().Format("2006-01-02")
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-stop:
				return
			default:
			}
			var channelID, pageID string
			err := testPool.QueryRow(context.Background(), `
SELECT j.channel_id::text, j.page_id::text
FROM note_worker_job j
JOIN note_page p ON p.id = j.page_id
WHERE j.agent_id = $1
  AND p.title LIKE '采集包%'
  AND length(trim(p.content)) = 0
ORDER BY j.created_at DESC
LIMIT 1`, collectorID).Scan(&channelID, &pageID)
			if err == nil && channelID != "" && pageID != "" {
				parts, _ := json.Marshal([]map[string]any{{
					"type":   "note_write",
					"ref_id": pageID,
					"text":   packBody,
				}})
				if _, insErr := testPool.Exec(context.Background(), `
INSERT INTO channel_message (channel_id, workspace_id, author_type, author_id, author_name, content, parts, source)
VALUES ($1::uuid, $2::uuid, 'agent', $3::uuid, 'collector', $4, $5::jsonb, 'multica')`,
					channelID, testWorkspaceID, collectorID, packBody, string(parts)); insErr != nil {
					t.Logf("insert running note_write proposal: %v", insErr)
					time.Sleep(40 * time.Millisecond)
					continue
				}
				// Intentionally leave agent_inbox_event / job non-terminal.
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
	if !containsNoteRetrospectiveSource(resp.SourcesUsed, notePeriodBriefSourceCollectors) {
		t.Fatalf("running-job note_write should mark collectors used: %v", resp.SourcesUsed)
	}
	if !strings.Contains(resp.Page.Content, "harvested before job completed") {
		t.Fatalf("draft missing running-job proposal pack: %s", resp.Page.Content)
	}
	var contextRaw []byte
	if err := testPool.QueryRow(context.Background(), `
SELECT context FROM agent_inbox_event WHERE id = $1`, *resp.Job.TaskID).Scan(&contextRaw); err != nil {
		t.Fatalf("load wake: %v", err)
	}
	var wake map[string]any
	_ = json.Unmarshal(contextRaw, &wake)
	prompt, _ := wake["prompt"].(string)
	if !strings.Contains(prompt, "harvested before job completed") {
		t.Fatalf("synthesizer packs missing running-job proposal: %s", prompt)
	}
}

// Regression: Pi/OpenAI often 400s (`input[n].status`) AFTER --note-write.
// Harvest the proposal even when the inbox task is acked/failed.
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

	synthID := createHandlerTestAgent(t, "Period Brief Failed Harvest Synth "+uuid.NewString()[:8], nil)
	collectorID := createPeriodBriefCollectorTestAgent(t, "Failed Harvest Collector")

	packBody := "# 采集包 after failed job\n\n## Highlights\n- harvested after api_invalid_request\n"
	day := time.Now().UTC().Format("2006-01-02")
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-stop:
				return
			default:
			}
			var jobID, taskID, channelID, pageID string
			err := testPool.QueryRow(context.Background(), `
SELECT j.id::text, j.task_id::text, j.channel_id::text, j.page_id::text
FROM note_worker_job j
JOIN note_page p ON p.id = j.page_id
WHERE j.agent_id = $1
  AND p.title LIKE '采集包%'
  AND length(trim(p.content)) = 0
ORDER BY j.created_at DESC
LIMIT 1`, collectorID).Scan(&jobID, &taskID, &channelID, &pageID)
			if err == nil && jobID != "" && channelID != "" && pageID != "" && taskID != "" {
				parts, _ := json.Marshal([]map[string]any{{
					"type":   "note_write",
					"ref_id": pageID,
					"text":   packBody,
				}})
				if _, insErr := testPool.Exec(context.Background(), `
INSERT INTO channel_message (channel_id, workspace_id, author_type, author_id, author_name, content, parts, source)
VALUES ($1::uuid, $2::uuid, 'agent', $3::uuid, 'collector', $4, $5::jsonb, 'multica')`,
					channelID, testWorkspaceID, collectorID, packBody, string(parts)); insErr != nil {
					t.Logf("insert failed-job note_write: %v", insErr)
					time.Sleep(40 * time.Millisecond)
					continue
				}
				if _, err := testPool.Exec(context.Background(), `
UPDATE agent_inbox_event
SET status = 'acked', terminal_outcome = 'failed', failure_reason = 'api_invalid_request',
    error = $2, started_at = now(), completed_at = now(), acked_at = now()
WHERE id = $1::uuid`, taskID, "Unknown parameter: 'input[86].status'"); err != nil {
					t.Logf("fail collector task: %v", err)
					time.Sleep(40 * time.Millisecond)
					continue
				}
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
	if !containsNoteRetrospectiveSource(resp.SourcesUsed, notePeriodBriefSourceCollectors) {
		t.Fatalf("failed job with note_write should mark collectors used: %v", resp.SourcesUsed)
	}
	if !strings.Contains(resp.Page.Content, "harvested after api_invalid_request") {
		t.Fatalf("draft missing failed-job proposal pack: %s", resp.Page.Content)
	}
	var contextRaw []byte
	if err := testPool.QueryRow(context.Background(), `
SELECT context FROM agent_inbox_event WHERE id = $1`, *resp.Job.TaskID).Scan(&contextRaw); err != nil {
		t.Fatalf("load wake: %v", err)
	}
	var wake map[string]any
	_ = json.Unmarshal(contextRaw, &wake)
	prompt, _ := wake["prompt"].(string)
	if !strings.Contains(prompt, "harvested after api_invalid_request") {
		t.Fatalf("synthesizer packs missing failed-job proposal: %s", prompt)
	}
	assertPeriodBriefInboxForceFresh(t, *resp.Job.TaskID)
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
	collectorID := createPeriodBriefCollectorTestAgent(t, "Member Collector")

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
