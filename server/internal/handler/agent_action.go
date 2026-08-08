package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/util"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// agent:create Proposals are canonical channel Messages. Agents prepare a
// message with a non-sensitive proposal snapshot; a human later supplies final
// runtime configuration while committing that Message exactly once.

const (
	agentActionTypeCreate     = "agent:create"
	agentActionStatusPrepared = "prepared"
	agentActionStatusExecuted = "executed"
)

type agentActionPrepareRequest struct {
	ActionType  string `json:"action_type"`
	Name        string `json:"name"`
	Description string `json:"description"`
	// PreferredComputer is the Raft-style preferred Computer suggestion. It is
	// a suggestion the human can change, never a required binding (LRM-2343).
	PreferredComputer *string `json:"preferred_computer,omitempty"`
	// Target is the canonical channel/DM/thread where the Proposal is posted.
	Target string `json:"target"`
	// ClientRequestID is the CLI/agent-generated stable idempotency key. Same
	// proposer/workspace + same normalized target/action returns the original
	// message_id; same key with different input returns 409 (LRM-2343 stories 4-6).
	ClientRequestID *string `json:"client_request_id,omitempty"`
}

type agentActionCreatePayload struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

type agentActionProposalResponse struct {
	ActionType        string                   `json:"action_type"`
	MessageID         string                   `json:"message_id"`
	Status            string                   `json:"status"`
	Payload           agentActionCreatePayload `json:"payload"`
	PreparedByAgentID string                   `json:"prepared_by_agent_id"`
}

// AgentTransportPrepareAction atomically prepares a human-confirmable
// agent:create Proposal Message and its server-side commit record.
func (h *Handler) AgentTransportPrepareAction(w http.ResponseWriter, r *http.Request) {
	source, ok := h.requireAgentTransportSource(w, r)
	if !ok {
		return
	}
	origin := source.origin
	if !h.isActiveOnboardingAgent(r.Context(), origin.workspaceID, origin.agentID) {
		writeError(w, http.StatusForbidden, "only the active onboarding agent may prepare hiring proposals")
		return
	}
	var req agentActionPrepareRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	actionType := strings.TrimSpace(req.ActionType)
	if actionType == "" {
		writeError(w, http.StatusBadRequest, "action_type is required")
		return
	}
	if actionType != agentActionTypeCreate {
		writeError(w, http.StatusBadRequest, "unsupported action_type (only agent:create)")
		return
	}

	name := strings.TrimSpace(req.Name)
	description := strings.TrimSpace(req.Description)
	if name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	if err := validateIdentityHandle(name); err != nil {
		writeError(w, http.StatusBadRequest, "name must use 1-32 lowercase letters, digits, or hyphens")
		return
	}
	if utf8.RuneCountInString(description) > maxAgentDescriptionLength {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("description must be %d characters or fewer", maxAgentDescriptionLength))
		return
	}
	targetRaw := strings.TrimSpace(req.Target)
	if targetRaw == "" {
		writeError(w, http.StatusBadRequest, "target is required")
		return
	}
	target, err := h.resolveAgentTransportTarget(r.Context(), source.origin, targetRaw, true)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid or ambiguous target; use #channel, #channel:<threadId>, or `dm:@<handle>`")
		return
	}
	clientRequestID := ""
	if req.ClientRequestID != nil {
		clientRequestID = strings.TrimSpace(*req.ClientRequestID)
	}
	if clientRequestID == "" {
		writeError(w, http.StatusBadRequest, "client_request_id is required")
		return
	}
	if len([]rune(clientRequestID)) > channelClientMessageIDMaxLen {
		writeError(w, http.StatusBadRequest, "client_request_id is too long")
		return
	}

	actionPart := agentActionMessagePart(name, description, req.PreferredComputer)
	result, err := h.createAgentTransportMessage(
		r.Context(), source, target, buildAgentCreationProposalContent(name), []protocol.MessagePart{actionPart}, nil,
		clientRequestID, 0, pgtype.UUID{}, false,
		func(ctx context.Context, tx pgx.Tx, message ChannelMessageResponse) error {
			return seedAgentActionMessageTx(ctx, tx, origin.workspaceID, message.ID, origin.agentID, name, description, req.PreferredComputer)
		},
	)
	if err != nil {
		if errors.Is(err, errChannelClientMessageConflict) {
			slog.Warn("agent action prepare idempotency payload conflict", "agent_id", uuidToString(origin.agentID), "client_request_id", clientRequestID, "target", target.raw)
			writeError(w, http.StatusConflict, "client_request_id conflicts with an existing action message")
			return
		}
		slog.Warn("agent action prepare create message failed", "agent_id", uuidToString(origin.agentID), "error", err)
		writeError(w, http.StatusInternalServerError, "failed to prepare agent:create proposal")
		return
	}
	if !actionPartPayloadMatches(result.Message.Parts, name, description, req.PreferredComputer) {
		writeError(w, http.StatusConflict, "client_request_id conflicts with a different agent:create proposal")
		return
	}
	proposalStatus := agentActionStatusPrepared
	if !result.Created {
		proposal, exists, err := h.loadAgentActionForCommit(r.Context(), origin.workspaceID, parseUUID(result.Message.ID))
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to load existing agent:create proposal")
			return
		}
		if !exists || !agentActionProposalMatches(proposal.proposed, name, description, req.PreferredComputer) {
			writeError(w, http.StatusConflict, "client_request_id conflicts with a different agent:create proposal")
			return
		}
		proposalStatus = proposal.status
	}

	writeJSON(w, http.StatusCreated, agentActionProposalResponse{
		ActionType:        agentActionTypeCreate,
		MessageID:         result.Message.ID,
		Status:            proposalStatus,
		Payload:           agentActionCreatePayload{Name: name, Description: description},
		PreparedByAgentID: uuidToString(origin.agentID),
	})
}

// extractActionMessageID reads the optional canonical action_message_id from
// CreateAgent raw JSON (LRM-2343 S2). Unlike the legacy action_card_id, this is
// the canonical channel_message id that carries the agent:create action part.
func extractActionMessageID(rawFields map[string]json.RawMessage) (pgtype.UUID, bool, error) {
	var empty pgtype.UUID
	raw, ok := rawFields["action_message_id"]
	if !ok {
		return empty, false, nil
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return empty, true, fmt.Errorf("action_message_id must be a UUID")
	}
	s = strings.TrimSpace(s)
	if s == "" {
		return empty, false, nil
	}
	id, err := util.ParseUUID(s)
	if err != nil || !id.Valid {
		return empty, true, fmt.Errorf("action_message_id must be a UUID")
	}
	return id, true, nil
}

// agentActionMessagePart builds the canonical agent:create action part attached
// to the prepared channel_message. The proposal only carries name, description
// and an optional preferred Computer — never runtime/model/reasoning/credential
// (LRM-2343 Implementation Decisions "Prepare contract and canonical Message").
// The part is a reference anchored to the proposal title span in the content so
// the server-side mention/reference resolver preserves it (references without a
// verified source span are dropped as unverified caller sidecars).
func agentActionMessagePart(name, description string, preferredComputer *string) protocol.MessagePart {
	params := map[string]any{
		"name":        name,
		"description": description,
		"status":      agentActionStatusPrepared,
	}
	if preferredComputer != nil && strings.TrimSpace(*preferredComputer) != "" {
		params["preferred_computer"] = strings.TrimSpace(*preferredComputer)
	}
	raw, err := json.Marshal(params)
	if err != nil {
		raw = nil
	}
	prefix := "[agent:create proposal] "
	nameUTF16 := utf16.Encode([]rune(name))
	start := len(utf16.Encode([]rune(prefix)))
	end := start + len(nameUTF16)
	return protocol.MessagePart{
		Type:              protocol.MessagePartTypeReference,
		RefType:           "agent:create",
		RefID:             name,
		Label:             name,
		ContentStartUTF16: &start,
		ContentEndUTF16:   &end,
		Params:            raw,
	}
}

// actionPartPayloadMatches reports whether an existing agent:create action part
// (matched by client_request_id idempotency) still carries the same normalized
// proposal payload as the current request. Used to return 409 when the same
// idempotency key is reused for a different action.
func actionPartPayloadMatches(parts []protocol.MessagePart, name, description string, preferredComputer *string) bool {
	var existing *protocol.MessagePart
	for i := range parts {
		if parts[i].RefType == "agent:create" {
			existing = &parts[i]
			break
		}
	}
	if existing == nil {
		return false
	}
	var p struct {
		Name              string `json:"name"`
		Description       string `json:"description"`
		PreferredComputer string `json:"preferred_computer"`
	}
	_ = json.Unmarshal(existing.Params, &p)
	wantedComputer := ""
	if preferredComputer != nil {
		wantedComputer = strings.TrimSpace(*preferredComputer)
	}
	return p.Name == name && p.Description == description && strings.TrimSpace(p.PreferredComputer) == wantedComputer
}

// buildAgentCreationProposalContent returns the canonical proposal Message
// content. The name is permanent after creation. It is kept in lock-step with the UTF-16 anchor computed by
// agentActionMessagePart.
func buildAgentCreationProposalContent(name string) string {
	return "[agent:create proposal] " + name
}

// seedAgentActionMessage records the canonical agent:create action commit state
// keyed by the prepared channel_message id when prepare created a Message
// (LRM-2343 S2). The proposed payload is the non-sensitive proposal snapshot
// (name/description/preferred Computer); status starts at prepared and is CAS'd
// to executed by CreateAgent inside the same commit transaction.
func seedAgentActionMessageTx(ctx context.Context, tx pgx.Tx, workspaceID pgtype.UUID, messageID string, preparedByAgentID pgtype.UUID, name, description string, preferredComputer *string) error {
	proposed := map[string]any{
		"name":        name,
		"description": description,
	}
	if preferredComputer != nil && strings.TrimSpace(*preferredComputer) != "" {
		proposed["preferred_computer"] = strings.TrimSpace(*preferredComputer)
	}
	proposedRaw, err := json.Marshal(proposed)
	if err != nil {
		return err
	}
	mid, err := util.ParseUUID(messageID)
	if err != nil || !mid.Valid {
		return fmt.Errorf("invalid channel_message id %q", messageID)
	}
	wsID := workspaceID
	if !wsID.Valid {
		return fmt.Errorf("invalid workspace id")
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO agent_action (
			channel_message_id, workspace_id, action_type, status,
			proposed_payload, prepared_by_agent_id, prepared_at
		) VALUES ($1, $2, $3, $4, $5::jsonb, $6, now())
		ON CONFLICT (channel_message_id) DO NOTHING`,
		mid, wsID, agentActionTypeCreate, agentActionStatusPrepared, proposedRaw, nullableUUID(preparedByAgentID))
	return err
}

func agentActionProposalMatches(proposed map[string]any, name, description string, preferredComputer *string) bool {
	if strings.TrimSpace(asString(proposed["name"])) != name || strings.TrimSpace(asString(proposed["description"])) != description {
		return false
	}
	wantedComputer := ""
	if preferredComputer != nil {
		wantedComputer = strings.TrimSpace(*preferredComputer)
	}
	return strings.TrimSpace(asString(proposed["preferred_computer"])) == wantedComputer
}

func asString(v any) string {
	s, _ := v.(string)
	return s
}
