package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

const (
	reminderMinDelay  = time.Minute
	reminderMaxDelay  = 90 * 24 * time.Hour
	reminderActiveCap = 25
)

type agentReminder struct {
	ID                        pgtype.UUID
	WorkspaceID               pgtype.UUID
	AgentID                   pgtype.UUID
	Title                     string
	AnchorChannelID           pgtype.UUID
	AnchorMessageID           pgtype.UUID
	AnchorThreadRootMessageID pgtype.UUID
	FireAt                    pgtype.Timestamptz
	Status                    string
	FiredTaskID               pgtype.UUID
	SnoozeCount               int32
	CreatedAt                 pgtype.Timestamptz
	UpdatedAt                 pgtype.Timestamptz
	FiredAt                   pgtype.Timestamptz
}

type agentReminderResponse struct {
	ID                        string  `json:"id"`
	Title                     string  `json:"title"`
	AnchorChannelID           string  `json:"anchor_channel_id"`
	AnchorMessageID           *string `json:"anchor_message_id,omitempty"`
	AnchorThreadRootMessageID *string `json:"anchor_thread_root_message_id,omitempty"`
	FireAt                    string  `json:"fire_at"`
	Status                    string  `json:"status"`
	SnoozeCount               int32   `json:"snooze_count"`
	CreatedAt                 string  `json:"created_at"`
	UpdatedAt                 string  `json:"updated_at"`
	FiredAt                   *string `json:"fired_at,omitempty"`
}

type agentReminderScheduleRequest struct {
	Title        string `json:"title"`
	DelaySeconds *int64 `json:"delay_seconds,omitempty"`
	FireAt       string `json:"fire_at,omitempty"`
	MessageID    string `json:"message_id,omitempty"`
}

type agentReminderIDRequest struct {
	ID string `json:"id"`
}

type agentReminderSnoozeRequest struct {
	ID           string `json:"id"`
	DelaySeconds *int64 `json:"delay_seconds,omitempty"`
	FireAt       string `json:"fire_at,omitempty"`
}

type agentReminderUpdateRequest struct {
	ID           string `json:"id"`
	Title        string `json:"title,omitempty"`
	DelaySeconds *int64 `json:"delay_seconds,omitempty"`
	FireAt       string `json:"fire_at,omitempty"`
}

type agentReminderListRequest struct {
	Status string `json:"status,omitempty"`
}

func reminderResponse(r agentReminder) agentReminderResponse {
	return agentReminderResponse{
		ID:                        uuidToString(r.ID),
		Title:                     r.Title,
		AnchorChannelID:           uuidToString(r.AnchorChannelID),
		AnchorMessageID:           uuidPtr(r.AnchorMessageID),
		AnchorThreadRootMessageID: uuidPtr(r.AnchorThreadRootMessageID),
		FireAt:                    timestampToString(r.FireAt),
		Status:                    r.Status,
		SnoozeCount:               r.SnoozeCount,
		CreatedAt:                 timestampToString(r.CreatedAt),
		UpdatedAt:                 timestampToString(r.UpdatedAt),
		FiredAt:                   timestampToPtr(r.FiredAt),
	}
}

func uuidPtr(id pgtype.UUID) *string {
	if !id.Valid {
		return nil
	}
	value := uuidToString(id)
	return &value
}

func scanAgentReminder(row rowScanner) (agentReminder, error) {
	var out agentReminder
	err := row.Scan(
		&out.ID, &out.WorkspaceID, &out.AgentID, &out.Title,
		&out.AnchorChannelID, &out.AnchorMessageID, &out.AnchorThreadRootMessageID,
		&out.FireAt, &out.Status, &out.FiredTaskID, &out.SnoozeCount,
		&out.CreatedAt, &out.UpdatedAt, &out.FiredAt,
	)
	return out, err
}

func reminderSelectColumns() string {
	return `id, workspace_id, agent_id, title, anchor_channel_id, anchor_message_id,
		anchor_thread_root_message_id, fire_at, status, fired_task_id, snooze_count,
		created_at, updated_at, fired_at`
}

func parseReminderFireAt(now time.Time, delaySeconds *int64, rawFireAt string) (time.Time, error) {
	if (delaySeconds == nil) == (strings.TrimSpace(rawFireAt) == "") {
		return time.Time{}, fmt.Errorf("provide exactly one of delay_seconds or fire_at")
	}
	var fireAt time.Time
	if delaySeconds != nil {
		fireAt = now.Add(time.Duration(*delaySeconds) * time.Second)
	} else {
		parsed, err := time.Parse(time.RFC3339, strings.TrimSpace(rawFireAt))
		if err != nil {
			return time.Time{}, fmt.Errorf("fire_at must be RFC3339")
		}
		fireAt = parsed
	}
	delay := fireAt.Sub(now)
	if delay < reminderMinDelay || delay > reminderMaxDelay {
		return time.Time{}, fmt.Errorf("reminder must be between 60 seconds and 90 days in the future")
	}
	return fireAt.UTC(), nil
}

func (h *Handler) AgentTransportScheduleReminder(w http.ResponseWriter, r *http.Request) {
	task, origin, ok := h.requireAgentTransportTask(w, r)
	if !ok {
		return
	}
	var req agentReminderScheduleRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	title := strings.TrimSpace(req.Title)
	if title == "" || len([]rune(title)) > 500 {
		writeError(w, http.StatusBadRequest, "title must be between 1 and 500 characters")
		return
	}
	fireAt, err := parseReminderFireAt(time.Now().UTC(), req.DelaySeconds, req.FireAt)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	anchorMessageID, threadRootID, ok := h.resolveReminderAnchor(w, r.Context(), task.ChatSessionID, task.ID, origin, req.MessageID)
	if !ok {
		return
	}
	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to schedule reminder")
		return
	}
	defer tx.Rollback(r.Context())
	if _, err := tx.Exec(r.Context(), `SELECT pg_advisory_xact_lock(hashtext($1))`, uuidToString(task.AgentID)); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to schedule reminder")
		return
	}
	var activeCount int
	if err := tx.QueryRow(r.Context(), `
		SELECT count(*)
		FROM agent_reminder
		WHERE workspace_id = $1 AND agent_id = $2 AND status = 'scheduled'`, origin.workspaceID, task.AgentID).Scan(&activeCount); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to schedule reminder")
		return
	}
	if activeCount >= reminderActiveCap {
		writeCodedError(w, http.StatusConflict, "reminder_cap_exceeded", "maximum 25 scheduled reminders per agent")
		return
	}
	row := tx.QueryRow(r.Context(), `
		INSERT INTO agent_reminder (
			workspace_id, agent_id, title, anchor_channel_id, anchor_message_id,
			anchor_thread_root_message_id, fire_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING `+reminderSelectColumns(),
		origin.workspaceID, task.AgentID, title, origin.channelID, anchorMessageID, nullableUUID(threadRootID), fireAt)
	created, err := scanAgentReminder(row)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to schedule reminder")
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to schedule reminder")
		return
	}
	recordAgentActivityEvent(r.Context(), h.DB,
		origin.workspaceID, task.AgentID, task.RuntimeID, task.ID,
		"lifecycle", "reminder_scheduled", "info",
		reminderTargetKind(threadRootID), origin.channelID, title,
		"", "Agent scheduled a future self-wake",
		map[string]any{"reminder_id": uuidToString(created.ID), "fire_at": fireAt.Format(time.RFC3339)},
	)
	writeJSON(w, http.StatusCreated, reminderResponse(created))
}

func (h *Handler) AgentTransportListReminders(w http.ResponseWriter, r *http.Request) {
	task, origin, ok := h.requireAgentTransportTask(w, r)
	if !ok {
		return
	}
	var req agentReminderListRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	status := strings.ToLower(strings.TrimSpace(req.Status))
	if status == "" {
		status = "active"
	}
	var rows pgx.Rows
	var err error
	switch status {
	case "active":
		rows, err = h.DB.Query(r.Context(), `SELECT `+reminderSelectColumns()+` FROM agent_reminder WHERE workspace_id = $1 AND agent_id = $2 AND status IN ('scheduled', 'firing') ORDER BY fire_at ASC`, origin.workspaceID, task.AgentID)
	case "all":
		rows, err = h.DB.Query(r.Context(), `SELECT `+reminderSelectColumns()+` FROM agent_reminder WHERE workspace_id = $1 AND agent_id = $2 ORDER BY created_at DESC`, origin.workspaceID, task.AgentID)
	case "scheduled", "firing", "fired", "cancelled":
		rows, err = h.DB.Query(r.Context(), `SELECT `+reminderSelectColumns()+` FROM agent_reminder WHERE workspace_id = $1 AND agent_id = $2 AND status = $3 ORDER BY fire_at ASC`, origin.workspaceID, task.AgentID, status)
	default:
		writeError(w, http.StatusBadRequest, "status must be active, scheduled, firing, fired, cancelled, or all")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list reminders")
		return
	}
	defer rows.Close()
	out := []agentReminderResponse{}
	for rows.Next() {
		item, scanErr := scanAgentReminder(rows)
		if scanErr != nil {
			writeError(w, http.StatusInternalServerError, "failed to list reminders")
			return
		}
		out = append(out, reminderResponse(item))
	}
	writeJSON(w, http.StatusOK, map[string]any{"reminders": out})
}

func (h *Handler) AgentTransportSnoozeReminder(w http.ResponseWriter, r *http.Request) {
	task, origin, ok := h.requireAgentTransportTask(w, r)
	if !ok {
		return
	}
	var req agentReminderSnoozeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	fireAt, err := parseReminderFireAt(time.Now().UTC(), req.DelaySeconds, req.FireAt)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	id, ok := h.resolveReminderID(w, r.Context(), origin.workspaceID, task.AgentID, req.ID)
	if !ok {
		return
	}
	row := h.DB.QueryRow(r.Context(), `
		UPDATE agent_reminder
		SET fire_at = $4, status = 'scheduled', fired_at = NULL, fired_task_id = NULL,
		    snooze_count = snooze_count + 1, updated_at = now()
		WHERE id = $1 AND workspace_id = $2 AND agent_id = $3 AND status IN ('scheduled', 'fired')
		RETURNING `+reminderSelectColumns(), id, origin.workspaceID, task.AgentID, fireAt)
	updated, err := scanAgentReminder(row)
	if err != nil {
		writeError(w, http.StatusConflict, "reminder cannot be snoozed in its current state")
		return
	}
	h.recordReminderActivity(r.Context(), task, updated, "reminder_snoozed", "Agent snoozed a reminder")
	writeJSON(w, http.StatusOK, reminderResponse(updated))
}

func (h *Handler) AgentTransportUpdateReminder(w http.ResponseWriter, r *http.Request) {
	task, origin, ok := h.requireAgentTransportTask(w, r)
	if !ok {
		return
	}
	var req agentReminderUpdateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if strings.TrimSpace(req.Title) == "" && req.DelaySeconds == nil && strings.TrimSpace(req.FireAt) == "" {
		writeError(w, http.StatusBadRequest, "title or schedule update is required")
		return
	}
	id, ok := h.resolveReminderID(w, r.Context(), origin.workspaceID, task.AgentID, req.ID)
	if !ok {
		return
	}
	var fireAt any
	if req.DelaySeconds != nil || strings.TrimSpace(req.FireAt) != "" {
		parsed, err := parseReminderFireAt(time.Now().UTC(), req.DelaySeconds, req.FireAt)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		fireAt = parsed
	}
	title := strings.TrimSpace(req.Title)
	if len([]rune(title)) > 500 {
		writeError(w, http.StatusBadRequest, "title must be at most 500 characters")
		return
	}
	row := h.DB.QueryRow(r.Context(), `
		UPDATE agent_reminder
		SET title = CASE WHEN $4 = '' THEN title ELSE $4 END,
		    fire_at = COALESCE($5::timestamptz, fire_at),
		    updated_at = now()
		WHERE id = $1 AND workspace_id = $2 AND agent_id = $3 AND status = 'scheduled'
		RETURNING `+reminderSelectColumns(), id, origin.workspaceID, task.AgentID, title, fireAt)
	updated, err := scanAgentReminder(row)
	if err != nil {
		writeError(w, http.StatusConflict, "reminder cannot be updated in its current state")
		return
	}
	h.recordReminderActivity(r.Context(), task, updated, "reminder_updated", "Agent updated a reminder")
	writeJSON(w, http.StatusOK, reminderResponse(updated))
}

func (h *Handler) AgentTransportCancelReminder(w http.ResponseWriter, r *http.Request) {
	task, origin, ok := h.requireAgentTransportTask(w, r)
	if !ok {
		return
	}
	var req agentReminderIDRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	id, ok := h.resolveReminderID(w, r.Context(), origin.workspaceID, task.AgentID, req.ID)
	if !ok {
		return
	}
	row := h.DB.QueryRow(r.Context(), `
		UPDATE agent_reminder
		SET status = 'cancelled', updated_at = now()
		WHERE id = $1 AND workspace_id = $2 AND agent_id = $3 AND status = 'scheduled'
		RETURNING `+reminderSelectColumns(), id, origin.workspaceID, task.AgentID)
	cancelled, err := scanAgentReminder(row)
	if err != nil {
		writeError(w, http.StatusConflict, "reminder cannot be cancelled in its current state")
		return
	}
	h.recordReminderActivity(r.Context(), task, cancelled, "reminder_cancelled", "Agent cancelled a reminder")
	writeJSON(w, http.StatusOK, reminderResponse(cancelled))
}

func (h *Handler) resolveReminderAnchor(w http.ResponseWriter, ctx context.Context, chatSessionID, taskID pgtype.UUID, origin chatOutputOrigin, rawMessageID string) (pgtype.UUID, pgtype.UUID, bool) {
	var messageID pgtype.UUID
	if strings.TrimSpace(rawMessageID) != "" {
		parsed, ok := parseUUIDOrBadRequest(w, strings.TrimSpace(rawMessageID), "message_id")
		if !ok {
			return pgtype.UUID{}, pgtype.UUID{}, false
		}
		messageID = parsed
	} else {
		messageID = h.channelTriggerMessageFromPrompt(ctx, chatSessionID, taskID)
	}
	if !messageID.Valid {
		writeError(w, http.StatusBadRequest, "message_id is required when the current task has no channel anchor")
		return pgtype.UUID{}, pgtype.UUID{}, false
	}
	var threadRootID pgtype.UUID
	var exists bool
	if err := h.DB.QueryRow(ctx, `
		SELECT true, thread_root_message_id
		FROM channel_message
		WHERE id = $1 AND channel_id = $2 AND workspace_id = $3 AND deleted_at IS NULL`,
		messageID, origin.channelID, origin.workspaceID).Scan(&exists, &threadRootID); err != nil || !exists {
		writeError(w, http.StatusNotFound, "anchor message not found")
		return pgtype.UUID{}, pgtype.UUID{}, false
	}
	return messageID, threadRootID, true
}

func (h *Handler) channelTriggerMessageFromPrompt(ctx context.Context, chatSessionID, taskID pgtype.UUID) pgtype.UUID {
	if !taskID.Valid {
		return pgtype.UUID{}
	}
	var content string
	if err := h.DB.QueryRow(ctx, `
		SELECT content FROM chat_message
		WHERE chat_session_id = $1 AND task_id = $2 AND role = 'user'
		ORDER BY created_at DESC
		LIMIT 1`, chatSessionID, taskID).Scan(&content); err != nil {
		return pgtype.UUID{}
	}
	for _, prefix := range []string{"Current message id: ", "Reaction target message id: "} {
		for _, line := range strings.Split(content, "\n") {
			if target, ok := strings.CutPrefix(strings.TrimSpace(line), prefix); ok {
				if parsed, err := parseUUIDString(strings.TrimSpace(target)); err == nil {
					return parsed
				}
			}
		}
	}
	return pgtype.UUID{}
}

func (h *Handler) resolveReminderID(w http.ResponseWriter, ctx context.Context, workspaceID, agentID pgtype.UUID, raw string) (pgtype.UUID, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		writeError(w, http.StatusBadRequest, "id is required")
		return pgtype.UUID{}, false
	}
	if parsed, err := parseUUIDString(raw); err == nil && parsed.Valid {
		var found pgtype.UUID
		if err := h.DB.QueryRow(ctx, `SELECT id FROM agent_reminder WHERE id = $1 AND workspace_id = $2 AND agent_id = $3`, parsed, workspaceID, agentID).Scan(&found); err != nil {
			writeError(w, http.StatusNotFound, "reminder not found")
			return pgtype.UUID{}, false
		}
		return found, true
	}
	if len(raw) < 8 {
		writeError(w, http.StatusBadRequest, "id prefix must be at least 8 characters")
		return pgtype.UUID{}, false
	}
	rows, err := h.DB.Query(ctx, `SELECT id FROM agent_reminder WHERE workspace_id = $1 AND agent_id = $2 AND id::text LIKE $3 ORDER BY created_at DESC LIMIT 2`, workspaceID, agentID, raw+"%")
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to resolve reminder")
		return pgtype.UUID{}, false
	}
	defer rows.Close()
	var ids []pgtype.UUID
	for rows.Next() {
		var id pgtype.UUID
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		writeError(w, http.StatusNotFound, "reminder not found")
		return pgtype.UUID{}, false
	}
	if len(ids) > 1 {
		writeCodedError(w, http.StatusConflict, "reminder_id_ambiguous", "reminder id prefix is ambiguous")
		return pgtype.UUID{}, false
	}
	return ids[0], true
}

func (h *Handler) recordReminderActivity(ctx context.Context, task db.AgentTaskQueue, reminder agentReminder, eventType, message string) {
	recordAgentActivityEvent(ctx, h.DB,
		reminder.WorkspaceID, reminder.AgentID, task.RuntimeID, task.ID,
		"lifecycle", eventType, "info",
		reminderTargetKind(reminder.AnchorThreadRootMessageID), reminder.AnchorChannelID, reminder.Title,
		"", message,
		map[string]any{"reminder_id": uuidToString(reminder.ID), "fire_at": timestampToString(reminder.FireAt)},
	)
}

func reminderTargetKind(threadRootID pgtype.UUID) string {
	if threadRootID.Valid {
		return "thread"
	}
	return "channel"
}

func (h *Handler) FireDueReminders(ctx context.Context) error {
	rows, err := h.DB.Query(ctx, `
		UPDATE agent_reminder
		SET status = 'firing', updated_at = now()
		WHERE id IN (
			SELECT id FROM agent_reminder
			WHERE status = 'scheduled' AND fire_at <= now()
			ORDER BY fire_at ASC
			FOR UPDATE SKIP LOCKED
			LIMIT 100
		)
		RETURNING `+reminderSelectColumns())
	if err != nil {
		return err
	}
	var due []agentReminder
	for rows.Next() {
		item, scanErr := scanAgentReminder(rows)
		if scanErr != nil {
			rows.Close()
			return scanErr
		}
		due = append(due, item)
	}
	rows.Close()
	for _, reminder := range due {
		if err := h.fireReminder(ctx, reminder); err != nil {
			slog.Warn("reminder fire failed", "reminder_id", uuidToString(reminder.ID), "error", err)
			_, _ = h.DB.Exec(ctx, `UPDATE agent_reminder SET status = 'scheduled', updated_at = now() WHERE id = $1 AND status = 'firing'`, reminder.ID)
		}
	}
	return nil
}

func (h *Handler) fireReminder(ctx context.Context, reminder agentReminder) error {
	ch, found := h.getChannel(ctx, uuidToString(reminder.WorkspaceID), reminder.AnchorChannelID)
	if !found || ch.ArchivedAt != nil {
		_, _ = h.DB.Exec(ctx, `UPDATE agent_reminder SET status = 'cancelled', updated_at = now() WHERE id = $1`, reminder.ID)
		return nil
	}
	agent, err := h.Queries.GetAgentInWorkspace(ctx, db.GetAgentInWorkspaceParams{ID: reminder.AgentID, WorkspaceID: reminder.WorkspaceID})
	if err != nil || agent.ArchivedAt.Valid {
		_, _ = h.DB.Exec(ctx, `UPDATE agent_reminder SET status = 'cancelled', updated_at = now() WHERE id = $1`, reminder.ID)
		return nil
	}
	var anchorExcerpt string
	if reminder.AnchorMessageID.Valid {
		_ = h.DB.QueryRow(ctx, `SELECT content FROM channel_message WHERE id = $1 AND channel_id = $2`, reminder.AnchorMessageID, reminder.AnchorChannelID).Scan(&anchorExcerpt)
	}
	trigger := ChannelMessageResponse{
		ID:           uuidToString(reminder.AnchorMessageID),
		AuthorName:   "Reminder",
		Type:         "system",
		Content:      reminder.Title,
		TriggerDepth: 1,
	}
	if reminder.AnchorThreadRootMessageID.Valid {
		root := uuidToString(reminder.AnchorThreadRootMessageID)
		trigger.ThreadRootMessageID = &root
		var threadID string
		if h.DB.QueryRow(ctx, `SELECT thread_id FROM channel_message WHERE id = $1`, reminder.AnchorThreadRootMessageID).Scan(&threadID) == nil && strings.TrimSpace(threadID) != "" {
			trigger.ThreadID = &threadID
		}
	}
	creatorID := agent.OwnerID
	session, err := h.ensureChannelAgentSession(ctx, ch, reminder.AgentID, creatorID)
	if err != nil {
		return err
	}
	prompt := buildReminderPrompt(ch, reminder, anchorExcerpt)
	promptMsg, err := h.createChannelAgentPromptMessage(ctx, session.ID, prompt, ch.Kind, trigger)
	if err != nil {
		return err
	}
	task, err := h.TaskService.EnqueueFreshChatTask(ctx, session, creatorID)
	if err != nil {
		return err
	}
	if _, err := h.DB.Exec(ctx, `UPDATE agent_reminder SET status = 'fired', fired_at = now(), fired_task_id = $2, updated_at = now() WHERE id = $1 AND status = 'firing'`, reminder.ID, task.ID); err != nil {
		return err
	}
	if _, err := h.DB.Exec(ctx, `UPDATE chat_message SET task_id = $1 WHERE id = $2`, task.ID, promptMsg.ID); err != nil {
		// The task is already durably queued, so never re-arm the reminder and risk a duplicate wake.
		slog.Warn("reminder prompt task tag failed", "reminder_id", uuidToString(reminder.ID), "task_id", uuidToString(task.ID), "error", err)
	}
	recordAgentActivityEvent(ctx, h.DB,
		reminder.WorkspaceID, reminder.AgentID, agent.RuntimeID, task.ID,
		"lifecycle", "reminder_fired", "info",
		reminderTargetKind(reminder.AnchorThreadRootMessageID), reminder.AnchorChannelID, reminder.Title,
		"", "Reminder fired and woke the agent",
		map[string]any{"reminder_id": uuidToString(reminder.ID)},
	)
	return nil
}

func buildReminderPrompt(ch ChannelResponse, reminder agentReminder, anchorExcerpt string) string {
	var b strings.Builder
	b.WriteString("A self-scheduled reminder is due. This is a directed wake that you previously requested.\n")
	fmt.Fprintf(&b, "Reminder id: %s\n", uuidToString(reminder.ID))
	fmt.Fprintf(&b, "Reminder title: %s\n", reminder.Title)
	fmt.Fprintf(&b, "Anchored surface: #%s\n", ch.Name)
	if reminder.AnchorMessageID.Valid {
		fmt.Fprintf(&b, "Current message id: %s\n", uuidToString(reminder.AnchorMessageID))
	}
	if strings.TrimSpace(anchorExcerpt) != "" {
		fmt.Fprintf(&b, "Anchor message excerpt: %s\n", truncateForActivity(anchorExcerpt, 500))
	}
	b.WriteString("Check the current state now. Reply in the anchored channel/thread only if there is a useful update, decision, follow-up question, or conclusion. If nothing changed, you may reschedule or finish without noise.\n")
	b.WriteString(channelOutputContractInstruction)
	b.WriteString("\n")
	b.WriteString(channelContinuationInstruction)
	return b.String()
}

func (h *Handler) RecoverStuckFiringReminders(ctx context.Context) error {
	_, err := h.DB.Exec(ctx, `
		UPDATE agent_reminder
		SET status = 'scheduled', updated_at = now()
		WHERE status = 'firing' AND fired_task_id IS NULL AND updated_at < now() - interval '5 minutes'`)
	return err
}
