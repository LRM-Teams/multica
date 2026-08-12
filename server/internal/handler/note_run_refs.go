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

func accessibleRunRef(pageID, runID, agentID, workspaceID, createdAt, label string) NotePageIssueRefResponse {
	lbl := label
	if strings.TrimSpace(lbl) == "" {
		lbl = "run"
	}
	return NotePageIssueRefResponse{
		Type:        "run",
		ID:          runID,
		Label:       &lbl,
		Accessible:  true,
		PageID:      pageID,
		AgentID:     agentID,
		WorkspaceID: workspaceID,
		CreatedAt:   createdAt,
	}
}

func inaccessibleRunRef(runID string) NotePageIssueRefResponse {
	return NotePageIssueRefResponse{
		Type:       "run",
		ID:         runID,
		Accessible: false,
	}
}

func (h *Handler) loadNotePageRunRefs(ctx context.Context, pageID, pageWorkspaceID pgtype.UUID) ([]NotePageIssueRefResponse, error) {
	rows, err := h.DB.Query(ctx, `
SELECT
  r.page_id,
  r.run_id,
  r.agent_id,
  r.workspace_id,
  r.created_at,
  e.id IS NOT NULL AND e.workspace_id = r.workspace_id AS accessible,
  a.name
FROM note_page_run_ref r
LEFT JOIN agent_inbox_event e ON e.id = r.run_id AND e.workspace_id = $2
LEFT JOIN agent a ON a.id = r.agent_id AND a.workspace_id = $2
WHERE r.page_id = $1
ORDER BY r.created_at ASC, r.run_id ASC`, pageID, pageWorkspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	refs := make([]NotePageIssueRefResponse, 0)
	for rows.Next() {
		var (
			pageUUID, runUUID, agentUUID, refWorkspaceID pgtype.UUID
			createdAt                                    pgtype.Timestamptz
			accessible                                   bool
			agentName                                    pgtype.Text
		)
		if err := rows.Scan(
			&pageUUID, &runUUID, &agentUUID, &refWorkspaceID, &createdAt,
			&accessible, &agentName,
		); err != nil {
			return nil, err
		}
		runID := uuidToString(runUUID)
		if !accessible {
			refs = append(refs, inaccessibleRunRef(runID))
			continue
		}
		label := "run"
		if agentName.Valid && strings.TrimSpace(agentName.String) != "" {
			label = agentName.String + " run"
		}
		refs = append(refs, accessibleRunRef(
			uuidToString(pageUUID),
			runID,
			uuidToString(agentUUID),
			uuidToString(refWorkspaceID),
			timestampToString(createdAt),
			label,
		))
	}
	return refs, rows.Err()
}

type notePageRunRefCreateRequest struct {
	RunID string `json:"run_id"`
}

func (h *Handler) ListNotePageRunRefs(w http.ResponseWriter, r *http.Request) {
	workspaceID, userID, _, ok := h.notesWorkspaceAndUser(w, r)
	if !ok {
		return
	}
	page, _, ok := h.loadAccessibleNote(w, r, chi.URLParam(r, "id"), workspaceID, userID)
	if !ok {
		return
	}
	refs, err := h.loadNotePageRunRefs(r.Context(), page.ID, page.WorkspaceID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list note run refs")
		return
	}
	writeJSON(w, http.StatusOK, NotePageIssueRefListResponse{Refs: refs})
}

func (h *Handler) CreateNotePageRunRef(w http.ResponseWriter, r *http.Request) {
	workspaceID, userID, _, ok := h.notesWorkspaceAndUser(w, r)
	if !ok {
		return
	}
	page, _, ok := h.loadAccessibleNote(w, r, chi.URLParam(r, "id"), workspaceID, userID)
	if !ok {
		return
	}

	var req notePageRunRefCreateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	runIDStr := strings.TrimSpace(req.RunID)
	if runIDStr == "" {
		writeError(w, http.StatusBadRequest, "run_id is required")
		return
	}
	runUUID, ok := parseUUIDOrBadRequest(w, runIDStr, "run_id")
	if !ok {
		return
	}

	var (
		agentUUID pgtype.UUID
		agentName string
	)
	err := h.DB.QueryRow(r.Context(), `
SELECT e.agent_id, a.name
FROM agent_inbox_event e
JOIN agent a ON a.id = e.agent_id
WHERE e.id = $1 AND e.workspace_id = $2`, runUUID, page.WorkspaceID).Scan(&agentUUID, &agentName)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "run not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to load run")
		return
	}

	var createdAt pgtype.Timestamptz
	err = h.DB.QueryRow(r.Context(), `
INSERT INTO note_page_run_ref (page_id, run_id, agent_id, workspace_id, created_by)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (page_id, run_id) DO UPDATE
SET created_by = note_page_run_ref.created_by
RETURNING created_at`, page.ID, runUUID, agentUUID, page.WorkspaceID, userID).Scan(&createdAt)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create note run ref")
		return
	}

	label := "run"
	if strings.TrimSpace(agentName) != "" {
		label = agentName + " run"
	}
	writeJSON(w, http.StatusCreated, accessibleRunRef(
		uuidToString(page.ID),
		uuidToString(runUUID),
		uuidToString(agentUUID),
		uuidToString(page.WorkspaceID),
		timestampToString(createdAt),
		label,
	))
}

func (h *Handler) DeleteNotePageRunRef(w http.ResponseWriter, r *http.Request) {
	workspaceID, userID, _, ok := h.notesWorkspaceAndUser(w, r)
	if !ok {
		return
	}
	page, _, ok := h.loadAccessibleNote(w, r, chi.URLParam(r, "id"), workspaceID, userID)
	if !ok {
		return
	}
	runUUID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "runId"), "run id")
	if !ok {
		return
	}

	tag, err := h.DB.Exec(r.Context(), `
DELETE FROM note_page_run_ref
WHERE page_id = $1 AND run_id = $2 AND workspace_id = $3`, page.ID, runUUID, page.WorkspaceID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to delete note run ref")
		return
	}
	if tag.RowsAffected() == 0 {
		writeError(w, http.StatusNotFound, "note run ref not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
