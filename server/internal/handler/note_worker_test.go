package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/multica-ai/multica/server/internal/service"
)

func TestCreateNoteWorkerJobRejectsEditorFields(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	agentID := createHandlerTestAgent(t, "Note Worker Reject Agent "+uuid.NewString()[:8], nil)
	noteID := createNotePageForAITest(t, "Worker reject note "+uuid.NewString())

	for _, body := range []map[string]any{
		{"agent_id": agentID, "instruction": "ship it", "prompt": "rewrite the page"},
		{"agent_id": agentID, "instruction": "ship it", "action": "replace_page"},
		{"agent_id": agentID, "instruction": "ship it", "intent": NoteIntentEditor},
	} {
		req := withURLParam(newRequest(http.MethodPost, "/api/notes/pages/"+noteID+"/worker-jobs", body), "id", noteID)
		w := httptest.NewRecorder()
		testHandler.CreateNoteWorkerJob(w, req)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("body=%v: expected 400, got %d: %s", body, w.Code, w.Body.String())
		}
		if !strings.Contains(w.Body.String(), errNoteWorkerRejectsEditor) {
			t.Fatalf("body=%v: expected editor-reject message, got %s", body, w.Body.String())
		}
	}
}

func TestCreateNoteAIJobDoesNotAttachNoteBrief(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	agentID := createHandlerTestAgent(t, "Note Editor No Brief "+uuid.NewString()[:8], nil)
	noteID := createNotePageForAITest(t, "Editor no brief "+uuid.NewString())
	job := createNoteAIJobForTest(t, noteID, agentID)

	var contextRaw []byte
	if err := testPool.QueryRow(context.Background(), `
SELECT context FROM agent_inbox_event WHERE id = $1`, job.TaskID).Scan(&contextRaw); err != nil {
		t.Fatalf("load editor task context: %v", err)
	}
	if _, ok, err := service.NoteBriefFromContext(contextRaw); err != nil || ok {
		t.Fatalf("Editor task must not carry note_brief: ok=%v err=%v raw=%s", ok, err, contextRaw)
	}
}


func TestCreateNoteWorkerJobDispatchesWithNoteBrief(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	agentID := createHandlerTestAgent(t, "Note Worker Agent "+uuid.NewString()[:8], nil)
	noteID := createNotePageForAITest(t, "Worker note "+uuid.NewString())
	if _, err := testPool.Exec(context.Background(), `
UPDATE note_page SET content = $1 WHERE id = $2`, "Brief body for worker", noteID); err != nil {
		t.Fatalf("set note content: %v", err)
	}

	req := withURLParam(newRequest(http.MethodPost, "/api/notes/pages/"+noteID+"/worker-jobs", map[string]any{
		"agent_id":    agentID,
		"instruction": "Turn this brief into an Issue and start work",
		"intent":      NoteIntentWorker,
	}), "id", noteID)
	w := httptest.NewRecorder()
	testHandler.CreateNoteWorkerJob(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("CreateNoteWorkerJob: expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var resp NoteWorkerJobResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode worker job: %v", err)
	}
	if resp.ID == "" || resp.PageID != noteID || resp.AgentID != agentID || resp.Status != "dispatched" || resp.Intent != NoteIntentWorker {
		t.Fatalf("worker job response = %#v", resp)
	}
	if resp.TaskID == nil || *resp.TaskID == "" {
		t.Fatal("expected dispatched task_id")
	}

	var aiJobs int
	if err := testPool.QueryRow(context.Background(), `
SELECT count(*) FROM note_ai_job WHERE page_id = $1`, noteID).Scan(&aiJobs); err != nil {
		t.Fatalf("count note_ai_job: %v", err)
	}
	if aiJobs != 0 {
		t.Fatalf("worker create leaked %d note_ai_job rows", aiJobs)
	}

	var contextRaw []byte
	var chatContent string
	if err := testPool.QueryRow(context.Background(), `
SELECT e.context, m.content
FROM agent_inbox_event e
JOIN chat_message m ON m.task_id = e.id AND m.role = 'user'
WHERE e.id = $1`, *resp.TaskID).Scan(&contextRaw, &chatContent); err != nil {
		t.Fatalf("load task context/message: %v", err)
	}
	brief, ok, err := service.NoteBriefFromContext(contextRaw)
	if err != nil || !ok {
		t.Fatalf("NoteBriefFromContext: ok=%v err=%v raw=%s", ok, err, contextRaw)
	}
	if brief.PageID != noteID {
		t.Fatalf("brief.page_id = %q, want %s", brief.PageID, noteID)
	}
	if !strings.Contains(chatContent, "<note>") || !strings.Contains(chatContent, "untrusted") || !strings.Contains(chatContent, "Brief body for worker") {
		t.Fatalf("chat prompt missing untrusted note wrap: %s", chatContent)
	}
	if !strings.Contains(chatContent, "<instruction>") || !strings.Contains(chatContent, "Turn this brief into an Issue") {
		t.Fatalf("chat prompt missing instruction: %s", chatContent)
	}

	getReq := withURLParam(newRequest(http.MethodGet, "/api/notes/worker-jobs/"+resp.ID, nil), "jobId", resp.ID)
	getRec := httptest.NewRecorder()
	testHandler.GetNoteWorkerJob(getRec, getReq)
	if getRec.Code != http.StatusOK {
		t.Fatalf("GetNoteWorkerJob: expected 200, got %d: %s", getRec.Code, getRec.Body.String())
	}
}

func TestGetNoteWorkerJobProjectsCompletedFromTask(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	agentID := createHandlerTestAgent(t, "Note Worker Project "+uuid.NewString()[:8], nil)
	noteID := createNotePageForAITest(t, "Worker project note "+uuid.NewString())

	createRec := httptest.NewRecorder()
	testHandler.CreateNoteWorkerJob(createRec, withURLParam(newRequest(http.MethodPost, "/api/notes/pages/"+noteID+"/worker-jobs", map[string]any{
		"agent_id":    agentID,
		"instruction": "finish something",
		"intent":      NoteIntentWorker,
	}), "id", noteID))
	if createRec.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", createRec.Code, createRec.Body.String())
	}
	var created NoteWorkerJobResponse
	if err := json.NewDecoder(createRec.Body).Decode(&created); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if created.TaskID == nil {
		t.Fatal("expected task_id")
	}

	if _, err := testPool.Exec(context.Background(), `
UPDATE agent_inbox_event
SET status = 'acked', terminal_outcome = 'completed', started_at = now(), completed_at = now(), acked_at = now()
WHERE id = $1`, *created.TaskID); err != nil {
		t.Fatalf("complete task: %v", err)
	}

	getRec := httptest.NewRecorder()
	testHandler.GetNoteWorkerJob(getRec, withURLParam(newRequest(http.MethodGet, "/api/notes/worker-jobs/"+created.ID, nil), "jobId", created.ID))
	if getRec.Code != http.StatusOK {
		t.Fatalf("get: %d %s", getRec.Code, getRec.Body.String())
	}
	var got NoteWorkerJobResponse
	if err := json.NewDecoder(getRec.Body).Decode(&got); err != nil {
		t.Fatalf("decode get: %v", err)
	}
	if got.Status != "completed" {
		t.Fatalf("projected status = %q, want completed", got.Status)
	}

	var stored string
	if err := testPool.QueryRow(context.Background(), `
SELECT status FROM note_worker_job WHERE id = $1`, created.ID).Scan(&stored); err != nil {
		t.Fatalf("load stored status: %v", err)
	}
	if stored != "completed" {
		t.Fatalf("persisted status = %q, want completed", stored)
	}
}

func TestCreateNoteWorkerJobRejectsOutsiderWithoutNoteAccess(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	agentID := createHandlerTestAgent(t, "Note Worker ACL Agent "+uuid.NewString()[:8], nil)
	noteID := createNotePageForAITest(t, "Private worker note "+uuid.NewString())
	outsiderID := createWorkspaceMemberForNoteACL(t, "note-worker-outsider")

	req := withURLParam(newRequestAs(outsiderID, http.MethodPost, "/api/notes/pages/"+noteID+"/worker-jobs", map[string]any{
		"agent_id":    agentID,
		"instruction": "should not dispatch",
	}), "id", noteID)
	w := httptest.NewRecorder()
	testHandler.CreateNoteWorkerJob(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("outsider worker create: expected 404, got %d: %s", w.Code, w.Body.String())
	}

	var jobs int
	if err := testPool.QueryRow(context.Background(), `
SELECT count(*) FROM note_worker_job WHERE page_id = $1`, noteID).Scan(&jobs); err != nil {
		t.Fatalf("count worker jobs: %v", err)
	}
	if jobs != 0 {
		t.Fatalf("outsider created %d worker jobs", jobs)
	}
}
