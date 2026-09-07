package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

type startAgentNotePeriodBriefRequest struct {
	ChatSessionID     string   `json:"chat_session_id"`
	ContextNotePageID string   `json:"context_note_page_id"`
	Window            string   `json:"window"`
	Date              string   `json:"date"`
	StartDate         string   `json:"start_date"`
	EndDate           string   `json:"end_date"`
	Timezone          string   `json:"timezone"`
	CollectorAgentIDs []string `json:"collector_agent_ids"`
	Focus             string   `json:"focus"`
}

type startAgentNotePeriodBriefResponse struct {
	Status        string `json:"status"`
	Dispatched    bool   `json:"dispatched"`
	RunID         string `json:"run_id"`
	DraftPageID   string `json:"draft_page_id,omitempty"`
	ChatSessionID string `json:"chat_session_id,omitempty"`
}

type insertAgentNotePeriodBriefRequest struct {
	TargetPageID string `json:"target_page_id"`
	Mode         string `json:"mode"`
}

type insertAgentNotePeriodBriefResponse struct {
	Status    string `json:"status"`
	Mode      string `json:"mode"`
	PageID    string `json:"page_id,omitempty"`
	PageTitle string `json:"page_title,omitempty"`
	Title     string `json:"title,omitempty"`
}

// StartAgentNotePeriodBrief is the Notes Assistant bubble tool for 写汇报.
// POST /api/agent/notes/period-briefs/start
func (h *Handler) StartAgentNotePeriodBrief(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.requireAgentPrincipal(w, r)
	if !ok {
		return
	}
	var req startAgentNotePeriodBriefRequest
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
	pageID, ok := parseUUIDOrBadRequest(w, req.ContextNotePageID, "context_note_page_id")
	if !ok {
		return
	}
	creatorID, found, err := h.loadAgentPeriodBriefBubbleSession(r.Context(), agentID, workspaceID, sessionID, pageID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load chat session")
		return
	}
	if !found {
		writeError(w, http.StatusNotFound, "notes bubble session not found")
		return
	}
	h.upsertPeriodBriefPlanFromAgentStart(r.Context(), workspaceID, creatorID, sessionID, pageID, req)

	raw, err := json.Marshal(createNotePeriodBriefRequest{
		Window:            req.Window,
		Date:              req.Date,
		StartDate:         req.StartDate,
		EndDate:           req.EndDate,
		Timezone:          req.Timezone,
		AgentID:           principal.AgentID,
		CollectorAgentIDs: req.CollectorAgentIDs,
		Focus:             req.Focus,
		ContextNotePageID: req.ContextNotePageID,
		ChatSessionID:     req.ChatSessionID,
		FromChat:          true,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to start period brief")
		return
	}
	inner := httptest.NewRequestWithContext(r.Context(), http.MethodPost, "/api/notes/period-briefs", bytes.NewReader(raw))
	inner.Header.Set("Content-Type", "application/json")
	inner.Header.Set("X-User-ID", uuidToString(creatorID))
	inner.Header.Set("X-Workspace-ID", principal.WorkspaceID)
	rec := httptest.NewRecorder()
	h.CreateNotePeriodBrief(rec, inner)
	if rec.Code != http.StatusCreated {
		for key, values := range rec.Header() {
			for _, value := range values {
				w.Header().Add(key, value)
			}
		}
		w.WriteHeader(rec.Code)
		_, _ = w.Write(rec.Body.Bytes())
		return
	}
	var created createNotePeriodBriefResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to start period brief")
		return
	}
	draftID, ok := parseUUIDOrBadRequest(w, created.Page.ID, "draft page id")
	if !ok {
		return
	}
	run, err := h.loadNotePeriodBriefRunByDraft(r.Context(), workspaceID, draftID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load period brief run")
		return
	}
	writeJSON(w, http.StatusCreated, startAgentNotePeriodBriefResponse{
		Status:        "dispatched",
		Dispatched:    true,
		RunID:         uuidToString(run.ID),
		DraftPageID:   created.Page.ID,
		ChatSessionID: created.ChatSessionID,
	})
}

// InsertAgentNotePeriodBrief inserts a finished brief, or returns needs_confirm
// when the target is not the issuing page.
// POST /api/agent/notes/period-briefs/{runId}/insert
func (h *Handler) InsertAgentNotePeriodBrief(w http.ResponseWriter, r *http.Request) {
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
	runID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "runId"), "run id")
	if !ok {
		return
	}
	var req insertAgentNotePeriodBriefRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	mode := strings.TrimSpace(strings.ToLower(req.Mode))
	if mode != "append" && mode != "child" {
		writeError(w, http.StatusBadRequest, "mode must be append or child")
		return
	}
	run, err := h.loadNotePeriodBriefRunByIDForAgent(r.Context(), workspaceID, agentID, runID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "period brief run not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to load period brief run")
		return
	}
	if run.Status != "awaiting_confirm" && run.Status != "done" {
		writeError(w, http.StatusConflict, "period brief is not ready to insert")
		return
	}
	userID := run.OwnerUserID
	userIDString := uuidToString(userID)
	target, targetTitle, err := h.resolvePeriodBriefInsertTarget(r.Context(), workspaceID, userID, run, req.TargetPageID)
	if err != nil {
		switch {
		case errors.Is(err, errPeriodBriefInsertBadTarget):
			writeError(w, http.StatusBadRequest, "target_page_id is invalid")
		case errors.Is(err, errPeriodBriefInsertForbidden), errors.Is(err, errPeriodBriefInsertNoPage):
			writeError(w, http.StatusNotFound, "note page not found")
		default:
			writeError(w, http.StatusInternalServerError, "failed to authorize note page")
		}
		return
	}
	sameAsSource := !run.SourcePageID.Valid || uuidToString(target) == uuidToString(run.SourcePageID)
	if !sameAsSource {
		h.postPeriodBriefBubbleMessage(r.Context(), run.ChatSessionID, run.WorkspaceID, run.OwnerUserID, userIDString, "assistant", "",
			periodBriefInsertProposePart(uuidToString(run.ID), uuidToString(target), mode, targetTitle))
		writeJSON(w, http.StatusOK, insertAgentNotePeriodBriefResponse{
			Status:    "needs_confirm",
			Mode:      mode,
			PageID:    uuidToString(target),
			PageTitle: targetTitle,
		})
		return
	}
	title, err := h.applyPeriodBriefInsert(r.Context(), run, workspaceID, userID, mode, target)
	if err != nil {
		if err == errPeriodBriefInsertNoPage {
			writeError(w, http.StatusConflict, "period brief has no source page")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to insert period brief")
		return
	}
	_, _ = h.DB.Exec(r.Context(), `
UPDATE note_period_brief_run SET status = 'done', updated_at = now() WHERE id = $1`, run.ID)
	h.postPeriodBriefBubbleProgress(r.Context(), run, userIDString, periodBriefInsertDoneCopy(mode, targetTitle, true, title))
	writeJSON(w, http.StatusOK, insertAgentNotePeriodBriefResponse{
		Status:    "inserted",
		Mode:      mode,
		PageID:    uuidToString(target),
		PageTitle: targetTitle,
		Title:     title,
	})
}

func periodBriefInsertProposePart(runID, targetPageID, mode, label string) protocol.MessagePart {
	return protocol.MessagePart{
		Type:             protocol.MessagePartTypePeriodBriefInsertPropose,
		RefID:            strings.TrimSpace(runID),
		TargetPageID:     strings.TrimSpace(targetPageID),
		SelectedOptionID: strings.TrimSpace(mode),
		Label:            strings.TrimSpace(label),
	}
}

func (h *Handler) loadAgentPeriodBriefBubbleSession(
	ctx context.Context,
	agentID, workspaceID, sessionID, pageID pgtype.UUID,
) (pgtype.UUID, bool, error) {
	var creatorID, contextPage pgtype.UUID
	var status string
	err := h.DB.QueryRow(ctx, `
SELECT creator_id, context_note_page_id, status
FROM chat_session
WHERE id = $1 AND workspace_id = $2 AND agent_id = $3`,
		sessionID, workspaceID, agentID,
	).Scan(&creatorID, &contextPage, &status)
	if errors.Is(err, pgx.ErrNoRows) {
		return pgtype.UUID{}, false, nil
	}
	if err != nil {
		return pgtype.UUID{}, false, err
	}
	if status != "active" || !contextPage.Valid || uuidToString(contextPage) != uuidToString(pageID) {
		return pgtype.UUID{}, false, nil
	}
	return creatorID, true, nil
}

func (h *Handler) loadNotePeriodBriefRunByIDForAgent(
	ctx context.Context,
	workspaceID, agentID, runID pgtype.UUID,
) (notePeriodBriefRunRow, error) {
	var ownerID, synthID, sessionID pgtype.UUID
	err := h.DB.QueryRow(ctx, `
SELECT owner_user_id, synthesizer_agent_id, chat_session_id
FROM note_period_brief_run
WHERE id = $1 AND workspace_id = $2`, runID, workspaceID).Scan(&ownerID, &synthID, &sessionID)
	if err != nil {
		return notePeriodBriefRunRow{}, err
	}
	allowed := uuidToString(synthID) == uuidToString(agentID)
	if !allowed && sessionID.Valid {
		var sessionAgent pgtype.UUID
		scanErr := h.DB.QueryRow(ctx, `
SELECT agent_id FROM chat_session
WHERE id = $1 AND workspace_id = $2 AND status = 'active'`,
			sessionID, workspaceID,
		).Scan(&sessionAgent)
		allowed = scanErr == nil && uuidToString(sessionAgent) == uuidToString(agentID)
	}
	if !allowed {
		return notePeriodBriefRunRow{}, pgx.ErrNoRows
	}
	return h.loadNotePeriodBriefRunByID(ctx, workspaceID, ownerID, runID)
}
