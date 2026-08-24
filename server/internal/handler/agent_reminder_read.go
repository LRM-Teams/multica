package handler

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"
)

type agentReminderAnchorResponse struct {
	Available   bool    `json:"available"`
	Kind        *string `json:"kind,omitempty"`
	DisplayName *string `json:"displayName,omitempty"`
	Href        *string `json:"href,omitempty"`
}

type agentReminderDefinitionResponse struct {
	ID               string                      `json:"id"`
	Title            string                      `json:"title"`
	Status           string                      `json:"status"`
	ScheduleKind     string                      `json:"scheduleKind"`
	NextFireAt       *string                     `json:"nextFireAt,omitempty"`
	LastFireAt       *string                     `json:"lastFireAt,omitempty"`
	Cadence          *string                     `json:"cadence,omitempty"`
	ScheduleTimezone *string                     `json:"scheduleTimezone,omitempty"`
	SnoozeCount      int32                       `json:"snoozeCount"`
	Anchor           agentReminderAnchorResponse `json:"anchor"`
}

type agentReminderListResponse struct {
	Definitions []agentReminderDefinitionResponse `json:"definitions"`
	Realtime    AgentRealtimeContract             `json:"realtime"`
}

func (h *Handler) ListAgentReminders(w http.ResponseWriter, r *http.Request) {
	request, ok := h.prepareAgentInternalRequest(w, r)
	if !ok {
		return
	}

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

	response := agentReminderListResponse{
		Definitions: []agentReminderDefinitionResponse{},
		Realtime:    agentReminderRealtime(uuidToString(request.agent.ID)),
	}
	for _, reminder := range scheduled {
		response.Definitions = append(response.Definitions, agentReminderDefinitionResponse{
			ID:               uuidToString(reminder.ID),
			Title:            reminder.Title,
			Status:           reminder.Status,
			ScheduleKind:     reminderScheduleKind(reminder.Cadence),
			NextFireAt:       timestampToPtr(reminder.FireAt),
			LastFireAt:       timestampToPtr(reminder.FiredAt),
			Cadence:          nullableTextPtr(reminder.Cadence),
			ScheduleTimezone: reminderTimezonePtr(reminder.Cadence, reminder.ScheduleTimezone),
			SnoozeCount:      reminder.SnoozeCount,
			Anchor:           h.buildAgentReminderAnchorResponse(r, request.userID, reminder),
		})
	}
	writeJSON(w, http.StatusOK, response)
}

func reminderScheduleKind(cadence pgtype.Text) string {
	if cadence.Valid {
		return "recurring"
	}
	return "one_shot"
}

func agentReminderRealtime(agentID string) AgentRealtimeContract {
	return AgentRealtimeContract{
		Scope: "agent", ID: agentID, EventType: "agent_reminder:changed", Payload: "agentId",
	}
}

func (h *Handler) buildAgentReminderAnchorResponse(r *http.Request, userID string, reminder agentReminder) agentReminderAnchorResponse {
	unavailable := agentReminderAnchorResponse{Available: false}
	viewerUserID := parseUUID(userID)
	anchor, err := loadAuthorizedReminderAnchor(r.Context(), h.DB, reminder, viewerUserID)
	if err != nil || !anchor.Available {
		return unavailable
	}
	kind := "channel"
	display := "# Unnamed channel"
	if anchor.ChannelKind == "dm" {
		display = strings.TrimSpace(anchor.DMPeerDisplay)
		if display == "" {
			display = "Unnamed direct message"
		}
	} else if name := strings.TrimSpace(anchor.ChannelName); name != "" {
		display = "#" + name
	}
	if reminder.AnchorMessageID.Valid && reminder.AnchorThreadRootMessageID.Valid {
		kind = "thread"
		display = "Thread in " + display
	}
	href := fmt.Sprintf("/%s/channels/%s", url.PathEscape(anchor.WorkspaceSlug),
		url.PathEscape(uuidToString(reminder.AnchorChannelID)))
	if reminder.AnchorMessageID.Valid {
		href += "?message=" + url.QueryEscape(uuidToString(reminder.AnchorMessageID))
	}
	if reminder.AnchorMessageID.Valid && reminder.AnchorThreadRootMessageID.Valid {
		query := "thread=" + url.QueryEscape(uuidToString(reminder.AnchorThreadRootMessageID)) +
			"&message=" + url.QueryEscape(uuidToString(reminder.AnchorMessageID))
		href = fmt.Sprintf("/%s/channels/%s?%s", url.PathEscape(anchor.WorkspaceSlug),
			url.PathEscape(uuidToString(reminder.AnchorChannelID)), query)
	}
	return agentReminderAnchorResponse{
		Available: true, Kind: &kind, DisplayName: &display, Href: &href,
	}
}
