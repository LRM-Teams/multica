package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
)

func createInboxEventForNoteRunRefTest(t *testing.T, agentID string) string {
	t.Helper()
	var runID string
	if err := testPool.QueryRow(context.Background(), `
INSERT INTO agent_inbox_event (
  workspace_id, agent_id, runtime_id, status, priority, reason, requires_wake, completed_at
)
VALUES ($1, $2, $3, 'completed', 0, 'mention', false, now())
RETURNING id`, testWorkspaceID, agentID, testRuntimeID).Scan(&runID); err != nil {
		t.Fatalf("create inbox event: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM agent_inbox_event WHERE id = $1`, runID)
	})
	return runID
}

func TestNotePageAgentRefCreateListDelete(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	noteID := createNotePageForAITest(t, "Agent ref note "+uuid.NewString())
	agentID := createHandlerTestAgent(t, "Note Agent Ref "+uuid.NewString()[:8], nil)

	createRec := httptest.NewRecorder()
	testHandler.CreateNotePageAgentRef(createRec, withURLParam(newRequest(http.MethodPost, "/api/notes/pages/"+noteID+"/agent-refs", map[string]any{
		"agent_id": agentID,
	}), "id", noteID))
	if createRec.Code != http.StatusCreated {
		t.Fatalf("CreateNotePageAgentRef: expected 201, got %d: %s", createRec.Code, createRec.Body.String())
	}
	var created NotePageIssueRefResponse
	if err := json.NewDecoder(createRec.Body).Decode(&created); err != nil {
		t.Fatalf("decode create: %v", err)
	}
	if created.Type != "agent" || created.ID != agentID || !created.Accessible || created.Label == nil || *created.Label == "" {
		t.Fatalf("created agent ref = %#v", created)
	}

	listRec := httptest.NewRecorder()
	testHandler.ListNotePageAgentRefs(listRec, withURLParam(newRequest(http.MethodGet, "/api/notes/pages/"+noteID+"/agent-refs", nil), "id", noteID))
	if listRec.Code != http.StatusOK {
		t.Fatalf("ListNotePageAgentRefs: expected 200, got %d: %s", listRec.Code, listRec.Body.String())
	}
	var listed NotePageIssueRefListResponse
	if err := json.NewDecoder(listRec.Body).Decode(&listed); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(listed.Refs) != 1 || listed.Refs[0].ID != agentID || !listed.Refs[0].Accessible {
		t.Fatalf("listed = %#v", listed.Refs)
	}

	deleteRec := httptest.NewRecorder()
	testHandler.DeleteNotePageAgentRef(deleteRec, withRouteParams(
		newRequest(http.MethodDelete, "/api/notes/pages/"+noteID+"/agent-refs/"+agentID, nil),
		"id", noteID, "agentId", agentID,
	))
	if deleteRec.Code != http.StatusNoContent {
		t.Fatalf("DeleteNotePageAgentRef: expected 204, got %d: %s", deleteRec.Code, deleteRec.Body.String())
	}
}

func TestNotePageAgentRefListMarksInaccessibleWithoutLeaking(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	noteID := createNotePageForAITest(t, "Agent inaccessible "+uuid.NewString())
	agentID := createHandlerTestAgent(t, "Visible Agent "+uuid.NewString()[:8], nil)

	createRec := httptest.NewRecorder()
	testHandler.CreateNotePageAgentRef(createRec, withURLParam(newRequest(http.MethodPost, "/api/notes/pages/"+noteID+"/agent-refs", map[string]any{
		"agent_id": agentID,
	}), "id", noteID))
	if createRec.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", createRec.Code, createRec.Body.String())
	}

	// Soft-delete / archive the agent so LEFT JOIN marks inaccessible.
	if _, err := testPool.Exec(context.Background(), `
UPDATE agent SET archived_at = now() WHERE id = $1`, agentID); err != nil {
		t.Fatalf("archive agent: %v", err)
	}

	listRec := httptest.NewRecorder()
	testHandler.ListNotePageAgentRefs(listRec, withURLParam(newRequest(http.MethodGet, "/api/notes/pages/"+noteID+"/agent-refs", nil), "id", noteID))
	if listRec.Code != http.StatusOK {
		t.Fatalf("list: %d %s", listRec.Code, listRec.Body.String())
	}
	var listed NotePageIssueRefListResponse
	if err := json.NewDecoder(listRec.Body).Decode(&listed); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(listed.Refs) != 1 || listed.Refs[0].ID != agentID || listed.Refs[0].Accessible || listed.Refs[0].Label != nil || listed.Refs[0].Title != "" {
		t.Fatalf("expected inaccessible agent ref without leak, got %#v", listed.Refs)
	}
}

func TestNotePageRunRefCreateListDelete(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	noteID := createNotePageForAITest(t, "Run ref note "+uuid.NewString())
	agentID := createHandlerTestAgent(t, "Note Run Ref Agent "+uuid.NewString()[:8], nil)
	runID := createInboxEventForNoteRunRefTest(t, agentID)

	createRec := httptest.NewRecorder()
	testHandler.CreateNotePageRunRef(createRec, withURLParam(newRequest(http.MethodPost, "/api/notes/pages/"+noteID+"/run-refs", map[string]any{
		"run_id": runID,
	}), "id", noteID))
	if createRec.Code != http.StatusCreated {
		t.Fatalf("CreateNotePageRunRef: expected 201, got %d: %s", createRec.Code, createRec.Body.String())
	}
	var created NotePageIssueRefResponse
	if err := json.NewDecoder(createRec.Body).Decode(&created); err != nil {
		t.Fatalf("decode create: %v", err)
	}
	if created.Type != "run" || created.ID != runID || created.AgentID != agentID || !created.Accessible || created.Label == nil {
		t.Fatalf("created run ref = %#v", created)
	}

	listRec := httptest.NewRecorder()
	testHandler.ListNotePageRunRefs(listRec, withURLParam(newRequest(http.MethodGet, "/api/notes/pages/"+noteID+"/run-refs", nil), "id", noteID))
	if listRec.Code != http.StatusOK {
		t.Fatalf("list: %d %s", listRec.Code, listRec.Body.String())
	}
	var listed NotePageIssueRefListResponse
	if err := json.NewDecoder(listRec.Body).Decode(&listed); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(listed.Refs) != 1 || listed.Refs[0].ID != runID || listed.Refs[0].AgentID != agentID {
		t.Fatalf("listed = %#v", listed.Refs)
	}

	deleteRec := httptest.NewRecorder()
	testHandler.DeleteNotePageRunRef(deleteRec, withRouteParams(
		newRequest(http.MethodDelete, "/api/notes/pages/"+noteID+"/run-refs/"+runID, nil),
		"id", noteID, "runId", runID,
	))
	if deleteRec.Code != http.StatusNoContent {
		t.Fatalf("delete: %d %s", deleteRec.Code, deleteRec.Body.String())
	}
}

func TestGetNotePageIncludesAgentAndRunRefs(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	noteID := createNotePageForAITest(t, "Mixed refs "+uuid.NewString())
	agentID := createHandlerTestAgent(t, "Mixed Ref Agent "+uuid.NewString()[:8], nil)
	runID := createInboxEventForNoteRunRefTest(t, agentID)
	issueID, _ := createIssueForNoteRefTest(t, testWorkspaceID, "Mixed issue "+uuid.NewString())

	for _, call := range []struct {
		name string
		fn   func(http.ResponseWriter, *http.Request)
		body map[string]any
	}{
		{"issue", testHandler.CreateNotePageIssueRef, map[string]any{"issue_id": issueID}},
		{"agent", testHandler.CreateNotePageAgentRef, map[string]any{"agent_id": agentID}},
		{"run", testHandler.CreateNotePageRunRef, map[string]any{"run_id": runID}},
	} {
		rec := httptest.NewRecorder()
		call.fn(rec, withURLParam(newRequest(http.MethodPost, "/api/notes/pages/"+noteID+"/"+call.name+"-refs", call.body), "id", noteID))
		if rec.Code != http.StatusCreated {
			t.Fatalf("%s create: %d %s", call.name, rec.Code, rec.Body.String())
		}
	}

	getRec := httptest.NewRecorder()
	testHandler.GetNotePage(getRec, withURLParam(newRequest(http.MethodGet, "/api/notes/pages/"+noteID, nil), "id", noteID))
	if getRec.Code != http.StatusOK {
		t.Fatalf("GetNotePage: %d %s", getRec.Code, getRec.Body.String())
	}
	var page NotePageResponse
	if err := json.NewDecoder(getRec.Body).Decode(&page); err != nil {
		t.Fatalf("decode page: %v", err)
	}
	types := map[string]int{}
	for _, ref := range page.Refs {
		types[ref.Type]++
		if !ref.Accessible {
			t.Fatalf("expected accessible ref, got %#v", ref)
		}
	}
	if types["issue"] != 1 || types["agent"] != 1 || types["run"] != 1 {
		t.Fatalf("page.refs types = %#v, want one of each", types)
	}
}
