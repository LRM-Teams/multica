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
	AgentID             string  `json:"agent_id"`
	AgentName           string  `json:"agent_name"`
	TaskID              string  `json:"task_id"`
	Status              string  `json:"status"`
	Outcome             *string `json:"outcome,omitempty"`
	Retryable           *bool   `json:"retryable,omitempty"`
	InboxEventID        *string `json:"inbox_event_id,omitempty"`
	DeliveryID          *string `json:"delivery_id,omitempty"`
	ConversationID      *string `json:"conversation_id,omitempty"`
	ChannelID           *string `json:"channel_id,omitempty"`
	ChatSessionID       *string `json:"chat_session_id,omitempty"`
	ThreadRootMessageID *string `json:"thread_root_message_id,omitempty"`
	SourceMessageID     *string `json:"source_message_id,omitempty"`
	TerminalAt          *string `json:"terminal_at,omitempty"`
}

type ChannelActiveTasksResponse struct {
	Tasks []ChannelActiveTask `json:"tasks"`
}

// ListChannelActiveTasks returns the latest non-terminal task per agent in the
// channel (queued / dispatched / running / waiting_local_directory) whose
// runtime is still online. Channel agents run through per-agent chat sessions
// (channel_agent_session), so we join the channel's sessions to their queued
// tasks.
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
			WITH legacy_active AS (
				SELECT DISTINCT ON (a.id)
				       a.id AS agent_id,
				       COALESCE(NULLIF(a.display_name, ''), a.name, '') AS agent_name,
				       atq.id AS task_id,
				       atq.status AS status,
				       ''::text AS terminal_outcome,
				       false AS retryable,
				       NULL::uuid AS inbox_event_id,
				       NULL::uuid AS delivery_id,
				       NULL::uuid AS conversation_id,
				       NULL::uuid AS channel_id,
				       NULL::uuid AS chat_session_id,
				       NULL::uuid AS thread_root_message_id,
				       NULL::uuid AS source_message_id,
				       NULL::timestamptz AS terminal_at,
				       atq.created_at AS sort_at
				FROM channel_agent_session cas
				JOIN chat_session cs ON cs.id = cas.chat_session_id
				JOIN agent_task_queue atq ON atq.chat_session_id = cs.id
				JOIN agent_runtime ar ON ar.id = atq.runtime_id AND ar.status = 'online'
				JOIN agent a ON a.id = cas.agent_id
				WHERE cas.channel_id = $1
				  AND atq.status IN ('queued', 'dispatched', 'running', 'waiting_local_directory')
				ORDER BY a.id, atq.created_at DESC
			),
			latest_inbox AS (
				SELECT DISTINCT ON (e.agent_id)
				       e.agent_id,
				       COALESCE(NULLIF(a.display_name, ''), a.name, '') AS agent_name,
				       e.id AS task_id,
				       COALESCE(e.terminal_outcome, e.status) AS status,
				       COALESCE(e.terminal_outcome, '') AS terminal_outcome,
				       e.retryable,
				       e.id AS inbox_event_id,
				       e.terminal_delivery_id AS delivery_id,
				       e.conversation_id,
				       e.channel_id,
				       e.chat_session_id,
				       COALESCE(trigger.thread_root_message_id, trigger.id) AS thread_root_message_id,
				       e.source_message_id,
				       e.terminal_at,
				       e.created_at AS sort_at
				FROM agent_inbox_event e
				JOIN agent a ON a.id = e.agent_id
				LEFT JOIN channel_message trigger ON trigger.id = e.source_message_id
				WHERE e.channel_id = $1
				  AND e.requires_wake
				ORDER BY e.agent_id, e.created_at DESC, e.id DESC
			),
			terminal_tasks AS (
				SELECT *
				FROM latest_inbox li
				WHERE (
				    li.terminal_outcome = 'failed'
				    OR (li.terminal_outcome = 'no_reply' AND li.terminal_at > now() - interval '2 minutes')
				  )
				  AND NOT EXISTS (
				    SELECT 1 FROM legacy_active la WHERE la.agent_id = li.agent_id
				  )
			)
			SELECT agent_id, agent_name, task_id, status, terminal_outcome, retryable,
			       inbox_event_id, delivery_id, conversation_id, channel_id, chat_session_id,
			       thread_root_message_id, source_message_id, terminal_at
			FROM legacy_active
			UNION ALL
			SELECT agent_id, agent_name, task_id, status, terminal_outcome, retryable,
			       inbox_event_id, delivery_id, conversation_id, channel_id, chat_session_id,
			       thread_root_message_id, source_message_id, terminal_at
			FROM terminal_tasks
			ORDER BY agent_name ASC, task_id ASC`, channelID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list active tasks")
		return
	}
	defer rows.Close()

	tasks := make([]ChannelActiveTask, 0)
	for rows.Next() {
		var agentID, taskID pgtype.UUID
		var inboxEventID, deliveryID, conversationID, rowChannelID, chatSessionID, threadRootMessageID, sourceMessageID pgtype.UUID
		var terminalAt pgtype.Timestamptz
		var name, status, terminalOutcome string
		var retryable bool
		if err := rows.Scan(&agentID, &name, &taskID, &status, &terminalOutcome, &retryable, &inboxEventID, &deliveryID, &conversationID, &rowChannelID, &chatSessionID, &threadRootMessageID, &sourceMessageID, &terminalAt); err != nil {
			continue
		}
		task := ChannelActiveTask{
			AgentID:   uuidToString(agentID),
			AgentName: name,
			TaskID:    uuidToString(taskID),
			Status:    status,
		}
		if terminalOutcome != "" {
			task.Outcome = stringPtr(terminalOutcome)
			task.Retryable = boolPtr(retryable)
			task.InboxEventID = uuidStringPtr(inboxEventID)
			task.DeliveryID = uuidStringPtr(deliveryID)
			task.ConversationID = uuidStringPtr(conversationID)
			task.ChannelID = uuidStringPtr(rowChannelID)
			task.ChatSessionID = uuidStringPtr(chatSessionID)
			task.ThreadRootMessageID = uuidStringPtr(threadRootMessageID)
			task.SourceMessageID = uuidStringPtr(sourceMessageID)
			task.TerminalAt = timestampToPtr(terminalAt)
		}
		tasks = append(tasks, task)
	}
	writeJSON(w, http.StatusOK, ChannelActiveTasksResponse{Tasks: tasks})
}
