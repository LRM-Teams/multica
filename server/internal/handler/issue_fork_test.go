package handler

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// These tests exercise the request-validation surface of the fork handlers.
// Like the rest of the handler package they run against a real Postgres
// fixture and are skipped wholesale when no database is reachable (see
// TestMain). createTestIssue / withURLParam / newRequest are shared helpers.

func TestForkIssue_RequiresTaskID(t *testing.T) {
	id := createTestIssue(t, "fork-missing-task", "todo", "none")
	t.Cleanup(func() { deleteTestIssue(t, id) })

	req := withURLParam(newRequest(http.MethodPost, "/api/issues/"+id+"/fork?seq=5", nil), "id", id)
	w := httptest.NewRecorder()
	testHandler.ForkIssue(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 when task_id is missing, got %d: %s", w.Code, w.Body.String())
	}
}

func TestForkIssue_RequiresSeq(t *testing.T) {
	id := createTestIssue(t, "fork-missing-seq", "todo", "none")
	t.Cleanup(func() { deleteTestIssue(t, id) })

	taskID := "00000000-0000-0000-0000-0000000000aa"
	req := withURLParam(newRequest(http.MethodPost, "/api/issues/"+id+"/fork?task_id="+taskID, nil), "id", id)
	w := httptest.NewRecorder()
	testHandler.ForkIssue(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 when seq is missing, got %d: %s", w.Code, w.Body.String())
	}
}

func TestForkIssue_RejectsNegativeSeq(t *testing.T) {
	id := createTestIssue(t, "fork-bad-seq", "todo", "none")
	t.Cleanup(func() { deleteTestIssue(t, id) })

	taskID := "00000000-0000-0000-0000-0000000000aa"
	req := withURLParam(newRequest(http.MethodPost, "/api/issues/"+id+"/fork?task_id="+taskID+"&seq=-1", nil), "id", id)
	w := httptest.NewRecorder()
	testHandler.ForkIssue(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for negative seq, got %d: %s", w.Code, w.Body.String())
	}
}

func TestForkIssue_RejectsNonUUIDTaskID(t *testing.T) {
	id := createTestIssue(t, "fork-bad-task", "todo", "none")
	t.Cleanup(func() { deleteTestIssue(t, id) })

	req := withURLParam(newRequest(http.MethodPost, "/api/issues/"+id+"/fork?task_id=not-a-uuid&seq=1", nil), "id", id)
	w := httptest.NewRecorder()
	testHandler.ForkIssue(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for non-UUID task_id, got %d: %s", w.Code, w.Body.String())
	}
}

func TestForkIssue_UnknownIssueReturns404(t *testing.T) {
	missing := "00000000-0000-0000-0000-0000000000ff"
	req := withURLParam(newRequest(http.MethodPost, "/api/issues/"+missing+"/fork?task_id=00000000-0000-0000-0000-0000000000aa&seq=1", nil), "id", missing)
	w := httptest.NewRecorder()
	testHandler.ForkIssue(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for unknown issue, got %d: %s", w.Code, w.Body.String())
	}
}

// TestDeleteForkedIssue_RejectsOriginal verifies the guard: a normal
// (non-forked) issue cannot be deleted through the fork-delete endpoint.
func TestDeleteForkedIssue_RejectsOriginal(t *testing.T) {
	id := createTestIssue(t, "fork-delete-guard", "todo", "none")
	t.Cleanup(func() { deleteTestIssue(t, id) })

	req := withURLParam(newRequest(http.MethodDelete, "/api/issues/"+id+"/fork", nil), "id", id)
	w := httptest.NewRecorder()
	testHandler.DeleteForkedIssue(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 deleting a non-fork, got %d: %s", w.Code, w.Body.String())
	}
}
