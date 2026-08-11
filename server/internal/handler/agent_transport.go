package handler

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
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
	Target          string          `json:"target"`
	Content         string          `json:"content"`
	AttachmentIDs   []string        `json:"attachment_ids"`
	Parts           json.RawMessage `json:"parts"`
	ClientMessageID string          `json:"client_message_id"`
	SeenUpToSeq     int64           `json:"seen_up_to_seq,omitempty"`
	ContextTarget   string          `json:"context_target,omitempty"`
	BypassFreshness bool            `json:"bypass_freshness,omitempty"`
	// ContinueAnyway lets an already-fresh agent explicitly say "I understand
	// there are newer messages I haven't read; proceed and place mine per the
	// proxy cursor anyway" instead of being soft-held. It does not skip the
	// freshness computation (use BypassFreshness for that); it only overrides the
	// held decision on a per-send basis, mirroring Raft's continueAnyway.
	ContinueAnyway bool `json:"continue_anyway,omitempty"`
	// Kind is the structured agent output kind (LRM-1529). When set, it wins
	// over the legacy confirmation lexicon and is persisted with kind_source=structured.
	Kind string `json:"kind,omitempty"`
	// OutputEnvelope is the optional full machine-readable agent output contract.
	// When Kind is empty, Envelope.Kind is used.
	OutputEnvelope *protocol.AgentOutputEnvelope `json:"output_envelope,omitempty"`
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
	Action                  string                               `json:"action"`
	Target                  string                               `json:"target"`
	State                   string                               `json:"state"`
	Outcome                 string                               `json:"outcome"`
	Subtype                 string                               `json:"subtype"`
	Reason                  string                               `json:"reason"`
	Decision                string                               `json:"decision"`
	ProducerFactID          string                               `json:"producerFactId"`
	AvailableActions        []string                             `json:"availableActions"`
	ContinueAnywaySuggested bool                                 `json:"continueAnywaySuggested,omitempty"`
	HeldMessages            []ChannelMessageResponse             `json:"heldMessages"`
	NewMessageCount         int64                                `json:"newMessageCount"`
	ShownMessageCount       int64                                `json:"shownMessageCount"`
	OmittedMessageCount     int64                                `json:"omittedMessageCount"`
	SeenUpToSeq             int64                                `json:"seenUpToSeq"`
	LatestSeq               int64                                `json:"latestSeq"`
	TransportID             string                               `json:"transport_id"`
	ContextWindow           AgentTransportFreshnessContextWindow `json:"contextWindow"`
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
	// Read anchors come in two grammars with separate fields: message identity
	// (*_id, what agents are taught to use) and target sequence (*_seq, machine
	// bookkeeping for proxy recovery). Splitting them keeps anchor resolution
	// free of shape-based guessing.
	BeforeID  string `json:"before_id,omitempty"`
	BeforeSeq int64  `json:"before_seq,omitempty"`
	AfterID   string `json:"after_id,omitempty"`
	AfterSeq  int64  `json:"after_seq,omitempty"`
	AroundID  string `json:"around_id,omitempty"`
	AroundSeq int64  `json:"around_seq,omitempty"`
	Limit     int    `json:"limit"`
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
	shortAgentMessageIDPattern           = regexp.MustCompile(`^[0-9a-fA-F]{8,35}$`)
)

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
	Message             ChannelMessageResponse
	Created             bool
	TransportID         string
	FreshnessResolution *AgentTransportFreshnessResolution
	AgentDM             agentDMSendReservation
}

type AgentTransportSearchRequest struct {
	Target string `json:"target"`
	Query  string `json:"query"`
	Sender string `json:"sender,omitempty"`
	Sort   string `json:"sort,omitempty"`
	Before string `json:"before,omitempty"`
	After  string `json:"after,omitempty"`
	Limit  int    `json:"limit"`
	Offset int    `json:"offset"`
}

type AgentTransportSearchResponse struct {
	Action      string                       `json:"action"`
	Target      string                       `json:"target,omitempty"`
	ChannelID   string                       `json:"channel_id"`
	Query       string                       `json:"query"`
	Sender      string                       `json:"sender,omitempty"`
	Sort        string                       `json:"sort"`
	Before      string                       `json:"before,omitempty"`
	After       string                       `json:"after,omitempty"`
	Limit       int                          `json:"limit"`
	Offset      int                          `json:"offset"`
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

type agentTransportSearchOptions struct {
	Target     *agentTransportTarget
	Query      string
	Sender     string
	SenderType string
	SenderID   pgtype.UUID
	Sort       string
	Before     time.Time
	After      time.Time
	Limit      int
	Offset     int
}

const (
	agentTransportSearchSortNewest   = "newest"
	agentTransportSearchSortOldest   = "oldest"
	agentTransportSearchDefaultLimit = 50
	agentTransportSearchMaxOffset    = 10_000
)

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
	origin chatOutputOrigin
}

func (h *Handler) channelInitiatorForTransportSource(ctx context.Context, source agentTransportSource) pgtype.UUID {
	return h.agentOwnerID(ctx, source.origin.workspaceID, source.origin.agentID)
}

func (h *Handler) agentTransportInitiatorUserID(r *http.Request, source agentTransportSource) (pgtype.UUID, bool) {
	ownerID := h.agentOwnerID(r.Context(), source.origin.workspaceID, source.origin.agentID)
	return ownerID, ownerID.Valid
}

func (h *Handler) AgentTransportSendMessage(w http.ResponseWriter, r *http.Request) {
	source, ok := h.requireAgentTransportSource(w, r)
	if !ok {
		return
	}
	var req AgentTransportSendRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Parts != nil {
		writeError(w, http.StatusBadRequest, "agent message send accepts content and attachment_ids, not parts")
		return
	}
	content := strings.TrimSpace(req.Content)
	if content == "" {
		writeError(w, http.StatusBadRequest, "content is required; attachment-only Agent messages are not supported")
		return
	}
	attachmentIDs, ok := parseUUIDSliceOrBadRequest(w, req.AttachmentIDs, "attachment_id")
	if !ok {
		return
	}
	parts := agentTransportAttachmentMessageParts(content, attachmentIDs)
	if len([]rune(content)) > channelMessageMaxLen {
		writeError(w, http.StatusBadRequest, "content is too long")
		return
	}
	clientMessageID := strings.TrimSpace(req.ClientMessageID)
	if clientMessageID == "" {
		writeError(w, http.StatusBadRequest, "client_message_id is required")
		return
	}
	if len([]rune(clientMessageID)) > channelClientMessageIDMaxLen {
		writeError(w, http.StatusBadRequest, "client_message_id is too long")
		return
	}
	target, err := h.resolveAgentTransportTarget(r.Context(), source.origin, req.Target, true)
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
	parts, err = h.normalizeLegacyAgentAudioVoiceReply(r.Context(), source, content, parts)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to normalize message delivery")
		return
	}
	parts, err = h.enforceAgentTransportVoiceReply(r.Context(), source, target, content, parts)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to enforce message delivery")
		return
	}
	content, parts, err = messageparts.Normalize(content, parts)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid message parts: "+err.Error())
		return
	}
	kindHint, ok := agentTransportKindHintFromRequest(w, req)
	if !ok {
		return
	}
	_, err = h.finalizedAgentTransportInsertInput(r.Context(), source, target, content, parts, clientMessageID, kindHint)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	initiatorID := h.channelInitiatorForTransportSource(r.Context(), source)
	seenUpToSeq := int64(-1)
	if !req.BypassFreshness {
		seenUpToSeq, err = h.agentTransportSeenUpToSeq(r.Context(), source, target.raw, req.SeenUpToSeq)
		if err != nil {
			slog.Warn("agent transport freshness check failed", "agent_id", uuidToString(source.origin.agentID), "error", err)
			writeError(w, http.StatusInternalServerError, "failed to send message")
			return
		}
	}
	result, err := h.createAgentTransportMessage(r.Context(), source, target, content, parts, attachmentIDs, clientMessageID, seenUpToSeq, initiatorID, req.ContinueAnyway, nil, kindHint)
	if err != nil {
		var freshnessHold *agentTransportFreshnessHoldError
		if errors.As(err, &freshnessHold) {
			writeAgentTransportHeldResponse(w, target, freshnessHold.decision, freshnessHold.transportID)
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
		var wrongTurn *agentDMTurnError
		if errors.As(err, &wrongTurn) {
			writeJSON(w, http.StatusConflict, map[string]any{
				"error":             wrongTurn.Error(),
				"state":             "waiting_for_peer",
				"expected_agent_id": uuidToString(wrongTurn.ExpectedSender),
			})
			return
		}
		slog.Warn("agent transport send failed", "agent_id", uuidToString(source.origin.agentID), "error", err)
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
}

func agentTransportAttachmentMessageParts(content string, attachmentIDs []pgtype.UUID) []protocol.MessagePart {
	parts := make([]protocol.MessagePart, 0, 1+len(attachmentIDs))
	parts = append(parts, protocol.MessagePart{Type: protocol.MessagePartTypeText, Text: content})
	for _, attachmentID := range attachmentIDs {
		parts = append(parts, protocol.MessagePart{Type: protocol.MessagePartTypeAttachment, AttachmentID: uuidToString(attachmentID)})
	}
	return parts
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
	target, err := h.resolveAgentTransportTarget(r.Context(), source.origin, req.Target, true)
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
	anchorMode, anchorID, anchorSeq, err := req.anchor()
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	limit := clampAgentTransportLimit(req.Limit, 20)
	target, err := h.resolveAgentTransportTarget(r.Context(), source.origin, req.Target, false)
	if err != nil {
		if errors.Is(err, errReminderSendOutsideAnchor) {
			writeError(w, http.StatusBadRequest, errReminderSendOutsideAnchor.Error())
			return
		}
		writeError(w, http.StatusBadRequest, "invalid target")
		return
	}
	messages, err := h.readAgentTransportMessages(r.Context(), target, anchorMode, anchorID, anchorSeq, limit)
	if err != nil {
		switch {
		case errors.Is(err, errAgentTransportReadAnchorInvalid):
			writeError(w, http.StatusBadRequest, err.Error())
		case errors.Is(err, errAgentTransportReadAnchorNotFound):
			writeError(w, http.StatusNotFound, err.Error())
		case errors.Is(err, errAgentTransportReadAnchorAmbiguous):
			writeError(w, http.StatusConflict, err.Error())
		default:
			slog.Warn("agent transport read failed", "agent_id", uuidToString(source.origin.agentID), "error", err)
			writeError(w, http.StatusInternalServerError, "failed to read messages")
		}
		return
	}
	h.decorateAgentTransportMessages(r.Context(), target.channel.WorkspaceID, messages)
	seenUpToSeq := maxChannelMessageSeq(messages)
	contextTarget := agentTransportCanonicalMessageTarget(target)
	response := AgentTransportReadResponse{
		Action:        agentTransportActionRead,
		Target:        target.raw,
		ChannelID:     target.channel.ID,
		ContextTarget: contextTarget,
		Messages:      messages,
		Limit:         limit,
		SeenUpToSeq:   seenUpToSeq,
		TransportID:   "",
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
	_ context.Context,
	_ agentTransportSource,
	_ agentTransportTarget,
	_ string,
	parts []protocol.MessagePart,
) ([]protocol.MessagePart, error) {
	// A canonical Message delivery deliberately has no hidden source task.
	// Explicit --voice remains supported; inferred modality cannot safely be
	// reconstructed from a retired inbox event.
	return parts, nil
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
	search, err := parseAgentTransportSearchOptions(req)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if rawTarget := strings.TrimSpace(req.Target); rawTarget != "" {
		target, err := h.resolveAgentTransportTarget(r.Context(), source.origin, rawTarget, false)
		if err != nil || !h.agentHasSurfaceAccess(r.Context(), source.origin.workspaceID, source.origin.agentID, parseUUID(target.channel.ID)) {
			writeError(w, http.StatusBadRequest, "invalid target")
			return
		}
		search.Target = &target
	}
	if search.Query == "" && search.Target == nil && !search.SenderID.Valid && search.Before.IsZero() && search.After.IsZero() {
		writeError(w, http.StatusBadRequest, "query or at least one filter is required")
		return
	}
	total, results, err := h.searchAgentTransportMessages(r.Context(), source, search)
	if err != nil {
		slog.Warn("agent transport search failed", "agent_id", uuidToString(source.origin.agentID), "error", err)
		writeError(w, http.StatusInternalServerError, "failed to search messages")
		return
	}
	resultIDs := make([]string, 0, len(results))
	for _, result := range results {
		resultIDs = append(resultIDs, result.MessageID)
	}
	channelID := pgtype.UUID{}
	targetRaw := ""
	if search.Target != nil {
		channelID = parseUUID(search.Target.channel.ID)
		targetRaw = search.Target.raw
	}
	_ = resultIDs
	writeJSON(w, http.StatusOK, AgentTransportSearchResponse{
		Action:      agentTransportActionSearch,
		Target:      targetRaw,
		ChannelID:   uuidToString(channelID),
		Query:       search.Query,
		Sender:      search.Sender,
		Sort:        search.Sort,
		Before:      agentTransportSearchTimeString(search.Before),
		After:       agentTransportSearchTimeString(search.After),
		Limit:       search.Limit,
		Offset:      search.Offset,
		Total:       total,
		Results:     results,
		TransportID: "",
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
		slog.Warn("agent transport canonical message lookup failed", "agent_id", uuidToString(source.origin.agentID), "error", err)
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
	target, err := h.resolveAgentTransportTarget(r.Context(), source.origin, req.Target, false)
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
	if changed {
		h.emitAgentThreadUnfollowedEvent(w, r.Context(), target.channel.WorkspaceID, channelID, target.threadRootMessageID, source.origin.agentID)
	}
	writeJSON(w, http.StatusOK, AgentTransportThreadUnfollowResponse{
		Action:      agentTransportActionThreadUnfollow,
		Target:      target.raw,
		ChannelID:   target.channel.ID,
		MessageID:   uuidToString(target.threadRootMessageID),
		TransportID: "",
	})
}

func (h *Handler) requireAgentTransportSource(w http.ResponseWriter, r *http.Request) (agentTransportSource, bool) {
	if r.Header.Get("X-Actor-Source") != "agent_credential" {
		writeError(w, http.StatusForbidden, "agent transport requires an agent credential")
		return agentTransportSource{}, false
	}
	for _, header := range []string{
		"X-Task-ID",
		"X-Agent-Inbox-Event-ID",
		"X-Agent-Inbox-Delivery-ID",
		"X-Agent-Inbox-Lease-Token",
	} {
		if strings.TrimSpace(r.Header.Get(header)) != "" {
			writeError(w, http.StatusBadRequest, "agent transport does not accept task or inbox delivery context")
			return agentTransportSource{}, false
		}
	}
	return h.requireAgentCredentialChatTransport(w, r)
}

// requireAgentCredentialChatTransport authorizes chat actions from the durable
// credential identity stamped by auth middleware. Chat is not scoped to an
// issue task, inbox delivery, lease, or current runtime turn.
func (h *Handler) requireAgentCredentialChatTransport(w http.ResponseWriter, r *http.Request) (agentTransportSource, bool) {
	workspaceID, ok := parseUUIDOrBadRequest(w, ctxWorkspaceID(r.Context()), "workspace id")
	if !ok {
		return agentTransportSource{}, false
	}
	agentID, ok := parseUUIDOrBadRequest(w, r.Header.Get("X-Agent-ID"), "agent id")
	if !ok {
		return agentTransportSource{}, false
	}
	return agentTransportSource{
		origin: chatOutputOrigin{workspaceID: workspaceID, agentID: agentID},
	}, true
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

func (h *Handler) resolveAgentTransportTarget(ctx context.Context, origin chatOutputOrigin, rawTarget string, createDM bool) (agentTransportTarget, error) {
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
		creatorID := h.agentOwnerID(ctx, origin.workspaceID, origin.agentID)
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

func (h *Handler) createAgentTransportMessage(ctx context.Context, source agentTransportSource, target agentTransportTarget, content string, parts []protocol.MessagePart, attachmentIDs []pgtype.UUID, clientMessageID string, seenUpToSeq int64, initiatorID pgtype.UUID, continueAnyway bool, afterInsert agentTransportMessageAfterInsert, kindHint channelMessageKindHint) (agentTransportMessageResult, error) {
	input, err := h.finalizedAgentTransportInsertInput(ctx, source, target, content, parts, clientMessageID, kindHint)
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
	result, err := h.insertAgentTransportMessage(ctx, source, target, input, content, parts, attachmentIDs, clientMessageID, seenUpToSeq, continueAnyway, afterInsert)
	if err != nil {
		return agentTransportMessageResult{}, err
	}
	{
		msgs := []ChannelMessageResponse{result.Message}
		h.attachChannelMessageAttachments(ctx, uuidToString(source.origin.workspaceID), msgs)
		result.Message = msgs[0]
	}
	if result.Created {
		if target.threadRootMessageID.Valid {
			h.followChannelThreadAgent(ctx, input.ChannelID, target.threadRootMessageID, source.origin.agentID)
			// A reply makes both its author and an agent-authored root's author
			// active thread participants. Keep an explicit unfollow by the root
			// author sticky; normal thread delivery is follower based.
			if target.threadRoot.Type == "agent" && target.threadRoot.AuthorID != nil {
				h.followChannelThreadAgentUnlessExplicitlyUnfollowed(ctx, input.ChannelID, target.threadRootMessageID, parseUUID(*target.threadRoot.AuthorID))
			}
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
			h.runAfterChannelMessageAck(ctx, func(ctx context.Context) {
				lowID, highID, ok := normalizedAgentDMPair(source.origin.agentID, target.recipientID)
				if ok {
					h.publishAgentDMToOwners(
						ctx, target.channel, lowID, highID, protocol.EventChannelMessage, msg,
					)
				}
			})
		}
	}
	return result, nil
}

// finalizedAgentTransportInsertInput is the write boundary for every visible
// agent-transport message. Drafts keep raw author intent; immediate sends rebuild destination-scoped reference anchors here immediately
// before persistence.
func (h *Handler) finalizedAgentTransportInsertInput(ctx context.Context, source agentTransportSource, target agentTransportTarget, content string, parts []protocol.MessagePart, clientMessageID string, kindHint channelMessageKindHint) (channelMessageInsertInput, error) {
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
		KindHint:            kindHint,
	}, nil
}

func agentTransportKindHintFromRequest(w http.ResponseWriter, req AgentTransportSendRequest) (channelMessageKindHint, bool) {
	kind := strings.TrimSpace(req.Kind)
	if kind == "" && req.OutputEnvelope != nil {
		kind = strings.TrimSpace(req.OutputEnvelope.Kind)
	}
	if kind == "" {
		return channelMessageKindHint{}, true
	}
	normalized := protocol.NormalizeChannelMessageKind(kind)
	if normalized == "" || normalized == protocol.ChannelMessageKindSystemReminder {
		writeError(w, http.StatusBadRequest, "invalid agent message kind")
		return channelMessageKindHint{}, false
	}
	return channelMessageKindHint{
		Kind:   normalized,
		Source: protocol.ChannelMessageKindSourceStructured,
	}, true
}

func (h *Handler) insertAgentTransportMessage(ctx context.Context, source agentTransportSource, target agentTransportTarget, input channelMessageInsertInput, _ string, _ []protocol.MessagePart, attachmentIDs []pgtype.UUID, clientMessageID string, seenUpToSeq int64, continueAnyway bool, afterInsert agentTransportMessageAfterInsert) (agentTransportMessageResult, error) {
	tx, err := h.TxStarter.Begin(ctx)
	if err != nil {
		return agentTransportMessageResult{}, err
	}
	if err := h.lockAgentTransportTargetForInsert(ctx, tx, target); err != nil {
		_ = tx.Rollback(ctx)
		return agentTransportMessageResult{}, err
	}
	if existing, found, err := h.findAgentChannelMessageByClientIDWithExec(ctx, tx, input.WorkspaceID, input.ChannelID, input.AuthorID, clientMessageID); err != nil {
		_ = tx.Rollback(ctx)
		return agentTransportMessageResult{}, err
	} else if found {
		result, err := h.completeDuplicateAgentTransportMessageWithExec(ctx, tx, input, attachmentIDs, existing)
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
		if decision.Hold && !continueAnyway {
			if err := tx.Commit(ctx); err != nil {
				return agentTransportMessageResult{}, err
			}
			return agentTransportMessageResult{}, &agentTransportFreshnessHoldError{decision: decision}
		}
	}
	reservation, err := h.reserveAgentDMSendTx(ctx, tx, source, target)
	if err != nil {
		_ = tx.Rollback(ctx)
		return agentTransportMessageResult{}, err
	}
	inserted, err := insertChannelMessageWithPartsExec(ctx, tx, input.ChannelID, input.WorkspaceID, "agent", input.AuthorID, input.AuthorName, input.Content, input.Parts, "multica", nil, input.ClientMessageID, pgtype.UUID{}, pgtype.UUID{}, nil, input.ThreadRootMessageID, input.ThreadID, input.TriggerDepth, input.KindHint)
	if err != nil {
		_ = tx.Rollback(ctx)
		if isUniqueViolation(err) {
			return h.resolveDuplicateAgentTransportMessageAtomic(ctx, target, input, attachmentIDs, clientMessageID)
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
		if err := h.linkVerifiedAgentUploadAttachmentsToChannelMessage(ctx, tx, source, target, parseUUID(msg.ID), attachmentIDs); err != nil {
			_ = tx.Rollback(ctx)
			return agentTransportMessageResult{}, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return agentTransportMessageResult{}, err
	}
	return agentTransportMessageResult{
		Message: msg,
		Created: true,
		AgentDM: reservation,
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

func (h *Handler) completeDuplicateAgentTransportMessageWithExec(ctx context.Context, _ dbExecutor, input channelMessageInsertInput, attachmentIDs []pgtype.UUID, existing ChannelMessageResponse) (agentTransportMessageResult, error) {
	ok, err := h.matchesChannelMessageIdempotencyPayload(ctx, existing, input, attachmentIDs)
	if err != nil {
		return agentTransportMessageResult{}, err
	}
	if !ok {
		return agentTransportMessageResult{}, errChannelClientMessageConflict
	}
	return agentTransportMessageResult{Message: existing, Created: false}, nil
}
func (h *Handler) resolveDuplicateAgentTransportMessageAtomic(ctx context.Context, target agentTransportTarget, input channelMessageInsertInput, attachmentIDs []pgtype.UUID, clientMessageID string) (agentTransportMessageResult, error) {
	tx, err := h.TxStarter.Begin(ctx)
	if err != nil {
		return agentTransportMessageResult{}, err
	}
	if err := h.lockAgentTransportTargetForInsert(ctx, tx, target); err != nil {
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
	result, err := h.completeDuplicateAgentTransportMessageWithExec(ctx, tx, input, attachmentIDs, existing)
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

func (req AgentTransportReadRequest) anchor() (mode, idAnchor string, seqAnchor int64, err error) {
	anchors := []struct {
		mode string
		id   string
		seq  int64
	}{
		{mode: "before", id: strings.TrimSpace(req.BeforeID), seq: req.BeforeSeq},
		{mode: "after", id: strings.TrimSpace(req.AfterID), seq: req.AfterSeq},
		{mode: "around", id: strings.TrimSpace(req.AroundID), seq: req.AroundSeq},
	}
	for _, candidate := range anchors {
		hasID := candidate.id != ""
		hasSeq := candidate.seq != 0
		if !hasID && !hasSeq {
			continue
		}
		if hasID && hasSeq {
			return "", "", 0, fmt.Errorf("use either %s_id or %s_seq, not both", candidate.mode, candidate.mode)
		}
		if mode != "" {
			return "", "", 0, errors.New("only one of before, after, or around may be set")
		}
		mode, idAnchor, seqAnchor = candidate.mode, candidate.id, candidate.seq
	}
	return mode, idAnchor, seqAnchor, nil
}

func agentTransportCanonicalMessageTarget(target agentTransportTarget) string {
	if target.threadRootMessageID.Valid {
		return "thread:" + uuidToString(target.threadRootMessageID)
	}
	return "channel:" + target.channel.ID
}

func (h *Handler) readAgentTransportMessages(ctx context.Context, target agentTransportTarget, mode, anchorID string, anchorSeq int64, limit int) ([]ChannelMessageResponse, error) {
	if mode == "" {
		return h.readAgentTransportMessageWindow(ctx, target, "recent", 0, limit)
	}
	var anchor ChannelMessageResponse
	var err error
	if anchorID != "" {
		anchor, err = h.resolveAgentTransportReadAnchorByID(ctx, target, anchorID)
	} else {
		anchor, err = h.resolveAgentTransportReadAnchorBySeq(ctx, target, anchorSeq)
	}
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

// resolveAgentTransportReadAnchorByID resolves a message identity anchor: a
// full message id or a unique 8+ character id prefix. Identity anchors never
// fall back to sequence interpretation, so a digits-only prefix (UUIDs
// sometimes have one) still resolves as an id.
func (h *Handler) resolveAgentTransportReadAnchorByID(ctx context.Context, target agentTransportTarget, raw string) (ChannelMessageResponse, error) {
	if id, err := uuid.Parse(raw); err == nil {
		messages, err := h.findAgentTransportReadAnchors(ctx, target, `m.id = $4`, id)
		if err != nil {
			return ChannelMessageResponse{}, err
		}
		return oneAgentTransportReadAnchor(messages)
	}
	if !shortAgentMessageIDPattern.MatchString(raw) {
		return ChannelMessageResponse{}, fmt.Errorf("%w: use a full message id or a unique 8+ character id prefix", errAgentTransportReadAnchorInvalid)
	}
	messages, err := h.findAgentTransportReadAnchors(ctx, target, `LOWER(m.id::text) LIKE LOWER($4) || '%'`, raw)
	if err != nil {
		return ChannelMessageResponse{}, err
	}
	return oneAgentTransportReadAnchor(messages)
}

// resolveAgentTransportReadAnchorBySeq resolves a target sequence anchor.
// Sequences are machine bookkeeping (proxy recovery and freshness), so the
// value arrives as an integer and only ever means a position in the target.
func (h *Handler) resolveAgentTransportReadAnchorBySeq(ctx context.Context, target agentTransportTarget, sequence int64) (ChannelMessageResponse, error) {
	if sequence <= 0 {
		return ChannelMessageResponse{}, fmt.Errorf("%w: sequence must be positive", errAgentTransportReadAnchorInvalid)
	}
	messages, err := h.findAgentTransportReadAnchors(ctx, target, `m.seq = $4`, sequence)
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
	h.attachChannelMessageAttachments(ctx, workspaceID, messages)
	h.attachChannelMessageReactions(ctx, workspaceID, messages)
	h.attachChannelMessageReplySummaries(ctx, workspaceID, messages)
	h.attachChannelMessageThreadRootSummaries(ctx, workspaceID, messages)
	applyChannelMessageTombstoneReadModel(messages)
}

func parseAgentTransportSearchOptions(req AgentTransportSearchRequest) (agentTransportSearchOptions, error) {
	options := agentTransportSearchOptions{
		Query:  strings.TrimSpace(req.Query),
		Sender: strings.TrimSpace(req.Sender),
		Sort:   strings.ToLower(strings.TrimSpace(req.Sort)),
		Limit:  req.Limit,
		Offset: req.Offset,
	}
	if options.Sort == "" {
		options.Sort = agentTransportSearchSortNewest
	}
	if options.Sort != agentTransportSearchSortNewest && options.Sort != agentTransportSearchSortOldest {
		return agentTransportSearchOptions{}, errors.New("sort must be newest or oldest")
	}
	if options.Limit == 0 {
		options.Limit = agentTransportSearchDefaultLimit
	}
	if options.Limit < 1 || options.Limit > channelMessagesMaxLimit {
		return agentTransportSearchOptions{}, fmt.Errorf("limit must be between 1 and %d", channelMessagesMaxLimit)
	}
	if options.Offset < 0 || options.Offset > agentTransportSearchMaxOffset {
		return agentTransportSearchOptions{}, fmt.Errorf("offset must be between 0 and %d", agentTransportSearchMaxOffset)
	}
	if options.Sender != "" {
		typ, rawID, ok := strings.Cut(options.Sender, ":")
		typ = strings.ToLower(strings.TrimSpace(typ))
		rawID = strings.TrimSpace(rawID)
		if !ok || (typ != "user" && typ != "agent") || rawID == "" {
			return agentTransportSearchOptions{}, errors.New("sender must be user:<uuid> or agent:<uuid>")
		}
		if _, err := uuid.Parse(rawID); err != nil {
			return agentTransportSearchOptions{}, errors.New("sender must be user:<uuid> or agent:<uuid>")
		}
		options.SenderType = typ
		options.SenderID = parseUUID(rawID)
		options.Sender = typ + ":" + uuidToString(options.SenderID)
	}
	var err error
	if options.Before, err = parseAgentTransportSearchTime(req.Before, "before"); err != nil {
		return agentTransportSearchOptions{}, err
	}
	if options.After, err = parseAgentTransportSearchTime(req.After, "after"); err != nil {
		return agentTransportSearchOptions{}, err
	}
	if !options.Before.IsZero() && !options.After.IsZero() && !options.After.Before(options.Before) {
		return agentTransportSearchOptions{}, errors.New("after must be earlier than before")
	}
	return options, nil
}

func parseAgentTransportSearchTime(raw, name string) (time.Time, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, nil
	}
	parsed, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return time.Time{}, fmt.Errorf("%s must be an RFC3339 timestamp", name)
	}
	return parsed.UTC(), nil
}

func agentTransportSearchTimeString(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339Nano)
}

func (h *Handler) searchAgentTransportMessages(ctx context.Context, source agentTransportSource, options agentTransportSearchOptions) (int, []ChannelMessageSearchResult, error) {
	args := []any{source.origin.workspaceID, source.origin.agentID}
	where := []string{
		"m.workspace_id = $1",
		"m.author_type IN ('user', 'agent')",
		"m.deleted_at IS NULL",
		`EXISTS (
			SELECT 1
			FROM channel_member visible
			WHERE visible.workspace_id = m.workspace_id
			  AND visible.channel_id = m.channel_id
			  AND visible.member_type = 'agent'
			  AND visible.member_id = $2
		)`,
	}
	if options.Target != nil {
		args = append(args, parseUUID(options.Target.channel.ID))
		where = append(where, fmt.Sprintf("m.channel_id = $%d", len(args)))
		if options.Target.threadRootMessageID.Valid {
			args = append(args, options.Target.threadRootMessageID)
			where = append(where, fmt.Sprintf("(m.id = $%d OR m.thread_root_message_id = $%d)", len(args), len(args)))
		} else {
			where = append(where, "m.thread_root_message_id IS NULL")
		}
	}
	if options.Query != "" {
		args = append(args, "%"+escapeLike(options.Query)+"%")
		where = append(where, fmt.Sprintf("m.content ILIKE $%d ESCAPE '\\'", len(args)))
	}
	if options.SenderID.Valid {
		args = append(args, options.SenderType, options.SenderID)
		where = append(where, fmt.Sprintf("m.author_type = $%d AND m.author_id = $%d", len(args)-1, len(args)))
	}
	if !options.Before.IsZero() {
		args = append(args, options.Before)
		where = append(where, fmt.Sprintf("m.created_at < $%d", len(args)))
	}
	if !options.After.IsZero() {
		args = append(args, options.After)
		where = append(where, fmt.Sprintf("m.created_at > $%d", len(args)))
	}
	whereSQL := strings.Join(where, " AND ")
	var total int
	if err := h.DB.QueryRow(ctx, `SELECT count(*) FROM channel_message m WHERE `+whereSQL, args...).Scan(&total); err != nil {
		return 0, nil, err
	}
	order := "m.created_at DESC, m.id DESC"
	if options.Sort == agentTransportSearchSortOldest {
		order = "m.created_at ASC, m.id ASC"
	}
	args = append(args, options.Limit, options.Offset)
	rows, err := h.DB.Query(ctx, `
		SELECT m.id, m.channel_id, m.thread_root_message_id, m.author_type, m.author_id, m.author_name,
		       m.content, m.created_at
		FROM channel_message m
		WHERE `+whereSQL+`
		ORDER BY `+order+`
		LIMIT $`+strconv.Itoa(len(args)-1)+` OFFSET $`+strconv.Itoa(len(args)), args...)
	if err != nil {
		return 0, nil, err
	}
	defer rows.Close()
	results := make([]ChannelMessageSearchResult, 0, options.Limit)
	for rows.Next() {
		var id, channelID, rootID, authorID pgtype.UUID
		var authorType, authorName, content string
		var createdAt pgtype.Timestamptz
		if err := rows.Scan(&id, &channelID, &rootID, &authorType, &authorID, &authorName, &content, &createdAt); err != nil {
			return 0, nil, err
		}
		results = append(results, ChannelMessageSearchResult{
			MessageID:           uuidToString(id),
			ChannelID:           uuidToString(channelID),
			ThreadRootMessageID: uuidToPtr(rootID),
			InThread:            rootID.Valid,
			Type:                authorType,
			AuthorID:            uuidToPtr(authorID),
			AuthorName:          authorName,
			Content:             content,
			CreatedAt:           timestampToString(createdAt),
		})
	}
	if err := rows.Err(); err != nil {
		return 0, nil, err
	}
	return total, results, nil
}

// agentTransportNewerMessagesHold decides the L3 transport hold policy for the
// bounded window of messages newer than the agent's cursor. It is deliberately
// conservative: when the bounded window is exhausted (omitted > 0) or empty, or
// any newer message carries new actionable content / a directive-mention, we
// keep the hold. Only when we can positively establish that every fetched newer
// message is a non-action pure confirmation (no new info, no directive) do we
// release the hold: the agent's pending content causes no genuine ordering
// conflict, so it is admitted directly (soft) instead of forcing a
// hold -> continue_anyway round-trip on every polite ack.
func agentTransportNewerMessagesHold(messages []ChannelMessageResponse, omitted int64) bool {
	if omitted > 0 {
		// Unseen tail may carry actionable content: cannot prove no conflict.
		return true
	}
	if len(messages) == 0 {
		// Nothing fetched to judge: keep the defensive hold.
		return true
	}
	for _, msg := range messages {
		pure, hasAgentMention := channelMessageIsPureConfirmation(msg)
		// A pure confirmation directed at an agent is still an actionable
		// directive the sender may not have seen — treat it as real conflict.
		if !pure || hasAgentMention {
			return true
		}
	}
	return false
}

func (h *Handler) agentTransportFreshnessDecisionWithSeen(ctx context.Context, exec dbExecutor, source agentTransportSource, target agentTransportTarget, seenUpToSeq int64) (agentTransportFreshnessDecision, error) {
	if seenUpToSeq <= 0 {
		return agentTransportFreshnessDecision{SeenUpToSeq: seenUpToSeq}, nil
	}
	latestSeq, totalNewer, err := h.agentTransportNewerMessageStats(ctx, exec, source, target, seenUpToSeq)
	if err != nil || totalNewer <= 0 {
		return agentTransportFreshnessDecision{SeenUpToSeq: seenUpToSeq, LatestSeq: latestSeq}, err
	}
	messages, err := h.readAgentTransportMessagesAfterSeq(ctx, source, target, seenUpToSeq, agentTransportFreshnessHoldLimit)
	if err != nil {
		return agentTransportFreshnessDecision{}, err
	}
	h.decorateAgentTransportMessages(ctx, target.channel.WorkspaceID, messages)
	omitted := totalNewer - int64(len(messages))
	if omitted < 0 {
		omitted = 0
	}
	hold := agentTransportNewerMessagesHold(messages, omitted)
	return agentTransportFreshnessDecision{
		Hold:        hold,
		SeenUpToSeq: seenUpToSeq,
		LatestSeq:   latestSeq,
		TotalNewer:  totalNewer,
		Messages:    messages,
		Omitted:     omitted,
		ProducerID:  agentTransportFreshnessProducerID(source, target, seenUpToSeq, latestSeq),
	}, nil
}

func (h *Handler) agentTransportSeenUpToSeq(ctx context.Context, source agentTransportSource, target string, requested int64) (int64, error) {
	_ = ctx
	_ = source
	_ = target
	if requested > 0 {
		return requested, nil
	}
	return 0, nil
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
		AvailableActions:        []string{"review_newer_messages", "resend_with_continue_anyway"},
		ContinueAnywaySuggested: true,
		HeldMessages:            decision.Messages,
		NewMessageCount:         decision.TotalNewer,
		ShownMessageCount:       int64(len(decision.Messages)),
		OmittedMessageCount:     decision.Omitted,
		SeenUpToSeq:             decision.SeenUpToSeq,
		LatestSeq:               decision.LatestSeq,
		TransportID:             transportID,
		ContextWindow: AgentTransportFreshnessContextWindow{
			OldestSeq:     firstChannelMessageSeqValue(decision.Messages),
			NewestSeq:     maxChannelMessageSeq(decision.Messages),
			OlderBoundary: olderBoundary,
			NewerBoundary: "No newer.",
		},
	})
}

func agentTransportFreshnessProducerID(source agentTransportSource, target agentTransportTarget, seenUpToSeq, latestSeq int64) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s:%s:%s:%d:%d", uuidToString(source.origin.workspaceID), uuidToString(source.origin.agentID), target.raw, seenUpToSeq, latestSeq)))
	return "freshness_decision_fact:" + hex.EncodeToString(sum[:8])
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
