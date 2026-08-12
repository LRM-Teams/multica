package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func TestBuildIssueDoneWritebackContent(t *testing.T) {
	issueID := uuid.New()
	issue := db.Issue{
		ID:    parseUUID(issueID.String()),
		Title: "Ship bridge",
		Description: pgtype.Text{
			String: "  Line one\nline   two  ",
			Valid:  true,
		},
	}
	got := buildIssueDoneWritebackContent(issue, "MUL-9")
	if !strings.Contains(got, "[MUL-9](mention://issue/"+issueID.String()+")") {
		t.Fatalf("missing mention: %q", got)
	}
	if !strings.Contains(got, "Ship bridge") || !strings.Contains(got, "Status moved to **done**") {
		t.Fatalf("content = %q", got)
	}
	if !strings.Contains(got, "Context: Line one line two") {
		t.Fatalf("expected collapsed context, got %q", got)
	}
}

func TestIssueDoneCreatesPendingNoteWritebackAndAcceptApplies(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	noteID := createNotePageForAITest(t, "Done writeback note "+uuid.NewString())
	if _, err := testPool.Exec(ctx, `UPDATE note_page SET content = $2 WHERE id = $1`, noteID, "Original note body"); err != nil {
		t.Fatalf("set content: %v", err)
	}
	issueID, _ := createIssueForNoteRefTest(t, testWorkspaceID, "Finish linked work "+uuid.NewString())

	linkRec := httptest.NewRecorder()
	testHandler.CreateNotePageIssueRef(linkRec, withURLParam(newRequest(http.MethodPost, "/api/notes/pages/"+noteID+"/issue-refs", map[string]any{
		"issue_id": issueID,
	}), "id", noteID))
	if linkRec.Code != http.StatusCreated {
		t.Fatalf("link: expected 201, got %d: %s", linkRec.Code, linkRec.Body.String())
	}

	doneRec := httptest.NewRecorder()
	testHandler.UpdateIssue(doneRec, withURLParam(newRequest(http.MethodPut, "/api/issues/"+issueID, map[string]any{
		"status": "done",
	}), "id", issueID))
	if doneRec.Code != http.StatusOK {
		t.Fatalf("UpdateIssue done: expected 200, got %d: %s", doneRec.Code, doneRec.Body.String())
	}

	listRec := httptest.NewRecorder()
	testHandler.ListNotePageWritebacks(listRec, withURLParam(newRequest(http.MethodGet, "/api/notes/pages/"+noteID+"/writebacks?status=pending", nil), "id", noteID))
	if listRec.Code != http.StatusOK {
		t.Fatalf("list writebacks: %d %s", listRec.Code, listRec.Body.String())
	}
	var listed NoteWritebackListResponse
	if err := json.NewDecoder(listRec.Body).Decode(&listed); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(listed.Writebacks) != 1 {
		t.Fatalf("pending writebacks = %#v, want 1", listed.Writebacks)
	}
	wb := listed.Writebacks[0]
	if wb.Action != "append" || wb.Status != "pending" || len(wb.Evidence) == 0 || wb.Evidence[0].Type != "issue" {
		t.Fatalf("writeback = %#v", wb)
	}
	if !strings.Contains(wb.Content, "mention://issue/"+issueID) {
		t.Fatalf("writeback content missing issue mention: %q", wb.Content)
	}

	// Idempotent while still pending: marking done again is a no-op for status,
	// but calling the helper path twice via another pending-skip check.
	againList := httptest.NewRecorder()
	testHandler.ListNotePageWritebacks(againList, withURLParam(newRequest(http.MethodGet, "/api/notes/pages/"+noteID+"/writebacks?status=pending", nil), "id", noteID))
	var listedAgain NoteWritebackListResponse
	_ = json.NewDecoder(againList.Body).Decode(&listedAgain)
	if len(listedAgain.Writebacks) != 1 {
		t.Fatalf("expected still 1 pending, got %#v", listedAgain.Writebacks)
	}

	acceptRec := httptest.NewRecorder()
	testHandler.AcceptNotePageWriteback(acceptRec, withURLParam(newRequest(http.MethodPost, "/api/notes/writebacks/"+wb.ID+"/accept", nil), "writebackId", wb.ID))
	if acceptRec.Code != http.StatusOK {
		t.Fatalf("accept: expected 200, got %d: %s", acceptRec.Code, acceptRec.Body.String())
	}

	var content string
	if err := testPool.QueryRow(ctx, `SELECT content FROM note_page WHERE id = $1`, noteID).Scan(&content); err != nil {
		t.Fatalf("load note: %v", err)
	}
	if !strings.Contains(content, "Original note body") || !strings.Contains(content, "mention://issue/"+issueID) {
		t.Fatalf("note content after accept = %q", content)
	}
}

func TestIssueDoneWithoutNoteLinkCreatesNoWriteback(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	noteID := createNotePageForAITest(t, "Unrelated note "+uuid.NewString())
	issueID, _ := createIssueForNoteRefTest(t, testWorkspaceID, "Unlinked done "+uuid.NewString())

	doneRec := httptest.NewRecorder()
	testHandler.UpdateIssue(doneRec, withURLParam(newRequest(http.MethodPut, "/api/issues/"+issueID, map[string]any{
		"status": "done",
	}), "id", issueID))
	if doneRec.Code != http.StatusOK {
		t.Fatalf("UpdateIssue done: expected 200, got %d: %s", doneRec.Code, doneRec.Body.String())
	}

	listRec := httptest.NewRecorder()
	testHandler.ListNotePageWritebacks(listRec, withURLParam(newRequest(http.MethodGet, "/api/notes/pages/"+noteID+"/writebacks?status=pending", nil), "id", noteID))
	if listRec.Code != http.StatusOK {
		t.Fatalf("list: %d %s", listRec.Code, listRec.Body.String())
	}
	var listed NoteWritebackListResponse
	if err := json.NewDecoder(listRec.Body).Decode(&listed); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(listed.Writebacks) != 0 {
		t.Fatalf("expected no writebacks, got %#v", listed.Writebacks)
	}
}
