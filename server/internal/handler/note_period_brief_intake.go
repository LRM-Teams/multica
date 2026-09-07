package handler

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

var (
	periodBriefIntentRe    = regexp.MustCompile(`(?i)(写|整理|做|生成|帮我).{0,12}(汇报|周报)|period\s*work\s*brief|period\s*brief|weekly\s*report|write\s+(a\s+)?(period\s+work\s+)?reports?|^(report|reports)$`)
	periodBriefCancelRe    = regexp.MustCompile(`(?i)^(取消|算了|先不写|不用写了|不要写了)`)
	periodBriefRecollectRe = regexp.MustCompile(`(?i)重新采集|再采集|再采一遍|再采一次|重采|重新走一遍|再扫一遍|re-?collect(?:ing)?|collect(?:ion)? again|walk again|re-?walk`)
)

type notePeriodBriefPromptRow struct {
	ID                pgtype.UUID
	WorkspaceID       pgtype.UUID
	OwnerUserID       pgtype.UUID
	ChatSessionID     pgtype.UUID
	SourcePageID      pgtype.UUID
	WindowKind        string
	WindowDate        string
	StartDate         string
	EndDate           string
	CollectorAgentIDs []string
	Focus             string
	AwaitingConfirm   bool
	Status            string
}

type periodBriefOwnedCollector struct {
	ID    string
	Label string
	Mode  string
}

func looksLikePeriodBriefRequest(text string) bool {
	return periodBriefIntentRe.MatchString(strings.TrimSpace(text))
}

func looksLikePeriodBriefRecollectAsk(text string) bool {
	return periodBriefRecollectRe.MatchString(strings.TrimSpace(text))
}

func looksLikePeriodBriefPlanAsk(text string) bool {
	return looksLikePeriodBriefRequest(text) || looksLikePeriodBriefRecollectAsk(text)
}

func periodBriefIntakeCancelled(text string) bool {
	return periodBriefCancelRe.MatchString(strings.TrimSpace(text))
}

func (h *Handler) listOwnedPeriodBriefCollectors(
	ctx context.Context,
	workspaceID, userID pgtype.UUID,
) []periodBriefOwnedCollector {
	agents, err := h.Queries.ListAgents(ctx, workspaceID)
	if err != nil {
		return nil
	}
	out := make([]periodBriefOwnedCollector, 0)
	for _, agent := range agents {
		if agent.ArchivedAt.Valid || !isPeriodBriefCollectorAgentName(agent.Name) || !agent.RuntimeID.Valid {
			continue
		}
		rt, err := h.Queries.GetAgentRuntime(ctx, agent.RuntimeID)
		if err != nil {
			continue
		}
		ownerID, err := h.resolveRuntimeOwnerQuery(ctx, rt)
		if err != nil || uuidToString(ownerID) != uuidToString(userID) {
			continue
		}
		label := strings.TrimSpace(agent.DisplayName)
		if label == "" {
			label = periodBriefCollectorDisplayName(rt, uuidToString(rt.ID), strings.EqualFold(rt.RuntimeMode, "cloud"))
		}
		out = append(out, periodBriefOwnedCollector{
			ID:    uuidToString(agent.ID),
			Label: label,
			Mode:  strings.ToLower(strings.TrimSpace(rt.RuntimeMode)),
		})
	}
	return out
}

func (h *Handler) loadPeriodBriefPrompt(
	ctx context.Context,
	sessionID, workspaceID, userID pgtype.UUID,
) (notePeriodBriefPromptRow, error) {
	var row notePeriodBriefPromptRow
	err := h.DB.QueryRow(ctx, `
SELECT id, workspace_id, owner_user_id, chat_session_id, source_page_id,
       window_kind, window_date, start_date, end_date, collector_agent_ids,
       focus, awaiting_confirm, status
FROM note_period_brief_prompt
WHERE chat_session_id = $1 AND workspace_id = $2 AND owner_user_id = $3
  AND status = 'clarifying'
ORDER BY created_at DESC
LIMIT 1`, sessionID, workspaceID, userID).Scan(
		&row.ID, &row.WorkspaceID, &row.OwnerUserID, &row.ChatSessionID, &row.SourcePageID,
		&row.WindowKind, &row.WindowDate, &row.StartDate, &row.EndDate, &row.CollectorAgentIDs,
		&row.Focus, &row.AwaitingConfirm, &row.Status,
	)
	return row, err
}

func (h *Handler) loadLatestPeriodBriefPromptAnyStatus(
	ctx context.Context,
	sessionID, workspaceID, userID pgtype.UUID,
) (notePeriodBriefPromptRow, error) {
	var row notePeriodBriefPromptRow
	err := h.DB.QueryRow(ctx, `
SELECT id, workspace_id, owner_user_id, chat_session_id, source_page_id,
       window_kind, window_date, start_date, end_date, collector_agent_ids,
       focus, awaiting_confirm, status
FROM note_period_brief_prompt
WHERE chat_session_id = $1 AND workspace_id = $2 AND owner_user_id = $3
ORDER BY created_at DESC
LIMIT 1`, sessionID, workspaceID, userID).Scan(
		&row.ID, &row.WorkspaceID, &row.OwnerUserID, &row.ChatSessionID, &row.SourcePageID,
		&row.WindowKind, &row.WindowDate, &row.StartDate, &row.EndDate, &row.CollectorAgentIDs,
		&row.Focus, &row.AwaitingConfirm, &row.Status,
	)
	return row, err
}

func (h *Handler) upsertPeriodBriefPrompt(ctx context.Context, row *notePeriodBriefPromptRow) error {
	if row.CollectorAgentIDs == nil {
		row.CollectorAgentIDs = []string{}
	}
	if row.ID.Valid {
		_, err := h.DB.Exec(ctx, `
UPDATE note_period_brief_prompt
SET window_kind = $2, window_date = $3, start_date = $4, end_date = $5,
    collector_agent_ids = $6, focus = $7, awaiting_confirm = $8, updated_at = now()
WHERE id = $1`,
			row.ID, row.WindowKind, row.WindowDate, row.StartDate, row.EndDate,
			row.CollectorAgentIDs, row.Focus, row.AwaitingConfirm,
		)
		return err
	}
	return h.DB.QueryRow(ctx, `
INSERT INTO note_period_brief_prompt (
  workspace_id, owner_user_id, chat_session_id, source_page_id,
  window_kind, window_date, start_date, end_date, collector_agent_ids,
  focus, awaiting_confirm, status
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,'clarifying')
RETURNING id`,
		row.WorkspaceID, row.OwnerUserID, row.ChatSessionID, row.SourcePageID,
		row.WindowKind, row.WindowDate, row.StartDate, row.EndDate, row.CollectorAgentIDs,
		row.Focus, row.AwaitingConfirm,
	).Scan(&row.ID)
}

func (h *Handler) closePeriodBriefPrompt(ctx context.Context, id pgtype.UUID, status string) {
	_, _ = h.DB.Exec(ctx, `
UPDATE note_period_brief_prompt SET status = $2, updated_at = now() WHERE id = $1`, id, status)
}

func (h *Handler) tryHandlePeriodBriefPlanAsk(
	r *http.Request,
	session db.ChatSession,
	userID, workspaceID pgtype.UUID,
	userIDString, content string,
) bool {
	pageID := session.ContextNotePageID
	if !pageID.Valid {
		raw := h.chatSessionContextNotePageID(r.Context(), session.ID)
		if raw == "" {
			return false
		}
		pageID = parseUUID(raw)
	}
	if periodBriefIntakeCancelled(content) {
		prompt, promptErr := h.loadPeriodBriefPrompt(r.Context(), session.ID, workspaceID, userID)
		if promptErr != nil && !errors.Is(promptErr, pgx.ErrNoRows) {
			return false
		}
		closedPlan := promptErr == nil
		if closedPlan {
			h.closePeriodBriefPrompt(r.Context(), prompt.ID, "cancelled")
		}
		stopped, stopErr := h.stopActivePeriodBriefRunForPage(r, workspaceID, userID, userIDString, pageID, true)
		if stopErr != nil {
			slog.Warn("period brief cancel from speech failed to stop run", "error", stopErr)
		}
		if !closedPlan && !stopped {
			return false
		}
		h.publishPeriodBriefPlanChanged(workspaceID, "user", uuidToString(userID), uuidToString(session.ID), uuidToString(userID), nil)
		if !stopped {
			h.postPeriodBriefBubbleMessage(r.Context(), session.ID, workspaceID, session.CreatorID, userIDString, "assistant", "好，那这次先不写汇报。")
		}
		return true
	}
	if !looksLikePeriodBriefPlanAsk(content) {
		return false
	}
	if run, err := h.loadActivePeriodBriefRunForPage(r.Context(), workspaceID, userID, pageID); err == nil && run.ID.Valid {
		run = h.reconcilePeriodBriefLock(r, workspaceID, userID, userIDString, run)
		if periodBriefRunLocksComposerStatus(run.Status) {
			h.postPeriodBriefBubbleMessage(r.Context(), session.ID, workspaceID, session.CreatorID, userIDString, "assistant", "上一份写汇报还在进行中，结束后我们再开新的。")
			return true
		}
	}
	owned := h.listOwnedPeriodBriefCollectors(r.Context(), workspaceID, userID)
	row, err := h.seedPeriodBriefPlan(r.Context(), session.ID, workspaceID, userID, pageID, owned)
	if err != nil {
		slog.Warn("period brief plan ask failed to seed plan", "error", err)
		return false
	}
	row.Status = "clarifying"
	row.AwaitingConfirm = true
	if err := h.upsertPeriodBriefPrompt(r.Context(), &row); err != nil {
		slog.Warn("period brief plan ask failed to save plan", "error", err)
		return false
	}
	plan := periodBriefPlanFromRow(row)
	h.publishPeriodBriefPlanChanged(workspaceID, "user", uuidToString(userID), uuidToString(session.ID), uuidToString(userID), &plan)
	h.postPeriodBriefBubbleMessage(r.Context(), session.ID, workspaceID, session.CreatorID, userIDString, "assistant", "好，先确认这次的时间段和电脑。改完再点开始采集。")
	return true
}

func (h *Handler) seedPeriodBriefPlan(
	ctx context.Context,
	sessionID, workspaceID, userID, pageID pgtype.UUID,
	owned []periodBriefOwnedCollector,
) (notePeriodBriefPromptRow, error) {
	row, err := h.loadPeriodBriefPrompt(ctx, sessionID, workspaceID, userID)
	if err == nil {
		return row, nil
	}
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return notePeriodBriefPromptRow{}, err
	}
	prev, prevErr := h.loadLatestPeriodBriefPromptAnyStatus(ctx, sessionID, workspaceID, userID)
	if prevErr == nil {
		prev.ID = pgtype.UUID{}
		prev.Status = "clarifying"
		prev.SourcePageID = pageID
		return prev, nil
	}
	if prevErr != nil && !errors.Is(prevErr, pgx.ErrNoRows) {
		return notePeriodBriefPromptRow{}, prevErr
	}
	row = notePeriodBriefPromptRow{
		WorkspaceID:   workspaceID,
		OwnerUserID:   userID,
		ChatSessionID: sessionID,
		SourcePageID:  pageID,
		WindowKind:    "week",
		Status:        "clarifying",
	}
	if latest, runErr := h.loadLatestPeriodBriefRunForPage(ctx, workspaceID, userID, pageID); runErr == nil && latest.ID.Valid {
		full, loadErr := h.loadNotePeriodBriefRunByID(ctx, workspaceID, userID, latest.ID)
		if loadErr == nil {
			row = periodBriefPlanFromRun(full, sessionID, pageID)
		}
	}
	if len(row.CollectorAgentIDs) == 0 {
		ids := make([]string, 0, len(owned))
		for _, collector := range owned {
			ids = append(ids, collector.ID)
		}
		row.CollectorAgentIDs = ids
	}
	return row, nil
}

func periodBriefPlanFromRun(run notePeriodBriefRunRow, sessionID, pageID pgtype.UUID) notePeriodBriefPromptRow {
	ids := make([]string, 0, len(run.Collectors))
	for _, ref := range run.Collectors {
		if id := strings.TrimSpace(ref.AgentID); id != "" {
			ids = append(ids, id)
		}
	}
	loc := resolveNoteRetrospectiveLocation(run.Timezone)
	kind := strings.TrimSpace(run.WindowKind)
	if kind == "" {
		kind = "week"
	}
	row := notePeriodBriefPromptRow{
		WorkspaceID:       run.WorkspaceID,
		OwnerUserID:       run.OwnerUserID,
		ChatSessionID:     sessionID,
		SourcePageID:      pageID,
		WindowKind:        kind,
		Focus:             run.UserFocus,
		CollectorAgentIDs: ids,
		Status:            "clarifying",
	}
	if kind == "custom" {
		row.StartDate = run.WindowStart.In(loc).Format("2006-01-02")
		end := run.WindowEnd.Add(-time.Second).In(loc)
		row.EndDate = end.Format("2006-01-02")
	} else if !run.WindowStart.IsZero() {
		row.WindowDate = run.WindowStart.In(loc).Format("2006-01-02")
	}
	return row
}

func periodBriefRunLocksComposerStatus(status string) bool {
	switch status {
	case "planning", "collecting", "synthesizing":
		return true
	default:
		return false
	}
}

func periodBriefRunIsOpen(status string) bool {
	return periodBriefRunLocksComposerStatus(status) || status == "awaiting_confirm"
}

func (h *Handler) loadLatestPeriodBriefRunForPage(
	ctx context.Context,
	workspaceID, userID, pageID pgtype.UUID,
) (notePeriodBriefRunRow, error) {
	var row notePeriodBriefRunRow
	err := h.DB.QueryRow(ctx, `
SELECT id, status, chat_session_id, source_page_id, draft_page_id
FROM note_period_brief_run
WHERE workspace_id = $1 AND owner_user_id = $2 AND source_page_id = $3
ORDER BY created_at DESC
LIMIT 1`, workspaceID, userID, pageID).Scan(
		&row.ID, &row.Status, &row.ChatSessionID, &row.SourcePageID, &row.DraftPageID,
	)
	return row, err
}

func (h *Handler) loadActivePeriodBriefRunForPage(
	ctx context.Context,
	workspaceID, userID, pageID pgtype.UUID,
) (notePeriodBriefRunRow, error) {
	row, err := h.loadLatestPeriodBriefRunForPage(ctx, workspaceID, userID, pageID)
	if err != nil {
		return row, err
	}
	if !periodBriefRunLocksComposerStatus(row.Status) {
		return notePeriodBriefRunRow{}, pgx.ErrNoRows
	}
	return row, nil
}

// settleWrittenPeriodBriefRun finishes a synthesizing run whose brief is
// already on the folder. Used when the HTTP waiter died (proxy reset /
// process restart) and left the page locked.
func (h *Handler) settleWrittenPeriodBriefRun(
	ctx context.Context,
	workspaceID, userID pgtype.UUID,
	userIDString string,
	existing notePeriodBriefRunRow,
) bool {
	if existing.Status != "synthesizing" || !existing.ID.Valid {
		return false
	}
	full, err := h.loadNotePeriodBriefRunByID(ctx, workspaceID, userID, existing.ID)
	if err != nil {
		return false
	}
	if h.loadPeriodBriefSynthesizerWrite(ctx, full, time.Time{}) == "" {
		return false
	}
	h.completePeriodBriefRunAfterSynth(ctx, full, userIDString, time.Time{})
	return true
}

// reconcilePeriodBriefLock aligns a status lock with live work. A finished
// synthesizer write is settled onto awaiting_confirm. A lock with no live
// collector / planner / synthesizer job is abandoned so the next start is
// not blocked by a dead HTTP waiter.
func (h *Handler) reconcilePeriodBriefLock(
	r *http.Request,
	workspaceID, userID pgtype.UUID,
	userIDString string,
	existing notePeriodBriefRunRow,
) notePeriodBriefRunRow {
	if !existing.ID.Valid || !periodBriefRunLocksComposerStatus(existing.Status) {
		return existing
	}
	if h.settleWrittenPeriodBriefRun(r.Context(), workspaceID, userID, userIDString, existing) {
		h.cancelPeriodBriefWorkerJobs(r, workspaceID, userIDString, existing)
		if latest, err := h.loadNotePeriodBriefRunByID(r.Context(), workspaceID, userID, existing.ID); err == nil {
			return latest
		}
		existing.Status = "awaiting_confirm"
		return existing
	}
	full, err := h.loadNotePeriodBriefRunByID(r.Context(), workspaceID, userID, existing.ID)
	if err != nil {
		return existing
	}
	if h.periodBriefHasLiveWorker(r.Context(), workspaceID, full) {
		return full
	}
	h.abandonDeadPeriodBriefRun(r.Context(), full, userIDString)
	full.Status = "cancelled"
	return full
}

func (h *Handler) periodBriefHasLiveWorker(
	ctx context.Context,
	workspaceID pgtype.UUID,
	run notePeriodBriefRunRow,
) bool {
	ids := extraPeriodBriefJobIDs(run)
	extras := make([]string, 0, len(ids))
	for _, id := range ids {
		if s := uuidToString(id); s != "" {
			extras = append(extras, s)
		}
	}
	var exists bool
	err := h.DB.QueryRow(ctx, `
SELECT EXISTS (
  SELECT 1 FROM note_worker_job
  WHERE workspace_id = $1
    AND status IN ('pending', 'dispatched', 'running')
    AND (
      page_id = $2
      OR id::text = ANY($3::text[])
    )
)`, workspaceID, run.DraftPageID, extras).Scan(&exists)
	return err == nil && exists
}

func (h *Handler) abandonDeadPeriodBriefRun(ctx context.Context, run notePeriodBriefRunRow, userIDString string) {
	if err := h.markNotePeriodBriefRunCancelled(ctx, run.ID); err != nil {
		return
	}
	run.Status = "cancelled"
	h.postPeriodBriefBubbleProgress(ctx, run, userIDString, "上次写汇报中断了，可以重新开始。")
}

func (h *Handler) allowPeriodBriefStartOnPage(
	w http.ResponseWriter,
	r *http.Request,
	workspaceID, userID pgtype.UUID,
	userIDString string,
	sourcePageID pgtype.UUID,
) bool {
	if !sourcePageID.Valid {
		return true
	}
	existing, err := h.loadActivePeriodBriefRunForPage(r.Context(), workspaceID, userID, sourcePageID)
	if err != nil || !existing.ID.Valid {
		return true
	}
	existing = h.reconcilePeriodBriefLock(r, workspaceID, userID, userIDString, existing)
	if !periodBriefRunLocksComposerStatus(existing.Status) {
		return true
	}
	writeError(w, http.StatusConflict, "a period brief is already running on this page")
	return false
}

func (h *Handler) loadOpenPeriodBriefRunForPage(
	ctx context.Context,
	workspaceID, userID, pageID pgtype.UUID,
) (notePeriodBriefRunRow, error) {
	row, err := h.loadLatestPeriodBriefRunForPage(ctx, workspaceID, userID, pageID)
	if err != nil {
		return row, err
	}
	if !periodBriefRunIsOpen(row.Status) {
		return notePeriodBriefRunRow{}, pgx.ErrNoRows
	}
	return row, nil
}

type errString string

func (e errString) Error() string { return string(e) }
