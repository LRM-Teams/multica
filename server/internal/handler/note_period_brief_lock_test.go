package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
)

func insertPeriodBriefLiveWorkerJob(t *testing.T, draftID, agentID string) string {
	t.Helper()
	var jobID string
	if err := testPool.QueryRow(context.Background(), `
INSERT INTO note_worker_job (workspace_id, page_id, creator_id, agent_id, instruction, status)
VALUES ($1, $2, $3, $4, 'period brief live work', 'running')
RETURNING id`, testWorkspaceID, draftID, testUserID, agentID).Scan(&jobID); err != nil {
		t.Fatalf("insert live worker: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM note_worker_job WHERE id = $1`, jobID)
	})
	return jobID
}

func TestGetActiveSettlesWrittenSynthesizingRun(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	sourcePageID := insertPeriodBriefFixtureDraft(t, "Lock settle page")
	synthID := createHandlerTestAgent(t, "Lock Settle Synth "+uuid.NewString()[:8], nil)
	sessionID := createHandlerTestChatSession(t, synthID)
	folderID, err := testHandler.ensureNotePeriodBriefFolder(context.Background(), parseUUID(testWorkspaceID), parseUUID(testUserID))
	if err != nil {
		t.Fatalf("folder: %v", err)
	}
	draftID := insertPeriodBriefFixtureDraft(t, "lock settle draft")
	runID := insertPeriodBriefFixtureRun(t, sourcePageID, uuidToString(folderID), synthID, draftID, "synthesizing", time.Now().Add(-time.Minute))
	if _, err := testPool.Exec(context.Background(), `
UPDATE note_period_brief_run SET chat_session_id = $2 WHERE id = $1`, runID, sessionID); err != nil {
		t.Fatalf("bind session: %v", err)
	}
	plantPeriodBriefFolderNoteWrite(t, synthID, uuidToString(folderID), "# 工作介绍 settled\n\nAlready written.")

	rec := httptest.NewRecorder()
	testHandler.GetActiveNotePeriodBrief(rec, newRequest(http.MethodGet, "/api/notes/period-briefs/active?page_id="+sourcePageID, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("active = %d: %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Run *notePeriodBriefActiveResponse `json:"run"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Run == nil || body.Run.Status != "awaiting_confirm" || body.Run.ID != runID {
		t.Fatalf("active run = %#v, want awaiting_confirm %s", body.Run, runID)
	}
}

func TestGetActiveClearsDeadSynthesizingLock(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	sourcePageID := insertPeriodBriefFixtureDraft(t, "Dead active page")
	synthID := createHandlerTestAgent(t, "Dead Active Synth "+uuid.NewString()[:8], nil)
	folderID, err := testHandler.ensureNotePeriodBriefFolder(context.Background(), parseUUID(testWorkspaceID), parseUUID(testUserID))
	if err != nil {
		t.Fatalf("folder: %v", err)
	}
	draftID := insertPeriodBriefFixtureDraft(t, "dead active draft")
	runID := insertPeriodBriefFixtureRun(t, sourcePageID, uuidToString(folderID), synthID, draftID, "synthesizing", time.Now().Add(-time.Minute))

	rec := httptest.NewRecorder()
	testHandler.GetActiveNotePeriodBrief(rec, newRequest(http.MethodGet, "/api/notes/period-briefs/active?page_id="+sourcePageID, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("active = %d: %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Run *notePeriodBriefActiveResponse `json:"run"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Run != nil {
		t.Fatalf("active run = %#v, want nil after dead lock", body.Run)
	}
	var status string
	if err := testPool.QueryRow(context.Background(), `
SELECT status FROM note_period_brief_run WHERE id = $1`, runID).Scan(&status); err != nil {
		t.Fatalf("load run: %v", err)
	}
	if status != "cancelled" {
		t.Fatalf("dead lock status = %s, want cancelled", status)
	}
}

func TestCreateAllowsStartWhenDeadSynthesizingHasNoLiveWork(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	prevBG := notePeriodBriefFinishInBackground
	notePeriodBriefFinishInBackground = true
	t.Cleanup(func() { notePeriodBriefFinishInBackground = prevBG })

	sourcePageID := insertPeriodBriefFixtureDraft(t, "Dead lock page")
	synthID := createHandlerTestAgent(t, "Dead Lock Synth "+uuid.NewString()[:8], nil)
	folderID, err := testHandler.ensureNotePeriodBriefFolder(context.Background(), parseUUID(testWorkspaceID), parseUUID(testUserID))
	if err != nil {
		t.Fatalf("folder: %v", err)
	}
	draftID := insertPeriodBriefFixtureDraft(t, "dead lock draft")
	runID := insertPeriodBriefFixtureRun(t, sourcePageID, uuidToString(folderID), synthID, draftID, "synthesizing", time.Now().Add(-time.Minute))
	collectorA := createPeriodBriefCollectorTestAgent(t, "Dead Lock Collector")

	started := time.Now()
	rec := httptest.NewRecorder()
	testHandler.CreateNotePeriodBrief(rec, newRequest(http.MethodPost, "/api/notes/period-briefs", map[string]any{
		"window":               "day",
		"date":                 time.Now().UTC().Format("2006-01-02"),
		"timezone":             "UTC",
		"agent_id":             synthID,
		"collector_agent_ids":  []string{collectorA},
		"context_note_page_id": sourcePageID,
	}))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create after dead lock = %d: %s", rec.Code, rec.Body.String())
	}
	if elapsed := time.Since(started); elapsed > 3*time.Second {
		t.Fatalf("create held the HTTP request for %s; it must return after dispatch", elapsed)
	}

	var oldStatus string
	if err := testPool.QueryRow(context.Background(), `
SELECT status FROM note_period_brief_run WHERE id = $1`, runID).Scan(&oldStatus); err != nil {
		t.Fatalf("load old run: %v", err)
	}
	if oldStatus != "cancelled" {
		t.Fatalf("dead orphan status = %s, want cancelled", oldStatus)
	}
}

func TestCreateConflictsWhileCollectorJobIsLive(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	sourcePageID := insertPeriodBriefFixtureDraft(t, "Live lock page")
	synthID := createHandlerTestAgent(t, "Live Lock Synth "+uuid.NewString()[:8], nil)
	folderID, err := testHandler.ensureNotePeriodBriefFolder(context.Background(), parseUUID(testWorkspaceID), parseUUID(testUserID))
	if err != nil {
		t.Fatalf("folder: %v", err)
	}
	draftID := insertPeriodBriefFixtureDraft(t, "live lock draft")
	insertPeriodBriefFixtureRun(t, sourcePageID, uuidToString(folderID), synthID, draftID, "collecting", time.Now())
	insertPeriodBriefLiveWorkerJob(t, draftID, synthID)
	collectorA := createPeriodBriefCollectorTestAgent(t, "Live Lock Collector")

	rec := httptest.NewRecorder()
	testHandler.CreateNotePeriodBrief(rec, newRequest(http.MethodPost, "/api/notes/period-briefs", map[string]any{
		"window":               "day",
		"date":                 time.Now().UTC().Format("2006-01-02"),
		"timezone":             "UTC",
		"agent_id":             synthID,
		"collector_agent_ids":  []string{collectorA},
		"context_note_page_id": sourcePageID,
	}))
	if rec.Code != http.StatusConflict {
		t.Fatalf("live lock create = %d, want 409: %s", rec.Code, rec.Body.String())
	}
}

func TestCancelSettlesWrittenSynthesizingOrphan(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	sourcePageID := insertPeriodBriefFixtureDraft(t, "Cancel settle page")
	synthID := createHandlerTestAgent(t, "Cancel Settle Synth "+uuid.NewString()[:8], nil)
	sessionID := createHandlerTestChatSession(t, synthID)
	folderID, err := testHandler.ensureNotePeriodBriefFolder(context.Background(), parseUUID(testWorkspaceID), parseUUID(testUserID))
	if err != nil {
		t.Fatalf("folder: %v", err)
	}
	draftID := insertPeriodBriefFixtureDraft(t, "cancel settle draft")
	runID := insertPeriodBriefFixtureRun(t, sourcePageID, uuidToString(folderID), synthID, draftID, "synthesizing", time.Now().Add(-time.Minute))
	if _, err := testPool.Exec(context.Background(), `
UPDATE note_period_brief_run SET chat_session_id = $2 WHERE id = $1`, runID, sessionID); err != nil {
		t.Fatalf("bind session: %v", err)
	}
	jobID := insertPeriodBriefLiveWorkerJob(t, draftID, synthID)
	plantPeriodBriefFolderNoteWrite(t, synthID, uuidToString(folderID), "# 工作介绍 cancel-settle\n\nAlready written.")

	req := newRequest(http.MethodPost, "/api/notes/period-briefs/"+runID+"/cancel", nil)
	req = withURLParam(req, "runId", runID)
	rec := httptest.NewRecorder()
	testHandler.CancelNotePeriodBrief(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("cancel written orphan = %d: %s", rec.Code, rec.Body.String())
	}
	var body cancelNotePeriodBriefResponse
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Run.Status != "awaiting_confirm" {
		t.Fatalf("cancel written orphan status = %s, want awaiting_confirm", body.Run.Status)
	}

	var runStatus, jobStatus string
	if err := testPool.QueryRow(context.Background(), `SELECT status FROM note_period_brief_run WHERE id = $1`, runID).Scan(&runStatus); err != nil {
		t.Fatalf("load run: %v", err)
	}
	if runStatus != "awaiting_confirm" {
		t.Fatalf("run status = %s, want awaiting_confirm (not cancelled)", runStatus)
	}
	if err := testPool.QueryRow(context.Background(), `SELECT status FROM note_worker_job WHERE id = $1`, jobID).Scan(&jobStatus); err != nil {
		t.Fatalf("load job: %v", err)
	}
	if jobStatus != "cancelled" {
		t.Fatalf("leftover job status = %s, want cancelled", jobStatus)
	}
}
