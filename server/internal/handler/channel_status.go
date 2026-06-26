package handler

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// ChannelActiveTask is the in-flight agent task surfaced to the channel UI so
// it can render a recoverable, query-authoritative working indicator
// (排队中 / 启动中 / 思考中 / 等待本地目录释放) per agent — instead of relying solely
// on a transient typing broadcast that vanishes if the client missed it.
type ChannelActiveTask struct {
	AgentID   string `json:"agent_id"`
	AgentName string `json:"agent_name"`
	TaskID    string `json:"task_id"`
	Status    string `json:"status"`
}

type ChannelActiveTasksResponse struct {
	Tasks []ChannelActiveTask `json:"tasks"`
}

// ListChannelActiveTasks returns the latest non-terminal task per agent in the
// channel (queued / dispatched / running / waiting_local_directory). Channel
// agents run through per-agent chat sessions (channel_agent_session), so we
// join the channel's sessions to their queued tasks.
func (h *Handler) ListChannelActiveTasks(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	workspaceID := ctxWorkspaceID(r.Context())
	channelID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "channelId"), "channel id")
	if !ok {
		return
	}
	if !h.requireChannelUserMember(w, r.Context(), workspaceID, channelID, parseUUID(userID)) {
		return
	}

	rows, err := h.DB.Query(r.Context(), `
		SELECT DISTINCT ON (a.id) a.id, a.name, atq.id, atq.status
		FROM channel_agent_session cas
		JOIN chat_session cs ON cs.id = cas.chat_session_id
		JOIN agent_task_queue atq ON atq.chat_session_id = cs.id
		JOIN agent a ON a.id = cas.agent_id
		WHERE cas.channel_id = $1
		  AND atq.status IN ('queued', 'dispatched', 'running', 'waiting_local_directory')
		ORDER BY a.id, atq.created_at DESC`, channelID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list active tasks")
		return
	}
	defer rows.Close()

	tasks := make([]ChannelActiveTask, 0)
	for rows.Next() {
		var agentID, taskID pgtype.UUID
		var name, status string
		if err := rows.Scan(&agentID, &name, &taskID, &status); err != nil {
			continue
		}
		tasks = append(tasks, ChannelActiveTask{
			AgentID:   uuidToString(agentID),
			AgentName: name,
			TaskID:    uuidToString(taskID),
			Status:    status,
		})
	}
	writeJSON(w, http.StatusOK, ChannelActiveTasksResponse{Tasks: tasks})
}
