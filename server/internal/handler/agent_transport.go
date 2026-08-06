package handler

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/messageparts"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

const (
	agentTransportActionSend           = "message_send"
	agentTransportActionReact          = "message_react"
	agentTransportActionRead           = "message_read"
	agentTransportActionSearch         = "message_search"
	agentTransportActionResolve        = "message_resolve"
	agentTransportActionThreadUnfollow = "thread_unfollow"

	agentTransportFreshnessHoldLimit = 3
)

type AgentTransportSendRequest struct {
	Target          string                 `json:"target"`
	Content         string                 `json:"content"`
	Parts           []protocol.MessagePart `json:"parts"`
	ClientMessageID string                 `json:"client_message_id"`
	SeenUpToSeq     int64                  `json:"seen_up_to_seq,omitempty"`
	ContextTarget   string                 `json:"context_target,omitempty"`
	BypassFreshness bool                   `json:"bypass_freshness,omitempty"`
}

// AgentTransportTargetRequest is an internal Credential Proxy preflight. It
// resolves the human-facing target spelling to the canonical coordinator key
// without reading or consuming any Message bodies.
type AgentTransportTargetRequest struct {
	Target string `json:"target"`
}

type AgentTransportTargetResponse struct {
	Target        string `json:"target"`
	ContextTarget string `json:"context_target"`
}

type AgentTransportSendResponse struct {
	Action              string                             `json:"action"`
	Target              string                             `json:"target"`
	Message             ChannelMessageResponse             `json:"message"`
	Created             bool                               `json:"created"`
	TransportID         string                             `json:"transport_id"`
	FreshnessResolution *AgentTransportFreshnessResolution `json:"freshnessResolution,omitempty"`
}

type AgentTransportFreshnessContextWindow struct {
	OldestSeq     int64  `json:"oldestSeq"`
	NewestSeq     int64  `json:"newestSeq"`
	OlderBoundary string `json:"olderBoundary"`
	NewerBoundary string `json:"newerBoundary"`
}

type AgentTransportFreshnessResolution struct {
	ProducerFactID                 string  `json:"producerFactId"`
	Outcome                        string  `json:"outcome"`
	FreshnessHoldResolutionSeconds float64 `json:"freshness_hold_resolution_seconds"`
	ResolutionMS                   int64   `json:"resolution_ms"`
}

type AgentTransportSendHeldResponse struct {
	Action              string                               `json:"action"`
	Target              string                               `json:"target"`
	State               string                               `json:"state"`
	Outcome             string                               `json:"outcome"`
	Subtype             string                               `json:"subtype"`
	Reason              string                               `json:"reason"`
	Decision            string                               `json:"decision"`
	ProducerFactID      string                               `json:"producerFactId"`
	AvailableActions    []string                             `json:"availableActions"`
	HeldMessages        []ChannelMessageResponse             `json:"heldMessages"`
	NewMessageCount     int64                                `json:"newMessageCount"`
	ShownMessageCount   int64                                `json:"shownMessageCount"`
	OmittedMessageCount int64                                `json:"omittedMessageCount"`
	SeenUpToSeq         int64                                `json:"seenUpToSeq"`
	LatestSeq           int64                                `json:"latestSeq"`
	TransportID         string                               `json:"transport_id"`
	ContextWindow       AgentTransportFreshnessContextWindow `json:"contextWindow"`
}

type AgentTransportReactRequest struct {
	MessageID string `json:"message_id"`
	Emoji     string `json:"emoji"`
	Remove    bool   `json:"remove"`
}

type AgentTransportReactResponse struct {
	Action    string                   `json:"action"`
	ChannelID string                   `json:"channel_id"`
	MessageID string                   `json:"message_id"`
	Emoji     string                   `json:"emoji"`
	Added     bool                     `json:"added"`
	Removed   bool                     `json:"removed"`
	Reaction  *ChannelReactionResponse `json:"reaction,omitempty"`
}

type AgentTransportReadRequest struct {
	Target string `json:"target"`
	Before string `json:"before,omitempty"`
	After  string `json:"after,omitempty"`
	Around string `json:"around,omitempty"`
	Limit  int    `json:"limit"`
}

type AgentTransportReadResponse struct {
	Action        string                   `json:"action"`
	Target        string                   `json:"target"`
	ChannelID     string                   `json:"channel_id"`
	ContextTarget string                   `json:"context_target"`
	Messages      []ChannelMessageResponse `json:"messages"`
	Limit         int                      `json:"limit"`
	SeenUpToSeq   int64                    `json:"seenUpToSeq"`
	TransportID   string                   `json:"transport_id"`
}

var (
	errAgentTransportReadAnchorInvalid   = errors.New("invalid read anchor")
	errAgentTransportReadAnchorNotFound  = errors.New("read anchor not found")
	errAgentTransportReadAnchorAmbiguous = errors.New("read anchor is ambiguous")
	decimalAgentMessageSequencePattern   = regexp.MustCompile(`^[0-9]+$`)
	shortAgentMessageIDPattern           = regexp.MustCompile(`^[0-9a-fA-F]{8,35}$`)
)

type agentTransportDraft struct {
	ID              pgtype.UUID
	Target          string
	ChannelID       pgtype.UUID
	ThreadRootID    pgtype.UUID
	Content         string
	Parts           []protocol.MessagePart
	ClientMessageID string
	SeenUpToSeq     int64
	HeldFromSeq     int64
	HeldToSeq       int64
	ShownFromSeq    pgtype.Int8
	ShownToSeq      pgtype.Int8
	DecisionFactID  string
}

type agentTransportFreshnessDecision struct {
	Hold        bool
	SeenUpToSeq int64
	LatestSeq   int64
	TotalNewer  int64
	Messages    []ChannelMessageResponse
	Omitted     int64
	ProducerID  string
}

type agentTransportFreshnessHoldError struct {
	decision    agentTransportFreshnessDecision
	transportID string
}

func (e *agentTransportFreshnessHoldError) Error() string {
	return "agent transport send held by freshness"
}

var errAgentTransportDraftNotFound = errors.New("saved draft not found")
var errAgentTransportSourceNotActive = errors.New("agent transport source is not active")
var errAgentTransportFreshnessDecisionProof = errors.New("freshness revision must use the returned ready command")

type agentTransportMessageResult struct {
	Message                  ChannelMessageResponse
	Created                  bool
	TransportID              string
	SentDraft                *agentTransportDraft
	FreshnessResolution      *AgentTransportFreshnessResolution
	FreshnessActivityEventID pgtype.UUID
	AgentDM                  agentDMSendReservation
}

type agentTransportFreshnessResolutionPublication struct {
	Source     agentTransportSource
	Target     agentTransportTarget
	Resolution AgentTransportFreshnessResolution
	EventID    pgtype.UUID
}

type AgentTransportSearchRequest struct {
	Target string `json:"target"`
	Query  string `json:"query"`
	Limit  int    `json:"limit"`
}

type AgentTransportSearchResponse struct {
	Action      string                       `json:"action"`
	Target      string                       `json:"target"`
	ChannelID   string                       `json:"channel_id"`
	Query       string                       `json:"query"`
	Total       int                          `json:"total"`
	Results     []ChannelMessageSearchResult `json:"results"`
	TransportID string                       `json:"transport_id"`
}

type AgentTransportResolveRequest struct {
	MessageID string `json:"message_id"`
}

type AgentTransportResolveTarget struct {
	ChannelID           string  `json:"channel_id"`
	ThreadRootMessageID *string `json:"thread_root_message_id,omitempty"`
}

type AgentTransportResolveResponse struct {
	Action  string                      `json:"action"`
	Target  AgentTransportResolveTarget `json:"target"`
	Message ChannelMessageResponse      `json:"message"`
}

type AgentTransportThreadUnfollowRequest struct {
	Target string `json:"target"`
}

type AgentTransportThreadUnfollowResponse struct {
	Action      string `json:"action"`
	Target      string `json:"target"`
	ChannelID   string `json:"channel_id"`
	MessageID   string `json:"message_id"`
	TransportID string `json:"transport_id"`
}

type agentTransportTarget struct {
	kind                chatOutputTargetKind
	channel             ChannelResponse
	recipientType       string
	recipientID         pgtype.UUID
	threadRoot          ChannelMessageResponse
	threadRootMessageID pgtype.UUID
	threadID            *string
	triggerDepth        int
	raw                 string
}

type agentTransportSource struct {
	task         db.AgentInboxEvent
	origin       chatOutputOrigin
	inboxEventID pgtype.UUID
}

func (h *Handler) agentTransportInitiatorUserID(r *http.Request, source agentTransportSource) (pgtype.UUID, bool) {
	switch r.Header.Get("X-Actor-Source") {
	case "task_token":
		return h.agentTaskInitiatorUserID(r, source.origin.workspaceID)
	case "agent_inbox_token", "agent_credential":
		return h.agentInboxInitiatorUserID(r, source.origin.workspaceID)
	default:
		return pgtype.UUID{}, false
	}
}

func (h *Handler) requireAgentTransportPublicResponseMode(ctx context.Context, source agentTransportSource) error {
	if !source.inboxEventID.Valid {
		return nil
	}
	var deliveryMode, responseMode string
	var channelID pgtype.UUID
	if err := h.DB.QueryRow(ctx, `
		SELECT delivery_mode, response_mode, channel_id
		FROM agent_inbox_event
		WHERE id = $1`, source.inboxEventID).Scan(&deliveryMode, &responseMode, &channelID); err != nil {
		return err
	}
	if responseMode == "public_response" {
		return nil
	}
	// LRM-1055: ambient observe wakes stay no_public_output for ordinary members,
	// but channel managers need send/react during GM/patrol ambient turns.
	if channelID.Valid && h.agentIsChannelManager(ctx, source.origin.workspaceID, channelID, source.origin.agentID) {
		return nil
	}
	_ = recordChannelDecisionAuditExec(ctx, h.DB, channelDecisionAuditEvent{
		WorkspaceID: source.origin.workspaceID, ChannelID: channelID, SourceKind: "agent_transport",
		EventType: "unauthorized_public_send_blocked", AgentID: source.origin.agentID, InboxEventID: source.inboxEventID,
		Payload: map[string]any{"response_mode": responseMode, "delivery_mode": deliveryMode},
	})
	return fmt.Errorf("agent inbox event response_mode %q does not grant public channel output", responseMode)
}

// requireAgentTransportVisibilityGrantActive checks the Collaboration turn
// grant. Channel Attention Round response grants were retired with the
// feature and its tables.
func (h *Handler) requireAgentTransportVisibilityGrantActive(ctx context.Context, source agentTransportSource) error {
	return h.requireAgentTransportTurnGrantActive(ctx, source)
}

func (h *Handler) requireAgentTransportTurnGrantActive(ctx context.Context, source agentTransportSource) error {
	var turnStatus, sessionStatus string
	var turnFresh, versionFresh bool
	err := h.DB.QueryRow(ctx, `
		SELECT turn.grant_status, session.status,
		       (turn.deadline_at IS NULL OR turn.deadline_at > now()),
		       turn.session_version = session.version
		FROM collaboration_turn turn
		JOIN collaboration_session session ON session.id = turn.session_id
		WHERE turn.inbox_event_id = $1 AND turn.agent_id = $2`, source.inboxEventID, source.origin.agentID).Scan(&turnStatus, &sessionStatus, &turnFresh, &versionFresh)
	if errorsIsNoRows(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if turnStatus != "granted" || sessionStatus != "active" || !turnFresh || !versionFresh {
		_ = recordChannelDecisionAuditExec(ctx, h.DB, channelDecisionAuditEvent{
			WorkspaceID: source.origin.workspaceID, ChannelID: source.origin.channelID, SourceKind: "collaboration_turn",
			EventType: "unauthorized_public_send_blocked", AgentID: source.origin.agentID, InboxEventID: source.inboxEventID,
			Payload: map[string]any{"reason": "turn_grant_stale", "turn_status": turnStatus, "session_status": sessionStatus, "turn_fresh": turnFresh, "version_fresh": versionFresh},
		})
		return fmt.Errorf("turn_grant is %s, session is %s, or grant is stale", turnStatus, sessionStatus)
	}
	return nil
}

func consumeAgentTransportVisibilityGrantTx(ctx context.Context, exec dbExecutor, source agentTransportSource, channelID, messageID pgtype.UUID) error {
	if !source.inboxEventID.Valid {
		return nil
	}
	var turnID, sessionID pgtype.UUID
	err := exec.QueryRow(ctx, `
		UPDATE collaboration_turn turn
		SET grant_status = 'consumed', result_message_id = $3, updated_at = now()
		FROM collaboration_session session
		WHERE turn.session_id = session.id
		  AND turn.inbox_event_id = $1
		  AND turn.agent_id = $2
		  AND turn.grant_status = 'granted'
		  AND session.status = 'active'
		  AND turn.session_version = session.version
		  AND (turn.deadline_at IS NULL OR turn.deadline_at > now())
		RETURNING turn.id, turn.session_id`, source.inboxEventID, source.origin.agentID, nullableUUID(messageID)).Scan(&turnID, &sessionID)
	if err != nil && !errorsIsNoRows(err) {
		return err
	}
	if err == nil {
		return recordChannelDecisionAuditExec(ctx, exec, channelDecisionAuditEvent{
			WorkspaceID: source.origin.workspaceID, ChannelID: channelID, SourceKind: "collaboration_turn", SourceID: turnID,
			EventType: "turn_grant_consumed", AgentID: source.origin.agentID, MessageID: messageID, InboxEventID: source.inboxEventID,
			Payload: map[string]any{"session_id": uuidToString(sessionID), "consumed_by": "agent_transport"},
		})
	}
	return nil
}

func (h *Handler) AgentTransportSendMessage(w http.ResponseWriter, r *http.Request) {
	source, ok := h.requireAgentTransportSource(w, r)
	if !ok {
		return
	}
	if err := h.requireAgentTransportPublicResponseMode(r.Context(), source); err != nil {
		writeError(w, http.StatusForbidden, err.Error())
		return
	}
	var req AgentTransportSendRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	onboarding, isChannelOnboarding, err := channelOnboardingForTransport(r.Context(), h.DB, source)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to validate channel onboarding send")
		return
	}
	if isChannelOnboarding {
		if err := h.requireActiveChannelOnboardingBeforeTarget(r.Context(), onboarding, source.inboxEventID); err != nil {
			if errors.Is(err, errChannelOnboardingExpired) {
				writeError(w, http.StatusConflict, errChannelOnboardingExpired.Error())
				return
			}
			writeError(w, http.StatusInternalServerError, "failed to validate channel onboarding membership generation")
			return
		}
	}
	content, parts, err := messageparts.Normalize(req.Content, req.Parts)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid message parts: "+err.Error())
		return
	}
	// Reference attachment resources from parts only (same contract as
	// channel/DM/thread user send). CLI --attachment-id sugar becomes parts; do not
	// dual-merge a sidecar attachment_ids field.
	attachmentIDs, ok := parseUUIDSliceOrBadRequest(w, attachmentIDsFromParts(parts), "attachment_id")
	if !ok {
		return
	}
	parts, err = h.normalizeLegacyAgentAudioVoiceReply(r.Context(), source, content, parts)
	if err != nil {
		slog.Warn("agent transport audio modality inspection failed", "task_id", uuidToString(source.task.ID), "agent_id", uuidToString(source.task.AgentID), "error", err)
		writeError(w, http.StatusInternalServerError, "failed to validate message attachments")
		return
	}
	content, parts, err = messageparts.Normalize(content, parts)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid message parts: "+err.Error())
		return
	}
	if strings.TrimSpace(content) == "" && len(parts) == 0 {
		writeError(w, http.StatusBadRequest, "content, sticker, or attachment is required")
		return
	}
	if len([]rune(content)) > channelMessageMaxLen {
		writeError(w, http.StatusBadRequest, "content is too long")
		return
	}
	clientMessageID := strings.TrimSpace(req.ClientMessageID)
	if isChannelOnboarding {
		clientMessageID = channelOnboardingClientMessageID(source.inboxEventID)
	}
	if clientMessageID == "" {
		writeError(w, http.StatusBadRequest, "client_message_id is required")
		return
	}
	if len([]rune(clientMessageID)) > channelClientMessageIDMaxLen {
		writeError(w, http.StatusBadRequest, "client_message_id is too long")
		return
	}
	target, err := h.resolveAgentTransportTarget(r.Context(), source.task, source.origin, req.Target, true)
	if err != nil {
		if errors.Is(err, errReminderSendOutsideAnchor) {
			writeError(w, http.StatusBadRequest, errReminderSendOutsideAnchor.Error())
			return
		}
		writeError(w, http.StatusBadRequest, "invalid or ambiguous target; use #channel, #channel:<threadId>, or `dm:@<handle>`")
		return
	}
	if expected := strings.TrimSpace(req.ContextTarget); expected != "" && expected != agentTransportCanonicalMessageTarget(target) {
		writeError(w, http.StatusConflict, "canonical send target changed during freshness preflight")
		return
	}
	if isChannelOnboarding && !channelOnboardingTargetMatches(onboarding, target) {
		writeError(w, http.StatusBadRequest, "channel onboarding must send to the joined channel main timeline")
		return
	}
	parts, err = h.enforceAgentTransportVoiceReply(r.Context(), source, target, content, parts)
	if err != nil {
		slog.Warn("agent transport voice modality inspection failed", "task_id", uuidToString(source.task.ID), "agent_id", uuidToString(source.task.AgentID), "error", err)
		writeError(w, http.StatusInternalServerError, "failed to preserve reply modality")
		return
	}
	content, parts, err = messageparts.Normalize(content, parts)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid message parts: "+err.Error())
		return
	}
	_, err = h.finalizedAgentTransportInsertInput(r.Context(), source, target, content, parts, clientMessageID)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	initiatorID := h.channelInitiatorForTask(r.Context(), source.task)
	if err := h.requireAgentTransportVisibilityGrantActive(r.Context(), source); err != nil {
		writeError(w, http.StatusForbidden, err.Error())
		return
	}
	seenUpToSeq := int64(-1)
	if !isChannelOnboarding && !req.BypassFreshness {
		seenUpToSeq, err = h.agentTransportSeenUpToSeq(r.Context(), source, target.raw, req.SeenUpToSeq)
		if err != nil {
			slog.Warn("agent transport freshness check failed", "task_id", uuidToString(source.task.ID), "agent_id", uuidToString(source.task.AgentID), "error", err)
			writeError(w, http.StatusInternalServerError, "failed to send message")
			return
		}
	}
	result, err := h.createAgentTransportMessage(r.Context(), source, target, content, parts, attachmentIDs, clientMessageID, seenUpToSeq, initiatorID, nil)
	if err != nil {
		var freshnessHold *agentTransportFreshnessHoldError
		if errors.As(err, &freshnessHold) {
			writeAgentTransportHeldResponse(w, target, freshnessHold.decision, freshnessHold.transportID)
			return
		}
		if errors.Is(err, errAgentTransportSourceNotActive) {
			writeError(w, http.StatusConflict, errAgentTransportSourceNotActive.Error())
			return
		}
		if errors.Is(err, errChannelClientMessageConflict) {
			writeError(w, http.StatusConflict, "client_message_id conflicts with an existing channel message")
			return
		}
		if errors.Is(err, errChannelAttachmentUnavailable) {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if errors.Is(err, errChannelOnboardingExpired) {
			writeError(w, http.StatusConflict, errChannelOnboardingExpired.Error())
			return
		}
		var wrongTurn *agentDMTurnError
		if errors.As(err, &wrongTurn) {
			writeJSON(w, http.StatusConflict, map[string]any{
				"error":             wrongTurn.Error(),
				"state":             "waiting_for_peer",
				"expected_agent_id": uuidToString(wrongTurn.ExpectedSender),
			})
			return
		}
		slog.Warn("agent transport send failed", "task_id", uuidToString(source.task.ID), "agent_id", uuidToString(source.task.AgentID), "error", err)
		writeError(w, http.StatusInternalServerError, "failed to send message")
		return
	}
	writeJSON(w, http.StatusCreated, AgentTransportSendResponse{
		Action:              agentTransportActionSend,
		Target:              target.raw,
		Message:             result.Message,
		Created:             result.Created,
		TransportID:         result.TransportID,
		FreshnessResolution: result.FreshnessResolution,
	})
	// Record a message-sent activity event (agent replied via multica message send).
	if !result.Created {
		return
	}
	h.recordAgentActivityEvent(r.Context(), h.DB,
		source.origin.workspaceID, source.task.AgentID, source.task.RuntimeID, nullableTaskIDForTransportSource(source),
		activityKindText, "message_sent", "info",
		"channel", parseUUID(target.channel.ID), target.raw,
		"", agentVisibleOutputActivityText(result.Message.Content, result.Message.Parts, result.Message.Attachments),
		map[string]any{
			"message_id": result.Message.ID,
			"created":    result.Created,
		},
	)
}

func (h *Handler) AgentTransportResolveMessageTarget(w http.ResponseWriter, r *http.Request) {
	source, ok := h.requireAgentTransportSource(w, r)
	if !ok {
		return
	}
	var req AgentTransportTargetRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if strings.TrimSpace(req.Target) == "" {
		writeError(w, http.StatusBadRequest, "target is required")
		return
	}
	target, err := h.resolveAgentTransportTarget(r.Context(), source.task, source.origin, req.Target, true)
	if err != nil {
		if errors.Is(err, errReminderSendOutsideAnchor) {
			writeError(w, http.StatusBadRequest, errReminderSendOutsideAnchor.Error())
			return
		}
		writeError(w, http.StatusBadRequest, "invalid or ambiguous target")
		return
	}
	writeJSON(w, http.StatusOK, AgentTransportTargetResponse{
		Target:        target.raw,
		ContextTarget: agentTransportCanonicalMessageTarget(target),
	})
}

// normalizeLegacyAgentAudioVoiceReply upgrades the one message shape emitted
// by runtimes that predate `message send --voice`. The attachment is inspected
// only as an owned modality signal; clients synthesize playback from content.
func (h *Handler) normalizeLegacyAgentAudioVoiceReply(
	ctx context.Context,
	source agentTransportSource,
	content string,
	parts []protocol.MessagePart,
) ([]protocol.MessagePart, error) {
	if strings.TrimSpace(content) == "" || len(parts) != 2 {
		return parts, nil
	}
	var attachmentID string
	textParts := 0
	for _, part := range parts {
		switch part.Type {
		case protocol.MessagePartTypeText:
			if strings.TrimSpace(part.Text) != "" {
				textParts++
			}
		case protocol.MessagePartTypeAttachment:
			attachmentID = strings.TrimSpace(part.AttachmentID)
		default:
			return parts, nil
		}
	}
	if textParts != 1 || attachmentID == "" {
		return parts, nil
	}

	var contentType string
	err := h.DB.QueryRow(ctx, `
		SELECT content_type
		FROM attachment
		WHERE id = $1
		  AND workspace_id = $2
		  AND uploader_type = 'agent'
		  AND uploader_id = $3`,
		parseUUID(attachmentID), source.origin.workspaceID, source.origin.agentID,
	).Scan(&contentType)
	if errorsIsNoRows(err) {
		return parts, nil
	}
	if err != nil {
		return nil, fmt.Errorf("inspect legacy agent audio attachment: %w", err)
	}
	if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(contentType)), "audio/") {
		return parts, nil
	}

	return append(parts, protocol.MessagePart{Type: protocol.MessagePartTypeVoice}), nil
}

func (h *Handler) AgentTransportReactMessage(w http.ResponseWriter, r *http.Request) {
	source, ok := h.requireAgentTransportSource(w, r)
	if !ok {
		return
	}
	var req AgentTransportReactRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	emoji, err := normalizeAgentTransportReactionEmoji(req.Emoji)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	message, err := h.resolveAgentTransportMessage(r.Context(), source, req.MessageID)
	if err != nil {
		h.writeAgentTransportMessageResolveError(w, source, err, "failed to react to message")
		return
	}
	response := AgentTransportReactResponse{
		Action:    agentTransportActionReact,
		ChannelID: message.ChannelID,
		MessageID: message.ID,
		Emoji:     emoji,
	}
	if req.Remove {
		removed, err := h.removeAgentTransportCanonicalReaction(r.Context(), source, message, emoji)
		if err != nil {
			h.writeAgentTransportMessageResolveError(w, source, err, "failed to react to message")
			return
		}
		response.Removed = removed
	} else {
		reaction, added, err := h.addAgentTransportCanonicalReaction(r.Context(), source, message, emoji)
		if err != nil {
			h.writeAgentTransportMessageResolveError(w, source, err, "failed to react to message")
			return
		}
		response.Added = added
		response.Reaction = &reaction
	}
	writeJSON(w, http.StatusOK, response)
}

func (h *Handler) AgentTransportReadMessages(w http.ResponseWriter, r *http.Request) {
	source, ok := h.requireAgentTransportSource(w, r)
	if !ok {
		return
	}
	var req AgentTransportReadRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	anchorMode, anchorRaw, err := req.anchor()
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	limit := clampAgentTransportLimit(req.Limit, 20)
	target, err := h.resolveAgentTransportTarget(r.Context(), source.task, source.origin, req.Target, false)
	if err != nil {
		if errors.Is(err, errReminderSendOutsideAnchor) {
			writeError(w, http.StatusBadRequest, errReminderSendOutsideAnchor.Error())
			return
		}
		writeError(w, http.StatusBadRequest, "invalid target")
		return
	}
	messages, err := h.readAgentTransportMessages(r.Context(), target, anchorMode, anchorRaw, limit)
	if err != nil {
		switch {
		case errors.Is(err, errAgentTransportReadAnchorInvalid):
			writeError(w, http.StatusBadRequest, err.Error())
		case errors.Is(err, errAgentTransportReadAnchorNotFound):
			writeError(w, http.StatusNotFound, err.Error())
		case errors.Is(err, errAgentTransportReadAnchorAmbiguous):
			writeError(w, http.StatusConflict, err.Error())
		default:
			slog.Warn("agent transport read failed", "task_id", uuidToString(source.task.ID), "error", err)
			writeError(w, http.StatusInternalServerError, "failed to read messages")
		}
		return
	}
	h.decorateAgentTransportMessages(r.Context(), target.channel.WorkspaceID, messages)
	messageIDs := channelMessageIDs(messages)
	seenUpToSeq := maxChannelMessageSeq(messages)
	contextTarget := agentTransportCanonicalMessageTarget(target)
	transportID, err := h.recordAgentTransportAudit(r.Context(), source, agentTransportActionRead, target.raw, parseUUID(target.channel.ID), pgtype.UUID{}, "", map[string]any{
		"channel_id":      target.channel.ID,
		"message_ids":     messageIDs,
		"limit":           limit,
		"seen_up_to_seq":  seenUpToSeq,
		"context_target":  contextTarget,
		"thread_root_id":  target.threadRootMessageID,
		"target_snapshot": target.raw,
	})
	if err != nil {
		slog.Warn("agent transport read audit failed", "task_id", uuidToString(source.task.ID), "error", err)
		writeError(w, http.StatusInternalServerError, "failed to record message read")
		return
	}
	response := AgentTransportReadResponse{
		Action:        agentTransportActionRead,
		Target:        target.raw,
		ChannelID:     target.channel.ID,
		ContextTarget: contextTarget,
		Messages:      messages,
		Limit:         limit,
		SeenUpToSeq:   seenUpToSeq,
		TransportID:   transportID,
	}
	writeJSON(w, http.StatusOK, response)
}

// enforceAgentTransportVoiceReply preserves the delivery modality selected by
// a human voice message at the final visible-message boundary. Prompt guidance
// remains useful for the model, but it cannot be the only guarantee: runtimes
// may omit --voice while still producing a valid text send.
//
// The source and destination must be the same timeline. This prevents a voice
// trigger from changing proactive output to another channel or thread.
func (h *Handler) enforceAgentTransportVoiceReply(
	ctx context.Context,
	source agentTransportSource,
	target agentTransportTarget,
	content string,
	parts []protocol.MessagePart,
) ([]protocol.MessagePart, error) {
	if strings.TrimSpace(content) == "" || channelMessageHasVoicePart(parts) {
		return parts, nil
	}

	sourceMessageID, err := h.agentTransportSourceMessageID(ctx, source)
	if err != nil {
		return nil, err
	}
	if !sourceMessageID.Valid {
		return parts, nil
	}
	trigger, found := h.channelMessageByID(
		ctx,
		uuidToString(source.origin.workspaceID),
		uuidToString(source.origin.channelID),
		uuidToString(sourceMessageID),
	)
	if !found {
		return nil, fmt.Errorf("source channel message %s not found", uuidToString(sourceMessageID))
	}
	return agentTransportVoiceReplyParts(trigger, target, content, parts), nil
}

func (h *Handler) agentTransportSourceMessageID(ctx context.Context, source agentTransportSource) (pgtype.UUID, error) {
	if !source.inboxEventID.Valid {
		return h.channelReactionTargetFromPrompt(ctx, source.task.ChatSessionID, source.task.ID), nil
	}
	var sourceMessageID pgtype.UUID
	if err := h.DB.QueryRow(ctx, `
		SELECT source_message_id
		FROM agent_inbox_event
		WHERE id = $1`, source.inboxEventID).Scan(&sourceMessageID); err != nil {
		return pgtype.UUID{}, fmt.Errorf("load inbox source message: %w", err)
	}
	return sourceMessageID, nil
}

func agentTransportVoiceReplyTargetMatches(trigger ChannelMessageResponse, target agentTransportTarget) bool {
	if !channelMessageIsHumanAuthored(trigger.Type) || !channelMessageHasVoicePart(trigger.Parts) || trigger.ChannelID != target.channel.ID {
		return false
	}
	if trigger.ThreadRootMessageID == nil {
		if target.kind != chatOutputTargetThread {
			return true
		}
		return trigger.ID != "" && trigger.ID == uuidToString(target.threadRootMessageID)
	}
	return target.kind == chatOutputTargetThread && *trigger.ThreadRootMessageID == uuidToString(target.threadRootMessageID)
}

func agentTransportVoiceReplyParts(trigger ChannelMessageResponse, target agentTransportTarget, content string, parts []protocol.MessagePart) []protocol.MessagePart {
	if !agentTransportHasReplyText(content, parts) || channelMessageHasVoicePart(parts) || !agentTransportVoiceReplyTargetMatches(trigger, target) {
		return parts
	}
	out := append([]protocol.MessagePart(nil), parts...)
	if !agentTransportPartsHaveText(out) {
		out = append(out, protocol.MessagePart{Type: protocol.MessagePartTypeText, Text: content})
	}
	return append(out, protocol.MessagePart{Type: protocol.MessagePartTypeVoice})
}

func agentTransportHasReplyText(content string, parts []protocol.MessagePart) bool {
	if strings.TrimSpace(content) == "" {
		return false
	}
	if agentTransportPartsHaveText(parts) {
		return true
	}
	// Normalize derives content from sticker alt text for sticker-only sends.
	// That label is not an answer transcript and must not be synthesized as
	// speech. Explicit text alongside a sticker differs from the fallback.
	return strings.TrimSpace(content) != messageparts.FallbackContent(parts)
}

func agentTransportPartsHaveText(parts []protocol.MessagePart) bool {
	for _, part := range parts {
		if part.Type == protocol.MessagePartTypeText && strings.TrimSpace(part.Text) != "" {
			return true
		}
	}
	return false
}

func (h *Handler) AgentTransportSearchMessages(w http.ResponseWriter, r *http.Request) {
	source, ok := h.requireAgentTransportSource(w, r)
	if !ok {
		return
	}
	var req AgentTransportSearchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	query := strings.TrimSpace(req.Query)
	if query == "" {
		writeError(w, http.StatusBadRequest, "query is required")
		return
	}
	limit := clampAgentTransportLimit(req.Limit, 50)
	target, err := h.resolveAgentTransportTarget(r.Context(), source.task, source.origin, req.Target, false)
	if err != nil {
		if errors.Is(err, errReminderSendOutsideAnchor) {
			writeError(w, http.StatusBadRequest, errReminderSendOutsideAnchor.Error())
			return
		}
		writeError(w, http.StatusBadRequest, "invalid target")
		return
	}
	total, results, err := h.searchAgentTransportMessages(r.Context(), target, query, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to search messages")
		return
	}
	resultIDs := make([]string, 0, len(results))
	for _, result := range results {
		resultIDs = append(resultIDs, result.MessageID)
	}
	transportID, err := h.recordAgentTransportAudit(r.Context(), source, agentTransportActionSearch, target.raw, parseUUID(target.channel.ID), pgtype.UUID{}, "", map[string]any{
		"channel_id":   target.channel.ID,
		"query":        query,
		"result_ids":   resultIDs,
		"result_count": len(results),
		"total":        total,
		"limit":        limit,
	})
	if err != nil {
		slog.Warn("agent transport search audit failed", "task_id", uuidToString(source.task.ID), "error", err)
		writeError(w, http.StatusInternalServerError, "failed to record message search")
		return
	}
	writeJSON(w, http.StatusOK, AgentTransportSearchResponse{
		Action:      agentTransportActionSearch,
		Target:      target.raw,
		ChannelID:   target.channel.ID,
		Query:       query,
		Total:       total,
		Results:     results,
		TransportID: transportID,
	})
}

var errAgentTransportMessageNotFound = errors.New("message not found")
var errAgentTransportMessageAmbiguous = errors.New("message id prefix is ambiguous")
var errAgentTransportMessageIDInvalid = errors.New("invalid message id")

func (h *Handler) writeAgentTransportMessageResolveError(w http.ResponseWriter, source agentTransportSource, err error, failure string) {
	switch {
	case errors.Is(err, errAgentTransportMessageNotFound):
		// The same response covers a missing Message and an inaccessible target.
		writeError(w, http.StatusNotFound, "message not found")
	case errors.Is(err, errAgentTransportMessageAmbiguous):
		writeError(w, http.StatusConflict, "message id prefix is ambiguous; use more characters")
	case errors.Is(err, errAgentTransportMessageIDInvalid):
		writeError(w, http.StatusBadRequest, err.Error())
	default:
		slog.Warn("agent transport canonical message lookup failed", "task_id", uuidToString(source.task.ID), "error", err)
		writeError(w, http.StatusInternalServerError, failure)
	}
}

func (h *Handler) AgentTransportResolveMessage(w http.ResponseWriter, r *http.Request) {
	source, ok := h.requireAgentTransportSource(w, r)
	if !ok {
		return
	}
	var req AgentTransportResolveRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	message, err := h.resolveAgentTransportMessage(r.Context(), source, req.MessageID)
	if err != nil {
		h.writeAgentTransportMessageResolveError(w, source, err, "failed to resolve message")
		return
	}
	messages := []ChannelMessageResponse{message}
	h.decorateAgentTransportMessages(r.Context(), message.WorkspaceID, messages)
	message = messages[0]
	writeJSON(w, http.StatusOK, AgentTransportResolveResponse{
		Action: agentTransportActionResolve,
		Target: AgentTransportResolveTarget{
			ChannelID:           message.ChannelID,
			ThreadRootMessageID: message.ThreadRootMessageID,
		},
		Message: message,
	})
}

func (h *Handler) resolveAgentTransportMessage(ctx context.Context, source agentTransportSource, rawMessageID string) (ChannelMessageResponse, error) {
	fullID, prefix, err := parseAgentTransportMessageID(rawMessageID)
	if err != nil {
		return ChannelMessageResponse{}, err
	}
	args := []any{source.origin.workspaceID, source.origin.agentID}
	whereID := ""
	if fullID != "" {
		args = append(args, parseUUID(fullID))
		whereID = fmt.Sprintf("m.id = $%d", len(args))
	} else {
		args = append(args, prefix)
		whereID = fmt.Sprintf("replace(m.id::text, '-', '') LIKE $%d || '%%'", len(args))
	}
	rows, err := h.DB.Query(ctx, `
		SELECT `+channelMessageColumnList+`
		FROM channel_message m
		WHERE m.workspace_id = $1
		  AND m.author_type IN ('user', 'agent')
		  AND m.deleted_at IS NULL
		  AND EXISTS (
			SELECT 1
			FROM channel_member visible
			WHERE visible.workspace_id = m.workspace_id
			  AND visible.channel_id = m.channel_id
			  AND visible.member_type = 'agent'
			  AND visible.member_id = $2
		  )
		  AND `+whereID+`
		ORDER BY m.id ASC
		LIMIT 2`, args...)
	if err != nil {
		return ChannelMessageResponse{}, err
	}
	defer rows.Close()
	matches := make([]ChannelMessageResponse, 0, 2)
	for rows.Next() {
		message, err := scanChannelMessage(rows)
		if err != nil {
			return ChannelMessageResponse{}, err
		}
		matches = append(matches, message)
	}
	if err := rows.Err(); err != nil {
		return ChannelMessageResponse{}, err
	}
	switch len(matches) {
	case 0:
		return ChannelMessageResponse{}, errAgentTransportMessageNotFound
	case 1:
		return matches[0], nil
	default:
		return ChannelMessageResponse{}, errAgentTransportMessageAmbiguous
	}
}

func parseAgentTransportMessageID(raw string) (fullID, prefix string, err error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", "", fmt.Errorf("%w: message id is required", errAgentTransportMessageIDInvalid)
	}
	if parsed, parseErr := uuid.Parse(raw); parseErr == nil {
		return parsed.String(), "", nil
	}
	prefix = strings.ToLower(strings.ReplaceAll(raw, "-", ""))
	if len(prefix) < 4 {
		return "", "", fmt.Errorf("%w: message id must be a full UUID or at least 4 hex characters", errAgentTransportMessageIDInvalid)
	}
	for _, char := range prefix {
		if !((char >= '0' && char <= '9') || (char >= 'a' && char <= 'f')) {
			return "", "", fmt.Errorf("%w: message id must be a full UUID or at least 4 hex characters", errAgentTransportMessageIDInvalid)
		}
	}
	return "", prefix, nil
}

func normalizeAgentTransportReactionEmoji(raw string) (string, error) {
	emoji := strings.TrimSpace(raw)
	switch strings.ToLower(emoji) {
	case "+1":
		emoji = "👍"
	case "-1":
		emoji = "👎"
	}
	// Text-style variation selectors make the same reaction look different
	// across clients. Store the emoji-style form so add/remove share one key.
	emoji = strings.ReplaceAll(emoji, "\ufe0e", "\ufe0f")
	if !utf8.ValidString(emoji) || !validAgentTransportReactionEmoji(emoji) {
		return "", errors.New("emoji must be a single valid emoji reaction")
	}
	return emoji, nil
}

func validAgentTransportReactionEmoji(emoji string) bool {
	runes := []rune(emoji)
	if len(runes) == 0 || len(runes) > 32 {
		return false
	}
	if isAgentTransportKeycap(runes) {
		return true
	}

	baseCount := 0
	regionalCount := 0
	afterJoiner := false
	for _, r := range runes {
		switch {
		case isAgentTransportEmojiModifier(r):
			if baseCount == 0 || afterJoiner {
				return false
			}
		case r == '\ufe0f' || isAgentTransportEmojiTag(r):
			if baseCount == 0 || afterJoiner {
				return false
			}
		case r == '\u200d':
			if baseCount == 0 || afterJoiner {
				return false
			}
			afterJoiner = true
		case isAgentTransportEmojiBase(r):
			if baseCount > 0 && !afterJoiner {
				if !(isAgentTransportRegionalIndicator(r) && regionalCount == 1 && baseCount == 1) {
					return false
				}
			}
			baseCount++
			if isAgentTransportRegionalIndicator(r) {
				regionalCount++
			}
			afterJoiner = false
		default:
			return false
		}
	}
	return baseCount > 0 && !afterJoiner && (regionalCount == 0 || regionalCount == 2)
}

func isAgentTransportKeycap(runes []rune) bool {
	if len(runes) == 2 {
		return isAgentTransportKeycapBase(runes[0]) && runes[1] == '\u20e3'
	}
	return len(runes) == 3 && isAgentTransportKeycapBase(runes[0]) && runes[1] == '\ufe0f' && runes[2] == '\u20e3'
}

func isAgentTransportKeycapBase(r rune) bool {
	return (r >= '0' && r <= '9') || r == '#' || r == '*'
}

func isAgentTransportEmojiModifier(r rune) bool {
	return r >= 0x1f3fb && r <= 0x1f3ff
}

func isAgentTransportEmojiTag(r rune) bool {
	return r >= 0xe0020 && r <= 0xe007f
}

func isAgentTransportRegionalIndicator(r rune) bool {
	return r >= 0x1f1e6 && r <= 0x1f1ff
}

func isAgentTransportEmojiBase(r rune) bool {
	switch {
	case r >= 0x1f000 && r <= 0x1faff:
		return true
	case r >= 0x2194 && r <= 0x21ff:
		return true
	case r >= 0x2300 && r <= 0x27ff:
		return true
	case r >= 0x2934 && r <= 0x2935:
		return true
	case r >= 0x2b05 && r <= 0x2b55:
		return true
	case r == 0x00a9 || r == 0x00ae || r == 0x3030 || r == 0x303d || r == 0x3297 || r == 0x3299:
		return true
	default:
		return false
	}
}

func (h *Handler) AgentTransportUnfollowThread(w http.ResponseWriter, r *http.Request) {
	source, ok := h.requireAgentTransportSource(w, r)
	if !ok {
		return
	}
	var req AgentTransportThreadUnfollowRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	target, err := h.resolveAgentTransportTarget(r.Context(), source.task, source.origin, req.Target, false)
	if err != nil {
		if errors.Is(err, errReminderSendOutsideAnchor) {
			writeError(w, http.StatusBadRequest, errReminderSendOutsideAnchor.Error())
			return
		}
		writeError(w, http.StatusBadRequest, "invalid target")
		return
	}
	if !target.threadRootMessageID.Valid {
		writeError(w, http.StatusBadRequest, "invalid target")
		return
	}
	channelID := parseUUID(target.channel.ID)
	changed, err := h.unfollowChannelThreadAgent(r.Context(), channelID, target.threadRootMessageID, source.origin.agentID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to unfollow thread")
		return
	}
	transportID, err := h.recordAgentTransportAudit(r.Context(), source, agentTransportActionThreadUnfollow, target.raw, channelID, target.threadRootMessageID, "", map[string]any{
		"channel_id": target.channel.ID,
		"message_id": uuidToString(target.threadRootMessageID),
	})
	if err != nil {
		slog.Warn("agent transport thread unfollow audit failed", "task_id", uuidToString(source.task.ID), "error", err)
		writeError(w, http.StatusInternalServerError, "failed to record thread unfollow")
		return
	}
	if changed {
		h.emitAgentThreadUnfollowedEvent(w, r.Context(), target.channel.WorkspaceID, channelID, target.threadRootMessageID, source.origin.agentID)
	}
	writeJSON(w, http.StatusOK, AgentTransportThreadUnfollowResponse{
		Action:      agentTransportActionThreadUnfollow,
		Target:      target.raw,
		ChannelID:   target.channel.ID,
		MessageID:   uuidToString(target.threadRootMessageID),
		TransportID: transportID,
	})
}

func (h *Handler) requireAgentTransportSource(w http.ResponseWriter, r *http.Request) (agentTransportSource, bool) {
	switch r.Header.Get("X-Actor-Source") {
	case "task_token":
		task, origin, ok := h.requireAgentTransportTask(w, r)
		if !ok {
			return agentTransportSource{}, false
		}
		return agentTransportSource{task: task, origin: origin}, true
	case "agent_inbox_token":
		task, origin, inboxEventID, ok := h.requireAgentTransportInboxEvent(w, r)
		if !ok {
			return agentTransportSource{}, false
		}
		return agentTransportSource{task: task, origin: origin, inboxEventID: inboxEventID}, true
	case "agent_credential":
		task, origin, inboxEventID, ok := h.requireAgentCredentialTransportInboxEvent(w, r)
		if !ok {
			return agentTransportSource{}, false
		}
		return agentTransportSource{task: task, origin: origin, inboxEventID: inboxEventID}, true
	default:
		writeError(w, http.StatusForbidden, "agent transport requires a task token")
		return agentTransportSource{}, false
	}
}

func (h *Handler) requireAgentTransportTask(w http.ResponseWriter, r *http.Request) (db.AgentInboxEvent, chatOutputOrigin, bool) {
	if r.Header.Get("X-Actor-Source") != "task_token" {
		writeError(w, http.StatusForbidden, "agent transport requires a task token")
		return db.AgentInboxEvent{}, chatOutputOrigin{}, false
	}
	workspaceID := ctxWorkspaceID(r.Context())
	wsUUID, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace id")
	if !ok {
		return db.AgentInboxEvent{}, chatOutputOrigin{}, false
	}
	agentID, ok := parseUUIDOrBadRequest(w, r.Header.Get("X-Agent-ID"), "agent id")
	if !ok {
		return db.AgentInboxEvent{}, chatOutputOrigin{}, false
	}
	taskID, ok := parseUUIDOrBadRequest(w, r.Header.Get("X-Task-ID"), "task id")
	if !ok {
		return db.AgentInboxEvent{}, chatOutputOrigin{}, false
	}
	task, err := h.Queries.GetAgentTask(r.Context(), taskID)
	if err != nil || task.AgentID != agentID {
		writeError(w, http.StatusForbidden, "task token does not match this agent task")
		return db.AgentInboxEvent{}, chatOutputOrigin{}, false
	}
	if task.Status != "draining" {
		writeError(w, http.StatusConflict, "agent task is not active")
		return db.AgentInboxEvent{}, chatOutputOrigin{}, false
	}
	origin, ok := h.chatOutputOriginForTask(r.Context(), task)
	if !ok || origin.workspaceID != wsUUID || origin.agentID != agentID {
		writeError(w, http.StatusForbidden, "agent task is not a channel task")
		return db.AgentInboxEvent{}, chatOutputOrigin{}, false
	}
	return task, origin, true
}

func (h *Handler) requireAgentTransportInboxEvent(w http.ResponseWriter, r *http.Request) (db.AgentInboxEvent, chatOutputOrigin, pgtype.UUID, bool) {
	workspaceID := ctxWorkspaceID(r.Context())
	wsUUID, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace id")
	if !ok {
		return db.AgentInboxEvent{}, chatOutputOrigin{}, pgtype.UUID{}, false
	}
	agentID, ok := parseUUIDOrBadRequest(w, r.Header.Get("X-Agent-ID"), "agent id")
	if !ok {
		return db.AgentInboxEvent{}, chatOutputOrigin{}, pgtype.UUID{}, false
	}
	eventID, ok := parseUUIDOrBadRequest(w, r.Header.Get("X-Agent-Inbox-Event-ID"), "inbox event id")
	if !ok {
		return db.AgentInboxEvent{}, chatOutputOrigin{}, pgtype.UUID{}, false
	}
	deliveryID, ok := parseUUIDOrBadRequest(w, r.Header.Get("X-Agent-Inbox-Delivery-ID"), "delivery id")
	if !ok {
		return db.AgentInboxEvent{}, chatOutputOrigin{}, pgtype.UUID{}, false
	}
	event, err := h.Queries.GetAgentInboxEvent(r.Context(), eventID)
	if err != nil || event.AgentID != agentID || event.WorkspaceID != wsUUID {
		writeError(w, http.StatusForbidden, "inbox token does not match this agent event")
		return db.AgentInboxEvent{}, chatOutputOrigin{}, pgtype.UUID{}, false
	}
	var deliveryActive bool
	if err := h.DB.QueryRow(r.Context(), `
		SELECT EXISTS (
			SELECT 1
			FROM agent_event_delivery d
			WHERE d.id = $1
			  AND d.inbox_event_id = $2
			  AND d.status IN ('leased', 'processing')
			  AND d.lease_expires_at > now()
		)`, deliveryID, event.ID).Scan(&deliveryActive); err != nil || !deliveryActive {
		writeError(w, http.StatusConflict, "agent inbox delivery is not active")
		return db.AgentInboxEvent{}, chatOutputOrigin{}, pgtype.UUID{}, false
	}
	task := agentInboxSyntheticTask(event, h.runtimeIDForAgentInboxDelivery(r.Context(), deliveryID))
	origin, ok := h.chatOutputOriginForTask(r.Context(), task)
	if !ok || origin.workspaceID != wsUUID || origin.agentID != agentID {
		// Channel-less wakes (issue-only) legitimately have no transport origin.
		writeError(w, http.StatusForbidden, "agent inbox event has no channel transport origin")
		return db.AgentInboxEvent{}, chatOutputOrigin{}, pgtype.UUID{}, false
	}
	return task, origin, event.ID, true
}

func (h *Handler) requireAgentCredentialTransportInboxEvent(w http.ResponseWriter, r *http.Request) (db.AgentInboxEvent, chatOutputOrigin, pgtype.UUID, bool) {
	event, runtimeID, ok := h.requireAgentCredentialActiveInboxDelivery(w, r)
	if !ok {
		return db.AgentInboxEvent{}, chatOutputOrigin{}, pgtype.UUID{}, false
	}
	workspaceID := ctxWorkspaceID(r.Context())
	wsUUID, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace id")
	if !ok {
		return db.AgentInboxEvent{}, chatOutputOrigin{}, pgtype.UUID{}, false
	}
	agentID, ok := parseUUIDOrBadRequest(w, r.Header.Get("X-Agent-ID"), "agent id")
	if !ok {
		return db.AgentInboxEvent{}, chatOutputOrigin{}, pgtype.UUID{}, false
	}
	task := agentInboxSyntheticTask(event, runtimeID)
	origin, ok := h.chatOutputOriginForTask(r.Context(), task)
	if !ok || origin.workspaceID != wsUUID || origin.agentID != agentID {
		// LRM-1055: missing chat_session alone is no longer a hard reject; reject
		// only when neither session nor channel_id yields a surface origin.
		writeError(w, http.StatusForbidden, "agent inbox event has no channel transport origin")
		return db.AgentInboxEvent{}, chatOutputOrigin{}, pgtype.UUID{}, false
	}
	return task, origin, event.ID, true
}

// requireAgentCredentialActiveInboxDelivery validates the short-lived delivery
// fence presented alongside a durable agent credential. It intentionally does
// not require a chat transport origin: non-chat agent APIs, including research
// result submission, still need freshness and ownership checks but do not send
// a message to a channel.
func (h *Handler) requireAgentCredentialActiveInboxDelivery(w http.ResponseWriter, r *http.Request) (db.AgentInboxEvent, pgtype.UUID, bool) {
	if strings.TrimSpace(r.Header.Get("X-Agent-Inbox-Event-ID")) == "" ||
		strings.TrimSpace(r.Header.Get("X-Agent-Inbox-Delivery-ID")) == "" ||
		strings.TrimSpace(r.Header.Get("X-Agent-Inbox-Lease-Token")) == "" {
		writeError(w, http.StatusForbidden, "agent credential transport requires active inbox delivery")
		return db.AgentInboxEvent{}, pgtype.UUID{}, false
	}

	workspaceID := ctxWorkspaceID(r.Context())
	wsUUID, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace id")
	if !ok {
		return db.AgentInboxEvent{}, pgtype.UUID{}, false
	}
	agentID, ok := parseUUIDOrBadRequest(w, r.Header.Get("X-Agent-ID"), "agent id")
	if !ok {
		return db.AgentInboxEvent{}, pgtype.UUID{}, false
	}
	eventID, ok := parseUUIDOrBadRequest(w, r.Header.Get("X-Agent-Inbox-Event-ID"), "inbox event id")
	if !ok {
		return db.AgentInboxEvent{}, pgtype.UUID{}, false
	}
	deliveryID, ok := parseUUIDOrBadRequest(w, r.Header.Get("X-Agent-Inbox-Delivery-ID"), "delivery id")
	if !ok {
		return db.AgentInboxEvent{}, pgtype.UUID{}, false
	}
	leaseToken, ok := parseUUIDOrBadRequest(w, r.Header.Get("X-Agent-Inbox-Lease-Token"), "lease token")
	if !ok {
		return db.AgentInboxEvent{}, pgtype.UUID{}, false
	}

	event, err := h.Queries.GetAgentInboxEvent(r.Context(), eventID)
	if err != nil || event.AgentID != agentID || event.WorkspaceID != wsUUID {
		writeError(w, http.StatusForbidden, "agent credential does not match this agent event")
		return db.AgentInboxEvent{}, pgtype.UUID{}, false
	}
	var runtimeID pgtype.UUID
	if err := h.DB.QueryRow(r.Context(), `
		SELECT d.runtime_id
		FROM agent_event_delivery d
		WHERE d.id = $1
		  AND d.inbox_event_id = $2
		  AND d.lease_token = $3
		  AND d.status IN ('leased', 'processing')
		  AND d.lease_expires_at > now()
		  AND EXISTS (
		    SELECT 1
		    FROM agent_inbox_event e
		    WHERE e.id = d.inbox_event_id
		      AND e.agent_session_id = d.agent_session_id
		      AND e.status = 'draining'
		  )
		  AND NOT EXISTS (
		    SELECT 1
		    FROM agent_event_delivery newer
		    WHERE newer.inbox_event_id = d.inbox_event_id
		      AND newer.id <> d.id
		      AND newer.created_at >= d.created_at
		  )`, deliveryID, event.ID, leaseToken).Scan(&runtimeID); err != nil {
		writeError(w, http.StatusConflict, "agent inbox delivery is not active")
		return db.AgentInboxEvent{}, pgtype.UUID{}, false
	}
	return event, runtimeID, true
}

func (h *Handler) resolveAgentTransportTarget(ctx context.Context, task db.AgentInboxEvent, origin chatOutputOrigin, rawTarget string, createDM bool) (agentTransportTarget, error) {
	rawTarget = strings.TrimSpace(rawTarget)
	if rawTarget == "" {
		return agentTransportTarget{}, errChatOutputInvalidTarget
	}
	resolved, err := h.resolveChatOutputTarget(ctx, origin, rawTarget)
	if err != nil {
		return agentTransportTarget{}, err
	}
	out := agentTransportTarget{
		kind: resolved.kind, channel: resolved.channel,
		recipientType: resolved.recipientType, recipientID: resolved.recipientID,
		threadRoot: resolved.threadRoot, raw: rawTarget,
	}
	switch resolved.kind {
	case chatOutputTargetDM:
		creatorID := task.InitiatorUserID
		if !creatorID.Valid {
			creatorID = h.agentOwnerID(ctx, origin.workspaceID, origin.agentID)
		}
		ch, ok := h.agentDMChannel(ctx, origin.workspaceID, origin.agentID, resolved.recipientType, resolved.recipientID, creatorID, createDM)
		if !ok {
			return agentTransportTarget{}, errChatOutputInvalidTarget
		}
		out.channel = ch
	case chatOutputTargetThread:
		threadID := resolved.threadRoot.ThreadID
		if threadID == nil || strings.TrimSpace(*threadID) == "" {
			fresh := uuid.NewString()
			threadID = &fresh
		}
		out.threadRootMessageID = parseUUID(resolved.threadRoot.ID)
		out.threadID = threadID
		out.triggerDepth = resolved.threadRoot.TriggerDepth + 1
	}
	// Reminder wake: hard pin to msg-id surface (thread→thread, main→main).
	threadRootID := ""
	if out.threadRootMessageID.Valid {
		threadRootID = uuidToString(out.threadRootMessageID)
	} else if strings.TrimSpace(out.threadRoot.ID) != "" {
		threadRootID = strings.TrimSpace(out.threadRoot.ID)
	}
	if err := enforceReminderAnchorSurface(task, out.channel.ID, out.kind, threadRootID); err != nil {
		return agentTransportTarget{}, err
	}
	return out, nil
}

func (h *Handler) agentOwnerID(ctx context.Context, workspaceID, agentID pgtype.UUID) pgtype.UUID {
	var ownerID pgtype.UUID
	_ = h.DB.QueryRow(ctx, `
		SELECT owner_id
		FROM agent
		WHERE id = $1 AND workspace_id = $2 AND archived_at IS NULL`,
		agentID, workspaceID).Scan(&ownerID)
	return ownerID
}

func (h *Handler) agentDMChannel(ctx context.Context, workspaceID, senderAgentID pgtype.UUID, recipientType string, recipientID, creatorID pgtype.UUID, create bool) (ChannelResponse, bool) {
	switch recipientType {
	case "user":
		return h.agentHumanDMChannel(ctx, workspaceID, senderAgentID, recipientID, create)
	case "agent":
		return h.agentAgentDMChannel(ctx, workspaceID, senderAgentID, recipientID, creatorID, create)
	default:
		return ChannelResponse{}, false
	}
}

func (h *Handler) agentHumanDMChannel(ctx context.Context, workspaceID, agentID, userID pgtype.UUID, create bool) (ChannelResponse, bool) {
	workspaceIDText := uuidToString(workspaceID)
	canonical := dmCanonicalName("user", uuidToString(userID), "agent", uuidToString(agentID))
	if ch, found := h.findDMChannel(ctx, workspaceIDText, canonical); found {
		if create {
			h.clearDMPeerHidden(ctx, workspaceIDText, uuidToString(userID), dmPeerRef{Type: "agent", ID: agentID})
		}
		return ch, true
	}
	if !create {
		return ChannelResponse{}, false
	}
	return h.ensureAgentHumanDMChannel(ctx, workspaceID, agentID, userID)
}

func (h *Handler) agentAgentDMChannel(ctx context.Context, workspaceID, senderAgentID, recipientAgentID, creatorID pgtype.UUID, create bool) (ChannelResponse, bool) {
	workspaceIDText := uuidToString(workspaceID)
	canonical := dmCanonicalName("agent", uuidToString(senderAgentID), "agent", uuidToString(recipientAgentID))
	if ch, found := h.findDMChannel(ctx, workspaceIDText, canonical); found {
		if h.agentAgentDMChannelMatches(ctx, workspaceID, parseUUID(ch.ID), senderAgentID, recipientAgentID) {
			return ch, true
		}
		return ChannelResponse{}, false
	}
	if !create || !creatorID.Valid {
		return ChannelResponse{}, false
	}
	return h.createDMChannel(ctx, nil, workspaceIDText, uuidToString(creatorID), canonical, []dmMember{
		{memberType: "agent", memberID: senderAgentID},
		{memberType: "agent", memberID: recipientAgentID},
	})
}

func (h *Handler) agentAgentDMChannelMatches(ctx context.Context, workspaceID, channelID, firstAgentID, secondAgentID pgtype.UUID) bool {
	var matches bool
	err := h.DB.QueryRow(ctx, `
		SELECT count(*) = 2
		  AND count(*) FILTER (WHERE member_type = 'agent') = 2
		  AND bool_and(member_id = ANY($3::uuid[]))
		FROM channel_member
		WHERE workspace_id = $1 AND channel_id = $2`,
		workspaceID, channelID, []pgtype.UUID{firstAgentID, secondAgentID},
	).Scan(&matches)
	return err == nil && matches
}

// agentTransportMessageAfterInsert runs inside the Message insert transaction.
// It is intentionally limited to durable database state: realtime delivery and
// all other effects remain after the transaction commits.
type agentTransportMessageAfterInsert func(context.Context, pgx.Tx, ChannelMessageResponse) error

func (h *Handler) createAgentTransportMessage(ctx context.Context, source agentTransportSource, target agentTransportTarget, content string, parts []protocol.MessagePart, attachmentIDs []pgtype.UUID, clientMessageID string, seenUpToSeq int64, initiatorID pgtype.UUID, afterInsert agentTransportMessageAfterInsert) (agentTransportMessageResult, error) {
	input, err := h.finalizedAgentTransportInsertInput(ctx, source, target, content, parts, clientMessageID)
	if err != nil {
		return agentTransportMessageResult{}, err
	}
	if seenUpToSeq == 0 {
		var err error
		seenUpToSeq, err = h.agentTransportSeenUpToSeq(ctx, source, target.raw, 0)
		if err != nil {
			return agentTransportMessageResult{}, err
		}
	}
	result, err := h.insertAgentTransportMessageWithAudit(ctx, source, target, input, content, parts, attachmentIDs, clientMessageID, seenUpToSeq, afterInsert)
	if err != nil {
		return agentTransportMessageResult{}, err
	}
	if result.FreshnessActivityEventID.Valid {
		h.publishAgentTransportFreshnessResolutionActivity(ctx, source, target, result.FreshnessActivityEventID)
	}
	if result.FreshnessResolution != nil {
		h.Metrics.ObserveFreshnessHoldResolution(
			result.FreshnessResolution.Outcome,
			result.FreshnessResolution.FreshnessHoldResolutionSeconds,
		)
	}
	{
		msgs := []ChannelMessageResponse{result.Message}
		h.attachChannelMessageAuthorAvatars(ctx, uuidToString(source.origin.workspaceID), msgs)
		h.attachChannelMessageAttachments(ctx, uuidToString(source.origin.workspaceID), msgs)
		result.Message = msgs[0]
	}
	if result.Created {
		if target.threadRootMessageID.Valid {
			h.followChannelThreadAgent(ctx, input.ChannelID, target.threadRootMessageID, source.origin.agentID)
		}
		_, _ = h.DB.Exec(ctx, `UPDATE channel SET updated_at = now() WHERE id = $1`, input.ChannelID)
		if target.channel.Kind == "dm" {
			h.clearDMHiddenForChannelMembers(ctx, target.channel.WorkspaceID, input.ChannelID)
		}
		h.publishChannelToMembers(ctx, protocol.EventChannelMessage, target.channel.WorkspaceID, "agent", uuidToString(source.origin.agentID), input.ChannelID, result.Message)
		// Ack path: WS already published so the channel can render immediately.
		// Mention wake / ambient delivery / Feishu are O(agents)/network — same
		// LRM-272 contract as human SendChannelMessage; do not inflate
		// `multica message send` RTT (Activity "Running command").
		if target.channel.Kind == "group" {
			ch := target.channel
			msg := result.Message
			threadRoot := target.threadRootMessageID
			h.runAfterChannelMessageAck(ctx, func(ctx context.Context) {
				if threadRoot.Valid {
					h.dispatchChannelThreadReplyMentions(ctx, ch, msg, initiatorID)
				} else {
					h.dispatchChannelMentions(ctx, ch, msg, initiatorID)
				}
				h.sendChannelMessageToFeishu(ctx, ch, msg.AuthorName, msg.Content)
			})
		} else if target.channel.Kind == "dm" && target.recipientType == "agent" {
			msg := result.Message
			reservation := result.AgentDM
			h.runAfterChannelMessageAck(ctx, func(ctx context.Context) {
				lowID, highID, ok := normalizedAgentDMPair(source.origin.agentID, target.recipientID)
				if ok {
					h.publishAgentDMToOwners(
						ctx, target.channel, lowID, highID, protocol.EventChannelMessage, msg,
					)
				}
				h.dispatchAgentDMAgentReply(ctx, source, target, msg, reservation, initiatorID)
			})
		}
	}
	return result, nil
}

// finalizedAgentTransportInsertInput is the write boundary for every visible
// agent-transport message. Drafts keep raw author intent; immediate sends rebuild destination-scoped reference anchors here immediately
// before persistence.
func (h *Handler) finalizedAgentTransportInsertInput(ctx context.Context, source agentTransportSource, target agentTransportTarget, content string, parts []protocol.MessagePart, clientMessageID string) (channelMessageInsertInput, error) {
	content, parts, err := h.finalizeAgentChannelMessage(ctx, target.channel, content, parts)
	if err != nil {
		return channelMessageInsertInput{}, err
	}
	return channelMessageInsertInput{
		ChannelID:           parseUUID(target.channel.ID),
		WorkspaceID:         source.origin.workspaceID,
		AuthorID:            source.origin.agentID,
		AuthorName:          h.agentName(ctx, source.origin.agentID),
		Content:             content,
		Parts:               parts,
		ThreadRootMessageID: target.threadRootMessageID,
		ThreadID:            target.threadID,
		TriggerDepth:        target.triggerDepth,
		ClientMessageID:     &clientMessageID,
	}, nil
}

func (h *Handler) insertAgentTransportMessageWithAudit(ctx context.Context, source agentTransportSource, target agentTransportTarget, input channelMessageInsertInput, draftContent string, draftParts []protocol.MessagePart, attachmentIDs []pgtype.UUID, clientMessageID string, seenUpToSeq int64, afterInsert agentTransportMessageAfterInsert) (agentTransportMessageResult, error) {
	tx, err := h.TxStarter.Begin(ctx)
	if err != nil {
		return agentTransportMessageResult{}, err
	}
	if source.task.Reason != channelOnboardingReason {
		if err := h.lockAgentTransportSource(ctx, tx, source); err != nil {
			_ = tx.Rollback(ctx)
			return agentTransportMessageResult{}, err
		}
	}
	if err := h.lockAgentTransportTargetForInsert(ctx, tx, target); err != nil {
		_ = tx.Rollback(ctx)
		return agentTransportMessageResult{}, err
	}
	if err := h.lockAgentTransportDraftSource(ctx, tx, source, target); err != nil {
		_ = tx.Rollback(ctx)
		return agentTransportMessageResult{}, err
	}
	if err := h.requireAgentTransportSourceActiveWithExec(ctx, tx, source); err != nil {
		_ = tx.Rollback(ctx)
		return agentTransportMessageResult{}, err
	}
	onboarding, isChannelOnboarding, err := channelOnboardingForTransport(ctx, tx, source)
	if err != nil {
		_ = tx.Rollback(ctx)
		return agentTransportMessageResult{}, err
	}
	if isChannelOnboarding {
		if !channelOnboardingTargetMatches(onboarding, target) {
			_ = tx.Rollback(ctx)
			return agentTransportMessageResult{}, errors.New("channel onboarding target changed before send")
		}
		active, err := channelOnboardingGenerationActiveTx(ctx, tx, onboarding.ID, onboarding.ChannelID, onboarding.AgentID, true)
		if err != nil {
			_ = tx.Rollback(ctx)
			return agentTransportMessageResult{}, err
		}
		if !active {
			if err := expireChannelOnboardingForInboxEventTx(ctx, tx, onboarding, source.inboxEventID); err != nil {
				_ = tx.Rollback(ctx)
				return agentTransportMessageResult{}, err
			}
			if err := tx.Commit(ctx); err != nil {
				return agentTransportMessageResult{}, err
			}
			return agentTransportMessageResult{}, errChannelOnboardingExpired
		}
	}
	if existing, found, err := h.findAgentChannelMessageByClientIDWithExec(ctx, tx, input.WorkspaceID, input.ChannelID, input.AuthorID, clientMessageID); err != nil {
		_ = tx.Rollback(ctx)
		return agentTransportMessageResult{}, err
	} else if found {
		result, err := h.completeDuplicateAgentTransportMessageWithExec(ctx, tx, source, target, input, attachmentIDs, existing, clientMessageID, nil)
		if err != nil {
			_ = tx.Rollback(ctx)
			return agentTransportMessageResult{}, err
		}
		if err := tx.Commit(ctx); err != nil {
			return agentTransportMessageResult{}, err
		}
		return result, nil
	}
	if seenUpToSeq > 0 {
		decision, err := h.agentTransportFreshnessDecisionWithSeen(ctx, tx, source, target, seenUpToSeq)
		if err != nil {
			_ = tx.Rollback(ctx)
			return agentTransportMessageResult{}, err
		}
		if decision.Hold {
			decision, transportID, emitted, err := h.holdAgentTransportSendWithExec(ctx, tx, source, target, draftContent, draftParts, clientMessageID, decision)
			if err != nil {
				_ = tx.Rollback(ctx)
				return agentTransportMessageResult{}, err
			}
			if err := tx.Commit(ctx); err != nil {
				return agentTransportMessageResult{}, err
			}
			if emitted {
				h.recordAgentTransportFreshnessHoldActivity(ctx, source, target, decision, transportID)
			}
			return agentTransportMessageResult{}, &agentTransportFreshnessHoldError{decision: decision, transportID: transportID}
		}
	}
	reservation, err := h.reserveAgentDMSendTx(ctx, tx, source, target)
	if err != nil {
		_ = tx.Rollback(ctx)
		return agentTransportMessageResult{}, err
	}
	inserted, err := insertChannelMessageWithPartsExec(ctx, tx, input.ChannelID, input.WorkspaceID, "agent", input.AuthorID, input.AuthorName, input.Content, input.Parts, "multica", nil, input.ClientMessageID, pgtype.UUID{}, pgtype.UUID{}, nil, input.ThreadRootMessageID, input.ThreadID, input.TriggerDepth)
	if err != nil {
		_ = tx.Rollback(ctx)
		if isUniqueViolation(err) {
			return h.resolveDuplicateAgentTransportMessageAtomic(ctx, source, target, input, attachmentIDs, clientMessageID, nil)
		}
		return agentTransportMessageResult{}, err
	}
	msg := inserted.Message
	if afterInsert != nil {
		if err := afterInsert(ctx, tx, msg); err != nil {
			_ = tx.Rollback(ctx)
			return agentTransportMessageResult{}, err
		}
	}
	if err := h.finishAgentDMSendTx(ctx, tx, reservation, parseUUID(msg.ID)); err != nil {
		_ = tx.Rollback(ctx)
		return agentTransportMessageResult{}, err
	}
	if len(attachmentIDs) > 0 {
		qtx := h.Queries.WithTx(tx)
		if err := linkOwnedAttachmentsToChannelMessage(ctx, qtx, parseUUID(msg.ID), source.origin.workspaceID, "agent", source.origin.agentID, attachmentIDs); err != nil {
			_ = tx.Rollback(ctx)
			return agentTransportMessageResult{}, err
		}
	}
	auditContext := map[string]any{
		"channel_id":        target.channel.ID,
		"message_id":        msg.ID,
		"client_message_id": clientMessageID,
		"created":           true,
		"seq":               msg.Seq,
		"thread_root_id":    msg.ThreadRootMessageID,
	}
	transportID, err := h.recordAgentTransportAuditExec(ctx, tx, source, agentTransportActionSend, target.raw, input.ChannelID, parseUUID(msg.ID), clientMessageID, auditContext)
	if err != nil {
		_ = tx.Rollback(ctx)
		return agentTransportMessageResult{}, err
	}
	if err := consumeAgentTransportVisibilityGrantTx(ctx, tx, source, input.ChannelID, parseUUID(msg.ID)); err != nil {
		_ = tx.Rollback(ctx)
		return agentTransportMessageResult{}, err
	}
	if err := h.deleteAgentTransportDraftWithExec(ctx, tx, source, target.raw); err != nil {
		_ = tx.Rollback(ctx)
		return agentTransportMessageResult{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return agentTransportMessageResult{}, err
	}
	return agentTransportMessageResult{
		Message:     msg,
		Created:     true,
		TransportID: transportID,
		AgentDM:     reservation,
	}, nil
}

func (h *Handler) lockAgentTransportTargetForInsert(ctx context.Context, exec dbExecutor, target agentTransportTarget) error {
	rootID := uuidToString(target.threadRootMessageID)
	if rootID == "" {
		rootID = "main"
	}
	_, err := exec.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1), hashtext($2))`, target.channel.ID, rootID)
	return err
}

func (h *Handler) lockAgentTransportSource(ctx context.Context, exec dbExecutor, source agentTransportSource) error {
	key := fmt.Sprintf(
		"%s:%s:%s",
		uuidToString(source.origin.workspaceID),
		uuidToString(source.origin.agentID),
		agentTransportSourceDecisionKey(source),
	)
	_, err := exec.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('agent_transport_source'), hashtext($1))`, key)
	return err
}

func (h *Handler) lockAgentTransportDraftSource(ctx context.Context, exec dbExecutor, source agentTransportSource, target agentTransportTarget) error {
	_, err := exec.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1), hashtext($2))`, agentTransportSourceDecisionKey(source), strings.TrimSpace(target.raw))
	return err
}

func (h *Handler) requireAgentTransportSourceActiveWithExec(ctx context.Context, exec dbExecutor, source agentTransportSource) error {
	var status string
	if err := exec.QueryRow(ctx, `
		SELECT status
		FROM agent_inbox_event
		WHERE id = $1
		  AND workspace_id = $2
		  AND agent_id = $3`,
		source.task.ID, source.origin.workspaceID, source.origin.agentID,
	).Scan(&status); err != nil {
		if errorsIsNoRows(err) {
			return errAgentTransportSourceNotActive
		}
		return err
	}
	if status != "draining" {
		return errAgentTransportSourceNotActive
	}
	return nil
}

func (h *Handler) agentTransportFreshnessResolutionWithExec(
	ctx context.Context,
	exec dbExecutor,
	source agentTransportSource,
	target agentTransportTarget,
	draft agentTransportDraft,
	outcome string,
) (*AgentTransportFreshnessResolution, error) {
	var seconds float64
	err := exec.QueryRow(ctx, `
		SELECT GREATEST(
			EXTRACT(EPOCH FROM (clock_timestamp() - created_at)),
			0
		)::double precision
		FROM agent_task_transport_audit
		WHERE workspace_id = $1
		  AND agent_id = $2
		  AND action = 'message_send'
		  AND target = $3
		  AND (($4::uuid IS NOT NULL AND task_id = $4) OR ($5::uuid IS NOT NULL AND inbox_event_id = $5))
		  AND COALESCE(context_pack->>'held', 'false') = 'true'
		  AND context_pack->>'subtype' = 'freshness'
		  AND context_pack->>'producer_fact_id' = $6
		ORDER BY created_at DESC, id DESC
		LIMIT 1`,
		source.origin.workspaceID,
		source.origin.agentID,
		target.raw,
		nullableTaskIDForTransportSource(source),
		nullableInboxEventIDForTransportSource(source),
		draft.DecisionFactID,
	).Scan(&seconds)
	if err != nil {
		return nil, err
	}
	return &AgentTransportFreshnessResolution{
		ProducerFactID:                 draft.DecisionFactID,
		Outcome:                        outcome,
		FreshnessHoldResolutionSeconds: seconds,
		ResolutionMS:                   int64(seconds * 1000),
	}, nil
}

func (h *Handler) resolvedAgentTransportFreshnessProducerWithExec(
	ctx context.Context,
	exec dbExecutor,
	source agentTransportSource,
	target agentTransportTarget,
	seenUpToSeq int64,
) (string, bool, error) {
	var producerFactID string
	err := exec.QueryRow(ctx, `
		SELECT hold.context_pack->>'producer_fact_id'
		FROM agent_task_transport_audit hold
		WHERE hold.workspace_id = $1
		  AND hold.agent_id = $2
		  AND hold.action = 'message_send'
		  AND hold.target = $3
		  AND (($4::uuid IS NOT NULL AND hold.task_id = $4) OR ($5::uuid IS NOT NULL AND hold.inbox_event_id = $5))
		  AND COALESCE(hold.context_pack->>'held', 'false') = 'true'
		  AND hold.context_pack->>'subtype' = 'freshness'
		  AND hold.context_pack->>'latest_seq' = $6
		  AND EXISTS (
		    SELECT 1
		    FROM agent_task_transport_audit resolution
		    WHERE resolution.workspace_id = hold.workspace_id
		      AND resolution.agent_id = hold.agent_id
		      AND resolution.target = hold.target
		      AND resolution.action = 'message_send'
		      AND (($4::uuid IS NOT NULL AND resolution.task_id = $4) OR ($5::uuid IS NOT NULL AND resolution.inbox_event_id = $5))
		      AND COALESCE(resolution.context_pack->>'freshness_resolution', 'false') = 'true'
		      AND resolution.context_pack->>'producer_fact_id' = hold.context_pack->>'producer_fact_id'
		  )
		ORDER BY hold.created_at DESC, hold.id DESC
		LIMIT 1`,
		source.origin.workspaceID,
		source.origin.agentID,
		target.raw,
		nullableTaskIDForTransportSource(source),
		nullableInboxEventIDForTransportSource(source),
		fmt.Sprint(seenUpToSeq),
	).Scan(&producerFactID)
	if err != nil {
		if errorsIsNoRows(err) {
			return "", false, nil
		}
		return "", false, err
	}
	return producerFactID, true, nil
}

// abandonAgentTransportFreshnessDraftsWithExec resolves every source-scoped
// draft that is still present when its execution completes. There is no Raft
// abandon command: finishing the source without either ready send command is
// the durable "do not send" decision. Call this before locking/acking the
// delivery so send and completion share target/source advisory-lock order.
func (h *Handler) abandonAgentTransportFreshnessDraftsWithExec(
	ctx context.Context,
	exec dbExecutor,
	event db.AgentInboxEvent,
	runtimeID pgtype.UUID,
) ([]agentTransportFreshnessResolutionPublication, error) {
	if event.Reason == channelOnboardingReason {
		return nil, nil
	}
	task := agentInboxSyntheticTask(event, runtimeID)
	origin := chatOutputOrigin{
		channelID:   event.ChannelID,
		workspaceID: event.WorkspaceID,
		agentID:     event.AgentID,
	}
	taskSource := agentTransportSource{task: task, origin: origin}
	inboxSource := agentTransportSource{task: task, origin: origin, inboxEventID: event.ID}
	// Completion must own both source namespaces before it enumerates drafts.
	// This closes the absent -> first-hold transition: a send that started from
	// a stale pre-ACK access check cannot create its first draft after this
	// scan, because it must acquire the same source lock before checking the
	// source's committed status.
	if err := h.lockAgentTransportSource(ctx, exec, taskSource); err != nil {
		return nil, err
	}
	if err := h.lockAgentTransportSource(ctx, exec, inboxSource); err != nil {
		return nil, err
	}
	rows, err := exec.Query(ctx, `
		SELECT target, channel_id, thread_root_message_id, task_id IS NOT NULL
		FROM agent_transport_draft
		WHERE workspace_id = $1
		  AND agent_id = $2
		  AND (task_id = $3 OR inbox_event_id = $3)
		ORDER BY channel_id ASC, thread_root_message_id ASC NULLS FIRST, target ASC`,
		event.WorkspaceID, event.AgentID, event.ID,
	)
	if err != nil {
		return nil, err
	}
	type candidate struct {
		target       string
		channelID    pgtype.UUID
		threadRootID pgtype.UUID
		taskScoped   bool
	}
	var candidates []candidate
	for rows.Next() {
		var item candidate
		if err := rows.Scan(&item.target, &item.channelID, &item.threadRootID, &item.taskScoped); err != nil {
			rows.Close()
			return nil, err
		}
		candidates = append(candidates, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()

	publications := make([]agentTransportFreshnessResolutionPublication, 0, len(candidates))
	for _, item := range candidates {
		source := taskSource
		if !item.taskScoped {
			source = inboxSource
		}
		target := agentTransportTarget{
			channel: ChannelResponse{
				ID:          uuidToString(item.channelID),
				WorkspaceID: uuidToString(event.WorkspaceID),
			},
			threadRootMessageID: item.threadRootID,
			raw:                 item.target,
		}
		if err := h.lockAgentTransportTargetForInsert(ctx, exec, target); err != nil {
			return nil, err
		}
		if err := h.lockAgentTransportDraftSource(ctx, exec, source, target); err != nil {
			return nil, err
		}
		draft, found, err := h.loadAgentTransportDraftWithExec(ctx, exec, source, item.target)
		if err != nil {
			return nil, err
		}
		if !found {
			continue
		}
		resolution, err := h.agentTransportFreshnessResolutionWithExec(
			ctx, exec, source, target, draft, "abandoned",
		)
		if err != nil {
			return nil, err
		}
		transportID, err := h.recordAgentTransportAuditExec(
			ctx, exec, source, agentTransportActionSend, target.raw,
			item.channelID, pgtype.UUID{}, draft.ClientMessageID,
			map[string]any{
				"channel_id":                        uuidToString(item.channelID),
				"thread_root_id":                    uuidToString(item.threadRootID),
				"client_message_id":                 draft.ClientMessageID,
				"created":                           false,
				"freshness_resolution":              true,
				"producer_fact_id":                  resolution.ProducerFactID,
				"outcome":                           resolution.Outcome,
				"freshness_hold_resolution_seconds": resolution.FreshnessHoldResolutionSeconds,
				"resolution_ms":                     resolution.ResolutionMS,
			},
		)
		if err != nil {
			return nil, err
		}
		eventID, err := h.insertAgentTransportFreshnessResolutionActivityWithExec(
			ctx, exec, source, target, *resolution, transportID, pgtype.UUID{},
		)
		if err != nil {
			return nil, err
		}
		if err := h.deleteAgentTransportDraftWithExec(ctx, exec, source, target.raw); err != nil {
			return nil, err
		}
		publications = append(publications, agentTransportFreshnessResolutionPublication{
			Source:     source,
			Target:     target,
			Resolution: *resolution,
			EventID:    eventID,
		})
	}
	return publications, nil
}

func (h *Handler) insertAgentTransportFreshnessResolutionActivityWithExec(
	ctx context.Context,
	exec dbExecutor,
	source agentTransportSource,
	target agentTransportTarget,
	resolution AgentTransportFreshnessResolution,
	transportID string,
	messageID pgtype.UUID,
) (pgtype.UUID, error) {
	message := agentTransportFreshnessResolutionActivityMessage(resolution.Outcome)
	details := map[string]any{
		"producer_fact_id":                  resolution.ProducerFactID,
		"outcome":                           resolution.Outcome,
		"freshness_hold_resolution_seconds": resolution.FreshnessHoldResolutionSeconds,
		"resolution_ms":                     resolution.ResolutionMS,
		"target":                            target.raw,
		"transport_id":                      transportID,
	}
	if messageID.Valid {
		details["message_id"] = uuidToString(messageID)
	}
	eventID, inserted := insertAgentActivityEvent(ctx, exec,
		source.origin.workspaceID, source.task.AgentID, source.task.RuntimeID, nullableTaskIDForTransportSource(source),
		activityKindText, "send_freshness_resolved", "info",
		"channel", parseUUID(target.channel.ID), target.raw,
		"", message, details,
	)
	if !inserted {
		return pgtype.UUID{}, errors.New("failed to persist freshness resolution activity")
	}
	return eventID, nil
}

func agentTransportFreshnessResolutionActivityMessage(outcome string) string {
	if outcome == "abandoned" {
		return "Held message was not sent"
	}
	return "Freshness hold resolved"
}

func (h *Handler) publishAgentTransportFreshnessResolutionActivity(
	ctx context.Context,
	source agentTransportSource,
	target agentTransportTarget,
	eventID pgtype.UUID,
) {
	if h == nil || h.Bus == nil || !eventID.Valid {
		return
	}
	workspaceID := uuidToString(source.origin.workspaceID)
	event := h.hydrateAgentActivityTimelineEvent(ctx, workspaceID, source.origin.agentID, eventID)
	targetRef := AgentActivityTargetRef{
		Kind: "channel",
		ID:   stringPtr(target.channel.ID),
		Slug: stringPtr(target.raw),
	}
	h.publishAgentActivityRealtimeEvent(ctx, workspaceID, uuidToString(source.origin.agentID), uuidToString(eventID), event, targetRef)
}

func (h *Handler) publishAgentTransportFreshnessResolutions(
	ctx context.Context,
	publications []agentTransportFreshnessResolutionPublication,
) {
	for _, publication := range publications {
		h.publishAgentTransportFreshnessResolutionActivity(
			ctx, publication.Source, publication.Target, publication.EventID,
		)
		h.Metrics.ObserveFreshnessHoldResolution(
			publication.Resolution.Outcome,
			publication.Resolution.FreshnessHoldResolutionSeconds,
		)
	}
}

func (h *Handler) completeDuplicateAgentTransportMessageWithExec(ctx context.Context, exec dbExecutor, source agentTransportSource, target agentTransportTarget, input channelMessageInsertInput, attachmentIDs []pgtype.UUID, existing ChannelMessageResponse, clientMessageID string, sentDraft *agentTransportDraft) (agentTransportMessageResult, error) {
	_, isChannelOnboarding, err := channelOnboardingForTransport(ctx, exec, source)
	if err != nil {
		return agentTransportMessageResult{}, err
	}
	if isChannelOnboarding {
		// The deterministic onboarding client id represents one canonical
		// visible decision. A retry after the first transaction committed must
		// reuse both the message and its audit evidence; recording another audit
		// would make one decision look like multiple sends.
		var transportID pgtype.UUID
		if err := exec.QueryRow(ctx, `
			SELECT id
			FROM agent_task_transport_audit
			WHERE inbox_event_id = $1
			  AND action = 'message_send'
			  AND channel_message_id = $2
			ORDER BY created_at, id
			LIMIT 1`, source.inboxEventID, parseUUID(existing.ID)).Scan(&transportID); err != nil {
			return agentTransportMessageResult{}, err
		}
		if err := h.deleteAgentTransportDraftWithExec(ctx, exec, source, target.raw); err != nil {
			return agentTransportMessageResult{}, err
		}
		return agentTransportMessageResult{Message: existing, Created: false, TransportID: uuidToString(transportID), SentDraft: sentDraft}, nil
	}
	ok, err := h.matchesChannelMessageIdempotencyPayload(ctx, existing, input, attachmentIDs)
	if err != nil {
		return agentTransportMessageResult{}, err
	}
	if !ok {
		return agentTransportMessageResult{}, errChannelClientMessageConflict
	}
	transportID, err := h.recordAgentTransportAuditExec(ctx, exec, source, agentTransportActionSend, target.raw, input.ChannelID, parseUUID(existing.ID), clientMessageID, map[string]any{
		"channel_id":        target.channel.ID,
		"message_id":        existing.ID,
		"client_message_id": clientMessageID,
		"created":           false,
		"seq":               existing.Seq,
		"thread_root_id":    existing.ThreadRootMessageID,
	})
	if err != nil {
		return agentTransportMessageResult{}, err
	}
	if err := h.deleteAgentTransportDraftWithExec(ctx, exec, source, target.raw); err != nil {
		return agentTransportMessageResult{}, err
	}
	return agentTransportMessageResult{Message: existing, Created: false, TransportID: transportID, SentDraft: sentDraft}, nil
}

func (h *Handler) resolveDuplicateAgentTransportMessageAtomic(ctx context.Context, source agentTransportSource, target agentTransportTarget, input channelMessageInsertInput, attachmentIDs []pgtype.UUID, clientMessageID string, sentDraft *agentTransportDraft) (agentTransportMessageResult, error) {
	tx, err := h.TxStarter.Begin(ctx)
	if err != nil {
		return agentTransportMessageResult{}, err
	}
	if source.task.Reason != channelOnboardingReason {
		if err := h.lockAgentTransportSource(ctx, tx, source); err != nil {
			_ = tx.Rollback(ctx)
			return agentTransportMessageResult{}, err
		}
	}
	if err := h.lockAgentTransportTargetForInsert(ctx, tx, target); err != nil {
		_ = tx.Rollback(ctx)
		return agentTransportMessageResult{}, err
	}
	if err := h.lockAgentTransportDraftSource(ctx, tx, source, target); err != nil {
		_ = tx.Rollback(ctx)
		return agentTransportMessageResult{}, err
	}
	if err := h.requireAgentTransportSourceActiveWithExec(ctx, tx, source); err != nil {
		_ = tx.Rollback(ctx)
		return agentTransportMessageResult{}, err
	}
	existing, found, err := h.findAgentChannelMessageByClientIDWithExec(ctx, tx, input.WorkspaceID, input.ChannelID, input.AuthorID, clientMessageID)
	if err != nil {
		_ = tx.Rollback(ctx)
		return agentTransportMessageResult{}, err
	}
	if !found {
		_ = tx.Rollback(ctx)
		return agentTransportMessageResult{}, errChannelClientMessageConflict
	}
	result, err := h.completeDuplicateAgentTransportMessageWithExec(ctx, tx, source, target, input, attachmentIDs, existing, clientMessageID, nil)
	if err != nil {
		_ = tx.Rollback(ctx)
		return agentTransportMessageResult{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return agentTransportMessageResult{}, err
	}
	return result, nil
}

func (h *Handler) findAgentChannelMessageByClientIDWithExec(ctx context.Context, exec dbExecutor, workspaceID, channelID, authorID pgtype.UUID, clientMessageID string) (ChannelMessageResponse, bool, error) {
	row := exec.QueryRow(ctx, `
		SELECT id, channel_id, workspace_id, author_type, author_id, author_name, content, parts, source, external_message_id, client_message_id, reply_to_message_id, quote_message_id, quote_snapshot, thread_root_message_id, thread_id, trigger_depth, seq, created_at, edited_at, deleted_at
		FROM channel_message
		WHERE workspace_id = $1 AND channel_id = $2 AND author_type = 'agent' AND author_id = $3 AND client_message_id = $4`,
		workspaceID, channelID, authorID, clientMessageID)
	msg, err := scanChannelMessage(row)
	if err != nil {
		if errorsIsNoRows(err) {
			return ChannelMessageResponse{}, false, nil
		}
		return ChannelMessageResponse{}, false, err
	}
	return msg, true, nil
}

func (h *Handler) addAgentTransportCanonicalReaction(ctx context.Context, source agentTransportSource, message ChannelMessageResponse, emoji string) (ChannelReactionResponse, bool, error) {
	channelID := parseUUID(message.ChannelID)
	messageID := parseUUID(message.ID)
	var id, returnedMessageID, actorID pgtype.UUID
	var createdAt pgtype.Timestamptz
	err := h.DB.QueryRow(ctx, `
		INSERT INTO channel_message_reaction (channel_message_id, workspace_id, actor_type, actor_id, emoji)
		SELECT cm.id, cm.workspace_id, 'agent', $3, $4
		FROM channel_message cm
		WHERE cm.id = $1
		  AND cm.channel_id = $2
		  AND cm.workspace_id = $5
		  AND cm.author_type IN ('user', 'agent')
		  AND cm.deleted_at IS NULL
		ON CONFLICT (channel_message_id, actor_type, actor_id, emoji) DO NOTHING
		RETURNING id, channel_message_id, actor_id, created_at`,
		messageID, channelID, source.origin.agentID, emoji, source.origin.workspaceID,
	).Scan(&id, &returnedMessageID, &actorID, &createdAt)
	if errors.Is(err, pgx.ErrNoRows) {
		err = h.DB.QueryRow(ctx, `
			SELECT r.id, r.channel_message_id, r.actor_id, r.created_at
			FROM channel_message_reaction r
			JOIN channel_message cm ON cm.id = r.channel_message_id
			WHERE r.channel_message_id = $1
			  AND cm.channel_id = $2
			  AND cm.workspace_id = $3
			  AND cm.author_type IN ('user', 'agent')
			  AND cm.deleted_at IS NULL
			  AND r.actor_type = 'agent'
			  AND r.actor_id = $4
			  AND r.emoji = $5`,
			messageID, channelID, source.origin.workspaceID, source.origin.agentID, emoji,
		).Scan(&id, &returnedMessageID, &actorID, &createdAt)
		if errors.Is(err, pgx.ErrNoRows) {
			return ChannelReactionResponse{}, false, errAgentTransportMessageNotFound
		}
		if err != nil {
			return ChannelReactionResponse{}, false, err
		}
		reaction := agentTransportCanonicalReactionResponse(channelID, id, returnedMessageID, actorID, emoji, createdAt)
		return reaction, false, nil
	}
	if err != nil {
		return ChannelReactionResponse{}, false, err
	}
	reaction := agentTransportCanonicalReactionResponse(channelID, id, returnedMessageID, actorID, emoji, createdAt)
	h.publishChannelToMembers(ctx, protocol.EventChannelReactionAdded, message.WorkspaceID, "agent", uuidToString(source.origin.agentID), channelID, map[string]any{
		"reaction":   reaction,
		"channel_id": message.ChannelID,
		"message_id": message.ID,
	})
	return reaction, true, nil
}

func (h *Handler) removeAgentTransportCanonicalReaction(ctx context.Context, source agentTransportSource, message ChannelMessageResponse, emoji string) (bool, error) {
	channelID := parseUUID(message.ChannelID)
	messageID := parseUUID(message.ID)
	tag, err := h.DB.Exec(ctx, `
		DELETE FROM channel_message_reaction r
		USING channel_message cm
		WHERE r.channel_message_id = cm.id
		  AND r.channel_message_id = $1
		  AND cm.channel_id = $2
		  AND cm.workspace_id = $3
		  AND cm.author_type IN ('user', 'agent')
		  AND cm.deleted_at IS NULL
		  AND r.actor_type = 'agent'
		  AND r.actor_id = $4
		  AND r.emoji = $5`,
		messageID, channelID, source.origin.workspaceID, source.origin.agentID, emoji,
	)
	if err != nil {
		return false, err
	}
	if tag.RowsAffected() == 0 {
		return false, nil
	}
	h.publishChannelToMembers(ctx, protocol.EventChannelReactionRemoved, message.WorkspaceID, "agent", uuidToString(source.origin.agentID), channelID, map[string]any{
		"channel_id": message.ChannelID,
		"message_id": message.ID,
		"emoji":      emoji,
		"actor_type": "agent",
		"actor_id":   uuidToString(source.origin.agentID),
	})
	return true, nil
}

func agentTransportCanonicalReactionResponse(channelID, id, messageID, actorID pgtype.UUID, emoji string, createdAt pgtype.Timestamptz) ChannelReactionResponse {
	return ChannelReactionResponse{
		ID:        uuidToString(id),
		ChannelID: uuidToString(channelID),
		MessageID: uuidToString(messageID),
		ActorType: "agent",
		ActorID:   uuidToString(actorID),
		Emoji:     emoji,
		CreatedAt: timestampToString(createdAt),
	}
}

func (req AgentTransportReadRequest) anchor() (mode, raw string, err error) {
	anchors := []struct {
		mode string
		raw  string
	}{
		{mode: "before", raw: strings.TrimSpace(req.Before)},
		{mode: "after", raw: strings.TrimSpace(req.After)},
		{mode: "around", raw: strings.TrimSpace(req.Around)},
	}
	for _, candidate := range anchors {
		if candidate.raw == "" {
			continue
		}
		if mode != "" {
			return "", "", errors.New("only one of before, after, or around may be set")
		}
		mode, raw = candidate.mode, candidate.raw
	}
	return mode, raw, nil
}

func agentTransportCanonicalMessageTarget(target agentTransportTarget) string {
	if target.threadRootMessageID.Valid {
		return "thread:" + uuidToString(target.threadRootMessageID)
	}
	return "channel:" + target.channel.ID
}

func (h *Handler) readAgentTransportMessages(ctx context.Context, target agentTransportTarget, mode, rawAnchor string, limit int) ([]ChannelMessageResponse, error) {
	if mode == "" {
		return h.readAgentTransportMessageWindow(ctx, target, "recent", 0, limit)
	}
	anchor, err := h.resolveAgentTransportReadAnchor(ctx, target, rawAnchor)
	if err != nil {
		return nil, err
	}
	switch mode {
	case "before":
		return h.readAgentTransportMessageWindow(ctx, target, "before", anchor.Seq, limit)
	case "after":
		return h.readAgentTransportMessageWindow(ctx, target, "after", anchor.Seq, limit)
	case "around":
		beforeLimit := (limit - 1) / 2
		before, err := h.readAgentTransportMessageWindow(ctx, target, "before", anchor.Seq, beforeLimit)
		if err != nil {
			return nil, err
		}
		after, err := h.readAgentTransportMessageWindow(ctx, target, "after", anchor.Seq, limit-1-len(before))
		if err != nil {
			return nil, err
		}
		return append(append(before, anchor), after...), nil
	default:
		return nil, fmt.Errorf("%w: unsupported window", errAgentTransportReadAnchorInvalid)
	}
}

func (h *Handler) resolveAgentTransportReadAnchor(ctx context.Context, target agentTransportTarget, raw string) (ChannelMessageResponse, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ChannelMessageResponse{}, fmt.Errorf("%w: anchor is required", errAgentTransportReadAnchorInvalid)
	}
	if decimalAgentMessageSequencePattern.MatchString(raw) {
		sequence, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return ChannelMessageResponse{}, fmt.Errorf("%w: sequence is out of range", errAgentTransportReadAnchorInvalid)
		}
		if sequence <= 0 {
			return ChannelMessageResponse{}, fmt.Errorf("%w: sequence must be positive", errAgentTransportReadAnchorInvalid)
		}
		messages, err := h.findAgentTransportReadAnchors(ctx, target, `m.seq = $4`, sequence)
		if err != nil {
			return ChannelMessageResponse{}, err
		}
		return oneAgentTransportReadAnchor(messages)
	}
	if id, err := uuid.Parse(raw); err == nil {
		messages, err := h.findAgentTransportReadAnchors(ctx, target, `m.id = $4`, id)
		if err != nil {
			return ChannelMessageResponse{}, err
		}
		return oneAgentTransportReadAnchor(messages)
	}
	if !shortAgentMessageIDPattern.MatchString(raw) {
		return ChannelMessageResponse{}, fmt.Errorf("%w: use a full message id, a unique 8+ character short id, or a positive sequence", errAgentTransportReadAnchorInvalid)
	}
	messages, err := h.findAgentTransportReadAnchors(ctx, target, `LOWER(m.id::text) LIKE LOWER($4) || '%'`, raw)
	if err != nil {
		return ChannelMessageResponse{}, err
	}
	return oneAgentTransportReadAnchor(messages)
}

func (h *Handler) findAgentTransportReadAnchors(ctx context.Context, target agentTransportTarget, predicate string, value any) ([]ChannelMessageResponse, error) {
	query := `SELECT ` + agentTransportReadMessageColumns + `
		FROM channel_message m
		WHERE ` + agentTransportReadTargetPredicate + ` AND ` + predicate + `
		ORDER BY m.seq ASC
		LIMIT $5`
	return h.readAgentTransportMessageRows(ctx, target, query, value, 2)
}

func oneAgentTransportReadAnchor(messages []ChannelMessageResponse) (ChannelMessageResponse, error) {
	switch len(messages) {
	case 0:
		return ChannelMessageResponse{}, errAgentTransportReadAnchorNotFound
	case 1:
		return messages[0], nil
	default:
		return ChannelMessageResponse{}, errAgentTransportReadAnchorAmbiguous
	}
}

const agentTransportReadMessageColumns = `m.id, m.channel_id, m.workspace_id, m.author_type, m.author_id, m.author_name, m.content, m.parts, m.source, m.external_message_id, m.client_message_id, m.reply_to_message_id, m.quote_message_id, m.quote_snapshot, m.thread_root_message_id, m.thread_id, m.trigger_depth, m.seq, m.created_at, m.edited_at, m.deleted_at`

const agentTransportReadTargetPredicate = `
		m.channel_id = $1
		AND m.workspace_id = $2
		AND m.deleted_at IS NULL
		AND (
			($3::uuid IS NULL AND m.thread_root_message_id IS NULL)
			OR ($3::uuid IS NOT NULL AND (m.id = $3 OR m.thread_root_message_id = $3))
		)`

func (h *Handler) readAgentTransportMessageWindow(ctx context.Context, target agentTransportTarget, direction string, sequence int64, limit int) ([]ChannelMessageResponse, error) {
	if limit <= 0 {
		return []ChannelMessageResponse{}, nil
	}
	var query string
	var args []any
	switch direction {
	case "recent":
		query = `SELECT * FROM (
			SELECT ` + agentTransportReadMessageColumns + `
			FROM channel_message m
			WHERE ` + agentTransportReadTargetPredicate + `
			ORDER BY m.seq DESC
			LIMIT $4
		) recent
		ORDER BY seq ASC`
		args = []any{limit}
	case "before":
		query = `SELECT * FROM (
			SELECT ` + agentTransportReadMessageColumns + `
			FROM channel_message m
			WHERE ` + agentTransportReadTargetPredicate + ` AND m.seq < $4
			ORDER BY m.seq DESC
			LIMIT $5
		) before_window
		ORDER BY seq ASC`
		args = []any{sequence, limit}
	case "after":
		query = `SELECT ` + agentTransportReadMessageColumns + `
			FROM channel_message m
			WHERE ` + agentTransportReadTargetPredicate + ` AND m.seq > $4
			ORDER BY m.seq ASC
			LIMIT $5`
		args = []any{sequence, limit}
	default:
		return nil, fmt.Errorf("unknown read direction %q", direction)
	}
	return h.readAgentTransportMessageRows(ctx, target, query, args...)
}

func (h *Handler) readAgentTransportMessageRows(ctx context.Context, target agentTransportTarget, query string, extra ...any) ([]ChannelMessageResponse, error) {
	args := []any{
		parseUUID(target.channel.ID),
		parseUUID(target.channel.WorkspaceID),
		nullableUUID(target.threadRootMessageID),
	}
	args = append(args, extra...)
	rows, err := h.DB.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var messages []ChannelMessageResponse
	for rows.Next() {
		message, err := scanChannelMessage(rows)
		if err != nil {
			return nil, err
		}
		messages = append(messages, message)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return messages, nil
}

func (h *Handler) decorateAgentTransportMessages(ctx context.Context, workspaceID string, messages []ChannelMessageResponse) {
	h.attachChannelMessageAuthorAvatars(ctx, workspaceID, messages)
	h.attachChannelMessageAttachments(ctx, workspaceID, messages)
	h.attachChannelMessageReactions(ctx, workspaceID, messages)
	h.attachChannelMessageReplySummaries(ctx, workspaceID, messages)
	h.attachChannelMessageThreadRootSummaries(ctx, workspaceID, messages)
	applyChannelMessageTombstoneReadModel(messages)
}

func (h *Handler) searchAgentTransportMessages(ctx context.Context, target agentTransportTarget, query string, limit int) (int, []ChannelMessageSearchResult, error) {
	pattern := "%" + escapeLike(query) + "%"
	threadRootID := nullableUUID(target.threadRootMessageID)
	var total int
	if err := h.DB.QueryRow(ctx, `
		SELECT count(*)
		FROM channel_message
		WHERE channel_id = $1 AND workspace_id = $2 AND author_type <> 'system' AND deleted_at IS NULL AND content ILIKE $3 ESCAPE '\'
		  AND ($4::uuid IS NULL OR id = $4 OR thread_root_message_id = $4)`,
		parseUUID(target.channel.ID), parseUUID(target.channel.WorkspaceID), pattern, threadRootID).Scan(&total); err != nil {
		return 0, nil, err
	}
	rows, err := h.DB.Query(ctx, `
		SELECT m.id, m.channel_id, m.thread_root_message_id, m.author_type, m.author_id, m.author_name,
		       CASE WHEN m.author_type = 'user' THEN u.avatar_url ELSE a.avatar_url END,
		       m.content, m.created_at
		FROM channel_message m
		LEFT JOIN "user" u ON m.author_type = 'user' AND u.id = m.author_id
		LEFT JOIN agent a ON m.author_type = 'agent' AND a.id = m.author_id AND a.workspace_id = m.workspace_id
		WHERE m.channel_id = $1 AND m.workspace_id = $2 AND m.author_type <> 'system' AND m.deleted_at IS NULL AND m.content ILIKE $3 ESCAPE '\'
		  AND ($4::uuid IS NULL OR m.id = $4 OR m.thread_root_message_id = $4)
		ORDER BY m.seq ASC
		LIMIT $5`, parseUUID(target.channel.ID), parseUUID(target.channel.WorkspaceID), pattern, threadRootID, limit)
	if err != nil {
		return 0, nil, err
	}
	defer rows.Close()
	var results []ChannelMessageSearchResult
	for rows.Next() {
		var id, chID, rootID, authorID pgtype.UUID
		var authorType, authorName, content string
		var avatarURL pgtype.Text
		var createdAt pgtype.Timestamptz
		if err := rows.Scan(&id, &chID, &rootID, &authorType, &authorID, &authorName, &avatarURL, &content, &createdAt); err != nil {
			return 0, nil, err
		}
		results = append(results, ChannelMessageSearchResult{
			MessageID:           uuidToString(id),
			ChannelID:           uuidToString(chID),
			ThreadRootMessageID: uuidToPtr(rootID),
			Type:                authorType,
			AuthorID:            uuidToPtr(authorID),
			AuthorName:          authorName,
			AuthorAvatarURL:     textToPtr(avatarURL),
			Content:             content,
			CreatedAt:           timestampToString(createdAt),
		})
	}
	return total, results, rows.Err()
}

func (h *Handler) agentTransportFreshnessDecisionWithSeen(ctx context.Context, exec dbExecutor, source agentTransportSource, target agentTransportTarget, seenUpToSeq int64) (agentTransportFreshnessDecision, error) {
	if seenUpToSeq <= 0 {
		return agentTransportFreshnessDecision{SeenUpToSeq: seenUpToSeq}, nil
	}
	latestSeq, totalNewer, err := h.agentTransportNewerMessageStats(ctx, exec, source, target, seenUpToSeq)
	if err != nil || totalNewer <= 0 {
		return agentTransportFreshnessDecision{SeenUpToSeq: seenUpToSeq, LatestSeq: latestSeq}, err
	}
	showAfterSeq := seenUpToSeq
	if draft, found, err := h.loadAgentTransportDraftWithExec(ctx, exec, source, target.raw); err != nil {
		return agentTransportFreshnessDecision{}, err
	} else if found && draft.HeldToSeq > showAfterSeq {
		showAfterSeq = draft.HeldToSeq
	}
	messages, err := h.readAgentTransportMessagesAfterSeq(ctx, source, target, showAfterSeq, agentTransportFreshnessHoldLimit)
	if err != nil {
		return agentTransportFreshnessDecision{}, err
	}
	h.decorateAgentTransportMessages(ctx, target.channel.WorkspaceID, messages)
	omitted := totalNewer - int64(len(messages))
	if omitted < 0 {
		omitted = 0
	}
	return agentTransportFreshnessDecision{
		Hold:        true,
		SeenUpToSeq: seenUpToSeq,
		LatestSeq:   latestSeq,
		TotalNewer:  totalNewer,
		Messages:    messages,
		Omitted:     omitted,
		ProducerID:  agentTransportFreshnessProducerID(source, target, seenUpToSeq, latestSeq),
	}, nil
}

func (h *Handler) agentTransportSeenUpToSeq(ctx context.Context, source agentTransportSource, target string, requested int64) (int64, error) {
	if requested > 0 {
		return requested, nil
	}
	if seq, ok, err := h.latestAgentTransportReadSeenSeq(ctx, source, target); err != nil {
		return 0, err
	} else if ok && seq > 0 {
		return seq, nil
	}
	if !source.inboxEventID.Valid {
		return 0, nil
	}
	var seq int64
	if err := h.DB.QueryRow(ctx, `
		SELECT seq_to
		FROM agent_inbox_event
		WHERE id = $1 AND workspace_id = $2 AND agent_id = $3`,
		source.inboxEventID, source.origin.workspaceID, source.origin.agentID).Scan(&seq); err != nil {
		if errorsIsNoRows(err) {
			return 0, nil
		}
		return 0, err
	}
	return seq, nil
}

func (h *Handler) latestAgentTransportReadSeenSeq(ctx context.Context, source agentTransportSource, target string) (int64, bool, error) {
	var seq int64
	err := h.DB.QueryRow(ctx, `
		SELECT COALESCE(NULLIF(context_pack->>'seen_up_to_seq', '')::bigint, 0)
		FROM agent_task_transport_audit
		WHERE workspace_id = $1
		  AND agent_id = $2
		  AND action = $3
		  AND target = $4
		  AND (($5::uuid IS NOT NULL AND task_id = $5) OR ($6::uuid IS NOT NULL AND inbox_event_id = $6))
		ORDER BY created_at DESC
		LIMIT 1`,
		source.origin.workspaceID, source.origin.agentID, agentTransportActionRead, strings.TrimSpace(target), nullableTaskIDForTransportSource(source), nullableInboxEventIDForTransportSource(source)).Scan(&seq)
	if err != nil {
		if errorsIsNoRows(err) {
			return 0, false, nil
		}
		return 0, false, err
	}
	return seq, true, nil
}

func (h *Handler) agentTransportNewerMessageStats(ctx context.Context, exec dbExecutor, source agentTransportSource, target agentTransportTarget, seenUpToSeq int64) (int64, int64, error) {
	var latestSeq, count int64
	err := exec.QueryRow(ctx, `
		SELECT COALESCE(MAX(seq), 0), COUNT(*)
		FROM channel_message
		WHERE channel_id = $1
		  AND workspace_id = $2
		  AND deleted_at IS NULL
		  AND seq > $3
		  AND NOT (author_type = 'agent' AND author_id = $5)
		  AND (
		    ($4::uuid IS NOT NULL AND (id = $4 OR thread_root_message_id = $4))
		    OR ($4::uuid IS NULL AND thread_root_message_id IS NULL)
		  )`,
		parseUUID(target.channel.ID), parseUUID(target.channel.WorkspaceID), seenUpToSeq, nullableUUID(target.threadRootMessageID), source.origin.agentID).Scan(&latestSeq, &count)
	return latestSeq, count, err
}

func (h *Handler) readAgentTransportMessagesAfterSeq(ctx context.Context, source agentTransportSource, target agentTransportTarget, afterSeq int64, limit int) ([]ChannelMessageResponse, error) {
	rows, err := h.DB.Query(ctx, `
		SELECT id, channel_id, workspace_id, author_type, author_id, author_name, content, parts, source, external_message_id, client_message_id, reply_to_message_id, quote_message_id, quote_snapshot, thread_root_message_id, thread_id, trigger_depth, seq, created_at, edited_at, deleted_at
		FROM (
			SELECT id, channel_id, workspace_id, author_type, author_id, author_name, content, parts, source, external_message_id, client_message_id, reply_to_message_id, quote_message_id, quote_snapshot, thread_root_message_id, thread_id, trigger_depth, seq, created_at, edited_at, deleted_at
			FROM channel_message
			WHERE channel_id = $1
			  AND workspace_id = $2
			  AND deleted_at IS NULL
			  AND seq > $3
			  AND NOT (author_type = 'agent' AND author_id = $6)
			  AND (
			    ($4::uuid IS NOT NULL AND (id = $4 OR thread_root_message_id = $4))
			    OR ($4::uuid IS NULL AND thread_root_message_id IS NULL)
			  )
			ORDER BY seq DESC
			LIMIT $5
		) newer
		ORDER BY seq ASC`,
		parseUUID(target.channel.ID), parseUUID(target.channel.WorkspaceID), afterSeq, nullableUUID(target.threadRootMessageID), limit, source.origin.agentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ChannelMessageResponse
	for rows.Next() {
		msg, err := scanChannelMessage(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, msg)
	}
	return out, rows.Err()
}

func (h *Handler) holdAgentTransportSend(ctx context.Context, source agentTransportSource, target agentTransportTarget, content string, parts []protocol.MessagePart, clientMessageID string, decision agentTransportFreshnessDecision) (agentTransportFreshnessDecision, string, error) {
	tx, err := h.TxStarter.Begin(ctx)
	if err != nil {
		return agentTransportFreshnessDecision{}, "", err
	}
	if err := h.lockAgentTransportDraftSource(ctx, tx, source, target); err != nil {
		_ = tx.Rollback(ctx)
		return agentTransportFreshnessDecision{}, "", err
	}
	chosen, transportID, emitted, err := h.holdAgentTransportSendWithExec(ctx, tx, source, target, content, parts, clientMessageID, decision)
	if err != nil {
		_ = tx.Rollback(ctx)
		return agentTransportFreshnessDecision{}, "", err
	}
	if err := tx.Commit(ctx); err != nil {
		return agentTransportFreshnessDecision{}, "", err
	}
	if emitted {
		h.recordAgentTransportFreshnessHoldActivity(ctx, source, target, chosen, transportID)
	}
	return chosen, transportID, nil
}

func (h *Handler) holdAgentTransportSendWithExec(ctx context.Context, exec dbExecutor, source agentTransportSource, target agentTransportTarget, content string, parts []protocol.MessagePart, clientMessageID string, decision agentTransportFreshnessDecision) (agentTransportFreshnessDecision, string, bool, error) {
	chosen, winner, err := h.saveAgentTransportDraftWithExec(ctx, exec, source, target, content, parts, clientMessageID, decision)
	if err != nil {
		return agentTransportFreshnessDecision{}, "", false, err
	}
	if !winner {
		return chosen, "", false, nil
	}
	transportID, err := h.recordAgentTransportAuditExec(ctx, exec, source, agentTransportActionSend, target.raw, parseUUID(target.channel.ID), pgtype.UUID{}, clientMessageID, map[string]any{
		"held":                  true,
		"subtype":               "freshness",
		"reason":                "newer_messages_available",
		"decision":              "local_hold",
		"producer_fact_id":      chosen.ProducerID,
		"channel_id":            target.channel.ID,
		"client_message_id":     clientMessageID,
		"seen_up_to_seq":        chosen.SeenUpToSeq,
		"latest_seq":            chosen.LatestSeq,
		"new_message_count":     chosen.TotalNewer,
		"shown_message_count":   len(chosen.Messages),
		"omitted_message_count": chosen.Omitted,
		"thread_root_id":        target.threadRootMessageID,
	})
	if err != nil {
		return agentTransportFreshnessDecision{}, "", false, err
	}
	return chosen, transportID, true, nil
}

func writeAgentTransportHeldResponse(w http.ResponseWriter, target agentTransportTarget, decision agentTransportFreshnessDecision, transportID string) {
	olderBoundary := "No older."
	if decision.Omitted > 0 {
		noun := "messages"
		if decision.Omitted == 1 {
			noun = "message"
		}
		olderBoundary = fmt.Sprintf("%d older %s omitted.", decision.Omitted, noun)
	}
	writeJSON(w, http.StatusOK, AgentTransportSendHeldResponse{
		Action:         agentTransportActionSend,
		Target:         target.raw,
		State:          "held",
		Outcome:        "held",
		Subtype:        "freshness",
		Reason:         "newer_messages_available",
		Decision:       "local_hold",
		ProducerFactID: decision.ProducerID,
		// A held draft is deliberately inert. Do not expose ready-to-execute
		// resend commands here: tool runtimes can mistake them for follow-up work
		// and publish a message that the freshness gate just withheld.
		AvailableActions:    []string{"review_newer_messages"},
		HeldMessages:        decision.Messages,
		NewMessageCount:     decision.TotalNewer,
		ShownMessageCount:   int64(len(decision.Messages)),
		OmittedMessageCount: decision.Omitted,
		SeenUpToSeq:         decision.SeenUpToSeq,
		LatestSeq:           decision.LatestSeq,
		TransportID:         transportID,
		ContextWindow: AgentTransportFreshnessContextWindow{
			OldestSeq:     firstChannelMessageSeqValue(decision.Messages),
			NewestSeq:     maxChannelMessageSeq(decision.Messages),
			OlderBoundary: olderBoundary,
			NewerBoundary: "No newer.",
		},
	})
}

func (h *Handler) recordAgentTransportFreshnessHoldActivity(ctx context.Context, source agentTransportSource, target agentTransportTarget, decision agentTransportFreshnessDecision, transportID string) {
	details := map[string]any{
		"reason":                "newer_messages_available",
		"decision":              "local_hold",
		"producer_fact_id":      decision.ProducerID,
		"transport_id":          transportID,
		"seen_up_to_seq":        decision.SeenUpToSeq,
		"latest_seq":            decision.LatestSeq,
		"new_message_count":     decision.TotalNewer,
		"shown_message_count":   len(decision.Messages),
		"omitted_message_count": decision.Omitted,
		"target":                target.raw,
		"recommended_action":    "review_newer_messages",
	}
	h.recordAgentActivityEvent(ctx, h.DB,
		source.origin.workspaceID, source.task.AgentID, source.task.RuntimeID, nullableTaskIDForTransportSource(source),
		activityKindBlocked, "send_freshness_hold", "info",
		"channel", parseUUID(target.channel.ID), target.raw,
		"", "Send held by freshness check",
		details,
	)
}

func (h *Handler) saveAgentTransportDraftWithExec(ctx context.Context, exec dbExecutor, source agentTransportSource, target agentTransportTarget, content string, parts []protocol.MessagePart, clientMessageID string, decision agentTransportFreshnessDecision) (agentTransportFreshnessDecision, bool, error) {
	partsJSON, err := json.Marshal(parts)
	if err != nil {
		return agentTransportFreshnessDecision{}, false, err
	}
	args := []any{
		source.origin.workspaceID, source.origin.agentID, strings.TrimSpace(target.raw), parseUUID(target.channel.ID), nullableUUID(target.threadRootMessageID),
		content, partsJSON, clientMessageID,
		decision.SeenUpToSeq, decision.SeenUpToSeq + 1, decision.LatestSeq, firstChannelMessageSeq(decision.Messages), maxChannelMessageSeq(decision.Messages), decision.ProducerID,
	}
	if source.inboxEventID.Valid {
		err = exec.QueryRow(ctx, `
			INSERT INTO agent_transport_draft (
				workspace_id, agent_id, inbox_event_id, target, channel_id, thread_root_message_id,
				content, parts, client_message_id,
				seen_up_to_seq, held_from_seq, held_to_seq, shown_from_seq, shown_to_seq, decision_fact_id
			)
			VALUES ($1, $2, $15, $3, $4, $5, $6, $7::jsonb, $8, $9, $10, $11, $12, $13, $14)
			ON CONFLICT (workspace_id, agent_id, inbox_event_id, target) WHERE inbox_event_id IS NOT NULL
			DO UPDATE SET
				channel_id = EXCLUDED.channel_id,
				thread_root_message_id = EXCLUDED.thread_root_message_id,
				content = EXCLUDED.content,
				parts = EXCLUDED.parts,
				client_message_id = EXCLUDED.client_message_id,
				seen_up_to_seq = EXCLUDED.seen_up_to_seq,
				held_from_seq = EXCLUDED.held_from_seq,
				held_to_seq = EXCLUDED.held_to_seq,
				shown_from_seq = EXCLUDED.shown_from_seq,
				shown_to_seq = EXCLUDED.shown_to_seq,
				decision_fact_id = EXCLUDED.decision_fact_id,
			updated_at = now()
			WHERE agent_transport_draft.held_to_seq < EXCLUDED.held_to_seq
			RETURNING decision_fact_id`, append(args, source.inboxEventID)...).Scan(&decision.ProducerID)
		if err == nil {
			return decision, true, nil
		}
		if !errorsIsNoRows(err) {
			return agentTransportFreshnessDecision{}, false, err
		}
		return h.replaceHeldAgentTransportDraftWithExec(ctx, exec, source, target, content, partsJSON, clientMessageID, decision)
	}
	err = exec.QueryRow(ctx, `
		INSERT INTO agent_transport_draft (
			workspace_id, agent_id, task_id, target, channel_id, thread_root_message_id,
			content, parts, client_message_id,
			seen_up_to_seq, held_from_seq, held_to_seq, shown_from_seq, shown_to_seq, decision_fact_id
		)
		VALUES ($1, $2, $15, $3, $4, $5, $6, $7::jsonb, $8, $9, $10, $11, $12, $13, $14)
		ON CONFLICT (workspace_id, agent_id, task_id, target) WHERE task_id IS NOT NULL
		DO UPDATE SET
			channel_id = EXCLUDED.channel_id,
			thread_root_message_id = EXCLUDED.thread_root_message_id,
			content = EXCLUDED.content,
			parts = EXCLUDED.parts,
			client_message_id = EXCLUDED.client_message_id,
			seen_up_to_seq = EXCLUDED.seen_up_to_seq,
			held_from_seq = EXCLUDED.held_from_seq,
			held_to_seq = EXCLUDED.held_to_seq,
			shown_from_seq = EXCLUDED.shown_from_seq,
			shown_to_seq = EXCLUDED.shown_to_seq,
			decision_fact_id = EXCLUDED.decision_fact_id,
		updated_at = now()
		WHERE agent_transport_draft.held_to_seq < EXCLUDED.held_to_seq
		RETURNING decision_fact_id`, append(args, source.task.ID)...).Scan(&decision.ProducerID)
	if err == nil {
		return decision, true, nil
	}
	if !errorsIsNoRows(err) {
		return agentTransportFreshnessDecision{}, false, err
	}
	return h.replaceHeldAgentTransportDraftWithExec(ctx, exec, source, target, content, partsJSON, clientMessageID, decision)
}

// replaceHeldAgentTransportDraftWithExec is called while the source/target
// advisory lock is held. A same-or-older range is a retry, so it can replace
// authored content but must reuse the existing decision and emit no audit or
// Activity row.
func (h *Handler) replaceHeldAgentTransportDraftWithExec(ctx context.Context, exec dbExecutor, source agentTransportSource, target agentTransportTarget, content string, partsJSON []byte, clientMessageID string, decision agentTransportFreshnessDecision) (agentTransportFreshnessDecision, bool, error) {
	tag, err := exec.Exec(ctx, `
		UPDATE agent_transport_draft
		SET content = $6, parts = $7::jsonb, client_message_id = $8, updated_at = now()
		WHERE workspace_id = $1 AND agent_id = $2 AND target = $3
		  AND (($4::uuid IS NOT NULL AND task_id = $4) OR ($5::uuid IS NOT NULL AND inbox_event_id = $5))`,
		source.origin.workspaceID, source.origin.agentID, strings.TrimSpace(target.raw), nullableTaskIDForTransportSource(source), nullableInboxEventIDForTransportSource(source), content, partsJSON, clientMessageID)
	if err != nil {
		return agentTransportFreshnessDecision{}, false, err
	}
	if tag.RowsAffected() != 1 {
		return agentTransportFreshnessDecision{}, false, errAgentTransportDraftNotFound
	}
	winner, found, err := h.loadAgentTransportDraftWithExec(ctx, exec, source, target.raw)
	if err != nil {
		return agentTransportFreshnessDecision{}, false, err
	}
	if !found {
		return agentTransportFreshnessDecision{}, false, errAgentTransportDraftNotFound
	}
	decision, err = h.agentTransportFreshnessDecisionFromDraft(ctx, exec, target, winner)
	return decision, false, err
}

func (h *Handler) agentTransportFreshnessDecisionFromDraft(ctx context.Context, exec dbExecutor, target agentTransportTarget, draft agentTransportDraft) (agentTransportFreshnessDecision, error) {
	totalNewer, err := h.countAgentTransportMessagesInSeqRange(ctx, exec, target, draft.SeenUpToSeq, draft.HeldToSeq)
	if err != nil {
		return agentTransportFreshnessDecision{}, err
	}
	var messages []ChannelMessageResponse
	if draft.ShownFromSeq.Valid && draft.ShownToSeq.Valid && draft.ShownFromSeq.Int64 > 0 && draft.ShownToSeq.Int64 >= draft.ShownFromSeq.Int64 {
		messages, err = h.readAgentTransportMessagesInSeqRange(ctx, exec, target, draft.ShownFromSeq.Int64-1, draft.ShownToSeq.Int64)
		if err != nil {
			return agentTransportFreshnessDecision{}, err
		}
		h.decorateAgentTransportMessages(ctx, target.channel.WorkspaceID, messages)
	}
	omitted := totalNewer - int64(len(messages))
	if omitted < 0 {
		omitted = 0
	}
	return agentTransportFreshnessDecision{
		Hold:        true,
		SeenUpToSeq: draft.SeenUpToSeq,
		LatestSeq:   draft.HeldToSeq,
		TotalNewer:  totalNewer,
		Messages:    messages,
		Omitted:     omitted,
		ProducerID:  draft.DecisionFactID,
	}, nil
}

func (h *Handler) countAgentTransportMessagesInSeqRange(ctx context.Context, exec dbExecutor, target agentTransportTarget, afterSeq, throughSeq int64) (int64, error) {
	var count int64
	err := exec.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM channel_message
		WHERE channel_id = $1
		  AND workspace_id = $2
		  AND deleted_at IS NULL
		  AND seq > $3
		  AND seq <= $4
		  AND (
		    ($5::uuid IS NOT NULL AND (id = $5 OR thread_root_message_id = $5))
		    OR ($5::uuid IS NULL AND thread_root_message_id IS NULL)
		  )`,
		parseUUID(target.channel.ID), parseUUID(target.channel.WorkspaceID), afterSeq, throughSeq, nullableUUID(target.threadRootMessageID)).Scan(&count)
	return count, err
}

func (h *Handler) readAgentTransportMessagesInSeqRange(ctx context.Context, exec dbExecutor, target agentTransportTarget, afterSeq, throughSeq int64) ([]ChannelMessageResponse, error) {
	rows, err := exec.Query(ctx, `
		SELECT id, channel_id, workspace_id, author_type, author_id, author_name, content, parts, source, external_message_id, client_message_id, reply_to_message_id, quote_message_id, quote_snapshot, thread_root_message_id, thread_id, trigger_depth, seq, created_at, edited_at, deleted_at
		FROM channel_message
		WHERE channel_id = $1
		  AND workspace_id = $2
		  AND deleted_at IS NULL
		  AND seq > $3
		  AND seq <= $4
		  AND (
		    ($5::uuid IS NOT NULL AND (id = $5 OR thread_root_message_id = $5))
		    OR ($5::uuid IS NULL AND thread_root_message_id IS NULL)
		  )
		ORDER BY seq ASC`,
		parseUUID(target.channel.ID), parseUUID(target.channel.WorkspaceID), afterSeq, throughSeq, nullableUUID(target.threadRootMessageID))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ChannelMessageResponse
	for rows.Next() {
		msg, err := scanChannelMessage(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, msg)
	}
	return out, rows.Err()
}

func (h *Handler) loadAgentTransportDraft(ctx context.Context, source agentTransportSource, target string) (agentTransportDraft, bool, error) {
	return h.loadAgentTransportDraftWithExec(ctx, h.DB, source, target)
}

func (h *Handler) loadAgentTransportDraftWithExec(ctx context.Context, exec dbExecutor, source agentTransportSource, target string) (agentTransportDraft, bool, error) {
	target = strings.TrimSpace(target)
	if target == "" {
		return agentTransportDraft{}, false, nil
	}
	var draft agentTransportDraft
	var partsRaw []byte
	err := exec.QueryRow(ctx, `
		SELECT id, target, channel_id, thread_root_message_id, content, parts, client_message_id, seen_up_to_seq, held_from_seq, held_to_seq, shown_from_seq, shown_to_seq, COALESCE(decision_fact_id, '')
		FROM agent_transport_draft
		WHERE workspace_id = $1 AND agent_id = $2 AND target = $3
		  AND (($4::uuid IS NOT NULL AND task_id = $4) OR ($5::uuid IS NOT NULL AND inbox_event_id = $5))`,
		source.origin.workspaceID, source.origin.agentID, target, nullableTaskIDForTransportSource(source), nullableInboxEventIDForTransportSource(source)).Scan(&draft.ID, &draft.Target, &draft.ChannelID, &draft.ThreadRootID, &draft.Content, &partsRaw, &draft.ClientMessageID, &draft.SeenUpToSeq, &draft.HeldFromSeq, &draft.HeldToSeq, &draft.ShownFromSeq, &draft.ShownToSeq, &draft.DecisionFactID)
	if err != nil {
		if errorsIsNoRows(err) {
			return agentTransportDraft{}, false, nil
		}
		return agentTransportDraft{}, false, err
	}
	if len(partsRaw) > 0 {
		if err := json.Unmarshal(partsRaw, &draft.Parts); err != nil {
			return agentTransportDraft{}, false, err
		}
	}
	return draft, true, nil
}

func (h *Handler) deleteAgentTransportDraftWithExec(ctx context.Context, exec dbExecutor, source agentTransportSource, target string) error {
	_, err := exec.Exec(ctx, `
		DELETE FROM agent_transport_draft
		WHERE workspace_id = $1 AND agent_id = $2 AND target = $3
		  AND (($4::uuid IS NOT NULL AND task_id = $4) OR ($5::uuid IS NOT NULL AND inbox_event_id = $5))`,
		source.origin.workspaceID, source.origin.agentID, strings.TrimSpace(target), nullableTaskIDForTransportSource(source), nullableInboxEventIDForTransportSource(source))
	return err
}

func agentTransportFreshnessProducerID(source agentTransportSource, target agentTransportTarget, seenUpToSeq, latestSeq int64) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s:%s:%s:%s:%d:%d", uuidToString(source.origin.workspaceID), uuidToString(source.origin.agentID), agentTransportSourceDecisionKey(source), target.raw, seenUpToSeq, latestSeq)))
	return "freshness_decision_fact:" + hex.EncodeToString(sum[:8])
}

func agentTransportFreshnessRevisedClientMessageID(producerFactID string) string {
	fact := strings.TrimPrefix(strings.TrimSpace(producerFactID), "freshness_decision_fact:")
	return "freshness-revised:" + fact
}

func agentTransportSourceDecisionKey(source agentTransportSource) string {
	if source.inboxEventID.Valid {
		return "inbox:" + uuidToString(source.inboxEventID)
	}
	return "task:" + uuidToString(source.task.ID)
}

func (h *Handler) recordAgentTransportAudit(ctx context.Context, source agentTransportSource, action, target string, channelID, messageID pgtype.UUID, clientMessageID string, contextPack map[string]any) (string, error) {
	return h.recordAgentTransportAuditExec(ctx, h.DB, source, action, target, channelID, messageID, clientMessageID, contextPack)
}

func (h *Handler) recordAgentTransportAuditExec(ctx context.Context, exec dbExecutor, source agentTransportSource, action, target string, channelID, messageID pgtype.UUID, clientMessageID string, contextPack map[string]any) (string, error) {
	pack, err := json.Marshal(contextPack)
	if err != nil {
		return "", err
	}
	var auditID pgtype.UUID
	if err := exec.QueryRow(ctx, `
		INSERT INTO agent_task_transport_audit (workspace_id, task_id, inbox_event_id, agent_id, action, target, channel_id, channel_message_id, client_message_id, context_pack)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10::jsonb)
		RETURNING id`,
		source.origin.workspaceID, nullableTaskIDForTransportSource(source), nullableInboxEventIDForTransportSource(source), source.task.AgentID, action, strings.TrimSpace(target), nullableUUID(channelID), nullableUUID(messageID), nullableAgentTransportClientID(clientMessageID), pack).Scan(&auditID); err != nil {
		return "", err
	}
	return uuidToString(auditID), nil
}

func nullableTaskIDForTransportSource(source agentTransportSource) pgtype.UUID {
	if source.inboxEventID.Valid {
		return pgtype.UUID{}
	}
	return source.task.ID
}

func nullableInboxEventIDForTransportSource(source agentTransportSource) pgtype.UUID {
	return source.inboxEventID
}

func nullableAgentTransportClientID(clientMessageID string) any {
	clientMessageID = strings.TrimSpace(clientMessageID)
	if clientMessageID == "" {
		return nil
	}
	return clientMessageID
}

func (h *Handler) taskHasAgentTransportVisibleOutput(ctx context.Context, taskID pgtype.UUID) bool {
	var exists bool
	err := h.DB.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM agent_task_transport_audit
			WHERE task_id = $1 AND action IN ('message_send', 'message_react')
			  AND channel_message_id IS NOT NULL
		)`, taskID).Scan(&exists)
	return err == nil && exists
}

// chatTaskHasAgentTransportVisibleOutput resolves the explicit transport write
// for both ordinary daemon tasks and inbox-backed synthetic tasks. The latter
// intentionally store their audit under inbox_event_id (rather than task_id),
// even though their synthetic task ID is the inbox event ID.
func (h *Handler) chatTaskHasAgentTransportVisibleOutput(ctx context.Context, task db.AgentInboxEvent) bool {
	return h.taskHasAgentTransportVisibleOutput(ctx, task.ID) || h.inboxEventHasAgentTransportVisibleOutput(ctx, task.ID)
}

func (h *Handler) inboxEventHasAgentTransportVisibleOutput(ctx context.Context, eventID pgtype.UUID) bool {
	var exists bool
	err := h.DB.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM agent_task_transport_audit
			WHERE inbox_event_id = $1 AND action IN ('message_send', 'message_react')
			  AND channel_message_id IS NOT NULL
		)`, eventID).Scan(&exists)
	return err == nil && exists
}

func (h *Handler) inboxEventHasAgentTransportMessageOutput(ctx context.Context, eventID pgtype.UUID) bool {
	var exists bool
	err := h.DB.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM agent_task_transport_audit
			WHERE inbox_event_id = $1
			  AND action = 'message_send'
			  AND channel_message_id IS NOT NULL
		)`, eventID).Scan(&exists)
	return err == nil && exists
}

// inboxEventHasAgentTransportFreshnessHold reports an unresolved Raft-compatible
// send boundary: the attempted output was saved as a draft because newer
// context arrived and no later decision resolved that producer fact.
func (h *Handler) inboxEventHasAgentTransportFreshnessHold(ctx context.Context, eventID pgtype.UUID) bool {
	var exists bool
	err := h.DB.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM agent_task_transport_audit hold
			WHERE hold.inbox_event_id = $1
			  AND hold.action = 'message_send'
			  AND COALESCE(hold.context_pack->>'held', 'false') = 'true'
			  AND hold.context_pack->>'subtype' = 'freshness'
			  AND NOT EXISTS (
			    SELECT 1
			    FROM agent_task_transport_audit resolution
			    WHERE resolution.inbox_event_id = hold.inbox_event_id
			      AND resolution.action = 'message_send'
			      AND resolution.target = hold.target
			      AND COALESCE(resolution.context_pack->>'freshness_resolution', 'false') = 'true'
			      AND resolution.context_pack->>'producer_fact_id' = hold.context_pack->>'producer_fact_id'
			  )
		)`, eventID).Scan(&exists)
	return err == nil && exists
}

func channelMessageIDs(messages []ChannelMessageResponse) []string {
	out := make([]string, 0, len(messages))
	for _, msg := range messages {
		out = append(out, msg.ID)
	}
	return out
}

func firstChannelMessageSeq(messages []ChannelMessageResponse) any {
	if len(messages) == 0 {
		return nil
	}
	return messages[0].Seq
}

func firstChannelMessageSeqValue(messages []ChannelMessageResponse) int64 {
	if len(messages) == 0 {
		return 0
	}
	return messages[0].Seq
}

func maxChannelMessageSeq(messages []ChannelMessageResponse) int64 {
	var out int64
	for _, msg := range messages {
		if msg.Seq > out {
			out = msg.Seq
		}
	}
	return out
}

func clampAgentTransportLimit(value, def int) int {
	if value <= 0 {
		value = def
	}
	if value > channelMessagesMaxLimit {
		return channelMessagesMaxLimit
	}
	return value
}
