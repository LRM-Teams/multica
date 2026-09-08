package handler

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// Disabled in handler tests so a late silence fallback cannot write after cleanup.
var periodBriefProgressSilenceBound = 8 * time.Second

type periodBriefProgressRow struct {
	Name   string
	Status string
}

type periodBriefProgressBoard struct {
	Event     string
	RunStatus string
	Window    string
	Focus     string
	Settled   []periodBriefProgressRow
	Waiting   []periodBriefProgressRow
	Retryable bool
}

func formatPeriodBriefProgressBoard(board periodBriefProgressBoard) string {
	event := strings.TrimSpace(board.Event)
	if event == "" {
		event = "update"
	}
	var b strings.Builder
	b.WriteString("<period_brief_progress>\n")
	if event == "run_started" {
		b.WriteString("This is a progress wake on the Notes FAB bubble session. Restate the confirmed window, selected computers, and focus. Tell the human collection already started with these conditions and ask them to wait. Collectors are already running. Do not start synthesis. Do not collect OS work. Do not emit chat XML.\n")
	} else {
		b.WriteString("This is a progress wake on the Notes FAB bubble session. Write one short reminder in final assistant output. Do not start synthesis. Do not collect OS work. Do not emit chat XML.\n")
	}
	b.WriteString("event: ")
	b.WriteString(event)
	b.WriteString("\n")
	if status := strings.TrimSpace(board.RunStatus); status != "" {
		b.WriteString("run_status: ")
		b.WriteString(status)
		b.WriteString("\n")
	}
	if window := strings.TrimSpace(board.Window); window != "" {
		b.WriteString("window: ")
		b.WriteString(window)
		b.WriteString("\n")
	}
	b.WriteString("focus: ")
	if focus := strings.TrimSpace(board.Focus); focus != "" {
		b.WriteString(focus)
	} else {
		b.WriteString("unconstrained")
	}
	b.WriteString("\n")
	b.WriteString("settled:\n")
	if len(board.Settled) == 0 {
		b.WriteString("- none\n")
	} else {
		for _, row := range board.Settled {
			b.WriteString("- name: ")
			b.WriteString(firstNonEmpty(row.Name, "采集员"))
			b.WriteString(" status: ")
			b.WriteString(firstNonEmpty(row.Status, "ready"))
			b.WriteString("\n")
		}
	}
	b.WriteString("waiting:\n")
	if len(board.Waiting) == 0 {
		b.WriteString("- none\n")
	} else {
		for _, row := range board.Waiting {
			b.WriteString("- name: ")
			b.WriteString(firstNonEmpty(row.Name, "采集员"))
			b.WriteString(" status: ")
			b.WriteString(firstNonEmpty(row.Status, "pending"))
			b.WriteString("\n")
		}
	}
	if board.Retryable {
		b.WriteString("retryable: yes\n")
	} else {
		b.WriteString("retryable: no\n")
	}
	b.WriteString("</period_brief_progress>\n\n")
	return b.String()
}

func periodBriefProgressBoardFromRun(
	run notePeriodBriefRunRow,
	names map[string]string,
	event string,
	packs []notePeriodBriefPackResult,
) periodBriefProgressBoard {
	board := periodBriefProgressBoard{
		Event:     event,
		RunStatus: run.Status,
		Window:    strings.TrimSpace(run.WindowLabel),
		Focus:     strings.TrimSpace(run.UserFocus),
	}
	if len(packs) > 0 {
		for _, pack := range packs {
			row := periodBriefProgressRow{
				Name:   firstNonEmpty(names[pack.AgentID], pack.Title, pack.AgentID),
				Status: firstNonEmpty(pack.Status, "pending"),
			}
			switch pack.Status {
			case "ready", "empty", "failed", "cancelled", "stalled":
				board.Settled = append(board.Settled, row)
				if pack.Retryable {
					board.Retryable = true
				}
			default:
				board.Waiting = append(board.Waiting, row)
			}
		}
		return board
	}
	for _, collector := range run.Collectors {
		name := firstNonEmpty(names[collector.AgentID], collector.AgentID)
		if strings.TrimSpace(collector.PackMarkdown) != "" {
			status := "ready"
			if periodBriefPackIsCarried(collector) {
				status = "carried"
			}
			board.Settled = append(board.Settled, periodBriefProgressRow{Name: name, Status: status})
			continue
		}
		board.Waiting = append(board.Waiting, periodBriefProgressRow{Name: name, Status: "pending"})
	}
	return board
}

func (h *Handler) periodBriefProgressNames(
	ctx context.Context,
	run notePeriodBriefRunRow,
	packs []notePeriodBriefPackResult,
) map[string]string {
	ids := make([]string, 0, len(run.Collectors)+len(packs))
	for _, collector := range run.Collectors {
		ids = append(ids, collector.AgentID)
	}
	for _, pack := range packs {
		ids = append(ids, pack.AgentID)
	}
	spoken := h.periodBriefCollectorSpokenNames(ctx, run.WorkspaceID, ids)
	out := make(map[string]string, len(ids))
	for i, id := range ids {
		if i < len(spoken) {
			out[id] = spoken[i]
		}
	}
	return out
}

func (h *Handler) wakePeriodBriefProgress(
	ctx context.Context,
	run notePeriodBriefRunRow,
	userIDString, event string,
	packs []notePeriodBriefPackResult,
) {
	if !run.ChatSessionID.Valid || h.TaskService == nil {
		return
	}
	session, err := h.Queries.GetChatSession(ctx, run.ChatSessionID)
	if err != nil {
		slog.Warn("period brief progress wake skipped: session", "error", err)
		return
	}
	if _, err := h.TaskService.EnqueueChatTask(ctx, session, run.OwnerUserID); err != nil {
		slog.Warn("period brief progress wake failed", "error", err)
		return
	}
	fallback := periodBriefProgressFallbackCopy(event, packs)
	failedIDs := periodBriefFailedCollectorIDs(packs)
	if periodBriefAnyCollectorFailed(packs) {
		spoken := joinPeriodBriefSpokenNames(h.periodBriefCollectorSpokenNames(ctx, run.WorkspaceID, failedIDs))
		if event == "collect_failed" {
			fallback = periodBriefMissingHarvestProgressCopy(spoken)
		} else if event == "materials_ready" && periodBriefAnyCollectorReady(packs) {
			fallback = periodBriefPartialHarvestProgressCopy(spoken)
		}
	}

	// Failure-bearing beats must land as durable platform lines. Silence-fence
	// recovery races with pack_received's generic "采集有了新进展" and can leave
	// the human with a finished brief and no failure notice.
	switch event {
	case "collect_failed", "collect_retrying":
		h.postPeriodBriefBubbleMessage(ctx, run.ChatSessionID, run.WorkspaceID, run.OwnerUserID, userIDString, "assistant", fallback)
	case "materials_ready":
		if periodBriefAnyCollectorFailed(packs) {
			h.postPeriodBriefBubbleMessage(ctx, run.ChatSessionID, run.WorkspaceID, run.OwnerUserID, userIDString, "assistant", fallback)
		} else {
			h.armPeriodBriefProgressSilenceFence(ctx, run, userIDString, fallback)
		}
	case "pack_received":
		// Wake only — do not silence-fence a generic progress line.
	default:
		h.armPeriodBriefProgressSilenceFence(ctx, run, userIDString, fallback)
	}
}

func (h *Handler) wakePeriodBriefRunStarted(
	ctx context.Context,
	workspaceID, draftID pgtype.UUID,
	userIDString string,
) {
	run, err := h.loadNotePeriodBriefRunByDraft(ctx, workspaceID, draftID)
	if err != nil {
		return
	}
	h.wakePeriodBriefProgress(ctx, run, userIDString, "run_started", nil)
}

func periodBriefProgressFallbackCopy(event string, packs []notePeriodBriefPackResult) string {
	if event == "materials_ready" || event == "collect_failed" {
		return periodBriefMaterialsProgressCopy(packs)
	}
	if event == "collect_retrying" {
		return "有采集没有成功，正在再发起一次采集。"
	}
	if event == "run_started" {
		return "已按这些条件开始采集，请稍等。"
	}
	return "采集有了新进展。"
}

func (h *Handler) armPeriodBriefProgressSilenceFence(
	ctx context.Context,
	run notePeriodBriefRunRow,
	userIDString, fallback string,
) {
	if periodBriefProgressSilenceBound <= 0 || strings.TrimSpace(fallback) == "" || !run.ChatSessionID.Valid {
		return
	}
	sessionID := run.ChatSessionID
	markedAt := time.Now()
	bg := context.WithoutCancel(ctx)
	go func() {
		timer := time.NewTimer(periodBriefProgressSilenceBound)
		defer timer.Stop()
		select {
		case <-bg.Done():
			return
		case <-timer.C:
		}
		var count int
		err := h.DB.QueryRow(bg, `
SELECT count(*)
FROM chat_message
WHERE chat_session_id = $1 AND role = 'assistant' AND created_at > $2 AND length(trim(content)) > 0`,
			sessionID, markedAt,
		).Scan(&count)
		if err != nil || count > 0 {
			return
		}
		h.postPeriodBriefBubbleMessage(bg, sessionID, run.WorkspaceID, run.OwnerUserID, userIDString, "assistant", fallback)
	}()
}

func (h *Handler) loadOpenPeriodBriefRunForSession(
	ctx context.Context,
	sessionID pgtype.UUID,
) (notePeriodBriefRunRow, bool) {
	var row notePeriodBriefRunRow
	if !sessionID.Valid {
		return row, false
	}
	var collectorsRaw []byte
	var planRaw []byte
	var channelID pgtype.UUID
	err := h.DB.QueryRow(ctx, `
SELECT id, workspace_id, owner_user_id, draft_page_id, folder_page_id, synthesizer_agent_id,
       window_label, window_start, window_end, timezone, window_kind,
       channel_id, facts_text, sources_used, sources_empty, sources_skipped,
       collectors, status, user_focus, collect_plan, planner_job_id,
       chat_session_id, source_page_id
FROM note_period_brief_run
WHERE chat_session_id = $1 AND status IN ('planning', 'collecting', 'synthesizing')
ORDER BY updated_at DESC
LIMIT 1`, sessionID).Scan(
		&row.ID, &row.WorkspaceID, &row.OwnerUserID, &row.DraftPageID, &row.FolderPageID, &row.SynthesizerAgentID,
		&row.WindowLabel, &row.WindowStart, &row.WindowEnd, &row.Timezone, &row.WindowKind,
		&channelID, &row.FactsText, &row.SourcesUsed, &row.SourcesEmpty, &row.SourcesSkipped,
		&collectorsRaw, &row.Status, &row.UserFocus, &planRaw, &row.PlannerJobID,
		&row.ChatSessionID, &row.SourcePageID,
	)
	if err != nil {
		if err != pgx.ErrNoRows {
			slog.Debug("period brief progress run lookup failed", "error", err)
		}
		return row, false
	}
	row.ChannelID = channelID
	if len(collectorsRaw) > 0 {
		_ = json.Unmarshal(collectorsRaw, &row.Collectors)
	}
	if len(planRaw) > 0 && string(planRaw) != "null" {
		var plan notePeriodBriefCollectPlan
		if json.Unmarshal(planRaw, &plan) == nil {
			row.CollectPlan = &plan
		}
	}
	return row, true
}
