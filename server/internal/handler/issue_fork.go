package handler

import (
	"math"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/multica-ai/multica/server/internal/service"
)

// ForkIssueResponse is returned by POST /api/issues/{id}/fork.
type ForkIssueResponse struct {
	ForkedIssueID string `json:"forked_issue_id"`
	Number        int32  `json:"number"`
}

// ForkIssue handles POST /api/issues/{id}/fork.
//
// Query params:
//   - task_id (UUID, required): the source agent's task whose transcript holds
//     the branch point.
//   - seq (non-negative int, required): the task_message.seq to branch at.
//
// The new issue records its provenance and has its overwritable fields rolled
// back to their value at the branch point (see IssueForkService).
func (h *Handler) ForkIssue(w http.ResponseWriter, r *http.Request) {
	issue, ok := h.loadIssueForUser(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}

	taskIDStr := r.URL.Query().Get("task_id")
	if taskIDStr == "" {
		writeError(w, http.StatusBadRequest, "task_id is required")
		return
	}
	taskID, ok := parseUUIDOrBadRequest(w, taskIDStr, "task_id")
	if !ok {
		return
	}

	seqStr := r.URL.Query().Get("seq")
	if seqStr == "" {
		writeError(w, http.StatusBadRequest, "seq is required")
		return
	}
	seq, err := strconv.Atoi(seqStr)
	if err != nil || seq < 0 || seq > math.MaxInt32 {
		writeError(w, http.StatusBadRequest, "seq must be a non-negative 32-bit integer")
		return
	}

	svc := service.NewIssueForkService(h.Queries)
	forked, err := svc.ForkIssueSubtree(r.Context(), issue.ID, taskID, int32(seq))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "fork failed")
		return
	}

	writeJSON(w, http.StatusCreated, ForkIssueResponse{
		ForkedIssueID: uuidToString(forked.ID),
		Number:        forked.Number,
	})
}

// DeleteForkedIssue handles DELETE /api/issues/{id}/fork.
//
// It refuses to delete an original issue: only rows with a non-NULL
// forked_from_issue_id may be removed through this endpoint. The DELETE query
// carries the same guard, so this is defense in depth, not the only check.
func (h *Handler) DeleteForkedIssue(w http.ResponseWriter, r *http.Request) {
	issue, ok := h.loadIssueForUser(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	if !issue.ForkedFromIssueID.Valid {
		writeError(w, http.StatusBadRequest, "issue is not a fork")
		return
	}
	if err := h.Queries.DeleteForkedIssue(r.Context(), issue.ID); err != nil {
		writeError(w, http.StatusInternalServerError, "delete fork failed")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
