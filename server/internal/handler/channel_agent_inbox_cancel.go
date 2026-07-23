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

// ChannelAgentInboxCancelResponse is the explicit inbox-wake cancel contract
// for channel Stop (LRM-425). Authority ID is agent_inbox_event.id — not
// agent_task_queue.
type ChannelAgentInboxCancelResponse struct {
	InboxEventID  string  `json:"inbox_event_id"`
	AgentID       string  `json:"agent_id"`
	RuntimeID     string  `json:"runtime_id,omitempty"`
	Status        string  `json:"status"`
	Priority      int32   `json:"priority"`
	CompletedAt   *string `json:"completed_at,omitempty"`
	CreatedAt     string  `json:"created_at"`
	ChatSessionID string  `json:"chat_session_id,omitempty"`
	Kind          string  `json:"kind"`
}

// ChannelAgentInboxCancelActiveResponse is the bulk Stop All contract: one
// request cancels every active wake inbox event in the channel (same scope as
// GET /active-tasks non-terminal rows). Frontend must not for-in single cancel.
type ChannelAgentInboxCancelActiveResponse struct {
	Cancelled      []ChannelAgentInboxCancelResponse `json:"cancelled"`
	CancelledCount int                               `json:"cancelled_count"`
}

type cancelledAgentInboxEventRow struct {
	ID            pgtype.UUID
	AgentID       pgtype.UUID
	RuntimeID     pgtype.UUID
	Priority      int32
	CreatedAt     pgtype.Timestamptz
	TerminalAt    pgtype.Timestamptz
	ChatSessionID pgtype.UUID
}

const cancelAgentInboxEventSQL = `
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
		LEFT JOIN agent_session s ON s.id = e.agent_session_id`

// cancelAgentInboxEventRow mutates one cancellable wake inbox event to the
// suppressed/no_reply terminal used by user Stop. Returns pgx.ErrNoRows when
// the event is missing or already terminal / not cancellable.
func (h *Handler) cancelAgentInboxEventRow(ctx context.Context, workspaceID, inboxEventID pgtype.UUID) (cancelledAgentInboxEventRow, error) {
	var row cancelledAgentInboxEventRow
	err := h.DB.QueryRow(ctx, cancelAgentInboxEventSQL, inboxEventID, workspaceID).Scan(
		&row.ID, &row.AgentID, &row.RuntimeID, &row.Priority, &row.CreatedAt, &row.TerminalAt, &row.ChatSessionID,
	)
	return row, err
}

func channelAgentInboxCancelResponse(workspaceID string, row cancelledAgentInboxEventRow) ChannelAgentInboxCancelResponse {
	return ChannelAgentInboxCancelResponse{
		InboxEventID:  uuidToString(row.ID),
		AgentID:       uuidToString(row.AgentID),
		RuntimeID:     uuidToString(row.RuntimeID),
		Status:        "cancelled",
		Priority:      row.Priority,
		CompletedAt:   timestampToPtr(row.TerminalAt),
		CreatedAt:     timestampToString(row.CreatedAt),
		ChatSessionID: uuidToString(row.ChatSessionID),
		Kind:          "chat",
	}
}

func (h *Handler) publishChannelAgentInboxCancelled(workspaceID, actorType, actorID string, row cancelledAgentInboxEventRow) {
	resp := channelAgentInboxCancelResponse(workspaceID, row)
	// Keep task:cancelled payload shape compatible with existing clients that
	// still key lifecycle rows by inbox_event_id (== former task_id).
	h.publishTask(protocol.EventTaskCancelled, workspaceID, actorType, actorID, uuidToString(row.ID), AgentTaskResponse{
		ID:            resp.InboxEventID,
		AgentID:       resp.AgentID,
		RuntimeID:     resp.RuntimeID,
		WorkspaceID:   workspaceID,
		Status:        resp.Status,
		Priority:      resp.Priority,
		CompletedAt:   resp.CompletedAt,
		Attempt:       1,
		MaxAttempts:   1,
		CreatedAt:     resp.CreatedAt,
		ChatSessionID: resp.ChatSessionID,
		Kind:          resp.Kind,
	})
}

// CancelChannelAgentInboxEvent cancels one channel wake inbox event.
// POST /api/channels/{channelId}/agent-inbox/events/{eventId}/cancel
func (h *Handler) CancelChannelAgentInboxEvent(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	workspaceID := ctxWorkspaceID(r.Context())
	workspaceUUID, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace id")
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

	var eventStatus, terminalOutcome string
	if err := h.DB.QueryRow(r.Context(), `
		SELECT e.status, COALESCE(e.terminal_outcome, '')
		FROM agent_inbox_event e
		WHERE e.id = $1
		  AND e.workspace_id = $2
		  AND e.channel_id = $3
		  AND e.requires_wake`, eventID, workspaceUUID, channelID).Scan(&eventStatus, &terminalOutcome); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "inbox event not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to load agent inbox event")
		return
	}
	if terminalOutcome != "" || eventStatus == "acked" || eventStatus == "suppressed" {
		writeError(w, http.StatusBadRequest, "inbox event is not cancellable")
		return
	}

	row, err := h.cancelAgentInboxEventRow(r.Context(), workspaceUUID, eventID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusBadRequest, "inbox event is not cancellable")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to cancel agent inbox event")
		return
	}

	actorType, actorID := h.resolveActor(r, userID, workspaceID)
	h.publishChannelAgentInboxCancelled(workspaceID, actorType, actorID, row)
	writeJSON(w, http.StatusOK, channelAgentInboxCancelResponse(workspaceID, row))
}

// CancelChannelActiveAgentInboxEvents bulk-cancels every active wake inbox
// event in the channel (Stop All). Scope matches GET .../active-tasks
// non-terminal rows: requires_wake, pending/draining/failed, no terminal
// outcome, excluding ambient/channel_onboarding.
// POST /api/channels/{channelId}/agent-inbox/cancel-active
func (h *Handler) CancelChannelActiveAgentInboxEvents(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	workspaceID := ctxWorkspaceID(r.Context())
	workspaceUUID, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace id")
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

	eventIDs, err := h.listCancellableChannelActiveInboxEventIDs(r.Context(), workspaceUUID, channelID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list active inbox events")
		return
	}

	cancelled := make([]ChannelAgentInboxCancelResponse, 0, len(eventIDs))
	actorType, actorID := h.resolveActor(r, userID, workspaceID)
	for _, eventID := range eventIDs {
		row, cancelErr := h.cancelAgentInboxEventRow(r.Context(), workspaceUUID, eventID)
		if cancelErr != nil {
			if errors.Is(cancelErr, pgx.ErrNoRows) {
				// Race: already terminal between list and cancel — skip.
				continue
			}
			writeError(w, http.StatusInternalServerError, "failed to cancel agent inbox event")
			return
		}
		h.publishChannelAgentInboxCancelled(workspaceID, actorType, actorID, row)
		cancelled = append(cancelled, channelAgentInboxCancelResponse(workspaceID, row))
	}

	writeJSON(w, http.StatusOK, ChannelAgentInboxCancelActiveResponse{
		Cancelled:      cancelled,
		CancelledCount: len(cancelled),
	})
}

func (h *Handler) listCancellableChannelActiveInboxEventIDs(ctx context.Context, workspaceID, channelID pgtype.UUID) ([]pgtype.UUID, error) {
	// Same active set as ListChannelActiveTasks (non-terminal), so Stop All
	// matches the strip / header authority without a client for-in.
	rows, err := h.DB.Query(ctx, `
		WITH latest_inbox AS (
			SELECT DISTINCT ON (e.agent_id)
			       e.id AS inbox_event_id,
			       e.status AS inbox_status,
			       e.reason,
			       CASE
			         WHEN e.terminal_outcome IS NOT NULL THEN e.terminal_outcome
			         WHEN COALESCE(latest_delivery.status, '') IN ('leased', 'processing') OR e.status = 'draining' THEN 'running'
			         ELSE 'queued'
			       END AS status,
			       COALESCE(e.terminal_outcome, '') AS terminal_outcome,
			       e.created_at
			FROM agent_inbox_event e
			LEFT JOIN LATERAL (
				SELECT d.status
				FROM agent_event_delivery d
				WHERE d.inbox_event_id = e.id
				ORDER BY d.created_at DESC, d.id DESC
				LIMIT 1
			) latest_delivery ON true
			WHERE e.channel_id = $1
			  AND e.workspace_id = $2
			  AND e.requires_wake
			  AND e.status <> 'suppressed'
			ORDER BY e.agent_id, e.created_at DESC, e.id DESC
		)
		SELECT inbox_event_id
		FROM latest_inbox
		WHERE terminal_outcome = ''
		  AND inbox_status IN ('pending', 'draining', 'failed')
		  AND status IN ('queued', 'running')
		  AND reason NOT IN ('ambient', 'channel_onboarding')
		ORDER BY created_at ASC, inbox_event_id ASC`, channelID, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	ids := make([]pgtype.UUID, 0)
	for rows.Next() {
		var id pgtype.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
