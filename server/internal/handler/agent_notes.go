package handler

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/middleware"
	"github.com/multica-ai/multica/server/internal/service"
	"github.com/multica-ai/multica/server/internal/util"
)

// GetAgentNotePage is the agent data-plane read for a single product note page
// (S2-C2). Authorization is fail-closed: the current task must authorize the
// page via note_worker_job or note_brief, and the Worker creator must still
// pass noteAccess. Agent OwnerUserID is never used as the note viewer.
func (h *Handler) GetAgentNotePage(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.requireAgentPrincipal(w, r)
	if !ok {
		return
	}
	page, viewerID, ok := h.loadAgentAccessibleNote(w, r, principal, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	refs, err := h.loadNotePageRefs(r.Context(), page.ID, page.WorkspaceID, viewerID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load note page refs")
		return
	}
	// Agents get a read-only projection: no share roster / manage flag.
	writeJSON(w, http.StatusOK, notePageToResponse(page, pgtype.UUID{}, []string{}, refs))
}

func (h *Handler) loadAgentAccessibleNote(w http.ResponseWriter, r *http.Request, principal middleware.AgentPrincipal, pageID string) (notePageRow, pgtype.UUID, bool) {
	pageUUID, ok := parseUUIDOrBadRequest(w, pageID, "note page id")
	if !ok {
		return notePageRow{}, pgtype.UUID{}, false
	}
	workspaceUUID, ok := parseUUIDOrBadRequest(w, principal.WorkspaceID, "workspace id")
	if !ok {
		return notePageRow{}, pgtype.UUID{}, false
	}
	agentUUID, ok := parseUUIDOrBadRequest(w, principal.AgentID, "agent id")
	if !ok {
		return notePageRow{}, pgtype.UUID{}, false
	}
	viewerID, authorized, err := h.resolveAgentNoteViewer(r.Context(), principal, agentUUID, workspaceUUID, pageUUID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to authorize note page")
		return notePageRow{}, pgtype.UUID{}, false
	}
	if !authorized {
		writeError(w, http.StatusNotFound, "note page not found")
		return notePageRow{}, pgtype.UUID{}, false
	}
	accessible, _, err := h.noteAccess(r.Context(), pageUUID, workspaceUUID, viewerID)
	if err != nil || !accessible {
		writeError(w, http.StatusNotFound, "note page not found")
		return notePageRow{}, pgtype.UUID{}, false
	}
	page, err := scanNotePage(h.DB.QueryRow(r.Context(), `
SELECT id, workspace_id, parent_id, owner_user_id, title, content, sort_key, created_at, updated_at, deleted_at
FROM note_page
WHERE id = $1 AND deleted_at IS NULL AND workspace_id = $2`, pageUUID, workspaceUUID))
	if err != nil {
		writeError(w, http.StatusNotFound, "note page not found")
		return notePageRow{}, pgtype.UUID{}, false
	}
	return page, viewerID, true
}

// resolveAgentNoteViewer returns the human viewer whose note ACL applies.
// Prefer the Worker job bound to this task; otherwise accept a matching
// note_brief on the task context and use the task initiator.
func (h *Handler) resolveAgentNoteViewer(
	ctx context.Context,
	principal middleware.AgentPrincipal,
	agentID, workspaceID, pageID pgtype.UUID,
) (pgtype.UUID, bool, error) {
	taskID := strings.TrimSpace(principal.TaskID)
	if taskID == "" {
		return pgtype.UUID{}, false, nil
	}
	taskUUID, err := util.ParseUUID(taskID)
	if err != nil {
		return pgtype.UUID{}, false, nil
	}

	var creatorID pgtype.UUID
	err = h.DB.QueryRow(ctx, `
SELECT creator_id
FROM note_worker_job
WHERE task_id = $1
  AND agent_id = $2
  AND page_id = $3
  AND workspace_id = $4`, taskUUID, agentID, pageID, workspaceID).Scan(&creatorID)
	if err == nil {
		return creatorID, true, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return pgtype.UUID{}, false, err
	}

	var contextJSON []byte
	var initiatorID pgtype.UUID
	err = h.DB.QueryRow(ctx, `
SELECT context, initiator_user_id
FROM agent_inbox_event
WHERE id = $1
  AND agent_id = $2
  AND workspace_id = $3`, taskUUID, agentID, workspaceID).Scan(&contextJSON, &initiatorID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return pgtype.UUID{}, false, nil
		}
		return pgtype.UUID{}, false, err
	}
	brief, present, briefErr := service.NoteBriefFromContext(contextJSON)
	if briefErr != nil || !present || brief.PageID != uuidToString(pageID) || !initiatorID.Valid {
		return pgtype.UUID{}, false, nil
	}
	return initiatorID, true, nil
}
