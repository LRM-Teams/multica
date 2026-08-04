package handler

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// Match-decision actions (LRM-1310 product freeze).
const (
	matchActionContinue       = "continue"
	matchActionBranchAfter    = "branch_after"
	matchActionDeprecate      = "deprecate"
	matchActionPendingConfirm = "pending_confirm"
)

// ResearchMatchDecisionItem is one per-node decision in an utterance envelope.
type ResearchMatchDecisionItem struct {
	NodeID string `json:"node_id"`
	Action string `json:"action"`
	Reason string `json:"reason,omitempty"`
}

// ResearchMatchDecision is the utterance-scoped envelope (LRM-1317/1330).
// Stored at research_message.meta.match_decision; also projected top-level.
type ResearchMatchDecision struct {
	UtteranceID          string                       `json:"utterance_id"`
	Confidence           *float64                     `json:"confidence,omitempty"`
	PrimaryAnchorNodeID  *string                      `json:"primary_anchor_node_id,omitempty"`
	MatchedNodeIDs       []string                     `json:"matched_node_ids"`
	Decisions            []ResearchMatchDecisionItem  `json:"decisions"`
}

func isMatchDecisionAction(action string) bool {
	switch action {
	case matchActionContinue, matchActionBranchAfter, matchActionDeprecate, matchActionPendingConfirm:
		return true
	default:
		return false
	}
}

// extractMatchDecisionFromMeta returns a normalized envelope when meta carries
// a non-empty match_decision. Missing/invalid → nil (omit; never fake []).
func extractMatchDecisionFromMeta(meta json.RawMessage, messageID string) *ResearchMatchDecision {
	if len(meta) == 0 || string(meta) == "null" || string(meta) == "{}" {
		return nil
	}
	var bag map[string]json.RawMessage
	if err := json.Unmarshal(meta, &bag); err != nil {
		return nil
	}
	raw, ok := bag["match_decision"]
	if !ok || len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	env, err := normalizeMatchDecision(raw, messageID)
	if err != nil {
		return nil
	}
	return env
}

func normalizeMatchDecision(raw json.RawMessage, messageID string) (*ResearchMatchDecision, error) {
	var in struct {
		UtteranceID         string                      `json:"utterance_id"`
		Confidence          *float64                    `json:"confidence"`
		PrimaryAnchorNodeID *string                     `json:"primary_anchor_node_id"`
		MatchedNodeIDs      []string                    `json:"matched_node_ids"`
		Decisions           []ResearchMatchDecisionItem `json:"decisions"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, fmt.Errorf("invalid match_decision: %w", err)
	}
	if len(in.Decisions) == 0 && len(in.MatchedNodeIDs) == 0 && in.PrimaryAnchorNodeID == nil && in.Confidence == nil {
		return nil, fmt.Errorf("match_decision empty")
	}

	out := &ResearchMatchDecision{
		UtteranceID:         strings.TrimSpace(in.UtteranceID),
		Confidence:          in.Confidence,
		MatchedNodeIDs:      make([]string, 0, len(in.MatchedNodeIDs)),
		Decisions:           make([]ResearchMatchDecisionItem, 0, len(in.Decisions)),
	}
	if out.UtteranceID == "" {
		out.UtteranceID = messageID
	}
	seenMatch := map[string]struct{}{}
	for _, id := range in.MatchedNodeIDs {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if _, dup := seenMatch[id]; dup {
			continue
		}
		seenMatch[id] = struct{}{}
		out.MatchedNodeIDs = append(out.MatchedNodeIDs, id)
	}
	if in.PrimaryAnchorNodeID != nil {
		anchor := strings.TrimSpace(*in.PrimaryAnchorNodeID)
		if anchor != "" {
			out.PrimaryAnchorNodeID = &anchor
		}
	}
	if out.Confidence != nil {
		c := *out.Confidence
		if c < 0 {
			c = 0
		}
		if c > 1 {
			c = 1
		}
		out.Confidence = &c
	}

	primaryAttach := 0
	for _, d := range in.Decisions {
		item := ResearchMatchDecisionItem{
			NodeID: strings.TrimSpace(d.NodeID),
			Action: strings.TrimSpace(d.Action),
			Reason: strings.TrimSpace(d.Reason),
		}
		if item.NodeID == "" || !isMatchDecisionAction(item.Action) {
			return nil, fmt.Errorf("invalid decision item")
		}
		if item.Action == matchActionDeprecate && item.Reason == "" {
			return nil, fmt.Errorf("deprecate requires reason")
		}
		if item.Action == matchActionContinue || item.Action == matchActionBranchAfter {
			primaryAttach++
		}
		out.Decisions = append(out.Decisions, item)
	}
	if primaryAttach > 1 {
		return nil, fmt.Errorf("at most one continue|branch_after per utterance")
	}
	return out, nil
}

type putResearchMatchDecisionRequest struct {
	MatchDecision json.RawMessage `json:"match_decision"`
}

// PutResearchMessageMatchDecision stores meta.match_decision on a human utterance.
// Agent data-plane only (fleet member). Does not run the matcher or mutate the graph.
func (h *Handler) PutResearchMessageMatchDecision(w http.ResponseWriter, r *http.Request) {
	workspaceID := h.resolveWorkspaceID(r)
	wsUUID, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace_id")
	if !ok {
		return
	}
	sessionID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "id")
	if !ok {
		return
	}
	messageID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "messageId"), "messageId")
	if !ok {
		return
	}
	if _, err := h.Queries.GetResearchSession(r.Context(), db.GetResearchSessionParams{ID: sessionID, WorkspaceID: wsUUID}); err != nil {
		writeError(w, http.StatusNotFound, "research session not found")
		return
	}
	if _, ok := h.requireActiveFleetMember(w, r, wsUUID); !ok {
		return
	}

	var req putResearchMatchDecisionRequest
	if !decodeResearchJSON(w, r, &req) {
		return
	}
	if len(req.MatchDecision) == 0 {
		writeError(w, http.StatusBadRequest, "match_decision is required")
		return
	}

	msgIDStr := uuidToString(messageID)
	env, err := normalizeMatchDecision(req.MatchDecision, msgIDStr)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	// Force utterance_id to the target message (human utterance row).
	env.UtteranceID = msgIDStr

	existing, err := h.Queries.GetResearchMessage(r.Context(), db.GetResearchMessageParams{
		ID:          messageID,
		SessionID:   sessionID,
		WorkspaceID: wsUUID,
	})
	if err != nil {
		writeError(w, http.StatusNotFound, "research message not found")
		return
	}
	if existing.SenderType != "user" {
		writeError(w, http.StatusBadRequest, "match_decision only attaches to human utterances")
		return
	}

	payload, err := json.Marshal(env)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to encode match_decision")
		return
	}
	updated, err := h.Queries.SetResearchMessageMatchDecision(r.Context(), db.SetResearchMessageMatchDecisionParams{
		ID:            messageID,
		SessionID:     sessionID,
		WorkspaceID:   wsUUID,
		MatchDecision: payload,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to persist match_decision")
		return
	}

	userID := requestUserID(r)
	actorType, actorID := h.resolveActor(r, userID, workspaceID)
	resp := mapMessages([]db.ResearchMessage{updated})[0]
	h.publish(protocol.EventResearchSessionMessage, workspaceID, actorType, actorID, map[string]any{
		"session_id": uuidToString(sessionID),
		"message":    resp,
		"op":         "match_decision",
	})
	writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) PutAgentResearchMessageMatchDecision(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAgentPrincipal(w, r); !ok {
		return
	}
	h.PutResearchMessageMatchDecision(w, r)
}
