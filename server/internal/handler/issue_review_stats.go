package handler

import "net/http"

// IssueReviewStatsResponse powers the overview "pending human approval" KPI:
// how many issues await human review, and how long the oldest has waited.
type IssueReviewStatsResponse struct {
	Count              int   `json:"count"`
	LongestWaitSeconds int64 `json:"longest_wait_seconds"`
}

// GetIssueReviewStats counts issues currently in `in_review` and computes the
// longest wait — now() minus the oldest "entered in_review" time. The entry
// time comes from the most recent status_changed→in_review activity_log row
// per issue, falling back to issue.updated_at when no such row exists (issues
// created directly in review, or pre-activity-log data).
func (h *Handler) GetIssueReviewStats(w http.ResponseWriter, r *http.Request) {
	workspaceID := h.resolveWorkspaceID(r)
	if _, ok := h.workspaceMember(w, r, workspaceID); !ok {
		return
	}

	var count int
	var longestWaitSeconds int64
	err := h.DB.QueryRow(r.Context(), `
		SELECT COUNT(*),
		       COALESCE(EXTRACT(EPOCH FROM (now() - MIN(COALESCE(al.entered, i.updated_at))))::bigint, 0)
		FROM issue i
		LEFT JOIN (
			SELECT issue_id, MAX(created_at) AS entered
			FROM activity_log
			WHERE action = 'status_changed' AND details->>'to' = 'in_review'
			GROUP BY issue_id
		) al ON al.issue_id = i.id
		WHERE i.workspace_id = $1 AND i.status = 'in_review'`,
		parseUUID(workspaceID),
	).Scan(&count, &longestWaitSeconds)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to compute review stats")
		return
	}
	if longestWaitSeconds < 0 {
		longestWaitSeconds = 0
	}
	writeJSON(w, http.StatusOK, IssueReviewStatsResponse{Count: count, LongestWaitSeconds: longestWaitSeconds})
}
