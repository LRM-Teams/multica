package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

var (
	periodBriefIntentRe       = regexp.MustCompile(`(?i)(写|整理|做|生成|帮我).{0,12}(汇报|周报)|period\s*work\s*brief|period\s*brief|weekly\s*report|write\s+(a\s+)?(period\s+work\s+)?reports?|^(report|reports)$`)
	periodBriefAllComputersRe = regexp.MustCompile(`(?i)全部|所有电脑|都要|都行|都可以|all(\s+computers?)?`)
	periodBriefCancelRe       = regexp.MustCompile(`(?i)^(取消|算了|先不写|不用写了|不要写了)`)
	periodBriefYMDRe          = regexp.MustCompile(`\d{4}-\d{2}-\d{2}`)
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

func periodBriefIntakeCancelled(text string) bool {
	return periodBriefCancelRe.MatchString(strings.TrimSpace(text))
}

func parsePeriodBriefIntakeWindow(text, today string) (kind, date, start, end string, ok bool) {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return "", "", "", "", false
	}
	ymds := periodBriefYMDRe.FindAllString(trimmed, 2)
	if len(ymds) >= 2 {
		start, end = ymds[0], ymds[1]
		if start > end {
			start, end = end, start
		}
		return "custom", "", start, end, true
	}
	switch {
	case regexp.MustCompile(`(?i)上个月|上月|last\s+month`).MatchString(trimmed):
		return "month", shiftPeriodBriefDay(today, -31), "", "", true
	case regexp.MustCompile(`(?i)本月|这个月|this\s+month`).MatchString(trimmed):
		return "month", today, "", "", true
	case regexp.MustCompile(`(?i)上周|上週|last\s+week`).MatchString(trimmed):
		return "week", shiftPeriodBriefDay(today, -7), "", "", true
	case regexp.MustCompile(`(?i)本周|本週|这周|這周|this\s+week`).MatchString(trimmed):
		return "week", today, "", "", true
	case regexp.MustCompile(`(?i)昨天|昨日|yesterday`).MatchString(trimmed):
		return "day", shiftPeriodBriefDay(today, -1), "", "", true
	case regexp.MustCompile(`(?i)今天|今日|本日|today`).MatchString(trimmed):
		return "day", today, "", "", true
	default:
		return "", "", "", "", false
	}
}

func shiftPeriodBriefDay(yyyyMMdd string, delta int) string {
	day, err := time.Parse("2006-01-02", yyyyMMdd)
	if err != nil {
		return yyyyMMdd
	}
	return day.AddDate(0, 0, delta).Format("2006-01-02")
}

func parsePeriodBriefIntakeCollectors(text string, owned []periodBriefOwnedCollector) ([]string, bool) {
	if len(owned) == 0 {
		return nil, false
	}
	if periodBriefAllComputersRe.MatchString(text) {
		ids := make([]string, 0, len(owned))
		for _, c := range owned {
			ids = append(ids, c.ID)
		}
		return ids, true
	}
	var ids []string
	for _, c := range owned {
		label := strings.TrimSpace(c.Label)
		if label != "" && strings.Contains(text, label) {
			ids = append(ids, c.ID)
			continue
		}
		stripped := strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(label, periodBriefCollectorDisplayLead), periodBriefCollectorCloudDisplayLead))
		if len([]rune(stripped)) >= 2 && strings.Contains(text, stripped) {
			ids = append(ids, c.ID)
		}
	}
	if len(ids) == 0 {
		return nil, false
	}
	return ids, true
}

var periodBriefWindowPhraseRe = regexp.MustCompile(`(?i)上个月|上月|last\s+month|本月|这个月|this\s+month|上周|上週|last\s+week|本周|本週|这周|這周|this\s+week|昨天|昨日|yesterday|今天|今日|本日|today|时间|日期`)

func periodBriefIntakeFocus(text string, owned []periodBriefOwnedCollector) string {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" || periodBriefIntakeCancelled(trimmed) || periodBriefConfirmDecision(trimmed) != "" {
		return ""
	}
	if looksLikePeriodBriefRequest(trimmed) && len([]rune(trimmed)) <= 12 {
		return ""
	}
	stripped := periodBriefWindowPhraseRe.ReplaceAllString(trimmed, " ")
	stripped = periodBriefAllComputersRe.ReplaceAllString(stripped, " ")
	stripped = periodBriefIntentRe.ReplaceAllString(stripped, " ")
	stripped = periodBriefYMDRe.ReplaceAllString(stripped, " ")
	for _, c := range owned {
		if label := strings.TrimSpace(c.Label); label != "" {
			stripped = strings.ReplaceAll(stripped, label, " ")
		}
		lead := strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(c.Label, periodBriefCollectorDisplayLead), periodBriefCollectorCloudDisplayLead))
		if lead != "" {
			stripped = strings.ReplaceAll(stripped, lead, " ")
		}
	}
	stripped = regexp.MustCompile(`[，,。.\s]+`).ReplaceAllString(stripped, "")
	if stripped == "" {
		return ""
	}
	return trimmed
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

func periodBriefPromptReady(row notePeriodBriefPromptRow) bool {
	if strings.TrimSpace(row.WindowKind) == "" || len(row.CollectorAgentIDs) == 0 {
		return false
	}
	if row.WindowKind == "custom" {
		return strings.TrimSpace(row.StartDate) != "" && strings.TrimSpace(row.EndDate) != ""
	}
	return true
}

func (h *Handler) applyPeriodBriefIntakeText(row *notePeriodBriefPromptRow, text string, owned []periodBriefOwnedCollector, today string) {
	if kind, date, start, end, ok := parsePeriodBriefIntakeWindow(text, today); ok {
		row.WindowKind = kind
		row.WindowDate = date
		row.StartDate = start
		row.EndDate = end
	}
	if ids, ok := parsePeriodBriefIntakeCollectors(text, owned); ok {
		row.CollectorAgentIDs = ids
	}
	if focus := periodBriefIntakeFocus(text, owned); focus != "" {
		row.Focus = focus
	}
}

func formatPeriodBriefIntakeAsk(row notePeriodBriefPromptRow, owned []periodBriefOwnedCollector) string {
	var computers []string
	for _, c := range owned {
		computers = append(computers, "「"+c.Label+"」")
	}
	computerLine := "你这边还没有可采集的电脑。接入自己的 Computer 之后再告诉我用哪几台。"
	if len(computers) > 0 {
		computerLine = "电脑用哪几台？现在有 " + strings.Join(computers, "、") + "。也可以说「全部」。"
	}
	needWindow := strings.TrimSpace(row.WindowKind) == ""
	needComputers := len(row.CollectorAgentIDs) == 0
	switch {
	case needWindow && needComputers:
		return "好，我来写汇报。先确认两件事：时间是今天、本周、本月，还是一段自定义日期？另外，" + computerLine
	case needWindow:
		return "电脑这边记下了。时间用今天、本周、本月，还是自定义日期？"
	case needComputers:
		return "时间这边记下了。" + computerLine
	default:
		return formatPeriodBriefIntakeConfirm(row, owned)
	}
}

func formatPeriodBriefIntakeConfirm(row notePeriodBriefPromptRow, owned []periodBriefOwnedCollector) string {
	label := periodBriefWindowKindLabel(row.WindowKind)
	if row.WindowKind == "custom" && row.StartDate != "" && row.EndDate != "" {
		label = row.StartDate + " 到 " + row.EndDate
	}
	names := make([]string, 0, len(row.CollectorAgentIDs))
	byID := map[string]string{}
	for _, c := range owned {
		byID[c.ID] = c.Label
	}
	for _, id := range row.CollectorAgentIDs {
		if name := byID[id]; name != "" {
			names = append(names, name)
		}
	}
	return "按「" + label + "」、电脑「" + strings.Join(names, "、") + "」来写，可以吗？"
}

func periodBriefWindowKindLabel(kind string) string {
	switch kind {
	case "day":
		return "今天"
	case "month":
		return "本月"
	case "custom":
		return "自定义"
	default:
		return "本周"
	}
}

func (h *Handler) tryHandlePeriodBriefBubbleChat(
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
	owned := h.listOwnedPeriodBriefCollectors(r.Context(), workspaceID, userID)
	prompt, promptErr := h.loadPeriodBriefPrompt(r.Context(), session.ID, workspaceID, userID)
	hasPrompt := promptErr == nil
	if !hasPrompt && !looksLikePeriodBriefRequest(content) {
		return false
	}

	if periodBriefIntakeCancelled(content) && hasPrompt {
		h.closePeriodBriefPrompt(r.Context(), prompt.ID, "cancelled")
		h.postPeriodBriefBubbleMessage(r.Context(), session.ID, workspaceID, session.CreatorID, userIDString, "assistant", "好，那这次先不写汇报。")
		return true
	}

	if run, err := h.loadActivePeriodBriefRunForPage(r.Context(), workspaceID, userID, pageID); err == nil && periodBriefRunLocksComposerStatus(run.Status) {
		h.postPeriodBriefBubbleMessage(r.Context(), session.ID, workspaceID, session.CreatorID, userIDString, "assistant", "上一份写汇报还在进行中，结束后我们再开新的。")
		return true
	}

	today := time.Now().Format("2006-01-02")
	if !hasPrompt {
		prompt = notePeriodBriefPromptRow{
			WorkspaceID:   workspaceID,
			OwnerUserID:   userID,
			ChatSessionID: session.ID,
			SourcePageID:  pageID,
		}
	}
	h.applyPeriodBriefIntakeText(&prompt, content, owned, today)

	ready := periodBriefPromptReady(prompt)
	if ready && hasPrompt && !prompt.AwaitingConfirm {
		if err := h.startPeriodBriefFromChat(r, session, userIDString, prompt, owned); err != nil {
			h.postPeriodBriefBubbleMessage(r.Context(), session.ID, workspaceID, session.CreatorID, userIDString, "assistant", "现在还开不了写汇报："+err.Error())
			return true
		}
		return true
	}
	if ready && prompt.AwaitingConfirm && periodBriefConfirmDecision(content) == "yes" {
		if err := h.startPeriodBriefFromChat(r, session, userIDString, prompt, owned); err != nil {
			h.postPeriodBriefBubbleMessage(r.Context(), session.ID, workspaceID, session.CreatorID, userIDString, "assistant", "现在还开不了写汇报："+err.Error())
			return true
		}
		return true
	}
	if ready {
		prompt.AwaitingConfirm = true
		if err := h.upsertPeriodBriefPrompt(r.Context(), &prompt); err != nil {
			slog.Warn("period brief prompt upsert failed", "error", err)
			return false
		}
		h.postPeriodBriefBubbleMessage(r.Context(), session.ID, workspaceID, session.CreatorID, userIDString, "assistant", formatPeriodBriefIntakeConfirm(prompt, owned))
		return true
	}

	prompt.AwaitingConfirm = false
	if err := h.upsertPeriodBriefPrompt(r.Context(), &prompt); err != nil {
		slog.Warn("period brief prompt upsert failed", "error", err)
		return false
	}
	h.postPeriodBriefBubbleMessage(r.Context(), session.ID, workspaceID, session.CreatorID, userIDString, "assistant", formatPeriodBriefIntakeAsk(prompt, owned))
	return true
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

func (h *Handler) startPeriodBriefFromChat(
	r *http.Request,
	session db.ChatSession,
	userIDString string,
	prompt notePeriodBriefPromptRow,
	owned []periodBriefOwnedCollector,
) error {
	body := createNotePeriodBriefRequest{
		Window:            prompt.WindowKind,
		Date:              prompt.WindowDate,
		StartDate:         prompt.StartDate,
		EndDate:           prompt.EndDate,
		AgentID:           uuidToString(session.AgentID),
		CollectorAgentIDs: prompt.CollectorAgentIDs,
		Focus:             prompt.Focus,
		ContextNotePageID: uuidToString(prompt.SourcePageID),
		ChatSessionID:     uuidToString(session.ID),
		FromChat:          true,
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, "/api/notes/period-briefs", bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header = r.Header.Clone()
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.CreateNotePeriodBrief(rec, req)
	if rec.Code != http.StatusCreated {
		return jsonErrorMessage(rec.Body.Bytes(), rec.Code)
	}
	h.closePeriodBriefPrompt(r.Context(), prompt.ID, "consumed")
	_ = owned
	_ = userIDString
	return nil
}

func jsonErrorMessage(body []byte, code int) error {
	var parsed struct {
		Error   string `json:"error"`
		Message string `json:"message"`
	}
	if json.Unmarshal(body, &parsed) == nil {
		if parsed.Error != "" {
			return errString(parsed.Error)
		}
		if parsed.Message != "" {
			return errString(parsed.Message)
		}
	}
	trimmed := strings.TrimSpace(string(body))
	if trimmed == "" {
		return errString(http.StatusText(code))
	}
	return errString(trimmed)
}

type errString string

func (e errString) Error() string { return string(e) }
