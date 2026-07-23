package handler

import (
	"context"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// Channel inbox cancel contract (LRM-425):
//   - Single stop: POST /api/channels/{channelId}/agent-inbox/events/{eventId}/cancel
//   - Stop All:    POST /api/channels/{channelId}/agent-inbox/cancel-active
//
// Authority is agent_inbox_event (same as active-tasks). Channel UI must not
// fan out N× POST /api/tasks/{id}/cancel for Stop All.

type cancelChannelInboxEventResponse struct {
	OK           bool   `json:"ok"`
	InboxEventID string `json:"inbox_event_id"`
	AgentID      string `json:"agent_id"`
	Status       string `json:"status"`
}

type cancelChannelActiveInboxResponse struct {
	OK                     bool     `json:"ok"`
	CancelledCount         int      `json:"cancelled_count"`
	CancelledInboxEventIDs []string `json:"cancelled_inbox_event_ids"`
}

type cancelledAgentInboxEvent struct {
	ID            pgtype.UUID
	AgentID       pgtype.UUID
	RuntimeID     pgtype.UUID
	Priority      int32
	CreatedAt     pgtype.Timestamptz
	TerminalAt    pgtype.Timestamptz
	ChatSessionID pgtype.UUID
}

var (
	errAgentInboxEventNotFound       = errors.New("agent inbox event not found")
	errAgentInboxEventNotCancellable = errors.New("task is not cancellable")
)

// CancelChannelAgentInboxEvent cancels one channel-scoped inbox wake event.
func (h *Handler) CancelChannelAgentInboxEvent(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	workspaceID := ctxWorkspaceID(r.Context())
	wsUUID, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace id")
	if !ok {
		return
	}
	channelID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "channelId"), "channel id")
	if !ok {
		return
	}
	eventID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "eventId"), "inbox event id")
	if !ok {
		return
	}
	if !h.requireChannelUserMember(w, r.Context(), workspaceID, channelID, parseUUID(userID)) {
		return
	}
	if !h.requireChannelWritable(w, r.Context(), workspaceID, channelID) {
		return
	}

	var eventChannelID pgtype.UUID
	if err := h.DB.QueryRow(r.Context(), `
		SELECT channel_id
		FROM agent_inbox_event
		WHERE id = $1
		  AND workspace_id = $2
		  AND requires_wake`, eventID, wsUUID).Scan(&eventChannelID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "inbox event not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to load inbox event")
		return
	}
	if !eventChannelID.Valid || uuidToString(eventChannelID) != uuidToString(channelID) {
		writeError(w, http.StatusNotFound, "inbox event not found")
		return
	}

	cancelled, err := h.cancelAgentInboxEventRow(r.Context(), wsUUID, eventID)
	if err != nil {
		if errors.Is(err, errAgentInboxEventNotCancellable) {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if errors.Is(err, errAgentInboxEventNotFound) {
			writeError(w, http.StatusNotFound, "inbox event not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to cancel inbox event")
		return
	}

	h.publishCancelledAgentInboxEvent(r, workspaceID, userID, cancelled)
	writeJSON(w, http.StatusOK, cancelChannelInboxEventResponse{
		OK:           true,
		InboxEventID: uuidToString(cancelled.ID),
		AgentID:      uuidToString(cancelled.AgentID),
		Status:       "cancelled",
	})
}

// CancelChannelActiveAgentInboxEvents stops every cancellable requires_wake
// inbox event in the channel in one request (Stop All).
func (h *Handler) CancelChannelActiveAgentInboxEvents(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	workspaceID := ctxWorkspaceID(r.Context())
	wsUUID, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace id")
	if !ok {
		return
	}
	channelID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "channelId"), "channel id")
	if !ok {
		return
	}
	if !h.requireChannelUserMember(w, r.Context(), workspaceID, channelID, parseUUID(userID)) {
		return
	}
	if !h.requireChannelWritable(w, r.Context(), workspaceID, channelID) {
		return
	}

	rows, err := h.DB.Query(r.Context(), `
		SELECT e.id
		FROM agent_inbox_event e
		WHERE e.channel_id = $1
		  AND e.workspace_id = $2
		  AND e.requires_wake
		  AND e.status IN ('pending', 'draining', 'failed')
		  AND e.terminal_outcome IS NULL
		ORDER BY e.created_at ASC, e.id ASC`, channelID, wsUUID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list active inbox events")
		return
	}
	defer rows.Close()

	eventIDs := make([]pgtype.UUID, 0)
	for rows.Next() {
		var id pgtype.UUID
		if err := rows.Scan(&id); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to list active inbox events")
			return
		}
		eventIDs = append(eventIDs, id)
	}
	if err := rows.Err(); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list active inbox events")
		return
	}

	cancelledIDs := make([]string, 0, len(eventIDs))
	for _, eventID := range eventIDs {
		cancelled, err := h.cancelAgentInboxEventRow(r.Context(), wsUUID, eventID)
		if err != nil {
			if errors.Is(err, errAgentInboxEventNotCancellable) || errors.Is(err, errAgentInboxEventNotFound) {
				continue
			}
			writeError(w, http.StatusInternalServerError, "failed to cancel inbox event")
			return
		}
		h.publishCancelledAgentInboxEvent(r, workspaceID, userID, cancelled)
		cancelledIDs = append(cancelledIDs, uuidToString(cancelled.ID))
	}

	writeJSON(w, http.StatusOK, cancelChannelActiveInboxResponse{
		OK:                     true,
		CancelledCount:         len(cancelledIDs),
		CancelledInboxEventIDs: cancelledIDs,
	})
}

func (h *Handler) publishCancelledAgentInboxEvent(r *http.Request, workspaceID, userID string, cancelled cancelledAgentInboxEvent) {
	resp := AgentTaskResponse{
		ID:            uuidToString(cancelled.ID),
		AgentID:       uuidToString(cancelled.AgentID),
		RuntimeID:     uuidToString(cancelled.RuntimeID),
		WorkspaceID:   workspaceID,
		Status:        "cancelled",
		Priority:      cancelled.Priority,
		CompletedAt:   timestampToPtr(cancelled.TerminalAt),
		Attempt:       1,
		MaxAttempts:   1,
		CreatedAt:     timestampToString(cancelled.CreatedAt),
		ChatSessionID: uuidToString(cancelled.ChatSessionID),
		Kind:          "chat",
	}
	actorType, actorID := h.resolveActor(r, userID, workspaceID)
	h.publishTask(protocol.EventTaskCancelled, workspaceID, actorType, actorID, uuidToString(cancelled.ID), resp)
}

// cancelAgentInboxEventRow suppresses one cancellable inbox wake event and
// fails any leased/processing delivery. Shared by the legacy tasks/{id}/cancel
// inbox branch and the channel-scoped LRM-425 cancel routes.
func (h *Handler) cancelAgentInboxEventRow(ctx context.Context, workspaceUUID, inboxEventID pgtype.UUID) (cancelledAgentInboxEvent, error) {
	var eventStatus, terminalOutcome string
	if err := h.DB.QueryRow(ctx, `
		SELECT e.status,
		       COALESCE(e.terminal_outcome, '')
		FROM agent_inbox_event e
		WHERE e.id = $1
		  AND e.workspace_id = $2
		  AND e.requires_wake`, inboxEventID, workspaceUUID).Scan(&eventStatus, &terminalOutcome); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return cancelledAgentInboxEvent{}, errAgentInboxEventNotFound
		}
		return cancelledAgentInboxEvent{}, err
	}
	if terminalOutcome != "" || eventStatus == "acked" || eventStatus == "suppressed" {
		return cancelledAgentInboxEvent{}, errAgentInboxEventNotCancellable
	}

	var cancelled cancelledAgentInboxEvent
	if err := h.DB.QueryRow(ctx, `
		WITH latest_delivery AS (
			SELECT d.id, d.runtime_id
			FROM agent_event_delivery d
			WHERE d.inbox_event_id = $1
			ORDER BY d.created_at DESC, d.id DESC
			LIMIT 1
		),
		cancelled_delivery AS (
			UPDATE agent_event_delivery d
			SET status = 'failed',
			    last_error = 'cancelled by user',
			    updated_at = now()
			WHERE d.inbox_event_id = $1
			  AND d.status IN ('leased', 'processing')
			RETURNING d.id, d.runtime_id
		),
		chosen_delivery AS (
			SELECT id, runtime_id FROM cancelled_delivery
			UNION ALL
			SELECT id, runtime_id FROM latest_delivery
			LIMIT 1
		),
		cancelled_event AS (
			UPDATE agent_inbox_event e
			SET status = 'suppressed',
			    terminal_outcome = 'no_reply',
			    terminal_delivery_id = (SELECT id FROM chosen_delivery LIMIT 1),
			    retryable = false,
			    terminal_at = now(),
			    acked_at = now(),
			    last_error = 'cancelled by user',
			    updated_at = now()
			WHERE e.id = $1
			  AND e.workspace_id = $2
			  AND e.requires_wake
			  AND e.status IN ('pending', 'draining', 'failed')
			  AND e.terminal_outcome IS NULL
			RETURNING e.id, e.agent_id, e.agent_session_id, e.priority, e.created_at, e.terminal_at, e.chat_session_id
		)
		SELECT e.id,
		       e.agent_id,
		       COALESCE((SELECT runtime_id FROM chosen_delivery LIMIT 1), s.runtime_id),
		       e.priority,
		       e.created_at,
		       e.terminal_at,
		       e.chat_session_id
		FROM cancelled_event e
		LEFT JOIN agent_session s ON s.id = e.agent_session_id`, inboxEventID, workspaceUUID).Scan(
		&cancelled.ID,
		&cancelled.AgentID,
		&cancelled.RuntimeID,
		&cancelled.Priority,
		&cancelled.CreatedAt,
		&cancelled.TerminalAt,
		&cancelled.ChatSessionID,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return cancelledAgentInboxEvent{}, errAgentInboxEventNotCancellable
		}
		return cancelledAgentInboxEvent{}, err
	}
	return cancelled, nil
}
