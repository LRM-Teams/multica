package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// NotePageIssueRefResponse is a structured note→target link (S1-R1 / S1-R3 / S2-R1 / N2-A1).
//
// Contract for agents/clients:
//   - Always: type, id, accessible
//   - type is "issue" | "agent" | "run" | "channel"
//   - When accessible=true: label (+ detail fields); run also includes agent_id;
//     channel puts kind in identifier ("worker" | "coordination")
//   - When accessible=false: no label/title/identifier — only type+id
type NotePageIssueRefResponse struct {
	Type        string  `json:"type"`
	ID          string  `json:"id"`
	Label       *string `json:"label,omitempty"`
	Accessible  bool    `json:"accessible"`
	PageID      string  `json:"page_id,omitempty"`
	IssueID     string  `json:"issue_id,omitempty"`
	AgentID     string  `json:"agent_id,omitempty"`
	WorkspaceID string  `json:"workspace_id,omitempty"`
	Identifier  string  `json:"identifier,omitempty"`
	Title       string  `json:"title,omitempty"`
	Number      *int32  `json:"number,omitempty"`
	CreatedAt   string  `json:"created_at,omitempty"`
}

type NotePageIssueRefListResponse struct {
	Refs []NotePageIssueRefResponse `json:"refs"`
}

type notePageIssueRefCreateRequest struct {
	IssueID string `json:"issue_id"`
}

func accessibleIssueRef(
	pageID, issueID, workspaceID, createdAt, prefix, title string,
	number int32,
) NotePageIssueRefResponse {
	identifier := prefix + "-" + strconv.Itoa(int(number))
	label := identifier
	num := number
	return NotePageIssueRefResponse{
		Type:        "issue",
		ID:          issueID,
		Label:       &label,
		Accessible:  true,
		PageID:      pageID,
		IssueID:     issueID,
		WorkspaceID: workspaceID,
		Identifier:  identifier,
		Title:       title,
		Number:      &num,
		CreatedAt:   createdAt,
	}
}

func inaccessibleIssueRef(issueID string) NotePageIssueRefResponse {
	return NotePageIssueRefResponse{
		Type:       "issue",
		ID:         issueID,
		Accessible: false,
	}
}

// loadNotePageRefs returns issue + agent + run + channel associations for a note page
// (S1-R1 / S2-R1 / N2-A1). Order: issues, agents, runs, channels (each by created_at).
func (h *Handler) loadNotePageRefs(
	ctx context.Context,
	pageID, pageWorkspaceID, userID pgtype.UUID,
) ([]NotePageIssueRefResponse, error) {
	issues, err := h.loadNotePageIssueRefs(ctx, pageID, pageWorkspaceID)
	if err != nil {
		return nil, err
	}
	agents, err := h.loadNotePageAgentRefs(ctx, pageID, pageWorkspaceID)
	if err != nil {
		return nil, err
	}
	runs, err := h.loadNotePageRunRefs(ctx, pageID, pageWorkspaceID)
	if err != nil {
		return nil, err
	}
	channels, err := h.loadNotePageChannelRefs(ctx, pageID, pageWorkspaceID, userID)
	if err != nil {
		return nil, err
	}
	out := make([]NotePageIssueRefResponse, 0, len(issues)+len(agents)+len(runs)+len(channels))
	out = append(out, issues...)
	out = append(out, agents...)
	out = append(out, runs...)
	out = append(out, channels...)
	return out, nil
}

// loadNotePageIssueRefs returns association rows for a note page.
// Inaccessible / cross-workspace targets are included with accessible=false
// and without label/title (S1-R3).
func (h *Handler) loadNotePageIssueRefs(ctx context.Context, pageID, pageWorkspaceID pgtype.UUID) ([]NotePageIssueRefResponse, error) {
	rows, err := h.DB.Query(ctx, `
SELECT
  r.page_id,
  r.issue_id,
  r.workspace_id,
  r.created_at,
  i.id IS NOT NULL AND i.workspace_id = r.workspace_id AS accessible,
  i.number,
  i.title,
  w.issue_prefix
FROM note_page_issue_ref r
LEFT JOIN issue i ON i.id = r.issue_id AND i.workspace_id = $2
LEFT JOIN workspace w ON w.id = i.workspace_id
WHERE r.page_id = $1
ORDER BY r.created_at ASC, r.issue_id ASC`, pageID, pageWorkspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	refs := make([]NotePageIssueRefResponse, 0)
	for rows.Next() {
		var (
			pageUUID, issueUUID, refWorkspaceID pgtype.UUID
			createdAt                           pgtype.Timestamptz
			accessible                          bool
			number                              pgtype.Int4
			title, prefix                       pgtype.Text
		)
		if err := rows.Scan(
			&pageUUID, &issueUUID, &refWorkspaceID, &createdAt,
			&accessible, &number, &title, &prefix,
		); err != nil {
			return nil, err
		}
		issueID := uuidToString(issueUUID)
		if !accessible || !number.Valid || !prefix.Valid {
			refs = append(refs, inaccessibleIssueRef(issueID))
			continue
		}
		refs = append(refs, accessibleIssueRef(
			uuidToString(pageUUID),
			issueID,
			uuidToString(refWorkspaceID),
			timestampToString(createdAt),
			prefix.String,
			title.String,
			number.Int32,
		))
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return refs, nil
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

	refs, err := h.loadNotePageIssueRefs(r.Context(), page.ID, page.WorkspaceID)
	if err != nil {
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

	writeJSON(w, http.StatusCreated, accessibleIssueRef(
		uuidToString(page.ID),
		uuidToString(issueUUID),
		uuidToString(page.WorkspaceID),
		timestampToString(createdAt),
		issuePrefix,
		issueTitle,
		issueNumber,
	))
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
