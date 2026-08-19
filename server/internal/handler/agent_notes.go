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
// page via note_worker_job, note_brief, or a note-scoped chat_session, and the
// human viewer must still pass noteAccess. Agent OwnerUserID is never used as
// the note viewer. Authorized roots also cover descendants (subtree read).
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

// ListAgentNoteTree returns the authorized page and its descendants as a flat
// outline (id, parent_id, title, depth). Same ACL as GetAgentNotePage on the
// root id in the path.
func (h *Handler) ListAgentNoteTree(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.requireAgentPrincipal(w, r)
	if !ok {
		return
	}
	page, _, ok := h.loadAgentAccessibleNote(w, r, principal, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	nodes, err := h.listNoteSubtreeNodes(r.Context(), page.ID, page.WorkspaceID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list note tree")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"pages": nodes})
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
// Prefer the Worker job bound to this task (exact page or descendant);
// otherwise accept a matching note_brief; then an active note-scoped
// chat_session. When TaskID is empty (durable agent_credential), fall back to
// an active Worker job or note chat_session for this agent+page.
func (h *Handler) resolveAgentNoteViewer(
	ctx context.Context,
	principal middleware.AgentPrincipal,
	agentID, workspaceID, pageID pgtype.UUID,
) (pgtype.UUID, bool, error) {
	taskID := strings.TrimSpace(principal.TaskID)
	if taskID != "" {
		taskUUID, err := util.ParseUUID(taskID)
		if err != nil {
			return pgtype.UUID{}, false, nil
		}

		var creatorID, jobPageID pgtype.UUID
		err = h.DB.QueryRow(ctx, `
SELECT creator_id, page_id
FROM note_worker_job
WHERE task_id = $1
  AND agent_id = $2
  AND workspace_id = $3`, taskUUID, agentID, workspaceID).Scan(&creatorID, &jobPageID)
		if err == nil {
			under, underErr := h.notePageIsUnderRoot(ctx, pageID, jobPageID, workspaceID)
			if underErr != nil {
				return pgtype.UUID{}, false, underErr
			}
			if under {
				return creatorID, true, nil
			}
		} else if !errors.Is(err, pgx.ErrNoRows) {
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
		if err == nil {
			brief, present, briefErr := service.NoteBriefFromContext(contextJSON)
			if briefErr == nil && present && initiatorID.Valid {
				rootUUID, parseErr := util.ParseUUID(brief.PageID)
				if parseErr == nil {
					under, underErr := h.notePageIsUnderRoot(ctx, pageID, rootUUID, workspaceID)
					if underErr != nil {
						return pgtype.UUID{}, false, underErr
					}
					if under {
						return initiatorID, true, nil
					}
				}
			}
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return pgtype.UUID{}, false, err
		}
	}

	rows, qerr := h.DB.Query(ctx, `
SELECT creator_id, page_id
FROM note_worker_job
WHERE agent_id = $1
  AND workspace_id = $2
  AND status IN ('pending', 'dispatched', 'running')
ORDER BY created_at DESC
LIMIT 16`, agentID, workspaceID)
	if qerr != nil {
		return pgtype.UUID{}, false, qerr
	}
	defer rows.Close()
	for rows.Next() {
		var creatorID, jobPageID pgtype.UUID
		if err := rows.Scan(&creatorID, &jobPageID); err != nil {
			return pgtype.UUID{}, false, err
		}
		under, underErr := h.notePageIsUnderRoot(ctx, pageID, jobPageID, workspaceID)
		if underErr != nil {
			return pgtype.UUID{}, false, underErr
		}
		if under {
			return creatorID, true, nil
		}
	}
	if err := rows.Err(); err != nil {
		return pgtype.UUID{}, false, err
	}

	return h.resolveNoteChatSessionViewer(ctx, agentID, workspaceID, pageID)
}
