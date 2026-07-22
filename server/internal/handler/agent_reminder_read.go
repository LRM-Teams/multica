package handler

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
)

type humanReminderAnchor struct {
	Available bool    `json:"available"`
	Kind      *string `json:"kind,omitempty"`
	Display   *string `json:"display,omitempty"`
	Href      *string `json:"href,omitempty"`
}

type humanReminderDefinition struct {
	ID               string              `json:"id"`
	Title            string              `json:"title"`
	Status           string              `json:"status"`
	ScheduleKind     string              `json:"schedule_kind"`
	NextFireAt       string              `json:"next_fire_at"`
	LastFireAt       *string             `json:"last_fire_at,omitempty"`
	Cadence          *string             `json:"cadence,omitempty"`
	ScheduleTimezone *string             `json:"schedule_timezone,omitempty"`
	SnoozeCount      int32               `json:"snooze_count"`
	Anchor           humanReminderAnchor `json:"anchor"`
}

type humanReminderOccurrence struct {
	ID                  string              `json:"id"`
	ReminderID          string              `json:"reminder_id"`
	Title               string              `json:"title"`
	Status              string              `json:"status"`
	DefinitionStatus    string              `json:"definition_status"`
	ScheduleKind        string              `json:"schedule_kind"`
	CadenceScheduledFor string              `json:"cadence_scheduled_for"`
	DueAt               string              `json:"due_at"`
	FiredAt             string              `json:"fired_at"`
	Cadence             *string             `json:"cadence,omitempty"`
	ScheduleTimezone    *string             `json:"schedule_timezone,omitempty"`
	Anchor              humanReminderAnchor `json:"anchor"`
}

type humanReminderCursor struct {
	FiredAt string `json:"fired_at"`
	ID      string `json:"id"`
}

type humanReminderPage struct {
	Definitions []humanReminderDefinition     `json:"definitions"`
	Occurrences []humanReminderOccurrence     `json:"occurrences"`
	Limit       int                           `json:"limit"`
	HasMore     bool                          `json:"has_more"`
	NextCursor  *string                       `json:"next_cursor,omitempty"`
	Realtime    AgentActivityRealtimeContract `json:"realtime"`
}

func (h *Handler) ListAgentReminders(w http.ResponseWriter, r *http.Request) {
	request, ok := h.prepareAgentActivityRequest(w, r)
	if !ok {
		return
	}
	status := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("status")))
	if status == "" {
		status = "all"
	}
	if status != "all" && status != "scheduled" && status != "fired" {
		writeError(w, http.StatusBadRequest, "status must be scheduled, fired, or all")
		return
	}
	limit := 20
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed <= 0 {
			writeError(w, http.StatusBadRequest, "invalid limit")
			return
		}
		if parsed < 100 {
			limit = parsed
		} else {
			limit = 100
		}
	}

	response := humanReminderPage{
		Definitions: []humanReminderDefinition{},
		Occurrences: []humanReminderOccurrence{},
		Limit:       limit,
		Realtime:    agentReminderRealtime(uuidToString(request.agent.ID)),
	}
	if status == "all" || status == "scheduled" {
		rows, err := h.DB.Query(r.Context(), `
			SELECT `+reminderSelectColumns()+`
			FROM agent_reminder
			WHERE workspace_id = $1 AND agent_id = $2 AND status IN ('scheduled', 'firing')
			ORDER BY fire_at ASC, id ASC`, request.agent.WorkspaceID, request.agent.ID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to load reminders")
			return
		}
		for rows.Next() {
			reminder, err := scanAgentReminder(rows)
			if err != nil {
				rows.Close()
				writeError(w, http.StatusInternalServerError, "failed to load reminders")
				return
			}
			response.Definitions = append(response.Definitions, humanReminderDefinition{
				ID: uuidToString(reminder.ID), Title: reminder.Title, Status: reminder.Status,
				ScheduleKind: reminderScheduleKind(reminder.Cadence), NextFireAt: timestampToString(reminder.FireAt),
				LastFireAt: timestampToPtr(reminder.FiredAt), Cadence: nullableTextPtr(reminder.Cadence),
				ScheduleTimezone: reminderTimezonePtr(reminder.Cadence, reminder.ScheduleTimezone), SnoozeCount: reminder.SnoozeCount,
				Anchor: h.safeHumanReminderAnchor(r, request.userID, reminder),
			})
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			writeError(w, http.StatusInternalServerError, "failed to load reminders")
			return
		}
		rows.Close()
	}

	if status == "all" || status == "fired" {
		cursorAt, cursorID, cursorOK := parseHumanReminderCursor(strings.TrimSpace(r.URL.Query().Get("cursor")))
		if !cursorOK {
			writeError(w, http.StatusBadRequest, "invalid cursor")
			return
		}
		rows, err := h.DB.Query(r.Context(), `
			SELECT occurrence.id, occurrence.reminder_id, occurrence.title_snapshot,
			       occurrence.status, occurrence.cadence_scheduled_for, occurrence.due_at,
			       occurrence.fired_at, occurrence.cadence_snapshot, occurrence.timezone_snapshot,
			       `+reminderSelectColumnsWithAlias("reminder")+`
			FROM agent_reminder_occurrence occurrence
			JOIN agent_reminder reminder ON reminder.id = occurrence.reminder_id
			WHERE occurrence.workspace_id = $1 AND occurrence.agent_id = $2
			  AND occurrence.status = 'fired'
			  AND ($3::timestamptz IS NULL OR (occurrence.fired_at, occurrence.id) < ($3, $4))
			ORDER BY occurrence.fired_at DESC, occurrence.id DESC
			LIMIT $5`, request.agent.WorkspaceID, request.agent.ID, cursorAt, nullableUUID(cursorID), limit+1)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to load reminder history")
			return
		}
		for rows.Next() {
			var occurrenceID, reminderID pgtype.UUID
			var title, occurrenceStatus string
			var scheduledFor, dueAt, firedAt pgtype.Timestamptz
			var cadence, timezone pgtype.Text
			var reminder agentReminder
			if err := rows.Scan(&occurrenceID, &reminderID, &title, &occurrenceStatus,
				&scheduledFor, &dueAt, &firedAt, &cadence, &timezone,
				&reminder.ID, &reminder.WorkspaceID, &reminder.AgentID, &reminder.InitiatorUserID, &reminder.Title,
				&reminder.AnchorChannelID, &reminder.AnchorMessageID, &reminder.AnchorThreadRootMessageID,
				&reminder.FireAt, &reminder.Status, &reminder.FiredTaskID, &reminder.SnoozeCount,
				&reminder.CreatedAt, &reminder.UpdatedAt, &reminder.FiredAt, &reminder.Cadence,
				&reminder.ScheduleTimezone, &reminder.CadenceNextAt, &reminder.CurrentOccurrenceID,
				&reminder.TerminalReason); err != nil {
				rows.Close()
				writeError(w, http.StatusInternalServerError, "failed to load reminder history")
				return
			}
			response.Occurrences = append(response.Occurrences, humanReminderOccurrence{
				ID: uuidToString(occurrenceID), ReminderID: uuidToString(reminderID), Title: title,
				Status: occurrenceStatus, DefinitionStatus: reminder.Status, ScheduleKind: reminderScheduleKind(cadence),
				CadenceScheduledFor: timestampToString(scheduledFor), DueAt: timestampToString(dueAt),
				FiredAt: timestampToString(firedAt), Cadence: nullableTextPtr(cadence),
				ScheduleTimezone: reminderTimezonePtr(cadence, timezone), Anchor: h.safeHumanReminderAnchor(r, request.userID, reminder),
			})
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			writeError(w, http.StatusInternalServerError, "failed to load reminder history")
			return
		}
		rows.Close()
		if len(response.Occurrences) > limit {
			response.HasMore = true
			response.Occurrences = response.Occurrences[:limit]
			last := response.Occurrences[len(response.Occurrences)-1]
			response.NextCursor = encodeHumanReminderCursor(humanReminderCursor{FiredAt: last.FiredAt, ID: last.ID})
		}
	}
	writeJSON(w, http.StatusOK, response)
}

func reminderSelectColumnsWithAlias(alias string) string {
	parts := strings.Split(reminderSelectColumns(), ",")
	for i, part := range parts {
		parts[i] = alias + "." + strings.TrimSpace(part)
	}
	return strings.Join(parts, ", ")
}

func reminderScheduleKind(cadence pgtype.Text) string {
	if cadence.Valid {
		return "recurring"
	}
	return "one_shot"
}

func agentReminderRealtime(agentID string) AgentActivityRealtimeContract {
	return AgentActivityRealtimeContract{
		Scope: "agent", ID: agentID, EventType: "agent_reminder:changed", Payload: "agent_id",
	}
}

func (h *Handler) safeHumanReminderAnchor(r *http.Request, userID string, reminder agentReminder) humanReminderAnchor {
	unavailable := humanReminderAnchor{Available: false}
	if !reminder.AnchorChannelID.Valid || !reminder.AnchorMessageID.Valid {
		return unavailable
	}
	var channelName, channelKind, workspaceSlug string
	var archivedAt pgtype.Timestamptz
	var member bool
	if err := h.DB.QueryRow(r.Context(), `
		SELECT channel.name, channel.kind, workspace.slug, channel.archived_at,
		       EXISTS (
		         SELECT 1 FROM channel_member
		         WHERE channel_id = channel.id AND workspace_id = channel.workspace_id
		           AND member_type = 'user' AND member_id = $3
		       )
		FROM channel
		JOIN workspace ON workspace.id = channel.workspace_id
		WHERE channel.id = $1 AND channel.workspace_id = $2`, reminder.AnchorChannelID, reminder.WorkspaceID, parseUUID(userID)).Scan(&channelName, &channelKind, &workspaceSlug, &archivedAt, &member); err != nil || archivedAt.Valid || !member {
		return unavailable
	}
	anchor, err := loadReminderAnchorSnapshot(r.Context(), h.DB, reminder)
	if err != nil || !anchor.Available {
		return unavailable
	}
	kind := "channel"
	display := "#" + channelName
	if channelKind == "dm" {
		display = "Direct message"
	}
	messageID := reminder.AnchorMessageID
	query := "message=" + url.QueryEscape(uuidToString(messageID))
	if reminder.AnchorThreadRootMessageID.Valid {
		kind = "thread"
		if channelKind == "dm" {
			display = "Thread in direct message"
		} else {
			display = "Thread in #" + channelName
		}
		query = "thread=" + url.QueryEscape(uuidToString(reminder.AnchorThreadRootMessageID)) +
			"&message=" + url.QueryEscape(uuidToString(reminder.AnchorMessageID))
	}
	href := fmt.Sprintf("/%s/channels/%s?%s", url.PathEscape(workspaceSlug),
		url.PathEscape(uuidToString(reminder.AnchorChannelID)), query)
	return humanReminderAnchor{
		Available: true, Kind: &kind, Display: &display, Href: &href,
	}
}

func encodeHumanReminderCursor(cursor humanReminderCursor) *string {
	raw, err := json.Marshal(cursor)
	if err != nil {
		return nil
	}
	encoded := base64.RawURLEncoding.EncodeToString(raw)
	return &encoded
}

func parseHumanReminderCursor(raw string) (any, pgtype.UUID, bool) {
	if raw == "" {
		return nil, pgtype.UUID{}, true
	}
	decoded, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return nil, pgtype.UUID{}, false
	}
	var cursor humanReminderCursor
	if err := json.Unmarshal(decoded, &cursor); err != nil {
		return nil, pgtype.UUID{}, false
	}
	firedAt, err := time.Parse(time.RFC3339Nano, cursor.FiredAt)
	if err != nil {
		return nil, pgtype.UUID{}, false
	}
	id, err := parseUUIDString(cursor.ID)
	if err != nil || !id.Valid {
		return nil, pgtype.UUID{}, false
	}
	return firedAt, id, true
}
