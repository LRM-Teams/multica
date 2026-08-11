package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
)

func TestListNotePagesIncludesOwnedNotesAcrossWorkspaces(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	var otherWorkspaceID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO workspace (name, slug, description, issue_prefix)
		VALUES ($1, $2, '', $3)
		RETURNING id
	`, "Notes Cross Workspace", "notes-cross-"+uuid.NewString(), "NCW").Scan(&otherWorkspaceID); err != nil {
		t.Fatalf("create second workspace: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM workspace WHERE id = $1`, otherWorkspaceID)
	})

	if _, err := testPool.Exec(ctx, `
		INSERT INTO member (workspace_id, user_id, role)
		VALUES ($1, $2, 'owner')
	`, otherWorkspaceID, testUserID); err != nil {
		t.Fatalf("add current user to second workspace: %v", err)
	}

	title := "Private cross-workspace note " + uuid.NewString()
	var noteID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO note_page (workspace_id, owner_user_id, title, content, sort_key, created_by, updated_by)
		VALUES ($1, $2, $3, 'private body', '00000000000000000001', $2, $2)
		RETURNING id
	`, testWorkspaceID, testUserID, title).Scan(&noteID); err != nil {
		t.Fatalf("create note: %v", err)
	}
	listReq := newRequest(http.MethodGet, "/api/notes/pages", nil)
	listReq.Header.Set("X-Workspace-ID", otherWorkspaceID)
	listRec := httptest.NewRecorder()
	testHandler.ListNotePages(listRec, listReq)
	if listRec.Code != http.StatusOK {
		t.Fatalf("ListNotePages: expected 200, got %d: %s", listRec.Code, listRec.Body.String())
	}

	var listResp struct {
		Pages []NotePageResponse `json:"pages"`
	}
	if err := json.NewDecoder(listRec.Body).Decode(&listResp); err != nil {
		t.Fatalf("decode list response: %v", err)
	}
	var listed *NotePageResponse
	for i := range listResp.Pages {
		if listResp.Pages[i].ID == noteID {
			listed = &listResp.Pages[i]
			break
		}
	}
	if listed == nil {
		t.Fatalf("owned private note %s was not listed from workspace %s", noteID, otherWorkspaceID)
	}
	if listed.WorkspaceID != testWorkspaceID || listed.Title != title || !listed.CanManageShares {
		t.Fatalf("listed note = %#v, want workspace %s title %q manageable", *listed, testWorkspaceID, title)
	}

	getReq := withURLParam(newRequest(http.MethodGet, "/api/notes/pages/"+noteID, nil), "id", noteID)
	getReq.Header.Set("X-Workspace-ID", otherWorkspaceID)
	getRec := httptest.NewRecorder()
	testHandler.GetNotePage(getRec, getReq)
	if getRec.Code != http.StatusOK {
		t.Fatalf("GetNotePage from second workspace: expected 200, got %d: %s", getRec.Code, getRec.Body.String())
	}
}

func createNotePageForAITest(t *testing.T, title string) string {
	t.Helper()
	var noteID string
	if err := testPool.QueryRow(context.Background(), `
		INSERT INTO note_page (workspace_id, owner_user_id, title, content, sort_key, created_by, updated_by)
		VALUES ($1, $2, $3, 'body', '00000000000000000001', $2, $2)
		RETURNING id
	`, testWorkspaceID, testUserID, title).Scan(&noteID); err != nil {
		t.Fatalf("create note page: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(context.Background(), `DELETE FROM note_page WHERE id = $1`, noteID) })
	return noteID
}

func createNoteAIJobForTest(t *testing.T, noteID, agentID string) NoteAIJobResponse {
	t.Helper()
	return createNoteAIJobWithPromptForTest(t, noteID, agentID, "rewrite this note excerpt")
}

func createNoteAIJobWithPromptForTest(t *testing.T, noteID, agentID, prompt string) NoteAIJobResponse {
	t.Helper()
	req := withURLParam(newRequest(http.MethodPost, "/api/notes/pages/"+noteID+"/ai-jobs", map[string]any{
		"agent_id": agentID,
		"prompt":   prompt,
		"title":    "Note AI Test",
	}), "id", noteID)
	w := httptest.NewRecorder()
	testHandler.CreateNoteAIJob(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("CreateNoteAIJob: expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var resp NoteAIJobResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode note AI job: %v", err)
	}
	if resp.ID == "" || resp.TaskID == "" || resp.ChatSessionID == "" || resp.Status == "" {
		t.Fatalf("incomplete note AI job response: %#v", resp)
	}
	return resp
}

func TestParseNoteAIEditResultAcceptsJSONLikeMarkdown(t *testing.T) {
	result, err := parseNoteAIEditResult(`{"action":"patch","target":"old equation","markdown":"# Title

$$
\nabla \times \mathbf{E}
$$","title":null,"rationale":"Updated equations."}`)
	if err != nil {
		t.Fatalf("parse JSON-like note AI result: %v", err)
	}
	if result.Action != "patch" || result.Target == nil || *result.Target != "old equation" || result.Markdown != "# Title\n\n$$\n\\nabla \\times \\mathbf{E}\n$$" || result.Rationale == nil || *result.Rationale != "Updated equations." {
		t.Fatalf("parsed result = %#v", result)
	}
}

func TestNoteAIJobCreateStatusAndHiddenChatSession(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	agentID := createHandlerTestAgent(t, "Note AI Job Agent "+uuid.NewString()[:8], nil)
	noteID := createNotePageForAITest(t, "AI note "+uuid.NewString())
	job := createNoteAIJobForTest(t, noteID, agentID)
	if job.ID != job.TaskID || job.PageID != noteID || job.AgentID != agentID || job.Status != "queued" {
		t.Fatalf("job response = %#v, want task-backed queued job", job)
	}

	getReq := withURLParam(newRequest(http.MethodGet, "/api/notes/ai-jobs/"+job.ID, nil), "jobId", job.ID)
	getRec := httptest.NewRecorder()
	testHandler.GetNoteAIJob(getRec, getReq)
	if getRec.Code != http.StatusOK {
		t.Fatalf("GetNoteAIJob: expected 200, got %d: %s", getRec.Code, getRec.Body.String())
	}

	listReq := withChatTestWorkspaceCtx(t, newRequest(http.MethodGet, "/api/chat/sessions?status=all", nil))
	listRec := httptest.NewRecorder()
	testHandler.ListChatSessions(listRec, listReq)
	if listRec.Code != http.StatusOK {
		t.Fatalf("ListChatSessions: expected 200, got %d: %s", listRec.Code, listRec.Body.String())
	}
	var sessions []ChatSessionResponse
	if err := json.NewDecoder(listRec.Body).Decode(&sessions); err != nil {
		t.Fatalf("decode sessions: %v", err)
	}
	for _, session := range sessions {
		if session.ID == job.ChatSessionID {
			t.Fatalf("note AI backing chat session leaked into chat list: %#v", session)
		}
	}
}

func TestNoteAIJobCompletedReturnsAssistantResult(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	agentID := createHandlerTestAgent(t, "Note AI Complete Agent "+uuid.NewString()[:8], nil)
	noteID := createNotePageForAITest(t, "AI completion note "+uuid.NewString())
	job := createNoteAIJobForTest(t, noteID, agentID)
	if _, err := testPool.Exec(context.Background(), `
		UPDATE agent_inbox_event
		SET status = 'acked', terminal_outcome = 'completed', terminal_at = now(), acked_at = now(), completed_at = now()
		WHERE id = $1
	`, job.TaskID); err != nil {
		t.Fatalf("complete task: %v", err)
	}
	if _, err := testPool.Exec(context.Background(), `
		INSERT INTO chat_message (chat_session_id, role, content, task_id)
		VALUES ($1, 'assistant', $2, $3)
	`, job.ChatSessionID, `{"action":"replace_selection","markdown":"improved note text","rationale":"cleaner"}`, job.TaskID); err != nil {
		t.Fatalf("insert assistant result: %v", err)
	}

	getReq := withURLParam(newRequest(http.MethodGet, "/api/notes/ai-jobs/"+job.ID, nil), "jobId", job.ID)
	getRec := httptest.NewRecorder()
	testHandler.GetNoteAIJob(getRec, getReq)
	if getRec.Code != http.StatusOK {
		t.Fatalf("GetNoteAIJob: expected 200, got %d: %s", getRec.Code, getRec.Body.String())
	}
	var resp NoteAIJobResponse
	if err := json.NewDecoder(getRec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode completed job: %v", err)
	}
	if resp.Status != "completed" || resp.Result == nil || resp.Result.Action != "replace_selection" || resp.Result.Markdown != "improved note text" || resp.Result.Rationale == nil || *resp.Result.Rationale != "cleaner" {
		t.Fatalf("completed job response = %#v", resp)
	}
}

func noteAIPagePromptForTest(instruction string) string {
	return `You are the in-note AI assistant for a user's Notion-style note page.
The user pressed Space on an empty line and is talking to you at that cursor.
Return ONLY a valid JSON object.
Full current page Markdown:
<page>
old page
</page>
User instruction:
<instruction>
` + instruction + `
</instruction>`
}

func TestNoteAIJobRepairsSelectedMarkdownOnlyResult(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	agentID := createHandlerTestAgent(t, "Note AI Repair Agent "+uuid.NewString()[:8], nil)
	noteID := createNotePageForAITest(t, "AI repair note "+uuid.NewString())
	prompt := `You are editing a selected Markdown excerpt inside a user's note.
For selected Markdown excerpt edits, action MUST be "replace_selection". Do not use insert, replace_page, or patch.
Selected Markdown excerpt to replace:
<selection>
old text
</selection>`
	job := createNoteAIJobWithPromptForTest(t, noteID, agentID, prompt)
	if _, err := testPool.Exec(context.Background(), `
		UPDATE agent_inbox_event
		SET status = 'acked', terminal_outcome = 'completed', terminal_at = now(), acked_at = now(), completed_at = now()
		WHERE id = $1
	`, job.TaskID); err != nil {
		t.Fatalf("complete task: %v", err)
	}
	if _, err := testPool.Exec(context.Background(), `
		INSERT INTO chat_message (chat_session_id, role, content, task_id)
		VALUES ($1, 'assistant', $2, $3)
	`, job.ChatSessionID, "**Improved** [text](https://example.com)", job.TaskID); err != nil {
		t.Fatalf("insert assistant result: %v", err)
	}

	getReq := withURLParam(newRequest(http.MethodGet, "/api/notes/ai-jobs/"+job.ID, nil), "jobId", job.ID)
	getRec := httptest.NewRecorder()
	testHandler.GetNoteAIJob(getRec, getReq)
	if getRec.Code != http.StatusOK {
		t.Fatalf("GetNoteAIJob: expected 200, got %d: %s", getRec.Code, getRec.Body.String())
	}
	var resp NoteAIJobResponse
	if err := json.NewDecoder(getRec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode repaired job: %v", err)
	}
	if resp.Status != "completed" || resp.Result == nil || resp.Result.Action != "replace_selection" || resp.Result.Markdown != "**Improved** [text](https://example.com)" || resp.FailureReason != nil || resp.RepairCode == nil || *resp.RepairCode != noteAIRepairSelectedOutput {
		t.Fatalf("repaired job response = %#v, want completed replace_selection result", resp)
	}
}

func TestNoteAIJobRepairsPageMarkdownOnlyResult(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	agentID := createHandlerTestAgent(t, "Note AI Page Repair Agent "+uuid.NewString()[:8], nil)
	noteID := createNotePageForAITest(t, "AI page repair note "+uuid.NewString())
	job := createNoteAIJobWithPromptForTest(t, noteID, agentID, noteAIPagePromptForTest("Rewrite the whole page and polish the language."))
	if _, err := testPool.Exec(context.Background(), `
		UPDATE agent_inbox_event
		SET status = 'acked', terminal_outcome = 'completed', terminal_at = now(), acked_at = now(), completed_at = now()
		WHERE id = $1
	`, job.TaskID); err != nil {
		t.Fatalf("complete task: %v", err)
	}
	if _, err := testPool.Exec(context.Background(), `
		INSERT INTO chat_message (chat_session_id, role, content, task_id)
		VALUES ($1, 'assistant', $2, $3)
	`, job.ChatSessionID, "# Better Page\n\nImproved body.", job.TaskID); err != nil {
		t.Fatalf("insert assistant result: %v", err)
	}

	getReq := withURLParam(newRequest(http.MethodGet, "/api/notes/ai-jobs/"+job.ID, nil), "jobId", job.ID)
	getRec := httptest.NewRecorder()
	testHandler.GetNoteAIJob(getRec, getReq)
	if getRec.Code != http.StatusOK {
		t.Fatalf("GetNoteAIJob: expected 200, got %d: %s", getRec.Code, getRec.Body.String())
	}
	var resp NoteAIJobResponse
	if err := json.NewDecoder(getRec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode repaired page job: %v", err)
	}
	if resp.Status != "completed" || resp.Result == nil || resp.Result.Action != "replace_page" || resp.Result.Markdown != "# Better Page\n\nImproved body." || resp.FailureReason != nil || resp.RepairCode == nil || *resp.RepairCode != noteAIRepairPageOutput {
		t.Fatalf("repaired page job response = %#v, want completed replace_page result", resp)
	}
}

func TestParseNoteAIEditResultRepairsPageJSONWithoutAction(t *testing.T) {
	outcome, err := parseNoteAIEditResultWithRepairOutcome(`{"markdown":"Inserted paragraph","title":"New title","rationale":"Continues the page."}`, noteAIPagePromptForTest("Continue this page from the cursor."))
	if err != nil {
		t.Fatalf("repair page JSON without action: %v", err)
	}
	result := outcome.Result
	if result.Action != "insert" || result.Markdown != "Inserted paragraph" || result.Title == nil || *result.Title != "New title" || result.Rationale == nil || *result.Rationale != "Continues the page." || outcome.RepairCode == nil || *outcome.RepairCode != noteAIRepairPageOutput {
		t.Fatalf("repaired page insert = result:%#v repair:%v", result, outcome.RepairCode)
	}
}

func TestNoteAIJobInvalidStructuredResultFails(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	agentID := createHandlerTestAgent(t, "Note AI Invalid Agent "+uuid.NewString()[:8], nil)
	noteID := createNotePageForAITest(t, "AI invalid note "+uuid.NewString())
	job := createNoteAIJobForTest(t, noteID, agentID)
	if _, err := testPool.Exec(context.Background(), `
		UPDATE agent_inbox_event
		SET status = 'acked', terminal_outcome = 'completed', terminal_at = now(), acked_at = now(), completed_at = now()
		WHERE id = $1
	`, job.TaskID); err != nil {
		t.Fatalf("complete task: %v", err)
	}
	if _, err := testPool.Exec(context.Background(), `
		INSERT INTO chat_message (chat_session_id, role, content, task_id)
		VALUES ($1, 'assistant', 'plain unstructured text', $2)
	`, job.ChatSessionID, job.TaskID); err != nil {
		t.Fatalf("insert assistant result: %v", err)
	}

	getReq := withURLParam(newRequest(http.MethodGet, "/api/notes/ai-jobs/"+job.ID, nil), "jobId", job.ID)
	getRec := httptest.NewRecorder()
	testHandler.GetNoteAIJob(getRec, getReq)
	if getRec.Code != http.StatusOK {
		t.Fatalf("GetNoteAIJob: expected 200, got %d: %s", getRec.Code, getRec.Body.String())
	}
	var resp NoteAIJobResponse
	if err := json.NewDecoder(getRec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode invalid job: %v", err)
	}
	if resp.Status != "failed" || resp.Result != nil || resp.FailureReason == nil || resp.FailureCode == nil || *resp.FailureCode != noteAIFailureInvalidOutput {
		t.Fatalf("invalid structured result response = %#v, want failed without result", resp)
	}
}

func TestNoteAIJobFallsBackToCompletionOutputWithoutChatMessage(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	agentID := createHandlerTestAgent(t, "Note AI Fallback Agent "+uuid.NewString()[:8], nil)
	noteID := createNotePageForAITest(t, "AI fallback note "+uuid.NewString())
	job := createNoteAIJobForTest(t, noteID, agentID)
	completion := map[string]any{
		"type":   "message",
		"action": "",
		"output": `{"action":"insert","markdown":"hi","target":null,"title":null,"rationale":"greeting"}`,
	}
	raw, err := json.Marshal(completion)
	if err != nil {
		t.Fatalf("marshal completion: %v", err)
	}
	if _, err := testPool.Exec(context.Background(), `
		UPDATE agent_inbox_event
		SET status = 'acked',
		    terminal_outcome = 'replied',
		    result = $2::jsonb,
		    terminal_at = now(),
		    acked_at = now(),
		    completed_at = now()
		WHERE id = $1
	`, job.TaskID, raw); err != nil {
		t.Fatalf("complete task with result only: %v", err)
	}

	getReq := withURLParam(newRequest(http.MethodGet, "/api/notes/ai-jobs/"+job.ID, nil), "jobId", job.ID)
	getRec := httptest.NewRecorder()
	testHandler.GetNoteAIJob(getRec, getReq)
	if getRec.Code != http.StatusOK {
		t.Fatalf("GetNoteAIJob: expected 200, got %d: %s", getRec.Code, getRec.Body.String())
	}
	var resp NoteAIJobResponse
	if err := json.NewDecoder(getRec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode fallback job: %v", err)
	}
	if resp.Status != "completed" || resp.Result == nil || resp.Result.Action != "insert" || resp.Result.Markdown != "hi" {
		t.Fatalf("fallback job response = %#v, want completed insert hi", resp)
	}
}

func TestNoteAIJobEmptyCompletionFails(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	agentID := createHandlerTestAgent(t, "Note AI Empty Agent "+uuid.NewString()[:8], nil)
	noteID := createNotePageForAITest(t, "AI empty note "+uuid.NewString())
	job := createNoteAIJobForTest(t, noteID, agentID)
	if _, err := testPool.Exec(context.Background(), `
		UPDATE agent_inbox_event
		SET status = 'acked', terminal_outcome = 'replied', terminal_at = now(), acked_at = now(), completed_at = now()
		WHERE id = $1
	`, job.TaskID); err != nil {
		t.Fatalf("complete empty task: %v", err)
	}

	getReq := withURLParam(newRequest(http.MethodGet, "/api/notes/ai-jobs/"+job.ID, nil), "jobId", job.ID)
	getRec := httptest.NewRecorder()
	testHandler.GetNoteAIJob(getRec, getReq)
	if getRec.Code != http.StatusOK {
		t.Fatalf("GetNoteAIJob: expected 200, got %d: %s", getRec.Code, getRec.Body.String())
	}
	var resp NoteAIJobResponse
	if err := json.NewDecoder(getRec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode empty job: %v", err)
	}
	if resp.Status != "failed" || resp.Result != nil || resp.FailureCode == nil || *resp.FailureCode != noteAIFailureEmptyOutput {
		t.Fatalf("empty completion response = %#v, want failed empty_structured_output", resp)
	}
}

func TestNoteAIJobCancelSuppressesTask(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	agentID := createHandlerTestAgent(t, "Note AI Cancel Agent "+uuid.NewString()[:8], nil)
	noteID := createNotePageForAITest(t, "AI cancel note "+uuid.NewString())
	job := createNoteAIJobForTest(t, noteID, agentID)

	cancelReq := withURLParam(newRequest(http.MethodPost, "/api/notes/ai-jobs/"+job.ID+"/cancel", nil), "jobId", job.ID)
	cancelRec := httptest.NewRecorder()
	testHandler.CancelNoteAIJob(cancelRec, cancelReq)
	if cancelRec.Code != http.StatusOK {
		t.Fatalf("CancelNoteAIJob: expected 200, got %d: %s", cancelRec.Code, cancelRec.Body.String())
	}
	var resp NoteAIJobResponse
	if err := json.NewDecoder(cancelRec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode cancelled job: %v", err)
	}
	if resp.Status != "cancelled" {
		t.Fatalf("cancelled job status = %q, want cancelled", resp.Status)
	}
	if got := taskStatus(t, job.TaskID); got != "suppressed" {
		t.Fatalf("task status = %q, want suppressed", got)
	}
}
