package handler

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"unicode/utf8"

	"github.com/jackc/pgx/v5/pgtype"
)

// agent:create action cards (Frank/Parker 2026-08-04 hire hard-cut).
//
// Agents prepare a slim hire card (name + optional description). Humans open
// CreateAgentDialog (existing FE) via multica://create-agent?draft_id=… and
// POST /api/agents with draft_id; CreateAgent marks the draft used.
//
// Storage reuses agent_creation_draft so existing FE draft_id wiring keeps
// working until FE rewires to a first-class action-card type. Fat draft create
// (instructions/tools/notes) is no longer the agent hire path.

const (
	agentActionTypeCreate     = "agent:create"
	agentActionStatusPrepared = "prepared"
)

type agentActionPrepareRequest struct {
	ActionType  string  `json:"action_type"`
	Name        string  `json:"name"`
	Description string  `json:"description"`
	DraftHint   string  `json:"draft_hint"` // UI-only; not persisted
	ChannelID   *string `json:"channel_id"` // optional group context for the draft row
}

type agentActionCreatePayload struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	DraftHint   string `json:"draft_hint,omitempty"`
}

type agentActionPrepareResponse struct {
	ActionType string                   `json:"action_type"`
	ID         string                   `json:"id"`
	Status     string                   `json:"status"`
	Payload    agentActionCreatePayload `json:"payload"`
	CardURL    string                   `json:"card_url"`
	Markdown   string                   `json:"markdown"`
	DraftID    string                   `json:"draft_id"` // same as id; explicit for FE/create
	TargetUser string                   `json:"target_user_id"`
	ChannelID  *string                  `json:"channel_id,omitempty"`
	PreparedBy string                   `json:"prepared_by_agent_id"`
	CreatedAt  string                   `json:"created_at"`
}

// AgentTransportPrepareAction prepares a human-confirmable action card.
// Currently only action_type=agent:create is supported.
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
	draftHint := strings.TrimSpace(req.DraftHint)
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
	if utf8.RuneCountInString(draftHint) > windyMaxDraftTextLen {
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

	// Human who will confirm: anchor human (none here) → agent owner → ws owner.
	targetUserID, okTarget := h.agentReminderScheduleInitiatorUserID(r.Context(), origin.workspaceID, task.AgentID, pgtype.UUID{})
	if !okTarget || !targetUserID.Valid {
		writeError(w, http.StatusUnprocessableEntity, "no human target available for agent:create card")
		return
	}

	// Slim draft: name + description only. Runtime/model/instructions chosen in Dialog.
	draftReq := CreateAgentDraftRequest{
		Name:        name,
		Description: description,
	}
	draft, err := h.insertAgentDraft(r, origin.workspaceID, targetUserID, task.AgentID, pgtype.UUID{}, channelID, draftReq)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to prepare agent:create action")
		return
	}

	cardURL := "multica://create-agent?draft_id=" + url.QueryEscape(draft.ID)
	markdown := fmt.Sprintf("[Create Agent: %s](%s)", name, cardURL)
	writeJSON(w, http.StatusCreated, agentActionPrepareResponse{
		ActionType: agentActionTypeCreate,
		ID:         draft.ID,
		Status:     agentActionStatusPrepared,
		Payload: agentActionCreatePayload{
			Name:        name,
			Description: description,
			DraftHint:   draftHint,
		},
		CardURL:    cardURL,
		Markdown:   markdown,
		DraftID:    draft.ID,
		TargetUser: draft.TargetUserID,
		ChannelID:  draft.ChannelID,
		PreparedBy: uuidToString(task.AgentID),
		CreatedAt:  draft.CreatedAt,
	})
}
