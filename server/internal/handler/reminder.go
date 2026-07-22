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
	"github.com/multica-ai/multica/server/pkg/protocol"
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
	InitiatorUserID           pgtype.UUID
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
	Cadence                   pgtype.Text
	ScheduleTimezone          pgtype.Text
	CadenceNextAt             pgtype.Timestamptz
	CurrentOccurrenceID       pgtype.UUID
	TerminalReason            pgtype.Text
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
	Cadence                   *string `json:"cadence,omitempty"`
	ScheduleTimezone          *string `json:"schedule_timezone,omitempty"`
	CadenceNextAt             *string `json:"cadence_next_at,omitempty"`
	CurrentOccurrenceID       *string `json:"current_occurrence_id,omitempty"`
	TerminalReason            *string `json:"terminal_reason,omitempty"`
}

type agentReminderScheduleRequest struct {
	Title        string `json:"title"`
	DelaySeconds *int64 `json:"delay_seconds,omitempty"`
	FireAt       string `json:"fire_at,omitempty"`
	Repeat       string `json:"repeat,omitempty"`
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
	Cadence      string `json:"cadence,omitempty"`
}

type agentReminderListRequest struct {
	Status string `json:"status,omitempty"`
}

type agentReminderLifecycleResponse struct {
	ID               string          `json:"id"`
	OccurrenceID     *string         `json:"occurrence_id,omitempty"`
	EventType        string          `json:"event_type"`
	ActorType        string          `json:"actor_type"`
	ActorID          *string         `json:"actor_id,omitempty"`
	PreviousFireAt   *string         `json:"previous_fire_at,omitempty"`
	NextFireAt       *string         `json:"next_fire_at,omitempty"`
	Title            string          `json:"title"`
	Cadence          *string         `json:"cadence,omitempty"`
	ScheduleTimezone *string         `json:"schedule_timezone,omitempty"`
	ResultingState   string          `json:"resulting_state"`
	ReasonCode       *string         `json:"reason_code,omitempty"`
	Details          json.RawMessage `json:"details"`
	CreatedAt        string          `json:"created_at"`
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
		Cadence:                   nullableTextPtr(r.Cadence),
		ScheduleTimezone:          reminderTimezonePtr(r.Cadence, r.ScheduleTimezone),
		CadenceNextAt:             timestampToPtr(r.CadenceNextAt),
		CurrentOccurrenceID:       uuidPtr(r.CurrentOccurrenceID),
		TerminalReason:            nullableTextPtr(r.TerminalReason),
	}
}

func nullableTextPtr(value pgtype.Text) *string {
	if !value.Valid {
		return nil
	}
	return &value.String
}

func nullableText(value pgtype.Text) any {
	if !value.Valid {
		return nil
	}
	return value.String
}

func reminderTimezonePtr(cadence, timezone pgtype.Text) *string {
	if !cadence.Valid || (!strings.HasPrefix(cadence.String, "daily@") && !strings.HasPrefix(cadence.String, "weekly:")) {
		return nil
	}
	return nullableTextPtr(timezone)
}

func reminderTimezoneValue(cadence, timezone pgtype.Text) any {
	value := reminderTimezonePtr(cadence, timezone)
	if value == nil {
		return nil
	}
	return *value
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
		&out.ID, &out.WorkspaceID, &out.AgentID, &out.InitiatorUserID, &out.Title,
		&out.AnchorChannelID, &out.AnchorMessageID, &out.AnchorThreadRootMessageID,
		&out.FireAt, &out.Status, &out.FiredTaskID, &out.SnoozeCount,
		&out.CreatedAt, &out.UpdatedAt, &out.FiredAt, &out.Cadence,
		&out.ScheduleTimezone, &out.CadenceNextAt, &out.CurrentOccurrenceID,
		&out.TerminalReason,
	)
	return out, err
}

func reminderSelectColumns() string {
	return `id, workspace_id, agent_id, initiator_user_id, title, anchor_channel_id, anchor_message_id,
		anchor_thread_root_message_id, fire_at, status, fired_task_id, snooze_count,
		created_at, updated_at, fired_at, cadence, schedule_timezone, cadence_next_at,
		current_occurrence_id, terminal_reason`
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
	timezone := reminderInitiatorTimezone(r.Context(), h.DB, task.InitiatorUserID)
	schedule, err := parseReminderSchedule(time.Now().UTC(), req.DelaySeconds, req.FireAt, req.Repeat, timezone)
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
		WHERE workspace_id = $1 AND agent_id = $2 AND status IN ('scheduled', 'firing')`, origin.workspaceID, task.AgentID).Scan(&activeCount); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to schedule reminder")
		return
	}
	if activeCount >= reminderActiveCap {
		writeCodedError(w, http.StatusConflict, "reminder_cap_exceeded", "maximum 25 scheduled reminders per agent")
		return
	}
	var cadence any
	var scheduleTimezone any
	var cadenceNextAt any
	if schedule.Cadence != nil {
		cadence = schedule.Cadence.Canonical
		cadenceNextAt = schedule.FireAt
		if schedule.Timezone != "" {
			scheduleTimezone = schedule.Timezone
		}
	}
	row := tx.QueryRow(r.Context(), `
		INSERT INTO agent_reminder (
			workspace_id, agent_id, initiator_user_id, title, anchor_channel_id, anchor_message_id,
			anchor_thread_root_message_id, fire_at, cadence, schedule_timezone, cadence_next_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
		RETURNING `+reminderSelectColumns(),
		origin.workspaceID, task.AgentID, nullableUUID(task.InitiatorUserID), title, origin.channelID,
		anchorMessageID, nullableUUID(threadRootID), schedule.FireAt, cadence, scheduleTimezone, cadenceNextAt)
	created, err := scanAgentReminder(row)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to schedule reminder")
		return
	}
	if _, err := tx.Exec(r.Context(), `
		INSERT INTO agent_reminder_lifecycle_event (
			reminder_id, workspace_id, agent_id, event_type, actor_type, actor_id,
			next_fire_at, title_snapshot, cadence_snapshot, timezone_snapshot, resulting_state
		) VALUES ($1, $2, $3, 'scheduled', 'agent', $3, $4, $5, $6, $7, 'scheduled')`,
		created.ID, created.WorkspaceID, created.AgentID, created.FireAt, created.Title,
		nullableText(created.Cadence), reminderTimezoneValue(created.Cadence, created.ScheduleTimezone)); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to schedule reminder")
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to schedule reminder")
		return
	}
	h.publishAgentReminderChanged(r.Context(), created.WorkspaceID, created.AgentID)
	h.recordAgentActivityEvent(r.Context(), h.DB,
		origin.workspaceID, task.AgentID, task.RuntimeID, task.ID,
		activityKindCustom, "reminder_scheduled", "info",
		reminderTargetKind(threadRootID), origin.channelID, title,
		"", "Agent scheduled a future self-wake",
		map[string]any{"reminder_id": uuidToString(created.ID), "fire_at": schedule.FireAt.Format(time.RFC3339)},
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
	if err := rows.Err(); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list reminders")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"reminders": out})
}

func (h *Handler) AgentTransportReminderLog(w http.ResponseWriter, r *http.Request) {
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
	rows, err := h.DB.Query(r.Context(), `
		SELECT id, occurrence_id, event_type, actor_type, actor_id,
		       previous_fire_at, next_fire_at, title_snapshot, cadence_snapshot,
		       timezone_snapshot, resulting_state, reason_code, details, created_at
		FROM agent_reminder_lifecycle_event
		WHERE reminder_id = $1 AND workspace_id = $2 AND agent_id = $3
		ORDER BY created_at ASC, id ASC`, id, origin.workspaceID, task.AgentID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load reminder log")
		return
	}
	defer rows.Close()
	items := []agentReminderLifecycleResponse{}
	for rows.Next() {
		var eventID, occurrenceID, actorID pgtype.UUID
		var eventType, actorType, title, state string
		var previousFireAt, nextFireAt, createdAt pgtype.Timestamptz
		var cadence, timezone, reason pgtype.Text
		var details []byte
		if err := rows.Scan(&eventID, &occurrenceID, &eventType, &actorType, &actorID,
			&previousFireAt, &nextFireAt, &title, &cadence, &timezone, &state,
			&reason, &details, &createdAt); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to load reminder log")
			return
		}
		items = append(items, agentReminderLifecycleResponse{
			ID: uuidToString(eventID), OccurrenceID: uuidPtr(occurrenceID), EventType: eventType,
			ActorType: actorType, ActorID: uuidPtr(actorID), PreviousFireAt: timestampToPtr(previousFireAt),
			NextFireAt: timestampToPtr(nextFireAt), Title: title, Cadence: nullableTextPtr(cadence),
			ScheduleTimezone: nullableTextPtr(timezone), ResultingState: state,
			ReasonCode: nullableTextPtr(reason), Details: json.RawMessage(details), CreatedAt: timestampToString(createdAt),
		})
	}
	if err := rows.Err(); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load reminder log")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"reminder_id": uuidToString(id), "events": items})
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
	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to snooze reminder")
		return
	}
	defer tx.Rollback(r.Context())
	previous, err := scanAgentReminder(tx.QueryRow(r.Context(), `SELECT `+reminderSelectColumns()+` FROM agent_reminder WHERE id = $1 AND workspace_id = $2 AND agent_id = $3 FOR UPDATE`, id, origin.workspaceID, task.AgentID))
	if err != nil || (previous.Status != "scheduled" && previous.Status != "fired") {
		writeError(w, http.StatusConflict, "reminder cannot be snoozed in its current state")
		return
	}
	row := tx.QueryRow(r.Context(), `
		UPDATE agent_reminder
		SET fire_at = $4, status = 'scheduled', fired_at = NULL, fired_task_id = NULL,
		    current_occurrence_id = NULL, terminal_reason = NULL,
		    snooze_count = snooze_count + 1, updated_at = now()
		WHERE id = $1 AND workspace_id = $2 AND agent_id = $3 AND status IN ('scheduled', 'fired')
		RETURNING `+reminderSelectColumns(), id, origin.workspaceID, task.AgentID, fireAt)
	updated, err := scanAgentReminder(row)
	if err != nil {
		writeError(w, http.StatusConflict, "reminder cannot be snoozed in its current state")
		return
	}
	if _, err := tx.Exec(r.Context(), `
		INSERT INTO agent_reminder_lifecycle_event (
			reminder_id, workspace_id, agent_id, event_type, actor_type, actor_id,
			previous_fire_at, next_fire_at, title_snapshot, cadence_snapshot,
			timezone_snapshot, resulting_state
		) VALUES ($1, $2, $3, 'snoozed', 'agent', $3, $4, $5, $6, $7, $8, 'scheduled')`,
		updated.ID, updated.WorkspaceID, updated.AgentID, previous.FireAt, updated.FireAt,
		updated.Title, nullableText(updated.Cadence), reminderTimezoneValue(updated.Cadence, updated.ScheduleTimezone)); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to snooze reminder")
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to snooze reminder")
		return
	}
	h.publishAgentReminderChanged(r.Context(), updated.WorkspaceID, updated.AgentID)
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
	if strings.TrimSpace(req.Title) == "" && req.DelaySeconds == nil && strings.TrimSpace(req.FireAt) == "" && strings.TrimSpace(req.Cadence) == "" {
		writeError(w, http.StatusBadRequest, "title or schedule update is required")
		return
	}
	if strings.TrimSpace(req.Cadence) != "" && (req.DelaySeconds != nil || strings.TrimSpace(req.FireAt) != "") {
		writeError(w, http.StatusBadRequest, "cadence cannot be combined with delay_seconds or fire_at")
		return
	}
	id, ok := h.resolveReminderID(w, r.Context(), origin.workspaceID, task.AgentID, req.ID)
	if !ok {
		return
	}
	title := strings.TrimSpace(req.Title)
	if len([]rune(title)) > 500 {
		writeError(w, http.StatusBadRequest, "title must be at most 500 characters")
		return
	}
	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update reminder")
		return
	}
	defer tx.Rollback(r.Context())
	previous, err := scanAgentReminder(tx.QueryRow(r.Context(), `SELECT `+reminderSelectColumns()+` FROM agent_reminder WHERE id = $1 AND workspace_id = $2 AND agent_id = $3 FOR UPDATE`, id, origin.workspaceID, task.AgentID))
	if err != nil || previous.Status != "scheduled" {
		writeError(w, http.StatusConflict, "reminder cannot be updated in its current state")
		return
	}

	nextTitle := previous.Title
	if title != "" {
		nextTitle = title
	}
	nextFireAt := previous.FireAt.Time
	var nextCadence any = nullableText(previous.Cadence)
	var nextTimezone any = nullableText(previous.ScheduleTimezone)
	var nextCadenceAt any
	if previous.CadenceNextAt.Valid {
		nextCadenceAt = previous.CadenceNextAt.Time
	}
	now := time.Now().UTC()
	if strings.TrimSpace(req.Cadence) != "" {
		rawCadence := strings.ToLower(strings.TrimSpace(req.Cadence))
		calendarRule := strings.HasPrefix(rawCadence, "daily@") || strings.HasPrefix(rawCadence, "weekly:")
		timezone := ""
		if calendarRule {
			if previous.ScheduleTimezone.Valid {
				timezone = previous.ScheduleTimezone.String
			} else {
				timezone = reminderInitiatorTimezone(r.Context(), tx, task.InitiatorUserID)
			}
		}
		cadence, parseErr := parseReminderCadence(rawCadence, timezone)
		if parseErr != nil {
			writeError(w, http.StatusBadRequest, parseErr.Error())
			return
		}
		next, nextErr := nextReminderCadence(cadence, now)
		if nextErr != nil {
			writeError(w, http.StatusBadRequest, nextErr.Error())
			return
		}
		nextFireAt = next.UTC()
		nextCadenceAt = nextFireAt
		nextCadence = cadence.Canonical
		if calendarRule {
			nextTimezone = cadence.Location.String()
		}
	} else if req.DelaySeconds != nil || strings.TrimSpace(req.FireAt) != "" {
		parsed, parseErr := parseReminderFireAt(now, req.DelaySeconds, req.FireAt)
		if parseErr != nil {
			writeError(w, http.StatusBadRequest, parseErr.Error())
			return
		}
		nextFireAt = parsed
	}

	row := tx.QueryRow(r.Context(), `
		UPDATE agent_reminder
		SET title = $4, fire_at = $5, cadence = $6, schedule_timezone = $7,
		    cadence_next_at = $8,
		    updated_at = now()
		WHERE id = $1 AND workspace_id = $2 AND agent_id = $3 AND status = 'scheduled'
		RETURNING `+reminderSelectColumns(), id, origin.workspaceID, task.AgentID,
		nextTitle, nextFireAt, nextCadence, nextTimezone, nextCadenceAt)
	updated, err := scanAgentReminder(row)
	if err != nil {
		writeError(w, http.StatusConflict, "reminder cannot be updated in its current state")
		return
	}
	if _, err := tx.Exec(r.Context(), `
		INSERT INTO agent_reminder_lifecycle_event (
			reminder_id, workspace_id, agent_id, event_type, actor_type, actor_id,
			previous_fire_at, next_fire_at, title_snapshot, cadence_snapshot,
			timezone_snapshot, resulting_state
		) VALUES ($1, $2, $3, 'updated', 'agent', $3, $4, $5, $6, $7, $8, 'scheduled')`,
		updated.ID, updated.WorkspaceID, updated.AgentID, previous.FireAt, updated.FireAt,
		updated.Title, nullableText(updated.Cadence), reminderTimezoneValue(updated.Cadence, updated.ScheduleTimezone)); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update reminder")
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update reminder")
		return
	}
	h.publishAgentReminderChanged(r.Context(), updated.WorkspaceID, updated.AgentID)
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
	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to cancel reminder")
		return
	}
	defer tx.Rollback(r.Context())
	row := tx.QueryRow(r.Context(), `
		UPDATE agent_reminder
		SET status = 'cancelled', terminal_reason = 'cancelled_by_author', current_occurrence_id = NULL, updated_at = now()
		WHERE id = $1 AND workspace_id = $2 AND agent_id = $3 AND status = 'scheduled'
		RETURNING `+reminderSelectColumns(), id, origin.workspaceID, task.AgentID)
	cancelled, err := scanAgentReminder(row)
	if err != nil {
		writeError(w, http.StatusConflict, "reminder cannot be cancelled in its current state")
		return
	}
	if _, err := tx.Exec(r.Context(), `
		INSERT INTO agent_reminder_lifecycle_event (
			reminder_id, workspace_id, agent_id, event_type, actor_type, actor_id,
			previous_fire_at, title_snapshot, cadence_snapshot, timezone_snapshot,
			resulting_state, reason_code
		) VALUES ($1, $2, $3, 'cancelled', 'agent', $3, $4, $5, $6, $7, 'cancelled', 'cancelled_by_author')`,
		cancelled.ID, cancelled.WorkspaceID, cancelled.AgentID, cancelled.FireAt,
		cancelled.Title, nullableText(cancelled.Cadence), reminderTimezoneValue(cancelled.Cadence, cancelled.ScheduleTimezone)); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to cancel reminder")
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to cancel reminder")
		return
	}
	h.publishAgentReminderChanged(r.Context(), cancelled.WorkspaceID, cancelled.AgentID)
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
	h.recordAgentActivityEvent(ctx, h.DB,
		reminder.WorkspaceID, reminder.AgentID, task.RuntimeID, task.ID,
		activityKindCustom, eventType, "info",
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

type agentReminderChangedPayload struct {
	AgentID string `json:"agent_id"`
}

// publishAgentReminderChanged emits only the invalidation key needed by human
// clients. Private-agent events use the same visibility boundary as the human
// read endpoint rather than leaking reminder or anchor metadata workspace-wide.
func (h *Handler) publishAgentReminderChanged(ctx context.Context, workspaceID, agentID pgtype.UUID) {
	if h == nil || h.Bus == nil || !workspaceID.Valid || !agentID.Valid {
		return
	}
	payload := agentReminderChangedPayload{AgentID: uuidToString(agentID)}
	var visibility string
	var ownerID pgtype.UUID
	var displayName string
	if err := h.DB.QueryRow(ctx, `
		SELECT visibility, owner_id, display_name
		FROM agent
		WHERE id = $1 AND workspace_id = $2`, agentID, workspaceID).Scan(&visibility, &ownerID, &displayName); err != nil {
		return
	}
	workspace := uuidToString(workspaceID)
	if visibility != "private" && !isWindyAgentName(displayName) {
		h.publish(protocol.EventAgentReminderChanged, workspace, "system", "", payload)
		return
	}

	recipients := map[string]bool{uuidToString(ownerID): ownerID.Valid}
	if !isWindyAgentName(displayName) {
		rows, err := h.DB.Query(ctx, `
			SELECT user_id
			FROM member
			WHERE workspace_id = $1 AND role IN ('owner', 'admin')`, workspaceID)
		if err == nil {
			for rows.Next() {
				var id pgtype.UUID
				if rows.Scan(&id) == nil && id.Valid {
					recipients[uuidToString(id)] = true
				}
			}
			rows.Close()
		}
	}
	h.publishToUsers(protocol.EventAgentReminderChanged, workspace, "system", "", recipientUserIDsFromSet(recipients), payload)
}

func (h *Handler) FireDueReminders(ctx context.Context) error {
	tx, err := h.TxStarter.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	rows, err := tx.Query(ctx, `
		SELECT `+reminderSelectColumns()+`
		FROM agent_reminder
		WHERE status = 'scheduled' AND fire_at <= now()
		ORDER BY fire_at ASC
		FOR UPDATE SKIP LOCKED
		LIMIT 100`)
	if err != nil {
		return err
	}
	type claimedReminder struct {
		reminderID   pgtype.UUID
		occurrenceID pgtype.UUID
	}
	var due []agentReminder
	var claimed []claimedReminder
	for rows.Next() {
		reminder, scanErr := scanAgentReminder(rows)
		if scanErr != nil {
			rows.Close()
			return scanErr
		}
		due = append(due, reminder)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	for _, reminder := range due {
		cadenceSlot := reminder.FireAt
		if reminder.CadenceNextAt.Valid {
			cadenceSlot = reminder.CadenceNextAt
		}
		var occurrenceID pgtype.UUID
		if err := tx.QueryRow(ctx, `
			INSERT INTO agent_reminder_occurrence (
				reminder_id, workspace_id, agent_id, cadence_scheduled_for, due_at,
				status, title_snapshot, cadence_snapshot, timezone_snapshot, claimed_at
			) VALUES ($1, $2, $3, $4, $5, 'claimed', $6, $7, $8, now())
			ON CONFLICT (reminder_id, cadence_scheduled_for) DO UPDATE
			SET claimed_at = CASE
				WHEN agent_reminder_occurrence.status IN ('pending', 'claimed')
				THEN now() ELSE agent_reminder_occurrence.claimed_at END,
				updated_at = now()
			RETURNING id`, reminder.ID, reminder.WorkspaceID, reminder.AgentID,
			cadenceSlot, reminder.FireAt, reminder.Title, nullableText(reminder.Cadence),
			reminderTimezoneValue(reminder.Cadence, reminder.ScheduleTimezone)).Scan(&occurrenceID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			UPDATE agent_reminder
			SET status = 'firing', current_occurrence_id = $2, updated_at = now()
			WHERE id = $1 AND status = 'scheduled'`, reminder.ID, occurrenceID); err != nil {
			return err
		}
		claimed = append(claimed, claimedReminder{reminderID: reminder.ID, occurrenceID: occurrenceID})
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	for _, item := range claimed {
		if err := h.fireReminderOccurrence(ctx, item.reminderID, item.occurrenceID); err != nil {
			slog.Warn("reminder occurrence fire failed", "reminder_id", uuidToString(item.reminderID), "occurrence_id", uuidToString(item.occurrenceID), "error", err)
		}
	}
	return nil
}

func (h *Handler) fireReminderOccurrence(ctx context.Context, reminderID, occurrenceID pgtype.UUID) error {
	tx, err := h.TxStarter.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	reminder, err := scanAgentReminder(tx.QueryRow(ctx, `SELECT `+reminderSelectColumns()+` FROM agent_reminder WHERE id = $1 FOR UPDATE`, reminderID))
	if err != nil {
		return err
	}
	var occurrenceStatus string
	var cadenceSlot pgtype.Timestamptz
	var existingTaskID pgtype.UUID
	if err := tx.QueryRow(ctx, `
		SELECT status, cadence_scheduled_for, fired_task_id
		FROM agent_reminder_occurrence
		WHERE id = $1 AND reminder_id = $2
		FOR UPDATE`, occurrenceID, reminderID).Scan(&occurrenceStatus, &cadenceSlot, &existingTaskID); err != nil {
		return err
	}
	if occurrenceStatus == "fired" || occurrenceStatus == "cancelled" {
		return tx.Commit(ctx)
	}
	// A durable task is the wake source of truth. This state can exist for a
	// legacy V1 firing row or a deliberately injected recovery fixture. Never
	// create a second task; finish the occurrence/definition ledger around the
	// already queued wake instead.
	if existingTaskID.Valid {
		return h.finalizeReminderOccurrenceWithExistingTask(ctx, tx, reminder, occurrenceID, cadenceSlot, existingTaskID)
	}

	var channelName, channelKind string
	var channelArchivedAt pgtype.Timestamptz
	if err := tx.QueryRow(ctx, `SELECT name, kind, archived_at FROM channel WHERE id = $1 AND workspace_id = $2`, reminder.AnchorChannelID, reminder.WorkspaceID).Scan(&channelName, &channelKind, &channelArchivedAt); err != nil {
		if errorsIsNoRows(err) {
			return h.terminalizeReminderOccurrence(ctx, tx, reminder, occurrenceID, "channel_deleted")
		}
		return err
	}
	if channelArchivedAt.Valid {
		return h.terminalizeReminderOccurrence(ctx, tx, reminder, occurrenceID, "channel_archived")
	}

	txQueries := h.Queries.WithTx(tx)
	agent, err := txQueries.GetAgentInWorkspace(ctx, db.GetAgentInWorkspaceParams{ID: reminder.AgentID, WorkspaceID: reminder.WorkspaceID})
	if err != nil {
		if errorsIsNoRows(err) {
			return h.terminalizeReminderOccurrence(ctx, tx, reminder, occurrenceID, "agent_deleted")
		}
		return err
	}
	if agent.ArchivedAt.Valid {
		return h.terminalizeReminderOccurrence(ctx, tx, reminder, occurrenceID, "agent_archived")
	}
	if !agent.RuntimeID.Valid {
		return h.terminalizeReminderOccurrence(ctx, tx, reminder, occurrenceID, "agent_runtime_unavailable")
	}
	ch := ChannelResponse{ID: uuidToString(reminder.AnchorChannelID), WorkspaceID: uuidToString(reminder.WorkspaceID), Name: channelName, Kind: channelKind}
	var anchorExcerpt string
	anchorAvailable := false
	if reminder.AnchorMessageID.Valid {
		var deletedAt pgtype.Timestamptz
		if err := tx.QueryRow(ctx, `SELECT content, deleted_at FROM channel_message WHERE id = $1 AND channel_id = $2 AND workspace_id = $3`, reminder.AnchorMessageID, reminder.AnchorChannelID, reminder.WorkspaceID).Scan(&anchorExcerpt, &deletedAt); err == nil && !deletedAt.Valid {
			anchorAvailable = true
		}
	}

	eventParams, err := json.Marshal(map[string]any{
		"reminder_id": uuidToString(reminder.ID), "occurrence_id": uuidToString(occurrenceID),
		"title": reminder.Title, "anchor_available": anchorAvailable,
		"cadence": nullableText(reminder.Cadence), "schedule_timezone": reminderTimezoneValue(reminder.Cadence, reminder.ScheduleTimezone),
	})
	if err != nil {
		return err
	}
	threadID := reminderThreadID(ctx, tx, reminder.AnchorThreadRootMessageID)
	externalID := "reminder_occurrence:" + uuidToString(occurrenceID)
	receiptContent := "Reminder fired: " + reminder.Title
	if !anchorAvailable {
		receiptContent += " · Anchor unavailable"
	}
	receipt, err := insertChannelMessageWithPartsExec(ctx, tx, reminder.AnchorChannelID, reminder.WorkspaceID,
		"system", pgtype.UUID{}, "system", receiptContent,
		[]protocol.MessagePart{{Type: protocol.MessagePartTypeSystemEvent, Event: "reminder_fired", EventParams: eventParams}},
		"multica", &externalID, nil, pgtype.UUID{}, pgtype.UUID{}, nil,
		reminder.AnchorThreadRootMessageID, threadID, 0)
	if err != nil {
		return err
	}
	trigger := ChannelMessageResponse{
		ID:           receipt.ID,
		AuthorName:   "Reminder",
		Type:         "system",
		Content:      reminder.Title,
		TriggerDepth: 1,
	}
	if anchorAvailable {
		trigger.ID = uuidToString(reminder.AnchorMessageID)
	}
	if reminder.AnchorThreadRootMessageID.Valid {
		root := uuidToString(reminder.AnchorThreadRootMessageID)
		trigger.ThreadRootMessageID = &root
		trigger.ThreadID = threadID
	}
	creatorID, err := reminderFireCreatorID(ctx, tx, reminder.WorkspaceID, reminder.InitiatorUserID, agent.OwnerID)
	if err != nil {
		return err
	}
	session, err := h.ensureChannelAgentSessionWithDB(ctx, txQueries, tx, ch, reminder.AgentID, creatorID)
	if err != nil {
		return err
	}
	prompt := buildReminderPrompt(ch, reminder, occurrenceID, anchorExcerpt, anchorAvailable)
	promptMsg, err := h.createChannelAgentPromptMessageWithDB(ctx, tx, session.ID, prompt, trigger)
	if err != nil {
		return err
	}
	task, err := h.TaskService.CreateFreshChatTaskRow(ctx, txQueries, session, creatorID)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE chat_message SET task_id = $1 WHERE id = $2`, task.ID, promptMsg.ID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE agent_reminder_occurrence
		SET status = 'fired', receipt_message_id = $2, fired_task_id = $3,
		    anchor_available = $4, fired_at = now(), updated_at = now()
		WHERE id = $1 AND status IN ('pending', 'claimed')`, occurrenceID, parseUUID(receipt.ID), task.ID, anchorAvailable); err != nil {
		return err
	}

	resultingState := "fired"
	var nextFireAt any
	if reminder.Cadence.Valid {
		cadence, parseErr := parseReminderCadence(reminder.Cadence.String, reminderCadenceTimezone(reminder))
		if parseErr != nil {
			return parseErr
		}
		next, nextErr := nextReminderCadenceAfterSlot(cadence, cadenceSlot.Time, time.Now().UTC())
		if nextErr != nil {
			return nextErr
		}
		resultingState = "scheduled"
		nextFireAt = next
		if _, err := tx.Exec(ctx, `
			UPDATE agent_reminder
			SET status = 'scheduled', fire_at = $2, cadence_next_at = $2,
			    current_occurrence_id = NULL, fired_task_id = $3, fired_at = now(), updated_at = now()
			WHERE id = $1 AND status = 'firing' AND current_occurrence_id = $4`, reminder.ID, next, task.ID, occurrenceID); err != nil {
			return err
		}
	} else {
		if _, err := tx.Exec(ctx, `
			UPDATE agent_reminder
			SET status = 'fired', current_occurrence_id = NULL,
			    fired_task_id = $2, fired_at = now(), updated_at = now()
			WHERE id = $1 AND status = 'firing' AND current_occurrence_id = $3`, reminder.ID, task.ID, occurrenceID); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO agent_reminder_lifecycle_event (
			reminder_id, workspace_id, agent_id, occurrence_id, event_type,
			actor_type, previous_fire_at, next_fire_at, title_snapshot,
			cadence_snapshot, timezone_snapshot, resulting_state
		) VALUES ($1, $2, $3, $4, 'fired', 'system', $5, $6, $7, $8, $9, $10)`,
		reminder.ID, reminder.WorkspaceID, reminder.AgentID, occurrenceID, reminder.FireAt,
		nextFireAt, reminder.Title, nullableText(reminder.Cadence), reminderTimezoneValue(reminder.Cadence, reminder.ScheduleTimezone), resultingState); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	h.publishAgentReminderChanged(ctx, reminder.WorkspaceID, reminder.AgentID)
	h.publishChannelToMembers(ctx, protocol.EventChannelMessage, uuidToString(reminder.WorkspaceID), "system", "", reminder.AnchorChannelID, receipt)
	h.TaskService.PublishChatTaskQueued(ctx, task, false)
	h.recordAgentActivityEvent(ctx, h.DB,
		reminder.WorkspaceID, reminder.AgentID, agent.RuntimeID, task.ID,
		activityKindCustom, "reminder_fired", "info",
		reminderTargetKind(reminder.AnchorThreadRootMessageID), reminder.AnchorChannelID, reminder.Title,
		"", "Reminder fired and woke the agent",
		map[string]any{"reminder_id": uuidToString(reminder.ID), "occurrence_id": uuidToString(occurrenceID)},
	)
	return nil
}

func reminderFireCreatorID(ctx context.Context, exec db.DBTX, workspaceID, initiatorUserID, agentOwnerID pgtype.UUID) (pgtype.UUID, error) {
	for _, candidate := range []pgtype.UUID{initiatorUserID, agentOwnerID} {
		if !candidate.Valid {
			continue
		}
		var exists bool
		if err := exec.QueryRow(ctx, `
			SELECT EXISTS (
				SELECT 1
				FROM member
				WHERE workspace_id = $1 AND user_id = $2
			)`, workspaceID, candidate).Scan(&exists); err != nil {
			return pgtype.UUID{}, fmt.Errorf("validate reminder fire creator: %w", err)
		}
		if exists {
			return candidate, nil
		}
	}
	return pgtype.UUID{}, fmt.Errorf("reminder fire creator is not a current workspace member")
}

func reminderThreadID(ctx context.Context, exec db.DBTX, threadRootID pgtype.UUID) *string {
	if !threadRootID.Valid {
		return nil
	}
	var threadID string
	if err := exec.QueryRow(ctx, `SELECT thread_id FROM channel_message WHERE id = $1 AND deleted_at IS NULL`, threadRootID).Scan(&threadID); err != nil || strings.TrimSpace(threadID) == "" {
		return nil
	}
	return &threadID
}

func (h *Handler) terminalizeReminderOccurrence(ctx context.Context, tx pgx.Tx, reminder agentReminder, occurrenceID pgtype.UUID, reason string) error {
	if _, err := tx.Exec(ctx, `
		UPDATE agent_reminder_occurrence
		SET status = 'cancelled', terminal_reason = $2, anchor_available = false, updated_at = now()
		WHERE id = $1 AND status IN ('pending', 'claimed')`, occurrenceID, reason); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE agent_reminder
		SET status = 'cancelled', terminal_reason = $2, current_occurrence_id = NULL, updated_at = now()
		WHERE id = $1`, reminder.ID, reason); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO agent_reminder_lifecycle_event (
			reminder_id, workspace_id, agent_id, occurrence_id, event_type,
			actor_type, previous_fire_at, title_snapshot, cadence_snapshot,
			timezone_snapshot, resulting_state, reason_code
		) VALUES ($1, $2, $3, $4, 'cancelled', 'system', $5, $6, $7, $8, 'cancelled', $9)`,
		reminder.ID, reminder.WorkspaceID, reminder.AgentID, occurrenceID, reminder.FireAt,
		reminder.Title, nullableText(reminder.Cadence), reminderTimezoneValue(reminder.Cadence, reminder.ScheduleTimezone), reason); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	h.publishAgentReminderChanged(ctx, reminder.WorkspaceID, reminder.AgentID)
	return nil
}

func (h *Handler) finalizeReminderOccurrenceWithExistingTask(ctx context.Context, tx pgx.Tx, reminder agentReminder, occurrenceID pgtype.UUID, cadenceSlot pgtype.Timestamptz, taskID pgtype.UUID) error {
	if _, err := tx.Exec(ctx, `
		UPDATE agent_reminder_occurrence
		SET status = 'fired', fired_at = COALESCE(fired_at, now()), updated_at = now()
		WHERE id = $1 AND status IN ('pending', 'claimed')`, occurrenceID); err != nil {
		return err
	}
	resultingState := "fired"
	var nextFireAt any
	if reminder.Cadence.Valid {
		cadence, err := parseReminderCadence(reminder.Cadence.String, reminderCadenceTimezone(reminder))
		if err != nil {
			return err
		}
		next, err := nextReminderCadenceAfterSlot(cadence, cadenceSlot.Time, time.Now().UTC())
		if err != nil {
			return err
		}
		resultingState = "scheduled"
		nextFireAt = next
		if _, err := tx.Exec(ctx, `
			UPDATE agent_reminder
			SET status = 'scheduled', fire_at = $2, cadence_next_at = $2,
			    current_occurrence_id = NULL, fired_task_id = $3,
			    fired_at = COALESCE(fired_at, now()), updated_at = now()
			WHERE id = $1`, reminder.ID, next, taskID); err != nil {
			return err
		}
	} else if _, err := tx.Exec(ctx, `
		UPDATE agent_reminder
		SET status = 'fired', current_occurrence_id = NULL, fired_task_id = $2,
		    fired_at = COALESCE(fired_at, now()), updated_at = now()
		WHERE id = $1`, reminder.ID, taskID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO agent_reminder_lifecycle_event (
			reminder_id, workspace_id, agent_id, occurrence_id, event_type,
			actor_type, previous_fire_at, next_fire_at, title_snapshot,
			cadence_snapshot, timezone_snapshot, resulting_state, reason_code
		) VALUES ($1, $2, $3, $4, 'fired', 'system', $5, $6, $7, $8, $9, $10, 'recovered_existing_task')
		ON CONFLICT (occurrence_id, event_type) WHERE occurrence_id IS NOT NULL AND event_type IN ('fired', 'cancelled') DO NOTHING`,
		reminder.ID, reminder.WorkspaceID, reminder.AgentID, occurrenceID, reminder.FireAt,
		nextFireAt, reminder.Title, nullableText(reminder.Cadence), reminderTimezoneValue(reminder.Cadence, reminder.ScheduleTimezone), resultingState); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	h.publishAgentReminderChanged(ctx, reminder.WorkspaceID, reminder.AgentID)
	return nil
}

func reminderCadenceTimezone(reminder agentReminder) string {
	if !reminder.Cadence.Valid || (!strings.HasPrefix(reminder.Cadence.String, "daily@") && !strings.HasPrefix(reminder.Cadence.String, "weekly:")) {
		return ""
	}
	return reminder.ScheduleTimezone.String
}

func buildReminderPrompt(ch ChannelResponse, reminder agentReminder, occurrenceID pgtype.UUID, anchorExcerpt string, anchorAvailable bool) string {
	var b strings.Builder
	b.WriteString("A self-scheduled reminder is due. This is a directed wake that you previously requested.\n")
	fmt.Fprintf(&b, "Reminder id: %s\n", uuidToString(reminder.ID))
	fmt.Fprintf(&b, "Occurrence id: %s\n", uuidToString(occurrenceID))
	fmt.Fprintf(&b, "Reminder title: %s\n", reminder.Title)
	if ch.Kind == "dm" {
		b.WriteString("Anchored surface: direct message\n")
	} else {
		fmt.Fprintf(&b, "Anchored surface: #%s\n", ch.Name)
	}
	if reminder.Cadence.Valid {
		fmt.Fprintf(&b, "Cadence: %s\n", reminder.Cadence.String)
	}
	if reminderTimezonePtr(reminder.Cadence, reminder.ScheduleTimezone) != nil {
		fmt.Fprintf(&b, "Locked schedule timezone: %s\n", reminder.ScheduleTimezone.String)
	}
	if anchorAvailable && reminder.AnchorMessageID.Valid {
		fmt.Fprintf(&b, "Current message id: %s\n", uuidToString(reminder.AnchorMessageID))
	} else {
		b.WriteString("Anchor message: unavailable (deleted); reply to the preserved channel/thread surface if useful.\n")
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
	rows, err := h.DB.Query(ctx, `
		SELECT reminder.id, occurrence.id
		FROM agent_reminder reminder
		JOIN agent_reminder_occurrence occurrence ON occurrence.id = reminder.current_occurrence_id
		WHERE reminder.status = 'firing'
		  AND occurrence.status = 'claimed'
		  AND occurrence.claimed_at < now() - interval '5 minutes'
		ORDER BY occurrence.claimed_at ASC
		LIMIT 100`)
	if err != nil {
		return err
	}
	var stale [][2]pgtype.UUID
	for rows.Next() {
		var reminderID, occurrenceID pgtype.UUID
		if err := rows.Scan(&reminderID, &occurrenceID); err != nil {
			rows.Close()
			return err
		}
		stale = append(stale, [2]pgtype.UUID{reminderID, occurrenceID})
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	for _, item := range stale {
		if err := h.fireReminderOccurrence(ctx, item[0], item[1]); err != nil {
			slog.Warn("stale reminder occurrence recovery failed", "reminder_id", uuidToString(item[0]), "occurrence_id", uuidToString(item[1]), "error", err)
		}
	}
	return nil
}
