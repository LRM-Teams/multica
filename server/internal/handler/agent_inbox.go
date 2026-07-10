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
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

const agentInboxDrainMessageLimit = 50

type DrainAgentInboxResponse struct {
	Events      []AgentInboxEventResponse `json:"events"`
	LastSeenSeq int64                     `json:"last_seen_seq"`
	HasMore     bool                      `json:"has_more"`
}

type AgentInboxEventResponse struct {
	ID              string                   `json:"id"`
	DeliveryID      string                   `json:"delivery_id"`
	AgentSessionID  string                   `json:"agent_session_id"`
	ConversationID  string                   `json:"conversation_id"`
	ChannelID       string                   `json:"channel_id,omitempty"`
	ChatSessionID   string                   `json:"chat_session_id,omitempty"`
	AgentID         string                   `json:"agent_id"`
	SourceMessageID string                   `json:"source_message_id,omitempty"`
	Reason          string                   `json:"reason"`
	RequiresWake    bool                     `json:"requires_wake"`
	Priority        int32                    `json:"priority"`
	SeqFrom         int64                    `json:"seq_from"`
	SeqTo           int64                    `json:"seq_to"`
	Messages        []ChannelMessageResponse `json:"messages,omitempty"`
	LeaseToken      string                   `json:"lease_token"`
	LeaseExpiresAt  string                   `json:"lease_expires_at"`
}

type AckAgentInboxEventRequest struct {
	DeliveryID  string `json:"delivery_id"`
	LeaseToken  string `json:"lease_token"`
	SeenUpToSeq int64  `json:"seen_up_to_seq"`
}

type FailAgentInboxEventRequest struct {
	DeliveryID string `json:"delivery_id"`
	LeaseToken string `json:"lease_token"`
	Error      string `json:"error"`
}

func (h *Handler) DrainAgentInboxByRuntime(w http.ResponseWriter, r *http.Request) {
	runtimeID := chi.URLParam(r, "runtimeId")
	runtime, ok := h.requireDaemonRuntimeAccess(w, r, runtimeID)
	if !ok {
		return
	}
	delivery, err := h.Queries.LeaseAgentInboxEventForRuntime(r.Context(), runtime.ID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeJSON(w, http.StatusOK, DrainAgentInboxResponse{Events: []AgentInboxEventResponse{}})
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to drain agent inbox")
		return
	}
	event, err := h.Queries.GetAgentInboxEvent(r.Context(), delivery.InboxEventID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load inbox event")
		return
	}
	if event.WorkspaceID != runtime.WorkspaceID {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	pending, _ := h.Queries.CountPendingAgentInboxEventsForRuntime(r.Context(), runtime.ID)
	respEvent := h.agentInboxEventResponse(r.Context(), event, delivery)
	writeJSON(w, http.StatusOK, DrainAgentInboxResponse{
		Events:      []AgentInboxEventResponse{respEvent},
		LastSeenSeq: event.SeqTo,
		HasMore:     pending > 0,
	})
}

func (h *Handler) AckAgentInboxEvent(w http.ResponseWriter, r *http.Request) {
	eventID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "eventId"), "event_id")
	if !ok {
		return
	}
	event, ok := h.requireDaemonInboxEventAccess(w, r, eventID)
	if !ok {
		return
	}
	var req AckAgentInboxEventRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	deliveryID, ok := parseUUIDOrBadRequest(w, req.DeliveryID, "delivery_id")
	if !ok {
		return
	}
	leaseToken, ok := parseUUIDOrBadRequest(w, req.LeaseToken, "lease_token")
	if !ok {
		return
	}
	seenUpToSeq := req.SeenUpToSeq
	if seenUpToSeq <= 0 || seenUpToSeq > event.SeqTo {
		seenUpToSeq = event.SeqTo
	}
	if seenUpToSeq < event.SeqTo {
		writeError(w, http.StatusConflict, "cannot ack before seeing the full event range")
		return
	}
	acked, err := h.Queries.AckAgentInboxDelivery(r.Context(), db.AckAgentInboxDeliveryParams{
		ID:           deliveryID,
		InboxEventID: event.ID,
		LeaseToken:   leaseToken,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusConflict, "delivery lease is no longer active")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to ack inbox delivery")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "acked_seq": acked.SeqTo})
}

func (h *Handler) FailAgentInboxEvent(w http.ResponseWriter, r *http.Request) {
	eventID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "eventId"), "event_id")
	if !ok {
		return
	}
	event, ok := h.requireDaemonInboxEventAccess(w, r, eventID)
	if !ok {
		return
	}
	var req FailAgentInboxEventRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	deliveryID, ok := parseUUIDOrBadRequest(w, req.DeliveryID, "delivery_id")
	if !ok {
		return
	}
	leaseToken, ok := parseUUIDOrBadRequest(w, req.LeaseToken, "lease_token")
	if !ok {
		return
	}
	errText := strings.TrimSpace(req.Error)
	if errText == "" {
		errText = "agent inbox delivery failed"
	}
	if _, err := h.Queries.FailAgentInboxDelivery(r.Context(), db.FailAgentInboxDeliveryParams{
		ID:           deliveryID,
		InboxEventID: event.ID,
		LastError:    strToText(errText),
		LeaseToken:   leaseToken,
	}); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusConflict, "delivery lease is no longer active")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to mark inbox delivery failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (h *Handler) agentInboxEventResponse(ctx context.Context, event db.AgentInboxEvent, delivery db.AgentEventDelivery) AgentInboxEventResponse {
	resp := AgentInboxEventResponse{
		ID:              uuidToString(event.ID),
		DeliveryID:      uuidToString(delivery.ID),
		AgentSessionID:  uuidToString(event.AgentSessionID),
		ConversationID:  uuidToString(event.ConversationID),
		ChannelID:       uuidToString(event.ChannelID),
		ChatSessionID:   uuidToString(event.ChatSessionID),
		AgentID:         uuidToString(event.AgentID),
		SourceMessageID: uuidToString(event.SourceMessageID),
		Reason:          event.Reason,
		RequiresWake:    event.RequiresWake,
		Priority:        event.Priority,
		SeqFrom:         event.SeqFrom,
		SeqTo:           event.SeqTo,
		LeaseToken:      uuidToString(delivery.LeaseToken),
		LeaseExpiresAt:  timestampToString(delivery.LeaseExpiresAt),
	}
	if event.ChannelID.Valid && event.SeqTo > 0 {
		from := event.SeqFrom - 1
		if from < 0 {
			from = 0
		}
		resp.Messages = h.channelAmbientUnreadMessages(ctx, h.DB, uuidToString(event.WorkspaceID), uuidToString(event.ChannelID), from, event.SeqTo, agentInboxDrainMessageLimit)
	}
	return resp
}

func (h *Handler) requireDaemonInboxEventAccess(w http.ResponseWriter, r *http.Request, eventID pgtype.UUID) (db.AgentInboxEvent, bool) {
	event, err := h.Queries.GetAgentInboxEvent(r.Context(), eventID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "not found")
			return db.AgentInboxEvent{}, false
		}
		writeError(w, http.StatusInternalServerError, "failed to load inbox event")
		return db.AgentInboxEvent{}, false
	}
	if !h.requireDaemonWorkspaceAccess(w, r, uuidToString(event.WorkspaceID)) {
		return db.AgentInboxEvent{}, false
	}
	return event, true
}
