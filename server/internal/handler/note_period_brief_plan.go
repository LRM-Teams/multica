package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

type notePeriodBriefPlanJSON struct {
	Window            string   `json:"window"`
	Date              string   `json:"date,omitempty"`
	StartDate         string   `json:"start_date,omitempty"`
	EndDate           string   `json:"end_date,omitempty"`
	CollectorAgentIDs []string `json:"collector_agent_ids"`
	Focus             string   `json:"focus,omitempty"`
}

type notePeriodBriefPlanResponse struct {
	Plan *notePeriodBriefPlanJSON `json:"plan"`
}

type putNotePeriodBriefPlanRequest struct {
	ChatSessionID     string    `json:"chat_session_id"`
	ContextNotePageID string    `json:"context_note_page_id"`
	Window            string    `json:"window"`
	Date              string    `json:"date"`
	StartDate         string    `json:"start_date"`
	EndDate           string    `json:"end_date"`
	CollectorAgentIDs *[]string `json:"collector_agent_ids"`
	Focus             *string   `json:"focus"`
}

func periodBriefPlanFromRow(row notePeriodBriefPromptRow) notePeriodBriefPlanJSON {
	ids := row.CollectorAgentIDs
	if ids == nil {
		ids = []string{}
	}
	return notePeriodBriefPlanJSON{
		Window:            row.WindowKind,
		Date:              row.WindowDate,
		StartDate:         row.StartDate,
		EndDate:           row.EndDate,
		CollectorAgentIDs: ids,
		Focus:             row.Focus,
	}
}

func normalizePeriodBriefPlanWindow(kind string) (string, bool) {
	switch strings.TrimSpace(strings.ToLower(kind)) {
	case "", "day", "week", "month", "custom":
		return strings.TrimSpace(strings.ToLower(kind)), true
	default:
		return "", false
	}
}

func formatPeriodBriefCurrentPlanBoard(plan *notePeriodBriefPlanJSON) string {
	var b strings.Builder
	b.WriteString("<period_brief_current_plan>\n")
	if plan == nil {
		b.WriteString("status: none\n")
		b.WriteString("No current plan in this bubble. If the human asks 写汇报, the platform opens the plan card. If the card is missing, call `multica notes period-brief plan --chat-session-id <chat_session_id from note_chat_context>` this turn with only the session id (page id optional). Do not set window, computers, or focus. Do not call start.\n")
		b.WriteString("Do not query the database for the session id. Do not emit chat XML. Do not claim the card exists unless the tool returned a plan object (not null). The human edits the card and clicks 开始采集 or 取消.\n")
	} else {
		b.WriteString("status: draft\n")
		fmt.Fprintf(&b, "window: %s\n", strings.TrimSpace(plan.Window))
		if strings.TrimSpace(plan.Date) != "" {
			fmt.Fprintf(&b, "date: %s\n", strings.TrimSpace(plan.Date))
		}
		if strings.TrimSpace(plan.StartDate) != "" || strings.TrimSpace(plan.EndDate) != "" {
			fmt.Fprintf(&b, "range: %s → %s\n", strings.TrimSpace(plan.StartDate), strings.TrimSpace(plan.EndDate))
		}
		collectors := strings.Join(plan.CollectorAgentIDs, ",")
		if collectors == "" {
			collectors = "(none)"
		}
		fmt.Fprintf(&b, "collectors: %s\n", collectors)
		focus := strings.TrimSpace(plan.Focus)
		if focus == "" {
			focus = "(empty)"
		}
		fmt.Fprintf(&b, "focus: %s\n", focus)
		b.WriteString("This is the current plan on the card. The human edits window, computers, and focus there. Do not PUT those fields. Do not call start.\n")
		b.WriteString("开始采集 walks every selected computer and then writes the brief. 取消 closes the card. A conflict means a run is already live; wait.\n")
		b.WriteString("Do not emit chat XML.\n")
	}
	b.WriteString("</period_brief_current_plan>\n\n")
	return b.String()
}

func (h *Handler) loadPeriodBriefPromptForSession(ctx context.Context, sessionID pgtype.UUID) (notePeriodBriefPromptRow, error) {
	var row notePeriodBriefPromptRow
	err := h.DB.QueryRow(ctx, `
SELECT id, workspace_id, owner_user_id, chat_session_id, source_page_id,
       window_kind, window_date, start_date, end_date, collector_agent_ids,
       focus, awaiting_confirm, status
FROM note_period_brief_prompt
WHERE chat_session_id = $1 AND status = 'clarifying'
ORDER BY created_at DESC
LIMIT 1`, sessionID).Scan(
		&row.ID, &row.WorkspaceID, &row.OwnerUserID, &row.ChatSessionID, &row.SourcePageID,
		&row.WindowKind, &row.WindowDate, &row.StartDate, &row.EndDate, &row.CollectorAgentIDs,
		&row.Focus, &row.AwaitingConfirm, &row.Status,
	)
	return row, err
}

func (h *Handler) loadHumanNoteBubbleSession(
	ctx context.Context,
	workspaceID, userID, sessionID pgtype.UUID,
) (pgtype.UUID, bool, error) {
	var creatorID, pageID pgtype.UUID
	var status string
	err := h.DB.QueryRow(ctx, `
SELECT creator_id, context_note_page_id, status
FROM chat_session
WHERE id = $1 AND workspace_id = $2`, sessionID, workspaceID).Scan(&creatorID, &pageID, &status)
	if errors.Is(err, pgx.ErrNoRows) {
		return pgtype.UUID{}, false, nil
	}
	if err != nil {
		return pgtype.UUID{}, false, err
	}
	if status != "active" || !pageID.Valid || uuidToString(creatorID) != uuidToString(userID) {
		return pgtype.UUID{}, false, nil
	}
	return pageID, true, nil
}

func (h *Handler) consumePeriodBriefSessionPlan(ctx context.Context, workspaceID, userID, sessionID pgtype.UUID) {
	if !sessionID.Valid {
		return
	}
	plan, err := h.loadPeriodBriefPrompt(ctx, sessionID, workspaceID, userID)
	if err != nil {
		return
	}
	h.closePeriodBriefPrompt(ctx, plan.ID, "consumed")
	h.publishPeriodBriefPlanChanged(workspaceID, "user", uuidToString(userID), uuidToString(sessionID), uuidToString(userID), nil)
}

func (h *Handler) publishPeriodBriefPlanChanged(workspaceID pgtype.UUID, actorType, actorID, sessionID, creatorUserID string, plan *notePeriodBriefPlanJSON) {
	if strings.TrimSpace(sessionID) == "" || strings.TrimSpace(creatorUserID) == "" {
		return
	}
	h.publishChatToCreator(protocol.EventNotePeriodBriefPlan, uuidToString(workspaceID), actorType, actorID, sessionID, creatorUserID, map[string]any{
		"chat_session_id": sessionID,
		"plan":            plan,
	})
}

func (h *Handler) mergePeriodBriefStartRequestFromPlan(
	w http.ResponseWriter,
	r *http.Request,
	workspaceID, userID pgtype.UUID,
	req *createNotePeriodBriefRequest,
) bool {
	sid := strings.TrimSpace(req.ChatSessionID)
	if sid == "" {
		return true
	}
	sessionID, ok := parseUUIDOrBadRequest(w, sid, "chat_session_id")
	if !ok {
		return false
	}
	plan, err := h.loadPeriodBriefPrompt(r.Context(), sessionID, workspaceID, userID)
	if err != nil {
		return true
	}
	if strings.TrimSpace(req.Window) == "" {
		req.Window = plan.WindowKind
		if strings.TrimSpace(req.Date) == "" {
			req.Date = plan.WindowDate
		}
		if strings.TrimSpace(req.StartDate) == "" {
			req.StartDate = plan.StartDate
		}
		if strings.TrimSpace(req.EndDate) == "" {
			req.EndDate = plan.EndDate
		}
	}
	if len(req.CollectorAgentIDs) == 0 {
		req.CollectorAgentIDs = append([]string(nil), plan.CollectorAgentIDs...)
	}
	if strings.TrimSpace(req.Focus) == "" {
		req.Focus = plan.Focus
	}
	return true
}

func applyPeriodBriefPlanPut(row *notePeriodBriefPromptRow, req putNotePeriodBriefPlanRequest, pageID pgtype.UUID) error {
	if kind, ok := normalizePeriodBriefPlanWindow(req.Window); !ok {
		return errString("window must be day, week, month, or custom")
	} else if kind != "" {
		row.WindowKind = kind
		row.WindowDate = strings.TrimSpace(req.Date)
		row.StartDate = strings.TrimSpace(req.StartDate)
		row.EndDate = strings.TrimSpace(req.EndDate)
	} else if strings.TrimSpace(row.WindowKind) == "" {
		row.WindowKind = "week"
	}
	if req.CollectorAgentIDs != nil {
		row.CollectorAgentIDs = append([]string(nil), *req.CollectorAgentIDs...)
	}
	if req.Focus != nil {
		row.Focus = strings.TrimSpace(*req.Focus)
	}
	if pageID.Valid {
		row.SourcePageID = pageID
	}
	row.AwaitingConfirm = false
	row.Status = "clarifying"
	return nil
}

// GetNotePeriodBriefPlan returns this bubble session's current 写汇报 plan.
// GET /api/notes/period-briefs/plan?chat_session_id=
func (h *Handler) GetNotePeriodBriefPlan(w http.ResponseWriter, r *http.Request) {
	workspaceID, userID, _, ok := h.notesWorkspaceAndUser(w, r)
	if !ok {
		return
	}
	sessionID, ok := parseUUIDOrBadRequest(w, r.URL.Query().Get("chat_session_id"), "chat_session_id")
	if !ok {
		return
	}
	if _, found, err := h.loadHumanNoteBubbleSession(r.Context(), workspaceID, userID, sessionID); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load chat session")
		return
	} else if !found {
		writeError(w, http.StatusNotFound, "notes bubble session not found")
		return
	}
	row, err := h.loadPeriodBriefPrompt(r.Context(), sessionID, workspaceID, userID)
	if errors.Is(err, pgx.ErrNoRows) {
		writeJSON(w, http.StatusOK, notePeriodBriefPlanResponse{Plan: nil})
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load period brief plan")
		return
	}
	plan := periodBriefPlanFromRow(row)
	writeJSON(w, http.StatusOK, notePeriodBriefPlanResponse{Plan: &plan})
}

// PutNotePeriodBriefPlan creates or updates this bubble session's current plan.
// PUT /api/notes/period-briefs/plan
func (h *Handler) PutNotePeriodBriefPlan(w http.ResponseWriter, r *http.Request) {
	workspaceID, userID, _, ok := h.notesWorkspaceAndUser(w, r)
	if !ok {
		return
	}
	var req putNotePeriodBriefPlanRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	sessionID, ok := parseUUIDOrBadRequest(w, req.ChatSessionID, "chat_session_id")
	if !ok {
		return
	}
	pageID, found, err := h.loadHumanNoteBubbleSession(r.Context(), workspaceID, userID, sessionID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load chat session")
		return
	}
	if !found {
		writeError(w, http.StatusNotFound, "notes bubble session not found")
		return
	}
	if raw := strings.TrimSpace(req.ContextNotePageID); raw != "" {
		asked, ok := parseUUIDOrBadRequest(w, raw, "context_note_page_id")
		if !ok {
			return
		}
		if uuidToString(asked) != uuidToString(pageID) {
			writeError(w, http.StatusNotFound, "notes bubble session not found")
			return
		}
	}
	row, loadErr := h.loadPeriodBriefPrompt(r.Context(), sessionID, workspaceID, userID)
	if loadErr != nil {
		row = notePeriodBriefPromptRow{
			WorkspaceID:   workspaceID,
			OwnerUserID:   userID,
			ChatSessionID: sessionID,
			SourcePageID:  pageID,
			WindowKind:    "week",
			Status:        "clarifying",
		}
	}
	if err := applyPeriodBriefPlanPut(&row, req, pageID); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := h.upsertPeriodBriefPrompt(r.Context(), &row); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to save period brief plan")
		return
	}
	plan := periodBriefPlanFromRow(row)
	h.publishPeriodBriefPlanChanged(workspaceID, "user", uuidToString(userID), uuidToString(sessionID), uuidToString(userID), &plan)
	writeJSON(w, http.StatusOK, notePeriodBriefPlanResponse{Plan: &plan})
}

// DeleteNotePeriodBriefPlan dismisses the current bubble plan card and
// stops any in-flight collect / synthesize on that note page.
// DELETE /api/notes/period-briefs/plan?chat_session_id=
func (h *Handler) DeleteNotePeriodBriefPlan(w http.ResponseWriter, r *http.Request) {
	workspaceID, userID, userIDString, ok := h.notesWorkspaceAndUser(w, r)
	if !ok {
		return
	}
	sessionID, ok := parseUUIDOrBadRequest(w, r.URL.Query().Get("chat_session_id"), "chat_session_id")
	if !ok {
		return
	}
	pageID, found, err := h.loadHumanNoteBubbleSession(r.Context(), workspaceID, userID, sessionID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load chat session")
		return
	}
	if !found {
		writeError(w, http.StatusNotFound, "notes bubble session not found")
		return
	}
	closedPlan := false
	prompt, promptErr := h.loadPeriodBriefPrompt(r.Context(), sessionID, workspaceID, userID)
	if promptErr != nil && !errors.Is(promptErr, pgx.ErrNoRows) {
		writeError(w, http.StatusInternalServerError, "failed to load period brief plan")
		return
	}
	if promptErr == nil {
		h.closePeriodBriefPrompt(r.Context(), prompt.ID, "cancelled")
		closedPlan = true
	}
	stoppedRun, stopErr := h.stopActivePeriodBriefRunForPage(r, workspaceID, userID, userIDString, pageID, true)
	if stopErr != nil {
		writeError(w, http.StatusInternalServerError, "failed to cancel period brief run")
		return
	}
	h.publishPeriodBriefPlanChanged(workspaceID, "user", uuidToString(userID), uuidToString(sessionID), uuidToString(userID), nil)
	if closedPlan && !stoppedRun {
		h.postPeriodBriefBubbleMessage(r.Context(), sessionID, workspaceID, userID, userIDString, "assistant", "好，那这次先不写汇报。")
	}
	writeJSON(w, http.StatusOK, notePeriodBriefPlanResponse{Plan: nil})
}

// GetAgentNotePeriodBriefPlan is the Notes Assistant read of the current plan.
// GET /api/agent/notes/period-briefs/plan?chat_session_id=
func (h *Handler) GetAgentNotePeriodBriefPlan(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.requireAgentPrincipal(w, r)
	if !ok {
		return
	}
	workspaceID, ok := parseUUIDOrBadRequest(w, principal.WorkspaceID, "workspace id")
	if !ok {
		return
	}
	agentID, ok := parseUUIDOrBadRequest(w, principal.AgentID, "agent id")
	if !ok {
		return
	}
	sessionID, ok := parseUUIDOrBadRequest(w, r.URL.Query().Get("chat_session_id"), "chat_session_id")
	if !ok {
		return
	}
	pageID, creatorID, found, err := h.loadAgentPeriodBriefBubbleSessionPage(r.Context(), agentID, workspaceID, sessionID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load chat session")
		return
	}
	if !found {
		writeError(w, http.StatusNotFound, "notes bubble session not found")
		return
	}
	row, err := h.loadPeriodBriefPrompt(r.Context(), sessionID, workspaceID, creatorID)
	if errors.Is(err, pgx.ErrNoRows) {
		writeJSON(w, http.StatusOK, notePeriodBriefPlanResponse{Plan: nil})
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load period brief plan")
		return
	}
	_ = pageID
	plan := periodBriefPlanFromRow(row)
	writeJSON(w, http.StatusOK, notePeriodBriefPlanResponse{Plan: &plan})
}

// PutAgentNotePeriodBriefPlan is the Notes Assistant write of the current plan.
// PUT /api/agent/notes/period-briefs/plan
func (h *Handler) PutAgentNotePeriodBriefPlan(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.requireAgentPrincipal(w, r)
	if !ok {
		return
	}
	var req putNotePeriodBriefPlanRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	workspaceID, ok := parseUUIDOrBadRequest(w, principal.WorkspaceID, "workspace id")
	if !ok {
		return
	}
	agentID, ok := parseUUIDOrBadRequest(w, principal.AgentID, "agent id")
	if !ok {
		return
	}
	sessionID, ok := parseUUIDOrBadRequest(w, req.ChatSessionID, "chat_session_id")
	if !ok {
		return
	}
	pageID, creatorID, found, err := h.loadAgentPeriodBriefBubbleSessionPage(r.Context(), agentID, workspaceID, sessionID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load chat session")
		return
	}
	if !found {
		writeError(w, http.StatusNotFound, "notes bubble session not found")
		return
	}
	if raw := strings.TrimSpace(req.ContextNotePageID); raw != "" {
		asked, ok := parseUUIDOrBadRequest(w, raw, "context_note_page_id")
		if !ok {
			return
		}
		if uuidToString(asked) != uuidToString(pageID) {
			writeError(w, http.StatusNotFound, "notes bubble session not found")
			return
		}
	}
	row, loadErr := h.loadPeriodBriefPrompt(r.Context(), sessionID, workspaceID, creatorID)
	if loadErr != nil {
		row = notePeriodBriefPromptRow{
			WorkspaceID:   workspaceID,
			OwnerUserID:   creatorID,
			ChatSessionID: sessionID,
			SourcePageID:  pageID,
			WindowKind:    "week",
			Status:        "clarifying",
		}
	}
	if err := applyPeriodBriefPlanPut(&row, req, pageID); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := h.upsertPeriodBriefPrompt(r.Context(), &row); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to save period brief plan")
		return
	}
	plan := periodBriefPlanFromRow(row)
	h.publishPeriodBriefPlanChanged(workspaceID, "agent", uuidToString(agentID), uuidToString(sessionID), uuidToString(creatorID), &plan)
	writeJSON(w, http.StatusOK, notePeriodBriefPlanResponse{Plan: &plan})
}

func (h *Handler) loadAgentPeriodBriefBubbleSessionPage(
	ctx context.Context,
	agentID, workspaceID, sessionID pgtype.UUID,
) (pgtype.UUID, pgtype.UUID, bool, error) {
	var creatorID, pageID pgtype.UUID
	var status string
	err := h.DB.QueryRow(ctx, `
SELECT creator_id, context_note_page_id, status
FROM chat_session
WHERE id = $1 AND workspace_id = $2 AND agent_id = $3`,
		sessionID, workspaceID, agentID,
	).Scan(&creatorID, &pageID, &status)
	if errors.Is(err, pgx.ErrNoRows) {
		return pgtype.UUID{}, pgtype.UUID{}, false, nil
	}
	if err != nil {
		return pgtype.UUID{}, pgtype.UUID{}, false, err
	}
	if status != "active" || !pageID.Valid {
		return pgtype.UUID{}, pgtype.UUID{}, false, nil
	}
	return pageID, creatorID, true, nil
}

func (h *Handler) upsertPeriodBriefPlanFromAgentStart(
	ctx context.Context,
	workspaceID, creatorID, sessionID, pageID pgtype.UUID,
	req startAgentNotePeriodBriefRequest,
) {
	hasFields := strings.TrimSpace(req.Window) != "" ||
		len(req.CollectorAgentIDs) > 0 ||
		strings.TrimSpace(req.Focus) != ""
	if !hasFields {
		return
	}
	row, err := h.loadPeriodBriefPrompt(ctx, sessionID, workspaceID, creatorID)
	if err != nil {
		row = notePeriodBriefPromptRow{
			WorkspaceID:   workspaceID,
			OwnerUserID:   creatorID,
			ChatSessionID: sessionID,
			SourcePageID:  pageID,
			WindowKind:    "week",
			Status:        "clarifying",
		}
	}
	if kind, ok := normalizePeriodBriefPlanWindow(req.Window); ok && kind != "" {
		row.WindowKind = kind
		row.WindowDate = strings.TrimSpace(req.Date)
		row.StartDate = strings.TrimSpace(req.StartDate)
		row.EndDate = strings.TrimSpace(req.EndDate)
	}
	if len(req.CollectorAgentIDs) > 0 {
		row.CollectorAgentIDs = append([]string(nil), req.CollectorAgentIDs...)
	}
	if strings.TrimSpace(req.Focus) != "" {
		row.Focus = strings.TrimSpace(req.Focus)
	}
	if !row.SourcePageID.Valid {
		row.SourcePageID = pageID
	}
	_ = h.upsertPeriodBriefPrompt(ctx, &row)
}
