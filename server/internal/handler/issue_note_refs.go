package handler

import (
	"context"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// IssueNoteRefResponse is a note linked to an issue via note_page_issue_ref
// (S3-R5b). Only notes the caller can access are returned — inaccessible notes
// are omitted entirely (no id stub) so private note UUIDs are not leaked to
// issue observers.
type IssueNoteRefResponse struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	CreatedAt string `json:"created_at"`
}

type IssueNoteRefListResponse struct {
	Notes []IssueNoteRefResponse `json:"notes"`
}

// ListIssueNoteRefs returns notes linked to an issue that the current user can
// open under note ACL (S3-R5b / D4 reverse discovery).
func (h *Handler) ListIssueNoteRefs(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	issue, ok := h.loadIssueForUser(w, r, id)
	if !ok {
		return
	}
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	userUUID, ok := parseUUIDOrBadRequest(w, userID, "user id")
	if !ok {
		return
	}

	notes, err := h.loadAccessibleNotesForIssue(r.Context(), issue.ID, issue.WorkspaceID, userUUID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list note refs")
		return
	}
	writeJSON(w, http.StatusOK, IssueNoteRefListResponse{Notes: notes})
}

func (h *Handler) loadAccessibleNotesForIssue(
	ctx context.Context,
	issueID, workspaceID, userID pgtype.UUID,
) ([]IssueNoteRefResponse, error) {
	rows, err := h.DB.Query(ctx, `
SELECT r.page_id, p.title, r.created_at
FROM note_page_issue_ref r
JOIN note_page p ON p.id = r.page_id
WHERE r.issue_id = $1
  AND r.workspace_id = $2
  AND p.workspace_id = $2
  AND p.deleted_at IS NULL
ORDER BY r.created_at ASC, r.page_id ASC`, issueID, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	type candidate struct {
		pageID    pgtype.UUID
		title     string
		createdAt pgtype.Timestamptz
	}
	// Buffer all rows before noteAccess — that helper acquires its own pool
	// connection; calling it while the Query cursor is open trips cursordeadlock.
	candidates := make([]candidate, 0)
	for rows.Next() {
		var c candidate
		if err := rows.Scan(&c.pageID, &c.title, &c.createdAt); err != nil {
			return nil, err
		}
		candidates = append(candidates, c)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	out := make([]IssueNoteRefResponse, 0, len(candidates))
	for _, c := range candidates {
		accessible, _, err := h.noteAccess(ctx, c.pageID, workspaceID, userID)
		if err != nil {
			return nil, err
		}
		if !accessible {
			continue
		}
		out = append(out, IssueNoteRefResponse{
			ID:        uuidToString(c.pageID),
			Title:     c.title,
			CreatedAt: timestampToString(c.createdAt),
		})
	}
	return out, nil
}
