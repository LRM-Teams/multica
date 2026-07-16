package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
)

// acceptance_criteria must be writable via the API (it was previously a dead
// column — the CLI/handler could not set it) and returned so self-verification
// and review can anchor on a structured definition of done.
func TestIssueAcceptanceCriteria_CreateGetUpdate(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	crit := []string{
		"bomb play shows screen-shake + sound + doubling indicator",
		"game-over shows a working 再来一局 entry",
	}

	// Create with acceptance_criteria.
	w := httptest.NewRecorder()
	testHandler.CreateIssue(w, newRequest("POST", "/api/issues?workspace_id="+testWorkspaceID, map[string]any{
		"title":               "ac-" + uuid.NewString(),
		"status":              "todo",
		"acceptance_criteria": crit,
	}))
	if w.Code != http.StatusCreated {
		t.Fatalf("CreateIssue: expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var created IssueResponse
	if err := json.NewDecoder(w.Body).Decode(&created); err != nil {
		t.Fatalf("decode create: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(context.Background(), `DELETE FROM issue WHERE id = $1`, created.ID) })
	if len(created.AcceptanceCriteria) != 2 || created.AcceptanceCriteria[0] != crit[0] {
		t.Fatalf("create response acceptance_criteria = %#v, want %#v", created.AcceptanceCriteria, crit)
	}

	// GET returns the persisted criteria.
	w = httptest.NewRecorder()
	testHandler.GetIssue(w, withURLParam(newRequest("GET", "/api/issues/"+created.ID, nil), "id", created.ID))
	if w.Code != http.StatusOK {
		t.Fatalf("GetIssue: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var got IssueResponse
	json.NewDecoder(w.Body).Decode(&got)
	if len(got.AcceptanceCriteria) != 2 {
		t.Fatalf("get acceptance_criteria = %#v, want 2 items", got.AcceptanceCriteria)
	}

	// Update replaces the criteria.
	w = httptest.NewRecorder()
	req := newRequest("PUT", "/api/issues/"+created.ID, map[string]any{"acceptance_criteria": []string{"only one now"}})
	testHandler.UpdateIssue(w, withURLParam(req, "id", created.ID))
	if w.Code != http.StatusOK {
		t.Fatalf("UpdateIssue replace: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var updated IssueResponse
	json.NewDecoder(w.Body).Decode(&updated)
	if len(updated.AcceptanceCriteria) != 1 || updated.AcceptanceCriteria[0] != "only one now" {
		t.Fatalf("update replace acceptance_criteria = %#v", updated.AcceptanceCriteria)
	}

	// Update with empty array clears it.
	w = httptest.NewRecorder()
	req = newRequest("PUT", "/api/issues/"+created.ID, map[string]any{"acceptance_criteria": []string{}})
	testHandler.UpdateIssue(w, withURLParam(req, "id", created.ID))
	if w.Code != http.StatusOK {
		t.Fatalf("UpdateIssue clear: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var cleared IssueResponse
	json.NewDecoder(w.Body).Decode(&cleared)
	if len(cleared.AcceptanceCriteria) != 0 {
		t.Fatalf("update clear acceptance_criteria = %#v, want empty", cleared.AcceptanceCriteria)
	}

	// Omitting the field leaves the (now-empty) value unchanged and never null.
	w = httptest.NewRecorder()
	req = newRequest("PUT", "/api/issues/"+created.ID, map[string]any{"priority": "high"})
	testHandler.UpdateIssue(w, withURLParam(req, "id", created.ID))
	var untouched IssueResponse
	json.NewDecoder(w.Body).Decode(&untouched)
	if untouched.AcceptanceCriteria == nil {
		t.Fatalf("omitted update should still emit acceptance_criteria as [], got nil")
	}
}
