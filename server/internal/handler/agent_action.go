package handler

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/util"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// agent:create action cards (Frank/Parker 2026-08-04 hire hard-cut, no draft bridge).
//
// Agents prepare a card (name + optional description). Humans open
// CreateAgentDialog bound to card id, POST /api/agents with action_card_id,
// and the card is marked done. No agent_creation_draft / draft_id hire path.

const (
	agentActionTypeCreate      = "agent:create"
	agentActionStatusPrepared  = "prepared"
	agentActionStatusDone      = "done"
	agentActionStatusDismissed = "dismissed"

	agentActionLookupOK          = "ok"
	agentActionLookupNotFound    = "agent_action_not_found"
	agentActionLookupNotPrepared = "agent_action_not_prepared"
)

type agentActionPrepareRequest struct {
	ActionType  string `json:"action_type"`
	Name        string `json:"name"`
	Description string `json:"description"`
	// PreferredComputer is the Raft-style preferred Computer suggestion. It is
	// a suggestion the human can change, never a required binding (LRM-2343).
	PreferredComputer *string `json:"preferred_computer,omitempty"`
	DraftHint         string  `json:"draft_hint"` // UI-only; not stored
	// ChannelID is the legacy explicit channel target for the card row.
	ChannelID *string `json:"channel_id"`
	// Target is the canonical channel/DM/thread the proposal card Message is
	// posted to (same spelling as message send). When set, prepare creates the
	// canonical Message in the same operation and returns message_id.
	Target *string `json:"target,omitempty"`
	// ClientRequestID is the CLI/agent-generated stable idempotency key. Same
	// proposer/workspace + same normalized target/action returns the original
	// message_id; same key with different input returns 409 (LRM-2343 stories 4-6).
	ClientRequestID *string `json:"client_request_id,omitempty"`
}

type agentActionCreatePayload struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

type agentActionCardResponse struct {
	ActionType string                   `json:"action_type"`
	ID         string                   `json:"id"`
	Status     string                   `json:"status"`
	Payload    agentActionCreatePayload `json:"payload"`
	// MessageID is the canonical channel_message id that carries the
	// agent:create action part. Non-empty when a target was provided.
	MessageID *string `json:"message_id,omitempty"`
	// Part is the structured message reference for send (issue-like, not multica://).
	Part              *protocol.MessagePart `json:"part,omitempty"`
	PreparedByAgentID *string               `json:"prepared_by_agent_id,omitempty"`
	ChannelID         *string               `json:"channel_id,omitempty"`
	CommittedByUserID *string               `json:"committed_by_user_id,omitempty"`
	CommittedAgentID  *string               `json:"committed_agent_id,omitempty"`
	CreatedAt         string                `json:"created_at"`
	UpdatedAt         string                `json:"updated_at"`
	DoneAt            *string               `json:"done_at,omitempty"`
}

// AgentTransportPrepareAction prepares a human-confirmable action card.
// Only action_type=agent:create is supported.
func (h *Handler) AgentTransportPrepareAction(w http.ResponseWriter, r *http.Request) {
	source, ok := h.requireAgentTransportSource(w, r)
	if !ok {
		return
	}
	task, origin := source.task, source.origin

	var req agentActionPrepareRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
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
	if utf8.RuneCountInString(name) > windyMaxDraftNameLen {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("name must be %d characters or fewer", windyMaxDraftNameLen))
		return
	}
	if utf8.RuneCountInString(description) > maxAgentDescriptionLength {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("description must be %d characters or fewer", maxAgentDescriptionLength))
		return
	}
	if utf8.RuneCountInString(strings.TrimSpace(req.DraftHint)) > windyMaxDraftTextLen {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("draft_hint must be %d characters or fewer", windyMaxDraftTextLen))
		return
	}

	var channelID pgtype.UUID
	if req.ChannelID != nil && strings.TrimSpace(*req.ChannelID) != "" {
		parsed, ok := parseUUIDOrBadRequest(w, strings.TrimSpace(*req.ChannelID), "channel_id")
		if !ok {
			return
		}
		var n int
		if err := h.DB.QueryRow(r.Context(), `
			SELECT count(*) FROM channel_member
			WHERE channel_id = $1 AND workspace_id = $2
			  AND member_type = 'agent' AND member_id = $3`,
			parsed, origin.workspaceID, task.AgentID).Scan(&n); err != nil || n == 0 {
			writeError(w, http.StatusForbidden, "agent is not a member of channel_id")
			return
		}
		channelID = parsed
	}

	// LRM-2343: prepare must already create the canonical Message carrying the
	// agent:create action part so the human can read the card through the normal
	// message path (story 2/3) — the CLI no longer issues a second send. The
	// canonical Message is the唯一 identity; the legacy agent_action_card row
	// remains only as the transitional commit seam until the S4 cutover removes
	// it. Supported targets span group channels, DMs and threads.
	var messageID string
	if req.Target != nil && strings.TrimSpace(*req.Target) != "" {
		target, err := h.resolveAgentTransportTarget(r.Context(), source.task, source.origin, strings.TrimSpace(*req.Target), true)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid or ambiguous target; use #channel, #channel:<threadId>, or `dm:@<handle>`")
			return
		}
		clientRequestID := ""
		if req.ClientRequestID != nil {
			clientRequestID = strings.TrimSpace(*req.ClientRequestID)
		}
		if clientRequestID == "" {
			writeError(w, http.StatusBadRequest, "client_request_id is required when target is provided")
			return
		}
		if len([]rune(clientRequestID)) > channelClientMessageIDMaxLen {
			writeError(w, http.StatusBadRequest, "client_request_id is too long")
			return
		}
		actionPart := agentActionMessagePart(name, description, req.PreferredComputer)
		content := buildActionCardContent(name)
		result, err := h.createAgentTransportMessage(r.Context(), source, target, content, []protocol.MessagePart{actionPart}, nil, clientRequestID, 0, pgtype.UUID{})
		if err != nil {
			if errors.Is(err, errChannelClientMessageConflict) {
				slog.Warn("agent action prepare idempotency payload conflict", "agent_id", uuidToString(task.AgentID), "client_request_id", clientRequestID, "target", target.raw)
				writeError(w, http.StatusConflict, "client_request_id conflicts with an existing action message")
				return
			}
			slog.Warn("agent action prepare create message failed", "agent_id", uuidToString(task.AgentID), "error", err)
			writeError(w, http.StatusInternalServerError, "failed to create action card message")
			return
		}
		// LRM-2343 story 6: same client_request_id reused with different
		// normalized action input must conflict, not silently reuse the old Message.
		if !result.Created && !actionPartPayloadMatches(result.Message.Parts, name, description, req.PreferredComputer) {
			writeError(w, http.StatusConflict, "client_request_id conflicts with a different agent:create proposal")
			return
		}
		messageID = result.Message.ID
		if !channelID.Valid && result.Message.ChannelID != "" {
			if mid, err := util.ParseUUID(result.Message.ChannelID); err == nil && mid.Valid {
				channelID = mid
			}
		}
	}

	payloadBytes, err := json.Marshal(agentActionCreatePayload{Name: name, Description: description})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to prepare agent:create action")
		return
	}

	var (
		id, preparedBy, chID pgtype.UUID
		status               string
		payloadRaw           []byte
		createdAt, updatedAt pgtype.Timestamptz
	)
	err = h.DB.QueryRow(r.Context(), `
		INSERT INTO agent_action_card (
			workspace_id, action_type, status, payload, prepared_by_agent_id, channel_id
		) VALUES ($1, $2, $3, $4::jsonb, $5, $6)
		RETURNING id, action_type, status, payload, prepared_by_agent_id, channel_id, created_at, updated_at`,
		origin.workspaceID, agentActionTypeCreate, agentActionStatusPrepared, payloadBytes,
		task.AgentID, nullableUUID(channelID),
	).Scan(&id, &actionType, &status, &payloadRaw, &preparedBy, &chID, &createdAt, &updatedAt)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to prepare agent:create action")
		return
	}

	cardID := uuidToString(id)
	part := actionCardMessagePart(cardID, name)
	resp := agentActionCardResponse{
		ActionType:        actionType,
		ID:                cardID,
		Status:            status,
		Payload:           agentActionCreatePayload{Name: name, Description: description},
		Part:              &part,
		PreparedByAgentID: uuidToPtr(preparedBy),
		ChannelID:         uuidToPtr(chID),
		CreatedAt:         timestampToString(createdAt),
		UpdatedAt:         timestampToString(updatedAt),
	}
	if messageID != "" {
		resp.MessageID = &messageID
	}
	writeJSON(w, http.StatusCreated, resp)
}

// GetActionCard loads a prepared/done/dismissed action card for workspace members (FE Dialog).
func (h *Handler) GetActionCard(w http.ResponseWriter, r *http.Request) {
	if _, ok := requireUserID(w, r); !ok {
		return
	}
	workspaceID := h.resolveWorkspaceID(r)
	if _, ok := h.workspaceMember(w, r, workspaceID); !ok {
		return
	}
	cardID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "action card id")
	if !ok {
		return
	}
	card, code := h.loadActionCard(r, workspaceID, cardID)
	if code != agentActionLookupOK {
		writeCodedError(w, http.StatusNotFound, agentActionLookupNotFound, "action card not found")
		return
	}
	writeJSON(w, http.StatusOK, card)
}

// DismissActionCard marks a prepared card dismissed (human cancel).
func (h *Handler) DismissActionCard(w http.ResponseWriter, r *http.Request) {
	if _, ok := requireUserID(w, r); !ok {
		return
	}
	workspaceID := h.resolveWorkspaceID(r)
	if _, ok := h.workspaceMember(w, r, workspaceID); !ok {
		return
	}
	cardID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "action card id")
	if !ok {
		return
	}
	tag, err := h.DB.Exec(r.Context(), `
		UPDATE agent_action_card
		SET status = $3, updated_at = now()
		WHERE id = $1 AND workspace_id = $2 AND status = $4`,
		cardID, parseUUID(workspaceID), agentActionStatusDismissed, agentActionStatusPrepared)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to dismiss action card")
		return
	}
	if tag.RowsAffected() == 0 {
		writeCodedError(w, http.StatusConflict, agentActionLookupNotPrepared, "action card is not prepared")
		return
	}
	card, code := h.loadActionCard(r, workspaceID, cardID)
	if code != agentActionLookupOK {
		writeCodedError(w, http.StatusNotFound, agentActionLookupNotFound, "action card not found")
		return
	}
	writeJSON(w, http.StatusOK, card)
}

func (h *Handler) loadActionCard(r *http.Request, workspaceID string, cardID pgtype.UUID) (agentActionCardResponse, string) {
	var empty agentActionCardResponse
	if h == nil || h.DB == nil || !cardID.Valid {
		return empty, agentActionLookupNotFound
	}
	var (
		id, preparedBy, chID, committedBy, committedAgent pgtype.UUID
		actionType, status                                string
		payloadRaw                                        []byte
		createdAt, updatedAt, doneAt                      pgtype.Timestamptz
	)
	err := h.DB.QueryRow(r.Context(), `
		SELECT id, action_type, status, payload, prepared_by_agent_id, channel_id,
		       committed_by_user_id, committed_agent_id, created_at, updated_at, done_at
		FROM agent_action_card
		WHERE id = $1 AND workspace_id = $2`,
		cardID, parseUUID(workspaceID),
	).Scan(&id, &actionType, &status, &payloadRaw, &preparedBy, &chID, &committedBy, &committedAgent, &createdAt, &updatedAt, &doneAt)
	if err != nil {
		return empty, agentActionLookupNotFound
	}
	var payload agentActionCreatePayload
	_ = json.Unmarshal(payloadRaw, &payload)
	idStr := uuidToString(id)
	part := actionCardMessagePart(idStr, payload.Name)
	return agentActionCardResponse{
		ActionType:        actionType,
		ID:                idStr,
		Status:            status,
		Payload:           payload,
		Part:              &part,
		PreparedByAgentID: uuidToPtr(preparedBy),
		ChannelID:         uuidToPtr(chID),
		CommittedByUserID: uuidToPtr(committedBy),
		CommittedAgentID:  uuidToPtr(committedAgent),
		CreatedAt:         timestampToString(createdAt),
		UpdatedAt:         timestampToString(updatedAt),
		DoneAt:            timestampToPtr(doneAt),
	}, agentActionLookupOK
}

// extractActionCardID reads optional action_card_id from CreateAgent raw JSON.
func extractActionCardID(rawFields map[string]json.RawMessage) (pgtype.UUID, bool, error) {
	var empty pgtype.UUID
	raw, ok := rawFields["action_card_id"]
	if !ok {
		return empty, false, nil
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return empty, true, fmt.Errorf("action_card_id must be a UUID")
	}
	s = strings.TrimSpace(s)
	if s == "" {
		return empty, false, nil
	}
	id, err := util.ParseUUID(s)
	if err != nil || !id.Valid {
		return empty, true, fmt.Errorf("action_card_id must be a UUID")
	}
	return id, true, nil
}

// markActionCardDone transitions prepared → done after human CreateAgent.
// Returns false when the card is missing or not in prepared state.
func (h *Handler) markActionCardDone(r *http.Request, workspaceID string, cardID, userID, createdAgentID pgtype.UUID) (string, error) {
	if h == nil || h.DB == nil || !cardID.Valid {
		return agentActionLookupNotFound, nil
	}
	tag, err := h.DB.Exec(r.Context(), `
		UPDATE agent_action_card
		SET status = $4,
		    committed_by_user_id = $5,
		    committed_agent_id = $6,
		    done_at = $7,
		    updated_at = $7
		WHERE id = $1
		  AND workspace_id = $2
		  AND action_type = $3
		  AND status = $8`,
		cardID, parseUUID(workspaceID), agentActionTypeCreate, agentActionStatusDone,
		userID, createdAgentID, time.Now().UTC(), agentActionStatusPrepared,
	)
	if err != nil {
		return "", err
	}
	if tag.RowsAffected() == 0 {
		// Distinguish not found vs already used.
		var status string
		qerr := h.DB.QueryRow(r.Context(), `
			SELECT status FROM agent_action_card WHERE id = $1 AND workspace_id = $2`,
			cardID, parseUUID(workspaceID)).Scan(&status)
		if qerr != nil {
			if qerr == pgx.ErrNoRows {
				return agentActionLookupNotFound, nil
			}
			return "", qerr
		}
		return agentActionLookupNotPrepared, nil
	}
	return agentActionLookupOK, nil
}

func actionCardMessagePart(cardID, name string) protocol.MessagePart {
	return protocol.MessagePart{
		Type:       protocol.MessagePartTypeReference,
		RefType:    "action_card",
		RefSubType: "agent:create",
		RefID:      cardID,
		Label:      name,
	}
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

// buildActionCardContent returns the canonical proposal Message content. It is
// kept in lock-step with the UTF-16 anchor computed by agentActionMessagePart.
func buildActionCardContent(name string) string {
	return "[agent:create proposal] " + name
}
