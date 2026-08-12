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

func TestValidateNoteWritebackEvidence(t *testing.T) {
	if _, err := validateNoteWritebackEvidence(nil); err == nil {
		t.Fatal("expected error for empty evidence")
	}
	if _, err := validateNoteWritebackEvidence([]noteWritebackEvidence{{Type: "issue", ID: ""}}); err == nil {
		t.Fatal("expected error for missing id")
	}
	out, err := validateNoteWritebackEvidence([]noteWritebackEvidence{{Type: " issue ", ID: " " + uuid.NewString() + " "}})
	if err != nil || len(out) != 1 || out[0].Type != "issue" || strings.Contains(out[0].ID, " ") {
		t.Fatalf("cleaned = %#v err=%v", out, err)
	}
}

func TestApplyNoteWritebackContent(t *testing.T) {
	got, err := applyNoteWritebackContent("Hello", "append", "World", nil)
	if err != nil || got != "Hello\n\nWorld" {
		t.Fatalf("append = %q err=%v", got, err)
	}
	target := "old"
	got, err = applyNoteWritebackContent("keep old text", "patch", "new", &target)
	if err != nil || got != "keep new text" {
		t.Fatalf("patch = %q err=%v", got, err)
	}
	got, err = applyNoteWritebackContent("old", "replace_page", "fresh", nil)
	if err != nil || got != "fresh" {
		t.Fatalf("replace = %q err=%v", got, err)
	}
}

func TestCreateNotePageWritebackRequiresEvidence(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	noteID := createNotePageForAITest(t, "Writeback evidence "+uuid.NewString())
	req := withURLParam(newRequest(http.MethodPost, "/api/notes/pages/"+noteID+"/writebacks", map[string]any{
		"action":   "append",
		"content":  "Should not land",
		"evidence": []any{},
	}), "id", noteID)
	rec := httptest.NewRecorder()
	testHandler.CreateNotePageWriteback(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestAcceptNotePageWritebackChangesContent(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	noteID := createNotePageForAITest(t, "Writeback accept "+uuid.NewString())
	if _, err := testPool.Exec(ctx, `UPDATE note_page SET content = $2 WHERE id = $1`, noteID, "Original body"); err != nil {
		t.Fatalf("set content: %v", err)
	}

	issueID := uuid.NewString()
	createRec := httptest.NewRecorder()
	testHandler.CreateNotePageWriteback(createRec, withURLParam(newRequest(http.MethodPost, "/api/notes/pages/"+noteID+"/writebacks", map[string]any{
		"action":  "append",
		"content": "Appended summary",
		"evidence": []any{
			map[string]any{"type": "issue", "id": issueID, "label": "MUL-1"},
		},
	}), "id", noteID))
	if createRec.Code != http.StatusCreated {
		t.Fatalf("create: expected 201, got %d: %s", createRec.Code, createRec.Body.String())
	}
	var created NoteWritebackResponse
	if err := json.NewDecoder(createRec.Body).Decode(&created); err != nil {
		t.Fatalf("decode create: %v", err)
	}
	if created.Status != "pending" || created.CreatedByType != "member" {
		t.Fatalf("created = %#v", created)
	}

	acceptRec := httptest.NewRecorder()
	testHandler.AcceptNotePageWriteback(acceptRec, withURLParam(newRequest(http.MethodPost, "/api/notes/writebacks/"+created.ID+"/accept", nil), "writebackId", created.ID))
	if acceptRec.Code != http.StatusOK {
		t.Fatalf("accept: expected 200, got %d: %s", acceptRec.Code, acceptRec.Body.String())
	}
	var accepted NoteWritebackResponse
	if err := json.NewDecoder(acceptRec.Body).Decode(&accepted); err != nil {
		t.Fatalf("decode accept: %v", err)
	}
	if accepted.Status != "applied" || accepted.ResolvedBy == nil {
		t.Fatalf("accepted = %#v", accepted)
	}

	var content, updatedBy string
	if err := testPool.QueryRow(ctx, `SELECT content, updated_by::text FROM note_page WHERE id = $1`, noteID).Scan(&content, &updatedBy); err != nil {
		t.Fatalf("load note: %v", err)
	}
	if content != "Original body\n\nAppended summary" {
		t.Fatalf("content = %q", content)
	}
	if updatedBy != testUserID {
		t.Fatalf("updated_by = %q, want %q", updatedBy, testUserID)
	}

	again := httptest.NewRecorder()
	testHandler.AcceptNotePageWriteback(again, withURLParam(newRequest(http.MethodPost, "/api/notes/writebacks/"+created.ID+"/accept", nil), "writebackId", created.ID))
	if again.Code != http.StatusConflict {
		t.Fatalf("second accept: expected 409, got %d: %s", again.Code, again.Body.String())
	}
}

func TestRejectNotePageWritebackLeavesContent(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	noteID := createNotePageForAITest(t, "Writeback reject "+uuid.NewString())
	original := "Keep me intact"
	if _, err := testPool.Exec(ctx, `UPDATE note_page SET content = $2 WHERE id = $1`, noteID, original); err != nil {
		t.Fatalf("set content: %v", err)
	}

	createRec := httptest.NewRecorder()
	testHandler.CreateNotePageWriteback(createRec, withURLParam(newRequest(http.MethodPost, "/api/notes/pages/"+noteID+"/writebacks", map[string]any{
		"action":  "replace_page",
		"content": "Should not apply",
		"evidence": []any{
			map[string]any{"type": "issue", "id": uuid.NewString()},
		},
	}), "id", noteID))
	if createRec.Code != http.StatusCreated {
		t.Fatalf("create: expected 201, got %d: %s", createRec.Code, createRec.Body.String())
	}
	var created NoteWritebackResponse
	if err := json.NewDecoder(createRec.Body).Decode(&created); err != nil {
		t.Fatalf("decode: %v", err)
	}

	rejectRec := httptest.NewRecorder()
	testHandler.RejectNotePageWriteback(rejectRec, withURLParam(newRequest(http.MethodPost, "/api/notes/writebacks/"+created.ID+"/reject", nil), "writebackId", created.ID))
	if rejectRec.Code != http.StatusOK {
		t.Fatalf("reject: expected 200, got %d: %s", rejectRec.Code, rejectRec.Body.String())
	}
	var rejected NoteWritebackResponse
	if err := json.NewDecoder(rejectRec.Body).Decode(&rejected); err != nil {
		t.Fatalf("decode reject: %v", err)
	}
	if rejected.Status != "rejected" {
		t.Fatalf("rejected = %#v", rejected)
	}

	var content string
	if err := testPool.QueryRow(ctx, `SELECT content FROM note_page WHERE id = $1`, noteID).Scan(&content); err != nil {
		t.Fatalf("load note: %v", err)
	}
	if content != original {
		t.Fatalf("content changed after reject: %q", content)
	}
}

func TestListNotePageWritebacksFiltersStatus(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	noteID := createNotePageForAITest(t, "Writeback list "+uuid.NewString())
	create := func() string {
		rec := httptest.NewRecorder()
		testHandler.CreateNotePageWriteback(rec, withURLParam(newRequest(http.MethodPost, "/api/notes/pages/"+noteID+"/writebacks", map[string]any{
			"action":  "append",
			"content": "chunk",
			"evidence": []any{
				map[string]any{"type": "issue", "id": uuid.NewString()},
			},
		}), "id", noteID))
		if rec.Code != http.StatusCreated {
			t.Fatalf("create: %d %s", rec.Code, rec.Body.String())
		}
		var created NoteWritebackResponse
		if err := json.NewDecoder(rec.Body).Decode(&created); err != nil {
			t.Fatalf("decode: %v", err)
		}
		return created.ID
	}
	first := create()
	_ = create()
	rejectRec := httptest.NewRecorder()
	testHandler.RejectNotePageWriteback(rejectRec, withURLParam(newRequest(http.MethodPost, "/api/notes/writebacks/"+first+"/reject", nil), "writebackId", first))
	if rejectRec.Code != http.StatusOK {
		t.Fatalf("reject: %d %s", rejectRec.Code, rejectRec.Body.String())
	}

	listRec := httptest.NewRecorder()
	req := withURLParam(newRequest(http.MethodGet, "/api/notes/pages/"+noteID+"/writebacks?status=pending", nil), "id", noteID)
	testHandler.ListNotePageWritebacks(listRec, req)
	if listRec.Code != http.StatusOK {
		t.Fatalf("list: %d %s", listRec.Code, listRec.Body.String())
	}
	var listed NoteWritebackListResponse
	if err := json.NewDecoder(listRec.Body).Decode(&listed); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(listed.Writebacks) != 1 || listed.Writebacks[0].Status != "pending" {
		t.Fatalf("listed = %#v", listed.Writebacks)
	}
}
