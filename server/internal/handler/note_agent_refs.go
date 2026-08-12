package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

func accessibleAgentRef(pageID, agentID, workspaceID, createdAt, name string) NotePageIssueRefResponse {
	label := name
	if strings.TrimSpace(label) == "" {
		label = agentID
	}
	return NotePageIssueRefResponse{
		Type:        "agent",
		ID:          agentID,
		Label:       &label,
		Accessible:  true,
		PageID:      pageID,
		WorkspaceID: workspaceID,
		Title:       name,
		CreatedAt:   createdAt,
	}
}

func inaccessibleAgentRef(agentID string) NotePageIssueRefResponse {
	return NotePageIssueRefResponse{
		Type:       "agent",
		ID:         agentID,
		Accessible: false,
	}
}

func (h *Handler) loadNotePageAgentRefs(ctx context.Context, pageID, pageWorkspaceID pgtype.UUID) ([]NotePageIssueRefResponse, error) {
	rows, err := h.DB.Query(ctx, `
SELECT
  r.page_id,
  r.agent_id,
  r.workspace_id,
  r.created_at,
  a.id IS NOT NULL AND a.workspace_id = r.workspace_id AND a.archived_at IS NULL AS accessible,
  a.name
FROM note_page_agent_ref r
LEFT JOIN agent a ON a.id = r.agent_id AND a.workspace_id = $2
WHERE r.page_id = $1
ORDER BY r.created_at ASC, r.agent_id ASC`, pageID, pageWorkspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	refs := make([]NotePageIssueRefResponse, 0)
	for rows.Next() {
		var (
			pageUUID, agentUUID, refWorkspaceID pgtype.UUID
			createdAt                           pgtype.Timestamptz
			accessible                          bool
			name                                pgtype.Text
		)
		if err := rows.Scan(&pageUUID, &agentUUID, &refWorkspaceID, &createdAt, &accessible, &name); err != nil {
			return nil, err
		}
		agentID := uuidToString(agentUUID)
		if !accessible {
			refs = append(refs, inaccessibleAgentRef(agentID))
			continue
		}
		refs = append(refs, accessibleAgentRef(
			uuidToString(pageUUID),
			agentID,
			uuidToString(refWorkspaceID),
			timestampToString(createdAt),
			name.String,
		))
	}
	return refs, rows.Err()
}

type notePageAgentRefCreateRequest struct {
	AgentID string `json:"agent_id"`
}

func (h *Handler) ListNotePageAgentRefs(w http.ResponseWriter, r *http.Request) {
	workspaceID, userID, _, ok := h.notesWorkspaceAndUser(w, r)
	if !ok {
		return
	}
	page, _, ok := h.loadAccessibleNote(w, r, chi.URLParam(r, "id"), workspaceID, userID)
	if !ok {
		return
	}
	refs, err := h.loadNotePageAgentRefs(r.Context(), page.ID, page.WorkspaceID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list note agent refs")
		return
	}
	writeJSON(w, http.StatusOK, NotePageIssueRefListResponse{Refs: refs})
}

func (h *Handler) CreateNotePageAgentRef(w http.ResponseWriter, r *http.Request) {
	workspaceID, userID, _, ok := h.notesWorkspaceAndUser(w, r)
	if !ok {
		return
	}
	page, _, ok := h.loadAccessibleNote(w, r, chi.URLParam(r, "id"), workspaceID, userID)
	if !ok {
		return
	}

	var req notePageAgentRefCreateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	agentIDStr := strings.TrimSpace(req.AgentID)
	if agentIDStr == "" {
		writeError(w, http.StatusBadRequest, "agent_id is required")
		return
	}
	agentUUID, ok := parseUUIDOrBadRequest(w, agentIDStr, "agent_id")
	if !ok {
		return
	}

	var agentName string
	err := h.DB.QueryRow(r.Context(), `
SELECT name
FROM agent
WHERE id = $1 AND workspace_id = $2 AND archived_at IS NULL`, agentUUID, page.WorkspaceID).Scan(&agentName)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "agent not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to load agent")
		return
	}

	var createdAt pgtype.Timestamptz
	err = h.DB.QueryRow(r.Context(), `
INSERT INTO note_page_agent_ref (page_id, agent_id, workspace_id, created_by)
VALUES ($1, $2, $3, $4)
ON CONFLICT (page_id, agent_id) DO UPDATE
SET created_by = note_page_agent_ref.created_by
RETURNING created_at`, page.ID, agentUUID, page.WorkspaceID, userID).Scan(&createdAt)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create note agent ref")
		return
	}

	writeJSON(w, http.StatusCreated, accessibleAgentRef(
		uuidToString(page.ID),
		uuidToString(agentUUID),
		uuidToString(page.WorkspaceID),
		timestampToString(createdAt),
		agentName,
	))
}

func (h *Handler) DeleteNotePageAgentRef(w http.ResponseWriter, r *http.Request) {
	workspaceID, userID, _, ok := h.notesWorkspaceAndUser(w, r)
	if !ok {
		return
	}
	page, _, ok := h.loadAccessibleNote(w, r, chi.URLParam(r, "id"), workspaceID, userID)
	if !ok {
		return
	}
	agentUUID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "agentId"), "agent id")
	if !ok {
		return
	}

	tag, err := h.DB.Exec(r.Context(), `
DELETE FROM note_page_agent_ref
WHERE page_id = $1 AND agent_id = $2 AND workspace_id = $3`, page.ID, agentUUID, page.WorkspaceID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to delete note agent ref")
		return
	}
	if tag.RowsAffected() == 0 {
		writeError(w, http.StatusNotFound, "note agent ref not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
