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
	// Soft-confirm after a 写汇报 detect (typed or FAB satellite seed).
	periodBriefIntentYesRe = regexp.MustCompile(`(?i)^(是|好|好的|确认|可以|要|嗯|行|开始)([的了吧啊～~]*)?$`)
	periodBriefIntentNoRe  = regexp.MustCompile(`(?i)^(否|不|不是|不要|不用|先不要|先不用)([的了吧啊～~]*)?$`)
)

// periodBriefPlanAskOutcome is how chat send continues after the 写汇报 intercept.
type periodBriefPlanAskOutcome struct {
	Handled bool
	// WakeContent, when non-empty, means Handled consumed the speech turn but
	// the Notes Assistant should still answer this text (decline / other ask).
	WakeContent string
}

const (
	periodBriefPromptStatusClarifying     = "clarifying"
	periodBriefPromptStatusAwaitingIntent = "awaiting_intent"
	periodBriefIntentConfirmCopy          = "看起来你是想写汇报。确认的话点「是」或回复「是」，我会打开采集卡；如果只是普通提问，点「否」或回复「否」，我按你的原话正常回答。"
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
	SourceAsk         string
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

func periodBriefIntentConfirmed(text string) bool {
	// Only explicit yes — repeating 「写汇报」 must not open the plan card.
	return periodBriefIntentYesRe.MatchString(strings.TrimSpace(text))
}

func periodBriefIntentDeclined(text string) bool {
	trimmed := strings.TrimSpace(text)
	if periodBriefIntentNoRe.MatchString(trimmed) {
		return true
	}
	return periodBriefIntakeCancelled(trimmed)
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
	return h.loadPeriodBriefPromptWithStatus(ctx, sessionID, workspaceID, userID, periodBriefPromptStatusClarifying)
}

func (h *Handler) loadPeriodBriefAwaitingIntent(
	ctx context.Context,
	sessionID, workspaceID, userID pgtype.UUID,
) (notePeriodBriefPromptRow, error) {
	return h.loadPeriodBriefPromptWithStatus(ctx, sessionID, workspaceID, userID, periodBriefPromptStatusAwaitingIntent)
}

func (h *Handler) loadPeriodBriefPromptWithStatus(
	ctx context.Context,
	sessionID, workspaceID, userID pgtype.UUID,
	status string,
) (notePeriodBriefPromptRow, error) {
	var row notePeriodBriefPromptRow
	err := h.DB.QueryRow(ctx, `
SELECT id, workspace_id, owner_user_id, chat_session_id, source_page_id,
       window_kind, window_date, start_date, end_date, collector_agent_ids,
       focus, COALESCE(source_ask, ''), awaiting_confirm, status
FROM note_period_brief_prompt
WHERE chat_session_id = $1 AND workspace_id = $2 AND owner_user_id = $3
  AND status = $4
ORDER BY created_at DESC
LIMIT 1`, sessionID, workspaceID, userID, status).Scan(
		&row.ID, &row.WorkspaceID, &row.OwnerUserID, &row.ChatSessionID, &row.SourcePageID,
		&row.WindowKind, &row.WindowDate, &row.StartDate, &row.EndDate, &row.CollectorAgentIDs,
		&row.Focus, &row.SourceAsk, &row.AwaitingConfirm, &row.Status,
	)
	return row, err
}

// loadLatestPeriodBriefPromptForSeed restores the last real collect draft,
// skipping soft-confirm awaiting_intent rows (empty window).
func (h *Handler) loadLatestPeriodBriefPromptForSeed(
	ctx context.Context,
	sessionID, workspaceID, userID pgtype.UUID,
) (notePeriodBriefPromptRow, error) {
	var row notePeriodBriefPromptRow
	err := h.DB.QueryRow(ctx, `
SELECT id, workspace_id, owner_user_id, chat_session_id, source_page_id,
       window_kind, window_date, start_date, end_date, collector_agent_ids,
       focus, COALESCE(source_ask, ''), awaiting_confirm, status
FROM note_period_brief_prompt
WHERE chat_session_id = $1 AND workspace_id = $2 AND owner_user_id = $3
  AND status <> $4
  AND trim(window_kind) <> ''
ORDER BY created_at DESC
LIMIT 1`, sessionID, workspaceID, userID, periodBriefPromptStatusAwaitingIntent).Scan(
		&row.ID, &row.WorkspaceID, &row.OwnerUserID, &row.ChatSessionID, &row.SourcePageID,
		&row.WindowKind, &row.WindowDate, &row.StartDate, &row.EndDate, &row.CollectorAgentIDs,
		&row.Focus, &row.SourceAsk, &row.AwaitingConfirm, &row.Status,
	)
	return row, err
}

func (h *Handler) upsertPeriodBriefPrompt(ctx context.Context, row *notePeriodBriefPromptRow) error {
	if row.CollectorAgentIDs == nil {
		row.CollectorAgentIDs = []string{}
	}
	if row.Status == "" {
		row.Status = periodBriefPromptStatusClarifying
	}
	if row.ID.Valid {
		_, err := h.DB.Exec(ctx, `
UPDATE note_period_brief_prompt
SET window_kind = $2, window_date = $3, start_date = $4, end_date = $5,
    collector_agent_ids = $6, focus = $7, source_ask = $8, awaiting_confirm = $9,
    status = $10, updated_at = now()
WHERE id = $1`,
			row.ID, row.WindowKind, row.WindowDate, row.StartDate, row.EndDate,
			row.CollectorAgentIDs, row.Focus, row.SourceAsk, row.AwaitingConfirm, row.Status,
		)
		return err
	}
	return h.DB.QueryRow(ctx, `
INSERT INTO note_period_brief_prompt (
  workspace_id, owner_user_id, chat_session_id, source_page_id,
  window_kind, window_date, start_date, end_date, collector_agent_ids,
  focus, source_ask, awaiting_confirm, status
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)
RETURNING id`,
		row.WorkspaceID, row.OwnerUserID, row.ChatSessionID, row.SourcePageID,
		row.WindowKind, row.WindowDate, row.StartDate, row.EndDate, row.CollectorAgentIDs,
		row.Focus, row.SourceAsk, row.AwaitingConfirm, row.Status,
	).Scan(&row.ID)
}

func (h *Handler) closePeriodBriefPrompt(ctx context.Context, id pgtype.UUID, status string) {
	_, _ = h.DB.Exec(ctx, `
UPDATE note_period_brief_prompt SET status = $2, updated_at = now() WHERE id = $1`, id, status)
}

func (h *Handler) openPeriodBriefPlanCard(
	ctx context.Context,
	session db.ChatSession,
	workspaceID, userID, pageID pgtype.UUID,
	userIDString string,
) bool {
	owned := h.listOwnedPeriodBriefCollectors(ctx, workspaceID, userID)
	row, err := h.seedPeriodBriefPlan(ctx, session.ID, workspaceID, userID, pageID, owned)
	if err != nil {
		slog.Warn("period brief plan ask failed to seed plan", "error", err)
		return false
	}
	row.Status = periodBriefPromptStatusClarifying
	row.AwaitingConfirm = true
	row.SourceAsk = ""
	if err := h.upsertPeriodBriefPrompt(ctx, &row); err != nil {
		slog.Warn("period brief plan ask failed to save plan", "error", err)
		return false
	}
	plan := periodBriefPlanFromRow(row)
	// Speak first, then show the card — otherwise the FE paints the card
	// before the bubble line that explains it.
	h.postPeriodBriefBubbleMessage(ctx, session.ID, workspaceID, session.CreatorID, userIDString, "assistant", "好，先确认这次的时间段和电脑。改完再点开始采集。")
	h.publishPeriodBriefPlanChanged(workspaceID, "user", uuidToString(userID), uuidToString(session.ID), uuidToString(userID), &plan)
	return true
}

func (h *Handler) tryHandlePeriodBriefPlanAsk(
	r *http.Request,
	session db.ChatSession,
	userID, workspaceID pgtype.UUID,
	userIDString, content string,
) periodBriefPlanAskOutcome {
	pageID := session.ContextNotePageID
	if !pageID.Valid {
		raw := h.chatSessionContextNotePageID(r.Context(), session.ID)
		if raw == "" {
			return periodBriefPlanAskOutcome{}
		}
		pageID = parseUUID(raw)
	}

	pending, pendingErr := h.loadPeriodBriefAwaitingIntent(r.Context(), session.ID, workspaceID, userID)
	if pendingErr != nil && !errors.Is(pendingErr, pgx.ErrNoRows) {
		return periodBriefPlanAskOutcome{}
	}
	if pendingErr == nil {
		original := strings.TrimSpace(pending.SourceAsk)
		if periodBriefIntentDeclined(content) {
			h.closePeriodBriefPrompt(r.Context(), pending.ID, "cancelled")
			h.publishPeriodBriefPlanChanged(workspaceID, "user", uuidToString(userID), uuidToString(session.ID), uuidToString(userID), nil)
			wake := original
			if wake == "" {
				wake = content
			}
			return periodBriefPlanAskOutcome{Handled: true, WakeContent: wake}
		}
		if periodBriefIntentConfirmed(content) {
			h.closePeriodBriefPrompt(r.Context(), pending.ID, "cancelled")
			if !h.openPeriodBriefPlanCard(r.Context(), session, workspaceID, userID, pageID, userIDString) {
				return periodBriefPlanAskOutcome{}
			}
			return periodBriefPlanAskOutcome{Handled: true}
		}
		if looksLikePeriodBriefPlanAsk(content) {
			// Same funnel again while we asked — keep waiting; refresh the ask.
			pending.SourceAsk = strings.TrimSpace(content)
			pending.AwaitingConfirm = true
			pending.Status = periodBriefPromptStatusAwaitingIntent
			if err := h.upsertPeriodBriefPrompt(r.Context(), &pending); err != nil {
				slog.Warn("period brief intent confirm failed to refresh", "error", err)
				return periodBriefPlanAskOutcome{}
			}
			intentPlan := periodBriefIntentConfirmPlan()
			h.postPeriodBriefBubbleMessage(r.Context(), session.ID, workspaceID, session.CreatorID, userIDString, "assistant", periodBriefIntentConfirmCopy)
			h.publishPeriodBriefPlanChanged(workspaceID, "user", uuidToString(userID), uuidToString(session.ID), uuidToString(userID), &intentPlan)
			return periodBriefPlanAskOutcome{Handled: true}
		}
		// Other speech while we asked — drop the soft confirm and answer this turn.
		h.closePeriodBriefPrompt(r.Context(), pending.ID, "cancelled")
		h.publishPeriodBriefPlanChanged(workspaceID, "user", uuidToString(userID), uuidToString(session.ID), uuidToString(userID), nil)
		return periodBriefPlanAskOutcome{Handled: true, WakeContent: content}
	}

	if periodBriefIntakeCancelled(content) {
		prompt, promptErr := h.loadPeriodBriefPrompt(r.Context(), session.ID, workspaceID, userID)
		if promptErr != nil && !errors.Is(promptErr, pgx.ErrNoRows) {
			return periodBriefPlanAskOutcome{}
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
			return periodBriefPlanAskOutcome{}
		}
		h.publishPeriodBriefPlanChanged(workspaceID, "user", uuidToString(userID), uuidToString(session.ID), uuidToString(userID), nil)
		if !stopped {
			h.postPeriodBriefBubbleMessage(r.Context(), session.ID, workspaceID, session.CreatorID, userIDString, "assistant", "好，那这次先不写汇报。")
		}
		return periodBriefPlanAskOutcome{Handled: true}
	}
	if !looksLikePeriodBriefPlanAsk(content) {
		return periodBriefPlanAskOutcome{}
	}
	if run, err := h.loadActivePeriodBriefRunForPage(r.Context(), workspaceID, userID, pageID); err == nil && run.ID.Valid {
		run = h.reconcilePeriodBriefLock(r, workspaceID, userID, userIDString, run)
		if periodBriefRunLocksComposerStatus(run.Status) {
			h.postPeriodBriefBubbleMessage(r.Context(), session.ID, workspaceID, session.CreatorID, userIDString, "assistant", "上一份写汇报还在进行中，结束后我们再开新的。")
			return periodBriefPlanAskOutcome{Handled: true}
		}
	}

	row := notePeriodBriefPromptRow{
		WorkspaceID:     workspaceID,
		OwnerUserID:     userID,
		ChatSessionID:   session.ID,
		SourcePageID:    pageID,
		SourceAsk:       strings.TrimSpace(content),
		AwaitingConfirm: true,
		Status:          periodBriefPromptStatusAwaitingIntent,
	}
	if err := h.upsertPeriodBriefPrompt(r.Context(), &row); err != nil {
		slog.Warn("period brief intent confirm failed to save", "error", err)
		return periodBriefPlanAskOutcome{}
	}
	intentPlan := periodBriefIntentConfirmPlan()
	h.postPeriodBriefBubbleMessage(r.Context(), session.ID, workspaceID, session.CreatorID, userIDString, "assistant", periodBriefIntentConfirmCopy)
	h.publishPeriodBriefPlanChanged(workspaceID, "user", uuidToString(userID), uuidToString(session.ID), uuidToString(userID), &intentPlan)
	return periodBriefPlanAskOutcome{Handled: true}
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
	prev, prevErr := h.loadLatestPeriodBriefPromptForSeed(ctx, sessionID, workspaceID, userID)
	if prevErr == nil {
		prev.ID = pgtype.UUID{}
		prev.Status = periodBriefPromptStatusClarifying
		prev.SourcePageID = pageID
		prev.SourceAsk = ""
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
		Status:        periodBriefPromptStatusClarifying,
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
