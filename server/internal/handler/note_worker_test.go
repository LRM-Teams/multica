package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
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

func TestCreateNoteAIJobRejectsWorkerFields(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	agentID := createHandlerTestAgent(t, "Note Editor Reject Agent "+uuid.NewString()[:8], nil)
	noteID := createNotePageForAITest(t, "Editor reject note "+uuid.NewString())

	for _, body := range []map[string]any{
		{"agent_id": agentID, "prompt": "rewrite", "intent": NoteIntentWorker},
		{"agent_id": agentID, "prompt": "rewrite", "instruction": "do platform work"},
		{"agent_id": agentID, "prompt": "rewrite", "action": "replace_page"},
	} {
		req := withURLParam(newRequest(http.MethodPost, "/api/notes/pages/"+noteID+"/ai-jobs", body), "id", noteID)
		w := httptest.NewRecorder()
		testHandler.CreateNoteAIJob(w, req)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("body=%v: expected 400, got %d: %s", body, w.Code, w.Body.String())
		}
		if !strings.Contains(w.Body.String(), errNoteEditorRejectsWorker) {
			t.Fatalf("body=%v: expected worker-reject message, got %s", body, w.Body.String())
		}
	}
}

func TestCreateNoteWorkerJobDoesNotCreateNoteAIJob(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	agentID := createHandlerTestAgent(t, "Note Worker Agent "+uuid.NewString()[:8], nil)
	noteID := createNotePageForAITest(t, "Worker note "+uuid.NewString())

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
	if resp.ID == "" || resp.PageID != noteID || resp.AgentID != agentID || resp.Status != "pending" || resp.Intent != NoteIntentWorker {
		t.Fatalf("worker job response = %#v", resp)
	}
	if resp.TaskID != nil {
		t.Fatalf("S2-C3 worker job must not dispatch a task yet, got task_id=%v", *resp.TaskID)
	}

	var aiJobs int
	if err := testPool.QueryRow(context.Background(), `
SELECT count(*) FROM note_ai_job WHERE page_id = $1`, noteID).Scan(&aiJobs); err != nil {
		t.Fatalf("count note_ai_job: %v", err)
	}
	if aiJobs != 0 {
		t.Fatalf("worker create leaked %d note_ai_job rows", aiJobs)
	}

	getReq := withURLParam(newRequest(http.MethodGet, "/api/notes/worker-jobs/"+resp.ID, nil), "jobId", resp.ID)
	getRec := httptest.NewRecorder()
	testHandler.GetNoteWorkerJob(getRec, getReq)
	if getRec.Code != http.StatusOK {
		t.Fatalf("GetNoteWorkerJob: expected 200, got %d: %s", getRec.Code, getRec.Body.String())
	}
}
