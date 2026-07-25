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
	// AvatarURL is the emit-time agent face (LRM-391 AC#5 / LRM-597). Working
	// / Presence facepile must not depend only on channel-member briefs or
	// ListAgents — those can miss channel/private / group-manager agents.
	AvatarURL *string `json:"avatar_url,omitempty"`
	TaskID    string  `json:"task_id"`
	Status    string  `json:"status"`
	// Kind discriminates composer-strip rows (LRM-287). Only `reply` rows are
	// meant to render above the composer; quick_create / issue_create are
	// filtered client-side (and omitted server-side when sourced here).
	Kind                string  `json:"kind,omitempty"`
	Reason              string  `json:"reason,omitempty"`
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

var channelComposerStripExcludedInboxReasons = map[string]struct{}{
	"ambient":            {},
	"channel_onboarding": {},
}

func channelComposerStripExcludedKind(kind string) bool {
	switch kind {
	case "quick_create", "issue_create":
		return true
	default:
		return false
	}
}

func channelComposerStripExcludedInboxReason(reason string) bool {
	_, excluded := channelComposerStripExcludedInboxReasons[reason]
	return excluded
}

// ListChannelActiveTasks returns the latest inbox read-model row per agent in
// the channel. Chat/channel agent work now runs through agent_inbox_event, so
// this endpoint must not use legacy agent_inbox_event rows as the current-state
// source for the conversation strip.
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
			WITH latest_inbox AS (
				SELECT DISTINCT ON (e.agent_id)
				       e.agent_id,
				       COALESCE(NULLIF(a.display_name, ''), a.name, '') AS agent_name,
				       a.avatar_url AS avatar_url,
				       e.id AS task_id,
				       e.status AS inbox_status,
				       e.reason,
				       CASE
				         WHEN e.terminal_outcome IS NOT NULL THEN e.terminal_outcome
				         WHEN COALESCE(latest_delivery.status, '') IN ('leased', 'processing') OR e.status = 'draining' THEN 'running'
				         ELSE 'queued'
				       END AS status,
				       COALESCE(e.terminal_outcome, '') AS terminal_outcome,
				       e.retryable,
				       e.id AS inbox_event_id,
				       COALESCE(e.terminal_delivery_id, latest_delivery.id) AS delivery_id,
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
				LEFT JOIN LATERAL (
					SELECT d.id, d.status
					FROM agent_event_delivery d
					WHERE d.inbox_event_id = e.id
					ORDER BY d.created_at DESC, d.id DESC
					LIMIT 1
				) latest_delivery ON true
				WHERE e.channel_id = $1
				  AND e.requires_wake
				  AND e.status <> 'suppressed'
				ORDER BY e.agent_id, e.created_at DESC, e.id DESC
			),
			active_tasks AS (
				SELECT *, 'reply'::text AS kind
				FROM latest_inbox li
				WHERE li.terminal_outcome = ''
				  AND li.inbox_status IN ('pending', 'draining', 'failed')
				  AND li.status IN ('queued', 'running')
			),
			terminal_tasks AS (
				SELECT *, 'reply'::text AS kind
				FROM latest_inbox li
				WHERE (
				    li.terminal_outcome = 'failed'
				    OR (li.terminal_outcome = 'no_reply' AND li.terminal_at > now() - interval '2 minutes')
				  )
			)
			SELECT agent_id, agent_name, avatar_url, task_id, status, terminal_outcome, retryable,
			       inbox_event_id, delivery_id, conversation_id, channel_id, chat_session_id,
			       thread_root_message_id, source_message_id, terminal_at, kind, reason
			FROM active_tasks
			UNION ALL
			SELECT agent_id, agent_name, avatar_url, task_id, status, terminal_outcome, retryable,
			       inbox_event_id, delivery_id, conversation_id, channel_id, chat_session_id,
			       thread_root_message_id, source_message_id, terminal_at, kind, reason
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
		var name, status, terminalOutcome, kind, reason string
		var avatarURL pgtype.Text
		var retryable bool
		if err := rows.Scan(&agentID, &name, &avatarURL, &taskID, &status, &terminalOutcome, &retryable, &inboxEventID, &deliveryID, &conversationID, &rowChannelID, &chatSessionID, &threadRootMessageID, &sourceMessageID, &terminalAt, &kind, &reason); err != nil {
			continue
		}
		if channelComposerStripExcludedKind(kind) || channelComposerStripExcludedInboxReason(reason) {
			continue
		}
		task := ChannelActiveTask{
			AgentID:             uuidToString(agentID),
			AgentName:           name,
			AvatarURL:           textToPtr(avatarURL),
			TaskID:              uuidToString(taskID),
			Status:              status,
			Kind:                kind,
			Reason:              reason,
			InboxEventID:        uuidStringPtr(inboxEventID),
			DeliveryID:          uuidStringPtr(deliveryID),
			ConversationID:      uuidStringPtr(conversationID),
			ChannelID:           uuidStringPtr(rowChannelID),
			ChatSessionID:       uuidStringPtr(chatSessionID),
			ThreadRootMessageID: uuidStringPtr(threadRootMessageID),
			SourceMessageID:     uuidStringPtr(sourceMessageID),
		}
		if terminalOutcome != "" {
			task.Outcome = stringPtr(terminalOutcome)
			task.Retryable = boolPtr(retryable)
			task.TerminalAt = timestampToPtr(terminalAt)
		}
		tasks = append(tasks, task)
	}
	writeJSON(w, http.StatusOK, ChannelActiveTasksResponse{Tasks: tasks})
}
