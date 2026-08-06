package handler

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/util"
)

type humanReminderAnchor struct {
	Available   bool    `json:"available"`
	Kind        *string `json:"kind,omitempty"`
	Display     *string `json:"display,omitempty"`
	DisplayName *string `json:"display_name,omitempty"`
	Href        *string `json:"href,omitempty"`
}

type humanReminderDefinition struct {
	ID               string              `json:"id"`
	Title            string              `json:"title"`
	Status           string              `json:"status"`
	ScheduleKind     string              `json:"schedule_kind"`
	NextFireAt       *string             `json:"next_fire_at,omitempty"`
	LastFireAt       *string             `json:"last_fire_at,omitempty"`
	Cadence          *string             `json:"cadence,omitempty"`
	ScheduleTimezone *string             `json:"schedule_timezone,omitempty"`
	SnoozeCount      int32               `json:"snooze_count"`
	OriginKind       string              `json:"origin_kind"`
	ManagedKind      *string             `json:"managed_kind,omitempty"`
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
			WHERE workspace_id = $1
			  AND agent_id = $2
				  AND status IN ('scheduled', 'firing')
			ORDER BY fire_at ASC, id ASC`, request.agent.WorkspaceID, request.agent.ID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to load reminders")
			return
		}
		// Scan every row into memory and close the cursor BEFORE calling
		// safeHumanReminderAnchor: that helper acquires its own pool
		// connection (channel/workspace lookups), and doing so while this
		// cursor is still open can deadlock a bounded pool under concurrent
		// requests (same shape as the #1803 attachAgentRuntimeNames bug).
		var scheduled []agentReminder
		for rows.Next() {
			reminder, err := scanAgentReminder(rows)
			if err != nil {
				rows.Close()
				writeError(w, http.StatusInternalServerError, "failed to load reminders")
				return
			}
			scheduled = append(scheduled, reminder)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			writeError(w, http.StatusInternalServerError, "failed to load reminders")
			return
		}
		rows.Close()
		for _, reminder := range scheduled {
			var nextFireAt *string
			if reminder.Status == "scheduled" || reminder.Status == "firing" {
				nextFireAt = timestampToPtr(reminder.FireAt)
			}
			response.Definitions = append(response.Definitions, humanReminderDefinition{
				ID: uuidToString(reminder.ID), Title: reminder.Title, Status: reminder.Status,
				ScheduleKind: reminderScheduleKind(reminder.Cadence), NextFireAt: nextFireAt,
				LastFireAt: timestampToPtr(reminder.FiredAt), Cadence: nullableTextPtr(reminder.Cadence),
				ScheduleTimezone: reminderTimezonePtr(reminder.Cadence, reminder.ScheduleTimezone), SnoozeCount: reminder.SnoozeCount,
				OriginKind: reminder.OriginKind, ManagedKind: nullableTextPtr(reminder.ManagedKind),
				Anchor: h.safeHumanReminderAnchor(r, request.userID, reminder),
			})
		}
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
		type firedRow struct {
			occurrenceID, reminderID     pgtype.UUID
			title, occurrenceStatus      string
			scheduledFor, dueAt, firedAt pgtype.Timestamptz
			cadence, timezone            pgtype.Text
			reminder                     agentReminder
		}
		// Same cursor-before-second-acquire hazard as the scheduled loop
		// above: drain into memory and close before safeHumanReminderAnchor.
		var fired []firedRow
		for rows.Next() {
			var fr firedRow
			if err := rows.Scan(&fr.occurrenceID, &fr.reminderID, &fr.title, &fr.occurrenceStatus,
				&fr.scheduledFor, &fr.dueAt, &fr.firedAt, &fr.cadence, &fr.timezone,
				&fr.reminder.ID, &fr.reminder.WorkspaceID, &fr.reminder.AgentID, &fr.reminder.InitiatorUserID, &fr.reminder.Title,
				&fr.reminder.AnchorChannelID, &fr.reminder.AnchorMessageID, &fr.reminder.AnchorThreadRootMessageID,
				&fr.reminder.FireAt, &fr.reminder.Status, &fr.reminder.FiredTaskID, &fr.reminder.SnoozeCount,
				&fr.reminder.CreatedAt, &fr.reminder.UpdatedAt, &fr.reminder.FiredAt, &fr.reminder.Cadence,
				&fr.reminder.ScheduleTimezone, &fr.reminder.CadenceNextAt, &fr.reminder.CurrentOccurrenceID,
				&fr.reminder.TerminalReason, &fr.reminder.Version, &fr.reminder.OriginKind,
				&fr.reminder.ManagedKind, &fr.reminder.OriginKey, &fr.reminder.ManagedBackoffStep); err != nil {
				rows.Close()
				writeError(w, http.StatusInternalServerError, "failed to load reminder history")
				return
			}
			fired = append(fired, fr)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			writeError(w, http.StatusInternalServerError, "failed to load reminder history")
			return
		}
		rows.Close()
		for _, fr := range fired {
			response.Occurrences = append(response.Occurrences, humanReminderOccurrence{
				ID: uuidToString(fr.occurrenceID), ReminderID: uuidToString(fr.reminderID), Title: fr.title,
				Status: fr.occurrenceStatus, DefinitionStatus: fr.reminder.Status, ScheduleKind: reminderScheduleKind(fr.cadence),
				CadenceScheduledFor: timestampToString(fr.scheduledFor), DueAt: timestampToString(fr.dueAt),
				FiredAt: timestampToString(fr.firedAt), Cadence: nullableTextPtr(fr.cadence),
				ScheduleTimezone: reminderTimezonePtr(fr.cadence, fr.timezone), Anchor: h.safeHumanReminderAnchor(r, request.userID, fr.reminder),
			})
		}
		if len(response.Occurrences) > limit {
			response.HasMore = true
			response.Occurrences = response.Occurrences[:limit]
			last := response.Occurrences[len(response.Occurrences)-1]
			response.NextCursor = encodeHumanReminderCursor(humanReminderCursor{FiredAt: last.FiredAt, ID: last.ID})
		}
	}
	writeJSON(w, http.StatusOK, response)
}

func (h *Handler) canUserManageGroupChannel(ctx context.Context, workspaceID, channelID, userID pgtype.UUID) bool {
	return canUserManageGroupChannelWithQuery(ctx, h.DB, workspaceID, channelID, userID)
}

func canUserManageGroupChannelWithQuery(ctx context.Context, q reminderQueryRower, workspaceID, channelID, userID pgtype.UUID) bool {
	if !workspaceID.Valid || !channelID.Valid || !userID.Valid {
		return false
	}
	var allowed bool
	err := q.QueryRow(ctx, `
		SELECT EXISTS (
		  SELECT 1
		  FROM channel ch
		  WHERE ch.id = $2
		    AND ch.workspace_id = $1
		    AND ch.kind = 'group'
		    AND (
		      ch.created_by = $3
		      OR EXISTS (
		        SELECT 1
		        FROM member m
		        WHERE m.workspace_id = $1
		          AND m.user_id = $3
		          AND m.role IN ('owner', 'admin')
		      )
		    )
		)`, workspaceID, channelID, userID).Scan(&allowed)
	return err == nil && allowed
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
	if !reminder.AnchorChannelID.Valid {
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
	kind := "channel"
	display := h.reminderAnchorDisplayName(r.Context(), reminder.WorkspaceID, reminder.AnchorChannelID, channelKind, channelName, parseUUID(userID))
	if reminder.AnchorMessageID.Valid {
		anchor, err := loadReminderAnchorSnapshot(r.Context(), h.DB, reminder)
		if err != nil || !anchor.Available {
			return unavailable
		}
	}
	if reminder.AnchorMessageID.Valid && reminder.AnchorThreadRootMessageID.Valid {
		kind = "thread"
		display = "Thread in " + display
	}
	href := fmt.Sprintf("/%s/channels/%s", url.PathEscape(workspaceSlug),
		url.PathEscape(uuidToString(reminder.AnchorChannelID)))
	if reminder.AnchorMessageID.Valid {
		href += "?message=" + url.QueryEscape(uuidToString(reminder.AnchorMessageID))
	}
	if reminder.AnchorMessageID.Valid && reminder.AnchorThreadRootMessageID.Valid {
		query := "thread=" + url.QueryEscape(uuidToString(reminder.AnchorThreadRootMessageID)) +
			"&message=" + url.QueryEscape(uuidToString(reminder.AnchorMessageID))
		href = fmt.Sprintf("/%s/channels/%s?%s", url.PathEscape(workspaceSlug),
			url.PathEscape(uuidToString(reminder.AnchorChannelID)), query)
	}
	return humanReminderAnchor{
		Available: true, Kind: &kind, Display: &display, DisplayName: &display, Href: &href,
	}
}

func (h *Handler) reminderAnchorDisplayName(ctx context.Context, workspaceID, channelID pgtype.UUID, channelKind, channelName string, userID pgtype.UUID) string {
	if channelKind != "dm" {
		if name := strings.TrimSpace(channelName); name != "" {
			return "#" + name
		}
		return "# Unnamed channel"
	}
	var peerName string
	if err := h.DB.QueryRow(ctx, `
		SELECT COALESCE(NULLIF(u.display_name, ''), u.name, u.email, NULLIF(a.display_name, ''), a.name, '')
		FROM channel_member peer
		LEFT JOIN "user" u ON peer.member_type = 'user' AND u.id = peer.member_id
		LEFT JOIN agent a ON peer.member_type = 'agent' AND a.id = peer.member_id
		WHERE peer.channel_id = $1 AND peer.workspace_id = $2
		  AND NOT (peer.member_type = 'user' AND peer.member_id = $3)
		ORDER BY peer.created_at ASC
		LIMIT 1`, channelID, workspaceID, userID).Scan(&peerName); err == nil {
		if name := strings.TrimSpace(peerName); name != "" {
			return name
		}
	}
	return "Unnamed direct message"
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
	id, err := util.ParseUUID(cursor.ID)
	if err != nil || !id.Valid {
		return nil, pgtype.UUID{}, false
	}
	return firedAt, id, true
}
