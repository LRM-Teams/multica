package handler

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/messageparts"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func formatPeriodBriefBubbleUserTurn(windowLabel string, collectorLabels []string, focus string) string {
	label := strings.TrimSpace(windowLabel)
	if label == "" {
		label = "本周"
	}
	var b strings.Builder
	b.WriteString("写汇报\n\n时间：")
	b.WriteString(label)
	if len(collectorLabels) > 0 {
		b.WriteString("\n电脑：")
		b.WriteString(strings.Join(collectorLabels, "、"))
	}
	if trimmed := strings.TrimSpace(focus); trimmed != "" {
		b.WriteString("\n\n")
		b.WriteString(trimmed)
	}
	return b.String()
}

func (h *Handler) periodBriefCollectorSpokenNames(
	ctx context.Context,
	workspaceID pgtype.UUID,
	collectorIDs []string,
) []string {
	out := make([]string, 0, len(collectorIDs))
	for _, id := range collectorIDs {
		agent, err := h.Queries.GetAgentInWorkspace(ctx, db.GetAgentInWorkspaceParams{
			ID:          parseUUID(id),
			WorkspaceID: workspaceID,
		})
		if err != nil {
			out = append(out, id)
			continue
		}
		name := strings.TrimSpace(agent.DisplayName)
		if name == "" {
			name = strings.TrimSpace(agent.Name)
		}
		if name == "" {
			name = id
		}
		out = append(out, name)
	}
	return out
}

func joinPeriodBriefSpokenNames(names []string) string {
	if len(names) == 0 {
		return "采集员"
	}
	return strings.Join(names, "、")
}

// ensurePeriodBriefBubbleSession uses chatSessionID when the bubble is
// already on a thread. An empty id starts a new session — never the
// latest session on the note (⊕ new chat must not jump back).
func (h *Handler) ensurePeriodBriefBubbleSession(
	ctx context.Context,
	workspaceID, userID, agentID, sourcePageID pgtype.UUID,
	chatSessionID string,
) (pgtype.UUID, error) {
	if trimmed := strings.TrimSpace(chatSessionID); trimmed != "" {
		id, ok := parseUUIDQuiet(trimmed)
		if ok {
			var found pgtype.UUID
			scanErr := h.DB.QueryRow(ctx, `
SELECT id FROM chat_session
WHERE id = $1 AND workspace_id = $2 AND creator_id = $3 AND agent_id = $4 AND status = 'active'`,
				id, workspaceID, userID, agentID,
			).Scan(&found)
			if scanErr == nil {
				if sourcePageID.Valid {
					_, _ = h.DB.Exec(ctx, `
UPDATE chat_session SET context_note_page_id = $2, updated_at = now()
WHERE id = $1 AND context_note_page_id IS NULL`, found, sourcePageID)
				}
				return found, nil
			}
		}
	}
	session, err := h.Queries.CreateChatSession(ctx, db.CreateChatSessionParams{
		WorkspaceID: workspaceID,
		AgentID:     agentID,
		CreatorID:   userID,
		Title:       "写汇报",
	})
	if err != nil {
		return pgtype.UUID{}, err
	}
	if sourcePageID.Valid {
		if _, err := h.DB.Exec(ctx, `
UPDATE chat_session SET context_note_page_id = $2, updated_at = now()
WHERE id = $1`, session.ID, sourcePageID); err != nil {
			return pgtype.UUID{}, err
		}
	}
	return session.ID, nil
}

func (h *Handler) postPeriodBriefBubbleMessage(
	ctx context.Context,
	sessionID, workspaceID, creatorID pgtype.UUID,
	userIDString, role, content string,
	parts ...protocol.MessagePart,
) {
	if !sessionID.Valid || strings.TrimSpace(content) == "" {
		return
	}
	msg, err := h.Queries.CreateChatMessage(ctx, db.CreateChatMessageParams{
		ChatSessionID: sessionID,
		Role:          role,
		Content:       content,
		Parts:         messageparts.MustJSON(parts),
	})
	if err != nil {
		slog.Warn("period brief bubble message failed", "role", role, "error", err)
		return
	}
	_ = h.Queries.TouchChatSession(ctx, sessionID)
	sessionKey := uuidToString(sessionID)
	workspaceKey := uuidToString(workspaceID)
	creatorKey := uuidToString(creatorID)
	if role == "user" {
		h.publishChatToCreator(protocol.EventChatMessage, workspaceKey, "member", userIDString, sessionKey, creatorKey, protocol.ChatMessagePayload{
			ChatSessionID: sessionKey,
			MessageID:     uuidToString(msg.ID),
			Role:          "user",
			Content:       content,
			Parts:         parts,
			CreatedAt:     timestampToString(msg.CreatedAt),
		})
		return
	}
	h.publishChatToCreator(protocol.EventChatDone, workspaceKey, "system", "", sessionKey, creatorKey, protocol.ChatDonePayload{
		ChatSessionID: sessionKey,
		Type:          protocol.ChatOutputKindMessage,
		MessageID:     uuidToString(msg.ID),
		Content:       content,
		Parts:         parts,
		CreatedAt:     timestampToString(msg.CreatedAt),
		ElapsedMs:     0,
	})
}

func periodBriefCollapsiblePart(refID, label, text string) protocol.MessagePart {
	title := strings.TrimSpace(label)
	if title == "" {
		title = "Untitled"
	}
	body := strings.TrimSpace(text)
	if utf8.RuneCountInString(body) > periodBriefPartBodyMaxRunes {
		body = string([]rune(body)[:periodBriefPartBodyMaxRunes]) + "…"
	}
	return protocol.MessagePart{
		Type:  protocol.MessagePartTypeNoteBrief,
		RefID: strings.TrimSpace(refID),
		Label: title,
		Text:  body,
	}
}

func periodBriefInsertActionsPart(runID string) protocol.MessagePart {
	return protocol.MessagePart{
		Type:  protocol.MessagePartTypePeriodBriefInsert,
		RefID: strings.TrimSpace(runID),
	}
}

const periodBriefPartBodyMaxRunes = 32_000

func (h *Handler) startPeriodBriefBubbleTranscript(
	ctx context.Context,
	workspaceID, userID, sessionID pgtype.UUID,
	userIDString, windowLabel, focus string,
	collectorIDs []string,
	skipUserTurn bool,
) {
	if !sessionID.Valid {
		return
	}
	names := h.periodBriefCollectorSpokenNames(ctx, workspaceID, collectorIDs)
	spoken := joinPeriodBriefSpokenNames(names)
	if !skipUserTurn {
		h.postPeriodBriefBubbleMessage(ctx, sessionID, workspaceID, userID, userIDString, "user",
			formatPeriodBriefBubbleUserTurn(windowLabel, names, focus))
	}
	h.postPeriodBriefBubbleMessage(ctx, sessionID, workspaceID, userID, userIDString, "assistant",
		"我将让"+spoken+"先采集信息。")
}

func (h *Handler) postPeriodBriefBubbleAssigned(
	ctx context.Context,
	workspaceID, userID, sessionID pgtype.UUID,
	userIDString string,
	collectorIDs []string,
) {
	if !sessionID.Valid {
		return
	}
	spoken := joinPeriodBriefSpokenNames(h.periodBriefCollectorSpokenNames(ctx, workspaceID, collectorIDs))
	h.postPeriodBriefBubbleMessage(ctx, sessionID, workspaceID, userID, userIDString, "assistant",
		"我已经将任务分派给"+spoken+"了。")
}

func (h *Handler) postPeriodBriefBubbleProgress(ctx context.Context, run notePeriodBriefRunRow, userIDString, content string) {
	h.postPeriodBriefBubbleMessage(ctx, run.ChatSessionID, run.WorkspaceID, run.OwnerUserID, userIDString, "assistant", content)
}

func (h *Handler) resolvePeriodBriefSourcePage(
	w http.ResponseWriter,
	r *http.Request,
	workspaceID, userID pgtype.UUID,
	raw string,
) (pgtype.UUID, bool) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return pgtype.UUID{}, true
	}
	pageID, ok := parseUUIDOrBadRequest(w, trimmed, "context_note_page_id")
	if !ok {
		return pgtype.UUID{}, false
	}
	accessible, _, err := h.noteAccess(r.Context(), pageID, workspaceID, userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to authorize note page")
		return pgtype.UUID{}, false
	}
	if !accessible {
		writeError(w, http.StatusNotFound, "note page not found")
		return pgtype.UUID{}, false
	}
	return pageID, true
}

type notePeriodBriefActiveResponse struct {
	ID            string `json:"id"`
	Status        string `json:"status"`
	ChatSessionID string `json:"chat_session_id,omitempty"`
	SourcePageID  string `json:"source_page_id,omitempty"`
	DraftPageID   string `json:"draft_page_id"`
}

func (h *Handler) GetActiveNotePeriodBrief(w http.ResponseWriter, r *http.Request) {
	workspaceID, userID, _, ok := h.notesWorkspaceAndUser(w, r)
	if !ok {
		return
	}
	pageID, ok := h.resolvePeriodBriefSourcePage(w, r, workspaceID, userID, r.URL.Query().Get("page_id"))
	if !ok {
		return
	}
	if !pageID.Valid {
		writeError(w, http.StatusBadRequest, "page_id is required")
		return
	}
	row, err := h.loadOpenPeriodBriefRunForPage(r.Context(), workspaceID, userID, pageID)
	if err != nil {
		if err == pgx.ErrNoRows {
			writeJSON(w, http.StatusOK, map[string]any{"run": nil})
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to load period brief run")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"run": notePeriodBriefActiveResponse{
			ID:            uuidToString(row.ID),
			Status:        row.Status,
			ChatSessionID: uuidToString(row.ChatSessionID),
			SourcePageID:  uuidToString(row.SourcePageID),
			DraftPageID:   uuidToString(row.DraftPageID),
		},
	})
}

func periodBriefConfirmDecision(text string) string {
	trimmed := strings.TrimSpace(strings.ToLower(text))
	if trimmed == "" {
		return ""
	}
	compact := strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) || r == '，' || r == ',' || r == '。' || r == '!' || r == '！' {
			return -1
		}
		return r
	}, trimmed)
	switch {
	case compact == "n" || compact == "no" || strings.HasPrefix(compact, "不") ||
		strings.HasPrefix(compact, "先不") || strings.HasPrefix(compact, "不用") ||
		strings.HasPrefix(compact, "不要"):
		return "no"
	case strings.Contains(compact, "插入笔记下面") || strings.Contains(compact, "插入笔记下方") ||
		strings.Contains(compact, "插入下面") || strings.Contains(compact, "插到下面") ||
		compact == "append" || compact == "below":
		return "append"
	case compact == "y" || compact == "yes" || compact == "ok" || compact == "okay" ||
		strings.Contains(compact, "插入") || compact == "好" || compact == "好的" ||
		compact == "是" || compact == "是的" || compact == "要" || compact == "可以" || compact == "行" ||
		compact == "child":
		return "yes"
	default:
		return ""
	}
}

func (h *Handler) tryCompletePeriodBriefInsertFromChat(
	ctx context.Context,
	session db.ChatSession,
	userID, workspaceID pgtype.UUID,
	userIDString, content string,
) bool {
	var run notePeriodBriefRunRow
	err := h.DB.QueryRow(ctx, `
SELECT id, workspace_id, owner_user_id, draft_page_id, folder_page_id, synthesizer_agent_id,
       window_label, status, chat_session_id, source_page_id
FROM note_period_brief_run
WHERE chat_session_id = $1 AND workspace_id = $2 AND owner_user_id = $3
  AND status = 'awaiting_confirm'
ORDER BY created_at DESC
LIMIT 1`, session.ID, workspaceID, userID).Scan(
		&run.ID, &run.WorkspaceID, &run.OwnerUserID, &run.DraftPageID, &run.FolderPageID, &run.SynthesizerAgentID,
		&run.WindowLabel, &run.Status, &run.ChatSessionID, &run.SourcePageID,
	)
	if err != nil {
		return false
	}
	decision := periodBriefConfirmDecision(content)
	if decision == "" {
		return false
	}
	if decision == "no" {
		_, _ = h.DB.Exec(ctx, `
UPDATE note_period_brief_run SET status = 'done', updated_at = now() WHERE id = $1`, run.ID)
		h.postPeriodBriefBubbleProgress(ctx, run, userIDString, "好，那这篇先留在对话里，不插入笔记。")
		return true
	}
	mode := "child"
	if decision == "append" {
		mode = "append"
	}
	title, err := h.applyPeriodBriefInsert(ctx, run, workspaceID, userID, mode)
	if err != nil {
		if err == errPeriodBriefInsertNoPage {
			_, _ = h.DB.Exec(ctx, `
UPDATE note_period_brief_run SET status = 'done', updated_at = now() WHERE id = $1`, run.ID)
			h.postPeriodBriefBubbleProgress(ctx, run, userIDString, "没有对应的笔记页，无法插入汇报稿。")
			return true
		}
		slog.Warn("period brief insert failed", "mode", mode, "error", err)
		if mode == "append" {
			h.postPeriodBriefBubbleProgress(ctx, run, userIDString, "插入笔记下面失败，请稍后重试。")
		} else {
			h.postPeriodBriefBubbleProgress(ctx, run, userIDString, "插入子笔记失败，请稍后重试。")
		}
		return true
	}
	_, _ = h.DB.Exec(ctx, `
UPDATE note_period_brief_run SET status = 'done', updated_at = now() WHERE id = $1`, run.ID)
	if mode == "append" {
		h.postPeriodBriefBubbleProgress(ctx, run, userIDString, "已插入当前页下面。")
	} else {
		h.postPeriodBriefBubbleProgress(ctx, run, userIDString, "已插入当前页的子笔记「"+title+"」。")
	}
	return true
}

func (h *Handler) loadPeriodBriefSynthesizerWrite(ctx context.Context, run notePeriodBriefRunRow, after time.Time) string {
	if !run.FolderPageID.Valid || !run.ID.Valid {
		return ""
	}
	var afterArg any
	if !after.IsZero() {
		afterArg = after.UTC()
	}
	var content string
	err := h.DB.QueryRow(ctx, `
SELECT m.content
FROM channel_message m
WHERE m.deleted_at IS NULL
  AND m.author_type = 'agent'
  AND length(trim(m.content)) > 0
  AND EXISTS (
    SELECT 1
    FROM jsonb_array_elements(COALESCE(m.parts, '[]'::jsonb)) part
    WHERE part->>'type' = 'note_write'
      AND part->>'ref_id' = $1
  )
  AND m.created_at >= COALESCE($3::timestamptz, (SELECT created_at FROM note_period_brief_run WHERE id = $2))
ORDER BY m.created_at DESC
LIMIT 1`, uuidToString(run.FolderPageID), run.ID, afterArg).Scan(&content)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(content)
}

func (h *Handler) awaitPeriodBriefSynthesizerWrite(ctx context.Context, run notePeriodBriefRunRow, after time.Time) string {
	wait := notePeriodBriefSynthWriteMaxWait
	if !notePeriodBriefFinishInBackground {
		wait = 0
	}
	deadline := time.Now().Add(wait)
	for {
		if got := h.loadPeriodBriefSynthesizerWrite(ctx, run, after); got != "" {
			return got
		}
		if wait <= 0 || time.Now().After(deadline) {
			return ""
		}
		timer := time.NewTimer(notePeriodBriefSynthWritePollEvery)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ""
		case <-timer.C:
		}
	}
}

func (h *Handler) completePeriodBriefRunAfterSynth(ctx context.Context, run notePeriodBriefRunRow, userIDString string, writeAfter time.Time) {
	if !periodBriefRunLocksComposerStatus(run.Status) {
		return
	}
	cleared := clearCollectorPackMarkdown(run.Collectors)
	if run.ChatSessionID.Valid {
		harvested := h.awaitPeriodBriefSynthesizerWrite(ctx, run, writeAfter)
		if latest, err := h.loadNotePeriodBriefRunByID(ctx, run.WorkspaceID, run.OwnerUserID, run.ID); err != nil || !periodBriefRunLocksComposerStatus(latest.Status) {
			return
		}
		_ = h.updateNotePeriodBriefRunCollectors(ctx, run.ID, cleared, "awaiting_confirm")
		run.Status = "awaiting_confirm"
		h.postPeriodBriefResultMessage(ctx, run, userIDString, harvested, writeAfter)
		return
	}
	_ = h.updateNotePeriodBriefRunCollectors(ctx, run.ID, cleared, "done")
}

func (h *Handler) markPeriodBriefAwaitingConfirm(ctx context.Context, workspaceID, draftID pgtype.UUID, userIDString string) {
	run, err := h.loadNotePeriodBriefRunByDraft(ctx, workspaceID, draftID)
	if err != nil || !run.ChatSessionID.Valid {
		return
	}
	harvested := h.awaitPeriodBriefSynthesizerWrite(ctx, run, time.Time{})
	_, _ = h.DB.Exec(ctx, `
UPDATE note_period_brief_run SET status = 'awaiting_confirm', updated_at = now() WHERE id = $1`, run.ID)
	run.Status = "awaiting_confirm"
	h.postPeriodBriefResultMessage(ctx, run, userIDString, harvested, time.Time{})
}

func (h *Handler) postPeriodBriefPackReceived(ctx context.Context, run notePeriodBriefRunRow, collectorName, markdown string) {
	name := strings.TrimSpace(collectorName)
	if name == "" {
		name = "采集员"
	}
	h.postPeriodBriefBubbleMessage(ctx, run.ChatSessionID, run.WorkspaceID, run.OwnerUserID, "", "assistant",
		"刚刚收到了"+name+"的采集包。",
		periodBriefCollapsiblePart("", "采集包 · "+name, markdown),
	)
}

func (h *Handler) postPeriodBriefResultMessage(ctx context.Context, run notePeriodBriefRunRow, userIDString, harvested string, writeAfter time.Time) {
	title, body := h.periodBriefResultMarkdown(ctx, run, harvested, writeAfter)
	h.postPeriodBriefBubbleMessage(ctx, run.ChatSessionID, run.WorkspaceID, run.OwnerUserID, userIDString, "assistant",
		"汇报稿整理完成了。",
		periodBriefCollapsiblePart(uuidToString(run.DraftPageID), title, body),
		periodBriefInsertActionsPart(uuidToString(run.ID)),
	)
}

func (h *Handler) periodBriefResultMarkdown(ctx context.Context, run notePeriodBriefRunRow, harvested string, writeAfter time.Time) (string, string) {
	title := strings.TrimSpace("工作介绍 " + run.WindowLabel)
	if title == "工作介绍" {
		title = "工作介绍"
	}
	var draftTitle, body string
	_ = h.DB.QueryRow(ctx, `SELECT title, content FROM note_page WHERE id = $1`, run.DraftPageID).Scan(&draftTitle, &body)
	usedHarvest := strings.TrimSpace(harvested)
	if usedHarvest == "" {
		usedHarvest = h.loadPeriodBriefSynthesizerWrite(ctx, run, writeAfter)
	}
	if usedHarvest != "" {
		body = usedHarvest
	} else if label := strings.TrimSpace(draftTitle); label != "" {
		title = label
	}
	return title, strings.TrimSpace(body)
}

func appendPeriodBriefBelowNote(existing, title, body string) string {
	heading := strings.TrimSpace(title)
	if heading == "" {
		heading = "Untitled"
	}
	section := "## " + heading
	if trimmed := strings.TrimSpace(body); trimmed != "" {
		section += "\n\n" + trimmed
	}
	base := strings.TrimRight(existing, " \t\r\n")
	if base == "" {
		return section
	}
	return base + "\n\n" + section
}

func (h *Handler) applyPeriodBriefInsert(
	ctx context.Context,
	run notePeriodBriefRunRow,
	workspaceID, userID pgtype.UUID,
	mode string,
) (string, error) {
	if !run.SourcePageID.Valid {
		return "", errPeriodBriefInsertNoPage
	}
	title, body := h.periodBriefResultMarkdown(ctx, run, "", time.Time{})
	if mode == "append" {
		var current string
		if err := h.DB.QueryRow(ctx, `
SELECT content FROM note_page
WHERE id = $1 AND workspace_id = $2 AND deleted_at IS NULL`, run.SourcePageID, workspaceID).Scan(&current); err != nil {
			return "", err
		}
		next := appendPeriodBriefBelowNote(current, title, body)
		if _, err := h.DB.Exec(ctx, `
UPDATE note_page SET content = $1, updated_at = now(), updated_by = $2
WHERE id = $3 AND workspace_id = $4 AND deleted_at IS NULL`,
			next, userID, run.SourcePageID, workspaceID); err != nil {
			return "", err
		}
		return title, nil
	}
	page, err := scanNotePage(h.DB.QueryRow(ctx, `
INSERT INTO note_page (workspace_id, parent_id, owner_user_id, title, content, sort_key, created_by, updated_by)
VALUES ($1, $2, $3, $4, $5, lpad((extract(epoch from now()) * 1000000)::bigint::text, 20, '0'), $3, $3)
RETURNING id, workspace_id, parent_id, owner_user_id, title, icon, content, sort_key, created_at, updated_at, deleted_at`,
		workspaceID, run.SourcePageID, userID, normalizeNoteTitle(title), body))
	if err != nil {
		return "", err
	}
	return page.Title, nil
}

var errPeriodBriefInsertNoPage = errors.New("period brief has no source page")

type insertNotePeriodBriefRequest struct {
	Mode string `json:"mode"`
}

type insertNotePeriodBriefResponse struct {
	Mode  string `json:"mode"`
	Title string `json:"title,omitempty"`
}

// InsertNotePeriodBrief applies the finished brief onto the issuing page.
// POST /api/notes/period-briefs/{runId}/insert
func (h *Handler) InsertNotePeriodBrief(w http.ResponseWriter, r *http.Request) {
	workspaceID, userID, userIDString, ok := h.notesWorkspaceAndUser(w, r)
	if !ok {
		return
	}
	runID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "runId"), "run id")
	if !ok {
		return
	}
	var req insertNotePeriodBriefRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	mode := strings.TrimSpace(strings.ToLower(req.Mode))
	if mode != "append" && mode != "child" {
		writeError(w, http.StatusBadRequest, "mode must be append or child")
		return
	}
	run, err := h.loadNotePeriodBriefRunByID(r.Context(), workspaceID, userID, runID)
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
	title, err := h.applyPeriodBriefInsert(r.Context(), run, workspaceID, userID, mode)
	if err != nil {
		if err == errPeriodBriefInsertNoPage {
			writeError(w, http.StatusConflict, "period brief has no source page")
			return
		}
		slog.Warn("period brief insert api failed", "mode", mode, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to insert period brief")
		return
	}
	_, _ = h.DB.Exec(r.Context(), `
UPDATE note_period_brief_run SET status = 'done', updated_at = now() WHERE id = $1`, run.ID)
	if mode == "append" {
		h.postPeriodBriefBubbleProgress(r.Context(), run, userIDString, "已插入当前页下面。")
	} else {
		h.postPeriodBriefBubbleProgress(r.Context(), run, userIDString, "已插入当前页的子笔记「"+title+"」。")
	}
	writeJSON(w, http.StatusOK, insertNotePeriodBriefResponse{Mode: mode, Title: title})
}
