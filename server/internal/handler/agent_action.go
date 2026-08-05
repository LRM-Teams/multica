package handler

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
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
	ActionType  string  `json:"action_type"`
	Name        string  `json:"name"`
	Description string  `json:"description"`
	DraftHint   string  `json:"draft_hint"` // UI-only; not stored
	ChannelID   *string `json:"channel_id"`
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
