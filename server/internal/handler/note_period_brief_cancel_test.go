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

func TestCancelNotePeriodBriefStopsCollectingRunAndInbox(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	var sourcePageID string
	if err := testPool.QueryRow(ctx, `
INSERT INTO note_page (workspace_id, owner_user_id, title, content, sort_key, created_by, updated_by)
VALUES ($1, $2, $3, '', lpad((extract(epoch from now()) * 1000000)::bigint::text, 20, '0'), $2, $2)
RETURNING id`, testWorkspaceID, testUserID, "Cancel brief page "+uuid.NewString()[:8]).Scan(&sourcePageID); err != nil {
		t.Fatalf("create source page: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(ctx, `DELETE FROM note_period_brief_run WHERE source_page_id = $1`, sourcePageID)
		_, _ = testPool.Exec(ctx, `DELETE FROM note_page WHERE id = $1`, sourcePageID)
	})

	synthID := createHandlerTestAgent(t, "Cancel Brief Synth "+uuid.NewString()[:8], nil)
	collectorID := createHandlerTestAgent(t, "Cancel Brief Collector "+uuid.NewString()[:8], nil)
	folderID, err := testHandler.ensureNotePeriodBriefFolder(ctx, parseUUID(testWorkspaceID), parseUUID(testUserID))
	if err != nil {
		t.Fatalf("folder: %v", err)
	}
	draftID := insertPeriodBriefFixtureDraft(t, "cancel draft")
	runID := insertPeriodBriefFixtureRun(t, sourcePageID, uuidToString(folderID), synthID, draftID, "collecting", time.Now())

	var eventID string
	if err := testPool.QueryRow(ctx, `
INSERT INTO agent_inbox_event (
  workspace_id, agent_id, reason, requires_wake, status, priority, seq_from, seq_to
)
VALUES ($1, $2, 'note_worker', true, 'draining', 100, 1, 1)
RETURNING id`, testWorkspaceID, collectorID).Scan(&eventID); err != nil {
		t.Fatalf("insert inbox event: %v", err)
	}
	t.Cleanup(func() { _, _ = testPool.Exec(ctx, `DELETE FROM agent_inbox_event WHERE id = $1`, eventID) })

	var jobID string
	if err := testPool.QueryRow(ctx, `
INSERT INTO note_worker_job (workspace_id, page_id, creator_id, agent_id, instruction, status, task_id)
VALUES ($1, $2, $3, $4, 'collect period brief', 'dispatched', $5)
RETURNING id`, testWorkspaceID, draftID, testUserID, collectorID, eventID).Scan(&jobID); err != nil {
		t.Fatalf("insert worker job: %v", err)
	}
	t.Cleanup(func() { _, _ = testPool.Exec(ctx, `DELETE FROM note_worker_job WHERE id = $1`, jobID) })

	req := newRequest(http.MethodPost, "/api/notes/period-briefs/"+runID+"/cancel", nil)
	req = withURLParam(req, "runId", runID)
	rec := httptest.NewRecorder()
	testHandler.CancelNotePeriodBrief(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("cancel = %d: %s", rec.Code, rec.Body.String())
	}
	var body cancelNotePeriodBriefResponse
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Run.ID != runID || body.Run.Status != "cancelled" {
		t.Fatalf("cancel response = %#v", body.Run)
	}

	var runStatus, jobStatus, eventStatus string
	if err := testPool.QueryRow(ctx, `SELECT status FROM note_period_brief_run WHERE id = $1`, runID).Scan(&runStatus); err != nil {
		t.Fatalf("load run: %v", err)
	}
	if runStatus != "cancelled" {
		t.Fatalf("run status = %q, want cancelled", runStatus)
	}
	if err := testPool.QueryRow(ctx, `SELECT status FROM note_worker_job WHERE id = $1`, jobID).Scan(&jobStatus); err != nil {
		t.Fatalf("load job: %v", err)
	}
	if jobStatus != "cancelled" {
		t.Fatalf("job status = %q, want cancelled", jobStatus)
	}
	if err := testPool.QueryRow(ctx, `SELECT status FROM agent_inbox_event WHERE id = $1`, eventID).Scan(&eventStatus); err != nil {
		t.Fatalf("load inbox: %v", err)
	}
	if eventStatus != "suppressed" {
		t.Fatalf("inbox status = %q, want suppressed", eventStatus)
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
	if active.Run != nil {
		t.Fatalf("active run = %#v, want nil after cancel", active.Run)
	}

	again := newRequest(http.MethodPost, "/api/notes/period-briefs/"+runID+"/cancel", nil)
	again = withURLParam(again, "runId", runID)
	againRec := httptest.NewRecorder()
	testHandler.CancelNotePeriodBrief(againRec, again)
	if againRec.Code != http.StatusOK {
		t.Fatalf("idempotent cancel = %d: %s", againRec.Code, againRec.Body.String())
	}
}

func TestCancelNotePeriodBriefRejectsFinishedRun(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	var sourcePageID string
	if err := testPool.QueryRow(ctx, `
INSERT INTO note_page (workspace_id, owner_user_id, title, content, sort_key, created_by, updated_by)
VALUES ($1, $2, $3, '', lpad((extract(epoch from now()) * 1000000)::bigint::text, 20, '0'), $2, $2)
RETURNING id`, testWorkspaceID, testUserID, "Done brief page "+uuid.NewString()[:8]).Scan(&sourcePageID); err != nil {
		t.Fatalf("create source page: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(ctx, `DELETE FROM note_period_brief_run WHERE source_page_id = $1`, sourcePageID)
		_, _ = testPool.Exec(ctx, `DELETE FROM note_page WHERE id = $1`, sourcePageID)
	})
	synthID := createHandlerTestAgent(t, "Done Brief Synth "+uuid.NewString()[:8], nil)
	folderID, err := testHandler.ensureNotePeriodBriefFolder(ctx, parseUUID(testWorkspaceID), parseUUID(testUserID))
	if err != nil {
		t.Fatalf("folder: %v", err)
	}
	draftID := insertPeriodBriefFixtureDraft(t, "done draft")
	runID := insertPeriodBriefFixtureRun(t, sourcePageID, uuidToString(folderID), synthID, draftID, "done", time.Now())

	req := newRequest(http.MethodPost, "/api/notes/period-briefs/"+runID+"/cancel", nil)
	req = withURLParam(req, "runId", runID)
	rec := httptest.NewRecorder()
	testHandler.CancelNotePeriodBrief(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("cancel finished run = %d, want 409: %s", rec.Code, rec.Body.String())
	}
}
