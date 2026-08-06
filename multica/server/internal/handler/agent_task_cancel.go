package handler

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/multica-ai/multica/server/internal/util"
)

// CancelAgentTask cancels only the task bound to the current task-scoped
// AgentPrincipal. It intentionally does not reuse human membership, private
// agent, channel, or OwnerUserID authorization: the two exact identity checks
// below are the complete data-plane authority.
func (h *Handler) CancelAgentTask(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.requireAgentPrincipal(w, r)
	if !ok {
		return
	}
	if principal.ActorSource != "task_token" && principal.ActorSource != "agent_inbox_token" {
		writeError(w, http.StatusForbidden, "task-scoped agent principal required")
		return
	}
	principalAgentID, agentOK := principal.AgentUUID()
	principalWorkspaceID, workspaceOK := principal.WorkspaceUUID()
	principalTaskID, taskErr := util.ParseUUID(principal.TaskID)
	if !agentOK || !workspaceOK || taskErr != nil {
		writeError(w, http.StatusForbidden, "task-scoped agent principal required")
		return
	}

	targetTaskID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "taskId"), "task id")
	if !ok {
		return
	}
	if targetTaskID != principalTaskID {
		writeError(w, http.StatusNotFound, "task not found")
		return
	}
	task, err := h.Queries.GetAgentTask(r.Context(), targetTaskID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "task not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to load task")
		return
	}
	if task.AgentID != principalAgentID || task.WorkspaceID != principalWorkspaceID {
		writeError(w, http.StatusNotFound, "task not found")
		return
	}

	cancelled, err := h.TaskService.CancelTaskWithResult(r.Context(), targetTaskID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to cancel task")
		return
	}
	writeJSON(w, http.StatusOK, taskToResponse(cancelled.Task, principal.WorkspaceID))
}
