package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// NotePageIssueRefResponse is one note→issue link (S1-R1).
// Inaccessible or cross-workspace issues are omitted from list results, not
// returned with a leaked label.
type NotePageIssueRefResponse struct {
	Type        string `json:"type"`
	PageID      string `json:"page_id"`
	IssueID     string `json:"issue_id"`
	WorkspaceID string `json:"workspace_id"`
	Identifier  string `json:"identifier"`
	Title       string `json:"title"`
	Number      int32  `json:"number"`
	CreatedAt   string `json:"created_at"`
}

type NotePageIssueRefListResponse struct {
	Refs []NotePageIssueRefResponse `json:"refs"`
}

type notePageIssueRefCreateRequest struct {
	IssueID string `json:"issue_id"`
}

func (h *Handler) ListNotePageIssueRefs(w http.ResponseWriter, r *http.Request) {
	workspaceID, userID, _, ok := h.notesWorkspaceAndUser(w, r)
	if !ok {
		return
	}
	page, _, ok := h.loadAccessibleNote(w, r, chi.URLParam(r, "id"), workspaceID, userID)
	if !ok {
		return
	}

	rows, err := h.DB.Query(r.Context(), `
SELECT
  r.page_id,
  r.issue_id,
  r.workspace_id,
  r.created_at,
  i.number,
  i.title,
  w.issue_prefix
FROM note_page_issue_ref r
JOIN issue i ON i.id = r.issue_id AND i.workspace_id = r.workspace_id
JOIN workspace w ON w.id = r.workspace_id
WHERE r.page_id = $1
  AND r.workspace_id = $2
ORDER BY r.created_at ASC, i.number ASC`, page.ID, page.WorkspaceID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list note issue refs")
		return
	}
	defer rows.Close()

	refs := make([]NotePageIssueRefResponse, 0)
	for rows.Next() {
		var (
			pageID, issueID, refWorkspaceID pgtype.UUID
			createdAt                       pgtype.Timestamptz
			number                          int32
			title, prefix                   string
		)
		if err := rows.Scan(&pageID, &issueID, &refWorkspaceID, &createdAt, &number, &title, &prefix); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to list note issue refs")
			return
		}
		refs = append(refs, NotePageIssueRefResponse{
			Type:        "issue",
			PageID:      uuidToString(pageID),
			IssueID:     uuidToString(issueID),
			WorkspaceID: uuidToString(refWorkspaceID),
			Identifier:  prefix + "-" + strconv.Itoa(int(number)),
			Title:       title,
			Number:      number,
			CreatedAt:   timestampToString(createdAt),
		})
	}
	if err := rows.Err(); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list note issue refs")
		return
	}
	writeJSON(w, http.StatusOK, NotePageIssueRefListResponse{Refs: refs})
}

func (h *Handler) CreateNotePageIssueRef(w http.ResponseWriter, r *http.Request) {
	workspaceID, userID, _, ok := h.notesWorkspaceAndUser(w, r)
	if !ok {
		return
	}
	page, _, ok := h.loadAccessibleNote(w, r, chi.URLParam(r, "id"), workspaceID, userID)
	if !ok {
		return
	}

	var req notePageIssueRefCreateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	issueIDStr := strings.TrimSpace(req.IssueID)
	if issueIDStr == "" {
		writeError(w, http.StatusBadRequest, "issue_id is required")
		return
	}
	issueUUID, ok := parseUUIDOrBadRequest(w, issueIDStr, "issue_id")
	if !ok {
		return
	}

	var (
		issueNumber int32
		issueTitle  string
		issuePrefix string
	)
	err := h.DB.QueryRow(r.Context(), `
SELECT i.number, i.title, w.issue_prefix
FROM issue i
JOIN workspace w ON w.id = i.workspace_id
WHERE i.id = $1 AND i.workspace_id = $2`, issueUUID, page.WorkspaceID).Scan(&issueNumber, &issueTitle, &issuePrefix)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// Do not leak whether the issue exists in another workspace.
			writeError(w, http.StatusNotFound, "issue not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to load issue")
		return
	}

	var createdAt pgtype.Timestamptz
	err = h.DB.QueryRow(r.Context(), `
INSERT INTO note_page_issue_ref (page_id, issue_id, workspace_id, created_by)
VALUES ($1, $2, $3, $4)
ON CONFLICT (page_id, issue_id) DO UPDATE
SET created_by = note_page_issue_ref.created_by
RETURNING created_at`, page.ID, issueUUID, page.WorkspaceID, userID).Scan(&createdAt)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create note issue ref")
		return
	}

	writeJSON(w, http.StatusCreated, NotePageIssueRefResponse{
		Type:        "issue",
		PageID:      uuidToString(page.ID),
		IssueID:     uuidToString(issueUUID),
		WorkspaceID: uuidToString(page.WorkspaceID),
		Identifier:  issuePrefix + "-" + strconv.Itoa(int(issueNumber)),
		Title:       issueTitle,
		Number:      issueNumber,
		CreatedAt:   timestampToString(createdAt),
	})
}

func (h *Handler) DeleteNotePageIssueRef(w http.ResponseWriter, r *http.Request) {
	workspaceID, userID, _, ok := h.notesWorkspaceAndUser(w, r)
	if !ok {
		return
	}
	page, _, ok := h.loadAccessibleNote(w, r, chi.URLParam(r, "id"), workspaceID, userID)
	if !ok {
		return
	}
	issueUUID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "issueId"), "issue id")
	if !ok {
		return
	}

	tag, err := h.DB.Exec(r.Context(), `
DELETE FROM note_page_issue_ref
WHERE page_id = $1 AND issue_id = $2 AND workspace_id = $3`, page.ID, issueUUID, page.WorkspaceID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to delete note issue ref")
		return
	}
	if tag.RowsAffected() == 0 {
		writeError(w, http.StatusNotFound, "note issue ref not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
