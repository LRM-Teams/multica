package handler

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/researchrun"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

const (
	researchDepthShallow  = "shallow"
	researchDepthStandard = "standard"
	researchDepthDeep     = "deep"

	researchDecisionContinue   = "continue"
	researchDecisionStopEnough = "stop_enough"
	researchDecisionStopBudget = "stop_budget"
)

// productRoundBudgetForTier maps LRM-676 depth tiers to product-round hard caps.
func productRoundBudgetForTier(tier string) int32 {
	switch strings.TrimSpace(strings.ToLower(tier)) {
	case researchDepthShallow:
		return 2
	case researchDepthDeep:
		return 10
	default:
		return 5
	}
}

func normalizeResearchDepthTier(tier string) string {
	switch strings.TrimSpace(strings.ToLower(tier)) {
	case researchDepthShallow:
		return researchDepthShallow
	case researchDepthDeep:
		return researchDepthDeep
	default:
		return researchDepthStandard
	}
}

// resolveProductRoundDecision enforces the hard cap: at/over budget, continue
// is coerced to stop_budget and cannot be bypassed.
func resolveProductRoundDecision(requested string, round, budget int32) (decision string, forced bool) {
	requested = strings.TrimSpace(strings.ToLower(requested))
	switch requested {
	case researchDecisionContinue, researchDecisionStopEnough, researchDecisionStopBudget:
		// ok
	default:
		return "", false
	}
	if round >= budget && requested == researchDecisionContinue {
		return researchDecisionStopBudget, true
	}
	if round > budget {
		return researchDecisionStopBudget, true
	}
	return requested, false
}

type ResearchProductRoundCardResp struct {
	ID                string          `json:"id"`
	SessionID         string          `json:"session_id"`
	RoundNumber       int32           `json:"round_number"`
	Decision          string          `json:"decision"`
	CoverageGaps      json.RawMessage `json:"coverage_gaps"`
	ConfidenceNote    string          `json:"confidence_note"`
	BudgetUsed        int32           `json:"budget_used"`
	BudgetRemaining   int32           `json:"budget_remaining"`
	GoalPatchProposal *string         `json:"goal_patch_proposal"`
	NextRoundFocus    *string         `json:"next_round_focus"`
	DecidedByAgentID  *string         `json:"decided_by_agent_id"`
	CreatedAt         string          `json:"created_at"`
}

func researchProductRoundCardToResp(c db.ResearchProductRoundCard) ResearchProductRoundCardResp {
	gaps := json.RawMessage(c.CoverageGaps)
	if len(gaps) == 0 {
		gaps = json.RawMessage("[]")
	}
	return ResearchProductRoundCardResp{
		ID:                uuidToString(c.ID),
		SessionID:         uuidToString(c.SessionID),
		RoundNumber:       c.RoundNumber,
		Decision:          c.Decision,
		CoverageGaps:      gaps,
		ConfidenceNote:    c.ConfidenceNote,
		BudgetUsed:        c.BudgetUsed,
		BudgetRemaining:   c.BudgetRemaining,
		GoalPatchProposal: textToPtr(c.GoalPatchProposal),
		NextRoundFocus:    textToPtr(c.NextRoundFocus),
		DecidedByAgentID:  uuidToPtr(c.DecidedByAgentID),
		CreatedAt:         timestampToString(c.CreatedAt),
	}
}

func mapProductRoundCards(rows []db.ResearchProductRoundCard) []ResearchProductRoundCardResp {
	out := make([]ResearchProductRoundCardResp, 0, len(rows))
	for _, row := range rows {
		out = append(out, researchProductRoundCardToResp(row))
	}
	return out
}

type submitResearchProductRoundJudgmentRequest struct {
	Decision          string          `json:"decision"`
	CoverageGaps      json.RawMessage `json:"coverage_gaps"`
	ConfidenceNote    string          `json:"confidence_note"`
	GoalPatchProposal *string         `json:"goal_patch_proposal"`
	NextRoundFocus    *string         `json:"next_round_focus"`
	// Round is optional; defaults to session.product_round (current open round).
	Round *int32 `json:"round"`
}

// ListResearchProductRoundCards returns persisted end-of-round judgment cards.
func (h *Handler) ListResearchProductRoundCards(w http.ResponseWriter, r *http.Request) {
	workspaceID := h.resolveWorkspaceID(r)
	wsUUID, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace_id")
	if !ok {
		return
	}
	sessionID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "id")
	if !ok {
		return
	}
	if _, err := h.Queries.GetResearchSession(r.Context(), db.GetResearchSessionParams{ID: sessionID, WorkspaceID: wsUUID}); err != nil {
		writeError(w, http.StatusNotFound, "research session not found")
		return
	}
	rows, err := h.Queries.ListResearchProductRoundCards(r.Context(), db.ListResearchProductRoundCardsParams{
		SessionID:   sessionID,
		WorkspaceID: wsUUID,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list product round cards")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"rounds": mapProductRoundCards(rows)})
}

// GetResearchProductRoundCard returns one judgment card by round number.
func (h *Handler) GetResearchProductRoundCard(w http.ResponseWriter, r *http.Request) {
	workspaceID := h.resolveWorkspaceID(r)
	wsUUID, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace_id")
	if !ok {
		return
	}
	sessionID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "id")
	if !ok {
		return
	}
	roundRaw := chi.URLParam(r, "round")
	roundN, err := strconv.ParseInt(roundRaw, 10, 32)
	if err != nil || roundN < 1 {
		writeError(w, http.StatusBadRequest, "round must be a positive integer")
		return
	}
	card, err := h.Queries.GetResearchProductRoundCard(r.Context(), db.GetResearchProductRoundCardParams{
		SessionID:   sessionID,
		WorkspaceID: wsUUID,
		RoundNumber: int32(roundN),
	})
	if err != nil {
		writeError(w, http.StatusNotFound, "product round card not found")
		return
	}
	writeJSON(w, http.StatusOK, researchProductRoundCardToResp(card))
}

// SubmitResearchProductRoundJudgment persists the end-of-round judgment card and
// either opens Round N+1 (continue) or closes the session (stop_*).
// goal_patch_proposal is stored as proposal only — never writes session.goal.
func (h *Handler) SubmitResearchProductRoundJudgment(w http.ResponseWriter, r *http.Request) {
	workspaceID := h.resolveWorkspaceID(r)
	wsUUID, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace_id")
	if !ok {
		return
	}
	lead, ok := h.requireResearchLeadActor(w, r, wsUUID)
	if !ok {
		return
	}
	sessionID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "id")
	if !ok {
		return
	}
	if h.rejectLegacyResearchMutation(w, r, wsUUID, sessionID) {
		return
	}
	session, err := h.Queries.GetResearchSession(r.Context(), db.GetResearchSessionParams{ID: sessionID, WorkspaceID: wsUUID})
	if err != nil {
		writeError(w, http.StatusNotFound, "research session not found")
		return
	}
	switch session.Status {
	case "running", "paused", "awaiting_user_confirm":
		// ok — product-round judgment is orthogonal to S1–S4 stage status
	case "completed", "archived":
		writeError(w, http.StatusBadRequest, "session already closed; cannot submit product round judgment")
		return
	default:
		writeError(w, http.StatusBadRequest, "session cannot accept product round judgment in current status")
		return
	}

	var req submitResearchProductRoundJudgmentRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	round := session.ProductRound
	if req.Round != nil {
		if *req.Round < 1 {
			writeError(w, http.StatusBadRequest, "round must be >= 1")
			return
		}
		if *req.Round != session.ProductRound {
			writeError(w, http.StatusBadRequest, fmt.Sprintf(
				"round must match current product_round (%d); product rounds are not search steps",
				session.ProductRound,
			))
			return
		}
		round = *req.Round
	}

	budget := session.ProductRoundBudget
	if budget <= 0 {
		budget = productRoundBudgetForTier(session.DepthTier)
	}

	decision, forced := resolveProductRoundDecision(req.Decision, round, budget)
	if decision == "" {
		writeError(w, http.StatusBadRequest, "decision must be continue, stop_enough, or stop_budget")
		return
	}

	gaps := req.CoverageGaps
	if len(gaps) == 0 {
		gaps = json.RawMessage("[]")
	} else if !json.Valid(gaps) {
		writeError(w, http.StatusBadRequest, "coverage_gaps must be valid JSON")
		return
	}

	nextFocus := strings.TrimSpace(ptrString(req.NextRoundFocus))
	if decision == researchDecisionContinue && nextFocus == "" {
		writeError(w, http.StatusBadRequest, "next_round_focus is required when decision is continue")
		return
	}
	if decision != researchDecisionContinue {
		nextFocus = ""
	}

	budgetUsed := round
	budgetRemaining := budget - budgetUsed
	if budgetRemaining < 0 {
		budgetRemaining = 0
	}

	goalPatch := strings.TrimSpace(ptrString(req.GoalPatchProposal))
	var goalPatchText pgtype.Text
	if goalPatch != "" {
		goalPatchText = pgtype.Text{String: goalPatch, Valid: true}
	}
	var nextFocusText pgtype.Text
	if nextFocus != "" {
		nextFocusText = pgtype.Text{String: nextFocus, Valid: true}
	}

	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to begin product round judgment")
		return
	}
	defer func() { _ = tx.Rollback(r.Context()) }()
	qtx := h.Queries.WithTx(tx)

	card, err := qtx.CreateResearchProductRoundCard(r.Context(), db.CreateResearchProductRoundCardParams{
		WorkspaceID:       wsUUID,
		SessionID:         sessionID,
		RoundNumber:       round,
		Decision:          decision,
		CoverageGaps:      []byte(gaps),
		ConfidenceNote:    strings.TrimSpace(req.ConfidenceNote),
		BudgetUsed:        budgetUsed,
		BudgetRemaining:   budgetRemaining,
		GoalPatchProposal: goalPatchText,
		NextRoundFocus:    nextFocusText,
		DecidedByAgentID:  lead.AgentID,
	})
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") || strings.Contains(err.Error(), "duplicate") {
			writeError(w, http.StatusConflict, "judgment card already exists for this product round")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to persist product round judgment")
		return
	}
	if err := researchrun.RegisterProductionProductRoundDecisionTx(
		r.Context(),
		tx,
		workspaceID,
		uuidToString(sessionID),
		uuidToString(card.ID),
	); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to register product round judgment artifact")
		return
	}

	update := db.UpdateResearchSessionParams{
		ID:          sessionID,
		WorkspaceID: wsUUID,
	}
	closed := decision != researchDecisionContinue
	if closed {
		update.Status = pgtype.Text{String: "completed", Valid: true}
	} else {
		update.Status = pgtype.Text{String: "running", Valid: true}
		update.CurrentStage = pgtype.Text{String: "s1_plan", Valid: true}
		update.ProductRound = pgtype.Int4{Int32: round + 1, Valid: true}
	}
	updated, err := qtx.UpdateResearchSession(r.Context(), update)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to advance session after product round judgment")
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to commit product round judgment")
		return
	}

	cardResp := researchProductRoundCardToResp(card)
	actorID := uuidToString(lead.AgentID)
	h.publish(protocol.EventResearchSessionProductRound, workspaceID, "agent", actorID, map[string]any{
		"session_id":         uuidToString(sessionID),
		"card":               cardResp,
		"forced_stop_budget": forced,
	})
	h.publish(protocol.EventResearchSessionStatusChanged, workspaceID, "agent", actorID, map[string]any{
		"session": researchSessionToResponse(updated),
	})

	title := fmt.Sprintf("产品轮 Round %d · %s", round, decision)
	body := req.ConfidenceNote
	if closed {
		body = fmt.Sprintf("会话已收口（%s）。未覆盖：%s", decision, string(gaps))
	} else {
		body = fmt.Sprintf("开启 Round %d。焦点：%s", round+1, nextFocus)
	}
	_, _, _ = h.createResearchGraphNodePublished(r.Context(), workspaceID, wsUUID, sessionID, "agent", actorID, db.CreateResearchGraphNodeParams{
		WorkspaceID:  wsUUID,
		SessionID:    sessionID,
		NodeType:     "product_round_gate",
		Title:        title,
		Summary:      body,
		Status:       ternary(closed, "done", "active"),
		ActorAgentID: lead.AgentID,
		Payload: marshalJSONRaw(map[string]any{
			"round":               round,
			"decision":            decision,
			"coverage_gaps":       json.RawMessage(gaps),
			"budget_used":         budgetUsed,
			"budget_remaining":    budgetRemaining,
			"goal_patch_proposal": goalPatch,
			"next_round_focus":    nextFocus,
			"forced_stop_budget":  forced,
			"goal_not_written":    true, // proposal only; authoritative goal → LRM-898
		}),
	}, pgtype.UUID{}, "leads_to")
	h.emitResearchProcessCard(r.Context(), workspaceID, wsUUID, sessionID, "agent", actorID, researchProcessEvent{
		Op:      "product_round_judgment",
		Title:   title,
		Body:    body,
		ActorID: lead.AgentID,
		Meta: map[string]any{
			"round":              round,
			"decision":           decision,
			"coverage_gaps":      json.RawMessage(gaps),
			"budget_used":        budgetUsed,
			"budget_remaining":   budgetRemaining,
			"forced_stop_budget": forced,
			"has_goal_patch":     goalPatch != "",
		},
	})

	writeJSON(w, http.StatusCreated, map[string]any{
		"card":               cardResp,
		"session":            researchSessionToResponse(updated),
		"forced_stop_budget": forced,
		// Explicit: goal_patch_proposal never mutates session.goal here.
		"goal_unchanged": true,
	})
}
