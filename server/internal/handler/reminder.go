package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/daemonws"
	"github.com/multica-ai/multica/server/internal/util"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

const (
	reminderMinDelay                  = time.Minute
	reminderMaxDelay                  = 90 * 24 * time.Hour
	reminderOwnerInputExcerptMaxRunes = 1000
)

var errReminderDaemonOutdated = fmt.Errorf("daemon_outdated")

func isReminderDaemonOutdatedError(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "P0001" && pgErr.Message == "daemon_outdated"
}

func lockReminderOwnerCapability(ctx context.Context, tx pgx.Tx, workspaceID, agentID pgtype.UUID) (pgtype.UUID, error) {
	var runtimeID pgtype.UUID
	err := tx.QueryRow(ctx, `
		SELECT agent.runtime_id
		FROM agent
		WHERE agent.id = $1 AND agent.workspace_id = $2 AND agent.archived_at IS NULL
		FOR UPDATE`, agentID, workspaceID).Scan(&runtimeID)
	if err != nil {
		return pgtype.UUID{}, err
	}
	if !runtimeID.Valid {
		return pgtype.UUID{}, errReminderDaemonOutdated
	}

	var capable bool
	err = tx.QueryRow(ctx, `
		SELECT COALESCE((metadata->'capabilities') @> '["reminder_versioned_cache_v1","reminder_transient_owner_input_v1"]'::jsonb, false)
		FROM agent_runtime
		WHERE id = $1 AND workspace_id = $2
		FOR SHARE`, runtimeID, workspaceID).Scan(&capable)
	if errors.Is(err, pgx.ErrNoRows) || !capable {
		return pgtype.UUID{}, errReminderDaemonOutdated
	}
	if err != nil {
		return pgtype.UUID{}, err
	}
	return runtimeID, nil
}

func writeReminderMutationError(w http.ResponseWriter, operation string, err error) {
	if errors.Is(err, errReminderDaemonOutdated) {
		writeCodedError(w, http.StatusConflict, "daemon_outdated", "owner runtime must upgrade before changing reminders")
		return
	}
	writeError(w, http.StatusInternalServerError, "failed to "+operation+" reminder")
}

// requireReminderAgentCredential keeps reminder lifecycle operations outside
// issue tasks and inbox deliveries. A reminder is owned by its agent; it does
// not inherit a chat turn, task, lease, or delivery identity.
func (h *Handler) requireReminderAgentCredential(w http.ResponseWriter, r *http.Request) (agentTransportSource, bool) {
	if r.Header.Get("X-Actor-Source") != "agent_credential" {
		writeError(w, http.StatusForbidden, "reminder transport requires an agent credential")
		return agentTransportSource{}, false
	}
	for _, header := range []string{
		"X-Task-ID",
		"X-Agent-Inbox-Event-ID",
		"X-Agent-Inbox-Delivery-ID",
		"X-Agent-Inbox-Lease-Token",
	} {
		if strings.TrimSpace(r.Header.Get(header)) != "" {
			writeError(w, http.StatusBadRequest, "reminder transport does not accept task or inbox delivery context")
			return agentTransportSource{}, false
		}
	}
	return h.requireAgentCredentialChatTransport(w, r)
}

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
	SnoozeCount               int32
	CreatedAt                 pgtype.Timestamptz
	UpdatedAt                 pgtype.Timestamptz
	FiredAt                   pgtype.Timestamptz
	Cadence                   pgtype.Text
	ScheduleTimezone          pgtype.Text
	CadenceNextAt             pgtype.Timestamptz
	CurrentOccurrenceID       pgtype.UUID
	TerminalReason            pgtype.Text
	Version                   int64
}

type reminderQueryRower interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

type reminderQueryer interface {
	reminderQueryRower
	Query(context.Context, string, ...any) (pgx.Rows, error)
}

type authorizedReminderAnchor struct {
	Available     bool
	MessageID     string
	Content       string
	ChannelName   string
	ChannelKind   string
	WorkspaceSlug string
	DMPeerDisplay string
}

type reminderFireCommit struct {
	Reminder   agentReminder
	OwnerInput protocol.ReminderOwnerInputPayload
}

func truncateReminderOwnerInputExcerpt(content string) string {
	runes := []rune(strings.TrimSpace(content))
	if len(runes) <= reminderOwnerInputExcerptMaxRunes {
		return string(runes)
	}
	return string(runes[:reminderOwnerInputExcerptMaxRunes]) + "…"
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
	Version                   int64   `json:"version"`
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
	ID           string  `json:"id"`
	Title        *string `json:"title,omitempty"`
	DelaySeconds *int64  `json:"delay_seconds,omitempty"`
	FireAt       *string `json:"fire_at,omitempty"`
	Cadence      *string `json:"cadence,omitempty"`
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
		Version:                   r.Version,
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
		&out.FireAt, &out.Status, &out.SnoozeCount,
		&out.CreatedAt, &out.UpdatedAt, &out.FiredAt, &out.Cadence,
		&out.ScheduleTimezone, &out.CadenceNextAt, &out.CurrentOccurrenceID,
		&out.TerminalReason, &out.Version,
	)
	return out, err
}

func reminderSelectColumns() string {
	return `id, workspace_id, agent_id, initiator_user_id, title, anchor_channel_id, anchor_message_id,
		anchor_thread_root_message_id, fire_at, status, snooze_count,
		created_at, updated_at, fired_at, cadence, schedule_timezone, cadence_next_at,
		current_occurrence_id, terminal_reason, version`
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
	source, ok := h.requireReminderAgentCredential(w, r)
	if !ok {
		return
	}
	origin := source.origin
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
	anchorMessageID, anchorChannelID, threadRootID, ok := h.resolveReminderAnchor(w, r.Context(), origin, req.MessageID)
	if !ok {
		return
	}
	// P0+P1 (Frank/Parker 2026-08-04): timezone never from user viewing
	// preference; default UTC. initiator_user_id is optional audit fill
	// (anchor human → owner → ws owner when available) and is NOT required
	// for 201 — agent self-schedule without a human member still succeeds.
	initiatorUserID, _ := h.agentReminderScheduleInitiatorUserID(r.Context(), origin.workspaceID, origin.agentID, anchorMessageID)
	timezone := reminderScheduleTimezone("") // explicit API field can be wired later
	schedule, err := parseReminderSchedule(time.Now().UTC(), req.DelaySeconds, req.FireAt, req.Repeat, timezone)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to schedule reminder")
		return
	}
	defer tx.Rollback(r.Context())
	if _, err := lockReminderOwnerCapability(r.Context(), tx, origin.workspaceID, origin.agentID); err != nil {
		writeReminderMutationError(w, "schedule", err)
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
	// Anchor channel is the message's channel and may differ from the wake
	// origin. Membership is already checked in resolveReminderAnchor.
	row := tx.QueryRow(r.Context(), `
		INSERT INTO agent_reminder (
			workspace_id, agent_id, initiator_user_id, title, anchor_channel_id, anchor_message_id,
			anchor_thread_root_message_id, fire_at, cadence, schedule_timezone, cadence_next_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
		RETURNING `+reminderSelectColumns(),
		origin.workspaceID, origin.agentID, initiatorUserID, title, anchorChannelID,
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
	h.projectReminderUpsert(r.Context(), created)
	writeJSON(w, http.StatusCreated, reminderResponse(created))
}

func (h *Handler) AgentTransportListReminders(w http.ResponseWriter, r *http.Request) {
	source, ok := h.requireReminderAgentCredential(w, r)
	if !ok {
		return
	}
	origin := source.origin
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
		rows, err = h.DB.Query(r.Context(), `SELECT `+reminderSelectColumns()+` FROM agent_reminder WHERE workspace_id = $1 AND agent_id = $2 AND status IN ('scheduled', 'firing') ORDER BY fire_at ASC`, origin.workspaceID, origin.agentID)
	case "all":
		rows, err = h.DB.Query(r.Context(), `SELECT `+reminderSelectColumns()+` FROM agent_reminder WHERE workspace_id = $1 AND agent_id = $2 ORDER BY created_at DESC`, origin.workspaceID, origin.agentID)
	case "scheduled", "firing", "fired", "cancelled":
		rows, err = h.DB.Query(r.Context(), `SELECT `+reminderSelectColumns()+` FROM agent_reminder WHERE workspace_id = $1 AND agent_id = $2 AND status = $3 ORDER BY fire_at ASC`, origin.workspaceID, origin.agentID, status)
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
	source, ok := h.requireReminderAgentCredential(w, r)
	if !ok {
		return
	}
	origin := source.origin
	var req agentReminderIDRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	id, ok := h.resolveReminderID(w, r.Context(), origin.workspaceID, origin.agentID, req.ID)
	if !ok {
		return
	}
	rows, err := h.DB.Query(r.Context(), `
		SELECT id, occurrence_id, event_type, actor_type, actor_id,
		       previous_fire_at, next_fire_at, title_snapshot, cadence_snapshot,
		       timezone_snapshot, resulting_state, reason_code, details, created_at
		FROM agent_reminder_lifecycle_event
		WHERE reminder_id = $1 AND workspace_id = $2 AND agent_id = $3
		ORDER BY created_at ASC, id ASC`, id, origin.workspaceID, origin.agentID)
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
	source, ok := h.requireReminderAgentCredential(w, r)
	if !ok {
		return
	}
	origin := source.origin
	var req agentReminderSnoozeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	now := time.Now().UTC()
	fireAt, err := parseReminderFireAt(now, req.DelaySeconds, req.FireAt)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	id, ok := h.resolveReminderID(w, r.Context(), origin.workspaceID, origin.agentID, req.ID)
	if !ok {
		return
	}
	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to snooze reminder")
		return
	}
	defer tx.Rollback(r.Context())
	if _, err := lockReminderOwnerCapability(r.Context(), tx, origin.workspaceID, origin.agentID); err != nil {
		writeReminderMutationError(w, "snooze", err)
		return
	}
	previous, err := scanAgentReminder(tx.QueryRow(r.Context(), `SELECT `+reminderSelectColumns()+` FROM agent_reminder WHERE id = $1 AND workspace_id = $2 AND agent_id = $3 FOR UPDATE`, id, origin.workspaceID, origin.agentID))
	if err != nil {
		writeError(w, http.StatusConflict, "reminder cannot be snoozed in its current state")
		return
	}
	if previous.Status != "scheduled" && previous.Status != "fired" {
		writeError(w, http.StatusConflict, "reminder cannot be snoozed in its current state")
		return
	}
	updated, err := scanAgentReminder(tx.QueryRow(r.Context(), `
			UPDATE agent_reminder
			SET fire_at = $4, status = 'scheduled', fired_at = NULL,
			    current_occurrence_id = NULL,
			    terminal_reason = NULL, snooze_count = snooze_count + 1,
			    version = version + 1, updated_at = now()
			WHERE id = $1 AND workspace_id = $2 AND agent_id = $3
			  AND status IN ('scheduled', 'fired')
			RETURNING `+reminderSelectColumns(), id, origin.workspaceID, origin.agentID, fireAt))
	if err != nil {
		writeError(w, http.StatusConflict, "reminder cannot be snoozed in its current state")
		return
	}
	if _, err := tx.Exec(r.Context(), `
		INSERT INTO agent_reminder_lifecycle_event (
			reminder_id, workspace_id, agent_id, event_type, actor_type, actor_id,
			previous_fire_at, next_fire_at, title_snapshot, cadence_snapshot,
			timezone_snapshot, resulting_state, reason_code, details
			) VALUES ($1, $2, $3, 'snoozed', 'agent', $3, $4, $5, $6, $7, $8, 'scheduled', NULL, '{}'::jsonb)`,
		updated.ID, updated.WorkspaceID, updated.AgentID,
		previous.FireAt, updated.FireAt, updated.Title, nullableText(updated.Cadence),
		reminderTimezoneValue(updated.Cadence, updated.ScheduleTimezone)); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to snooze reminder")
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to snooze reminder")
		return
	}
	h.publishAgentReminderChanged(r.Context(), updated.WorkspaceID, updated.AgentID)
	h.projectReminderUpsert(r.Context(), updated)
	writeJSON(w, http.StatusOK, reminderResponse(updated))
}

func (h *Handler) AgentTransportUpdateReminder(w http.ResponseWriter, r *http.Request) {
	source, ok := h.requireReminderAgentCredential(w, r)
	if !ok {
		return
	}
	origin := source.origin
	var req agentReminderUpdateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	mutationCount := 0
	if req.Title != nil {
		mutationCount++
	}
	if req.DelaySeconds != nil {
		mutationCount++
	}
	if req.FireAt != nil {
		mutationCount++
	}
	if req.Cadence != nil {
		mutationCount++
	}
	if mutationCount != 1 {
		writeError(w, http.StatusBadRequest, "provide exactly one of title, delay_seconds, fire_at, or cadence")
		return
	}
	title := ""
	if req.Title != nil {
		title = strings.TrimSpace(*req.Title)
		if title == "" {
			writeError(w, http.StatusBadRequest, "title must not be empty")
			return
		}
	}
	if len([]rune(title)) > 500 {
		writeError(w, http.StatusBadRequest, "title must be at most 500 characters")
		return
	}
	now := time.Now().UTC()
	rawFireAt := ""
	var explicitFireAt *time.Time
	if req.DelaySeconds != nil {
		parsed, parseErr := parseReminderFireAt(now, req.DelaySeconds, "")
		if parseErr != nil {
			writeError(w, http.StatusBadRequest, parseErr.Error())
			return
		}
		explicitFireAt = &parsed
	}
	if req.FireAt != nil {
		rawFireAt = strings.TrimSpace(*req.FireAt)
		if rawFireAt == "" {
			writeError(w, http.StatusBadRequest, "fire_at must not be empty")
			return
		}
		parsed, parseErr := parseReminderFireAt(now, nil, rawFireAt)
		if parseErr != nil {
			writeError(w, http.StatusBadRequest, parseErr.Error())
			return
		}
		explicitFireAt = &parsed
	}
	rawCadence := ""
	if req.Cadence != nil {
		rawCadence = strings.ToLower(strings.TrimSpace(*req.Cadence))
		if rawCadence == "" {
			writeError(w, http.StatusBadRequest, "cadence must not be empty")
			return
		}
		validationTimezone := ""
		if strings.HasPrefix(rawCadence, "daily@") || strings.HasPrefix(rawCadence, "weekly:") {
			validationTimezone = "UTC"
		}
		if _, parseErr := parseReminderCadence(rawCadence, validationTimezone); parseErr != nil {
			writeError(w, http.StatusBadRequest, parseErr.Error())
			return
		}
	}
	id, ok := h.resolveReminderID(w, r.Context(), origin.workspaceID, origin.agentID, req.ID)
	if !ok {
		return
	}
	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update reminder")
		return
	}
	defer tx.Rollback(r.Context())
	if _, err := lockReminderOwnerCapability(r.Context(), tx, origin.workspaceID, origin.agentID); err != nil {
		writeReminderMutationError(w, "update", err)
		return
	}
	previous, err := scanAgentReminder(tx.QueryRow(r.Context(), `SELECT `+reminderSelectColumns()+` FROM agent_reminder WHERE id = $1 AND workspace_id = $2 AND agent_id = $3 FOR UPDATE`, id, origin.workspaceID, origin.agentID))
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
	if rawCadence != "" {
		calendarRule := strings.HasPrefix(rawCadence, "daily@") || strings.HasPrefix(rawCadence, "weekly:")
		timezone := ""
		if calendarRule {
			if previous.ScheduleTimezone.Valid {
				timezone = reminderScheduleTimezone(previous.ScheduleTimezone.String)
			} else {
				// P0: never fall back to user viewing timezone.
				timezone = reminderScheduleTimezone("")
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
	} else if explicitFireAt != nil {
		nextFireAt = *explicitFireAt
		// An explicit instant replaces the recurrence with a one-shot. Keep the
		// historical timezone lock so a later calendar recurrence resumes in the
		// same zone, but do not expose it while the definition is one-shot.
		nextCadence = nil
		nextCadenceAt = nil
	}

	row := tx.QueryRow(r.Context(), `
		UPDATE agent_reminder
		SET title = $4, fire_at = $5, cadence = $6, schedule_timezone = $7,
			    cadence_next_at = $8,
			    version = version + 1, updated_at = now()
		WHERE id = $1 AND workspace_id = $2 AND agent_id = $3 AND status = 'scheduled'
		RETURNING `+reminderSelectColumns(), id, origin.workspaceID, origin.agentID,
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
			timezone_snapshot, resulting_state, reason_code, details
		) VALUES ($1, $2, $3, 'updated', 'agent', $3, $4, $5, $6, $7, $8, 'scheduled', $9, $10)`,
		updated.ID, updated.WorkspaceID, updated.AgentID, previous.FireAt, updated.FireAt,
		updated.Title, nullableText(updated.Cadence), reminderTimezoneValue(updated.Cadence, updated.ScheduleTimezone),
		nil, []byte(`{}`)); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update reminder")
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update reminder")
		return
	}
	h.publishAgentReminderChanged(r.Context(), updated.WorkspaceID, updated.AgentID)
	h.projectReminderUpsert(r.Context(), updated)
	writeJSON(w, http.StatusOK, reminderResponse(updated))
}

func (h *Handler) AgentTransportCancelReminder(w http.ResponseWriter, r *http.Request) {
	source, ok := h.requireReminderAgentCredential(w, r)
	if !ok {
		return
	}
	origin := source.origin
	var req agentReminderIDRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	id, ok := h.resolveReminderID(w, r.Context(), origin.workspaceID, origin.agentID, req.ID)
	if !ok {
		return
	}
	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to cancel reminder")
		return
	}
	defer tx.Rollback(r.Context())
	if _, err := lockReminderOwnerCapability(r.Context(), tx, origin.workspaceID, origin.agentID); err != nil {
		writeReminderMutationError(w, "cancel", err)
		return
	}
	previous, err := scanAgentReminder(tx.QueryRow(r.Context(), `
		SELECT `+reminderSelectColumns()+`
		FROM agent_reminder
		WHERE id = $1 AND workspace_id = $2 AND agent_id = $3
		FOR UPDATE`, id, origin.workspaceID, origin.agentID))
	if err != nil || previous.Status != "scheduled" {
		writeError(w, http.StatusConflict, "reminder cannot be cancelled in its current state")
		return
	}
	terminalReason := "cancelled_by_author"
	row := tx.QueryRow(r.Context(), `
		UPDATE agent_reminder
		SET status = 'cancelled', terminal_reason = $4, current_occurrence_id = NULL,
		    version = version + 1, updated_at = now()
		WHERE id = $1 AND workspace_id = $2 AND agent_id = $3 AND status = 'scheduled'
		RETURNING `+reminderSelectColumns(), id, origin.workspaceID, origin.agentID, terminalReason)
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
		) VALUES ($1, $2, $3, 'cancelled', 'agent', $3, $4, $5, $6, $7, 'cancelled', $8)`,
		cancelled.ID, cancelled.WorkspaceID, cancelled.AgentID, cancelled.FireAt,
		cancelled.Title, nullableText(cancelled.Cadence),
		reminderTimezoneValue(cancelled.Cadence, cancelled.ScheduleTimezone), terminalReason); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to cancel reminder")
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to cancel reminder")
		return
	}
	h.publishAgentReminderChanged(r.Context(), cancelled.WorkspaceID, cancelled.AgentID)
	h.projectReminderCancel(r.Context(), cancelled)
	writeJSON(w, http.StatusOK, reminderResponse(cancelled))
}

// agentReminderScheduleInitiatorUserID resolves initiator_user_id for schedule
// (and calendar-update timezone fallback) only. Product lock (Frank/Parker
// 2026-08-04 long-term):
//
//  1. anchor message author is human (user|member) and still a workspace member
//  2. else agent.owner (if member)
//  3. else workspace owner (oldest)
//
// Does not require the wake source / inbox event to have a human author —
// reminder re-anchor and agent-authored anchors fall through to owner.
func (h *Handler) agentReminderScheduleInitiatorUserID(ctx context.Context, workspaceID, agentID, anchorMessageID pgtype.UUID) (pgtype.UUID, bool) {
	var empty pgtype.UUID
	if h == nil || h.DB == nil || !workspaceID.Valid || !agentID.Valid {
		return empty, false
	}
	var target pgtype.UUID
	err := h.DB.QueryRow(ctx, `
		SELECT c.user_id
		FROM (
			SELECT 1 AS ord, msg.author_id AS user_id
			FROM channel_message msg
			WHERE msg.id = $3
			  AND msg.workspace_id = $1
			  AND msg.deleted_at IS NULL
			  AND msg.author_type IN ('user', 'member')
			UNION ALL
			SELECT 2, a.owner_id
			FROM agent a
			WHERE a.id = $2
			  AND a.workspace_id = $1
			  AND a.archived_at IS NULL
			UNION ALL
			SELECT 3, (
				SELECT m.user_id
				FROM member m
				WHERE m.workspace_id = $1 AND m.role = 'owner'
				ORDER BY m.created_at ASC NULLS LAST, m.user_id ASC
				LIMIT 1
			)
		) c
		JOIN member mem ON mem.workspace_id = $1 AND mem.user_id = c.user_id
		WHERE c.user_id IS NOT NULL
		ORDER BY c.ord ASC
		LIMIT 1`, workspaceID, agentID, anchorMessageID).Scan(&target)
	return target, err == nil && target.Valid
}

// resolveReminderAnchor locates the anchor message by id in the workspace and
// returns (messageID, channelID, threadRootID, ok).
//
// Product lock B (2026-08-04): schedule may cross channels — msg-id is the
// source of truth for the anchor channel. The agent must currently be a
// member/manager of that channel (agentHasSurfaceAccess). Wake origin.channel
// is NOT required to match the request's wake channel.
func (h *Handler) resolveReminderAnchor(w http.ResponseWriter, ctx context.Context, origin chatOutputOrigin, rawMessageID string) (pgtype.UUID, pgtype.UUID, pgtype.UUID, bool) {
	rawMessageID = strings.TrimSpace(rawMessageID)
	if rawMessageID == "" {
		writeError(w, http.StatusBadRequest, "message_id is required")
		return pgtype.UUID{}, pgtype.UUID{}, pgtype.UUID{}, false
	}
	messageID, ok := parseUUIDOrBadRequest(w, rawMessageID, "message_id")
	if !ok {
		return pgtype.UUID{}, pgtype.UUID{}, pgtype.UUID{}, false
	}
	var channelID, threadRootID pgtype.UUID
	if err := h.DB.QueryRow(ctx, `
		SELECT channel_id, thread_root_message_id
		FROM channel_message
		WHERE id = $1 AND workspace_id = $2 AND deleted_at IS NULL`,
		messageID, origin.workspaceID).Scan(&channelID, &threadRootID); err != nil {
		writeError(w, http.StatusNotFound, "anchor message not found")
		return pgtype.UUID{}, pgtype.UUID{}, pgtype.UUID{}, false
	}
	// Member or manager on the anchor channel (surface access = current
	// channel_member agent row). Not a member → 403, not 404 (message exists).
	if !h.agentHasSurfaceAccess(ctx, origin.workspaceID, origin.agentID, channelID) {
		writeError(w, http.StatusForbidden, "agent is not a member of the anchor channel")
		return pgtype.UUID{}, pgtype.UUID{}, pgtype.UUID{}, false
	}
	return messageID, channelID, threadRootID, true
}

func (h *Handler) resolveReminderID(w http.ResponseWriter, ctx context.Context, workspaceID, agentID pgtype.UUID, raw string) (pgtype.UUID, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		writeError(w, http.StatusBadRequest, "id is required")
		return pgtype.UUID{}, false
	}
	if parsed, err := util.ParseUUID(raw); err == nil && parsed.Valid {
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

func reminderTargetKind(threadRootID pgtype.UUID) string {
	if threadRootID.Valid {
		return "thread"
	}
	return "channel"
}

func (h *Handler) reminderOwnerRuntime(ctx context.Context, reminder agentReminder) (string, bool) {
	if h == nil || h.ReminderNotifier == nil {
		return "", false
	}
	var runtimeID pgtype.UUID
	if err := h.DB.QueryRow(ctx, `
		SELECT runtime_id
		FROM agent
		WHERE id = $1 AND workspace_id = $2 AND archived_at IS NULL`, reminder.AgentID, reminder.WorkspaceID).Scan(&runtimeID); err != nil || !runtimeID.Valid {
		return "", false
	}
	return uuidToString(runtimeID), true
}

func (h *Handler) projectReminderUpsert(ctx context.Context, reminder agentReminder) {
	if h == nil || h.ReminderNotifier == nil {
		return
	}
	event, err := h.latestReminderProjection(ctx, reminder.ID)
	if err != nil {
		return
	}
	// A timer projection is only meaningful after the daemon has learned the
	// authoritative owner generation. Re-publishing the current start is
	// idempotent for a healthy residency, repairs locally bootstrapped
	// generation-zero residencies, and causes the daemon to request a current
	// snapshot if the projection was already ACKed or arrives out of order.
	h.projectReminderOwnerStart(ctx, event.AgentID, event.RuntimeID)
	h.ReminderNotifier.NotifyReminderProjection(event.RuntimeID, event)
}

func (h *Handler) projectReminderCancel(ctx context.Context, reminder agentReminder) {
	h.publishLatestReminderProjection(ctx, reminder)
}

func scanReminderProjection(row rowScanner) (protocol.ReminderProjectionEvent, error) {
	var event protocol.ReminderProjectionEvent
	var fireAt pgtype.Timestamptz
	err := row.Scan(&event.Seq, &event.PrevSeq, &event.RuntimeID, &event.AgentID, &event.PlacementGeneration, &event.EventType, &event.ReminderID, &event.Version, &fireAt, &event.Terminal)
	if err != nil {
		return event, err
	}
	if fireAt.Valid {
		event.FireAt = fireAt.Time.UTC().Format(time.RFC3339Nano)
		event.Reminder = protocol.ReminderTimerJob{ReminderID: event.ReminderID, OwnerAgentID: event.AgentID, Version: event.Version, FireAt: event.FireAt}
	}
	return event, nil
}

func reminderProjectionSelectColumns() string {
	return `seq, prev_seq, runtime_id::text, agent_id::text, placement_generation, event_type, reminder_id::text, version, fire_at, terminal`
}

func (h *Handler) latestReminderProjection(ctx context.Context, reminderID pgtype.UUID) (protocol.ReminderProjectionEvent, error) {
	return scanReminderProjection(h.DB.QueryRow(ctx, `SELECT `+reminderProjectionSelectColumns()+`
		FROM agent_reminder_daemon_projection_event
		WHERE reminder_id = $1 ORDER BY seq DESC LIMIT 1`, reminderID))
}

func (h *Handler) publishLatestReminderProjection(ctx context.Context, reminder agentReminder) {
	if h == nil || h.ReminderNotifier == nil {
		return
	}
	event, err := h.latestReminderProjection(ctx, reminder.ID)
	if err != nil {
		return
	}
	h.ReminderNotifier.NotifyReminderProjection(event.RuntimeID, event)
}

type agentAttachmentCommand struct {
	agentID, runtimeID, workspaceID, daemonID, correlationID string
	attachmentGeneration, lifecycleSeq                       int64
}

func (h *Handler) latestAgentAttachmentCommand(ctx context.Context, agentID, runtimeID, eventType string) (agentAttachmentCommand, bool) {
	var command agentAttachmentCommand
	err := h.DB.QueryRow(ctx, `
		SELECT e.agent_id::text, e.runtime_id::text, e.workspace_id::text,
		       r.daemon_id::text, e.attachment_generation, e.lifecycle_seq,
		       e.correlation_id::text
		FROM agent_attachment_projection_event e
		JOIN agent_runtime r ON r.id = e.runtime_id
		WHERE e.agent_id::text = $1 AND e.runtime_id::text = $2 AND e.event_type = $3
		ORDER BY e.lifecycle_seq DESC LIMIT 1`, agentID, runtimeID, eventType).Scan(
		&command.agentID, &command.runtimeID, &command.workspaceID, &command.daemonID,
		&command.attachmentGeneration, &command.lifecycleSeq, &command.correlationID,
	)
	return command, err == nil
}

func (h *Handler) projectReminderOwnerStart(ctx context.Context, agentID, runtimeID string) {
	if h == nil || h.ReminderNotifier == nil {
		return
	}
	command, ok := h.latestAgentAttachmentCommand(ctx, agentID, runtimeID, "attach")
	if !ok {
		return
	}
	h.ReminderNotifier.NotifyAgentAttachmentAdded(command.workspaceID, command.daemonID, protocol.WorkspaceRunnerAgentAttachPayload{
		AgentID: command.agentID, RuntimeID: command.runtimeID, AttachmentGeneration: command.attachmentGeneration,
		LifecycleSeq: command.lifecycleSeq, CorrelationID: command.correlationID,
	})
}

func (h *Handler) projectReminderOwnerStop(ctx context.Context, agentID, runtimeID string) {
	if h == nil || h.ReminderNotifier == nil {
		return
	}
	command, ok := h.latestAgentAttachmentCommand(ctx, agentID, runtimeID, "detach")
	if !ok {
		return
	}
	h.ReminderNotifier.NotifyAgentAttachmentRemoved(command.workspaceID, command.daemonID, protocol.WorkspaceRunnerAgentDetachPayload{
		AgentID: command.agentID, RuntimeID: command.runtimeID, AttachmentGeneration: command.attachmentGeneration,
		LifecycleSeq: command.lifecycleSeq, CorrelationID: command.correlationID,
	})
}

type agentReminderChangedPayload struct {
	AgentID string `json:"agent_id"`
}

// publishAgentReminderChanged emits only the invalidation key needed by human
// clients. Task #908 retired agent.visibility — reminder-changed events
// broadcast workspace-wide like any other usage surface. Frank, 2026-07-31
// (Wendy DM incident, #prj-daemon): no agent — including the onboarding
// agent (Wendy) — gets owner-only scoping; every agent is usable by every
// workspace member.
func (h *Handler) publishAgentReminderChanged(ctx context.Context, workspaceID, agentID pgtype.UUID) {
	if h == nil || h.Bus == nil || !workspaceID.Valid || !agentID.Valid {
		return
	}
	payload := agentReminderChangedPayload{AgentID: uuidToString(agentID)}
	h.publish(protocol.EventAgentReminderChanged, uuidToString(workspaceID), "system", "", payload)
}

func daemonIdentityOwnsRuntime(identity daemonws.ClientIdentity, runtimeID pgtype.UUID) bool {
	if !runtimeID.Valid {
		return false
	}
	want := uuidToString(runtimeID)
	for _, candidate := range identity.RuntimeIDs {
		if candidate == want {
			return true
		}
	}
	return false
}

func daemonIdentityOwnsWorkspace(identity daemonws.ClientIdentity, workspaceID pgtype.UUID) bool {
	return strings.TrimSpace(identity.WorkspaceID) == "" || identity.WorkspaceID == uuidToString(workspaceID)
}

func (h *Handler) HandleDaemonReminderSnapshot(ctx context.Context, identity daemonws.ClientIdentity, payload protocol.ReminderSnapshotRequestPayload) (*protocol.ReminderSnapshotPayload, error) {
	agentID, err := util.ParseUUID(strings.TrimSpace(payload.AgentID))
	if err != nil || !agentID.Valid {
		return nil, fmt.Errorf("invalid reminder snapshot agent_id")
	}
	requestRuntimeID, err := util.ParseUUID(strings.TrimSpace(payload.RuntimeID))
	if err != nil || !requestRuntimeID.Valid || payload.PlacementGeneration < 0 || !daemonIdentityOwnsRuntime(identity, requestRuntimeID) {
		return nil, fmt.Errorf("invalid reminder snapshot placement")
	}
	tx, err := h.TxStarter.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	placementGeneration := payload.PlacementGeneration
	var workspaceID, runtimeID pgtype.UUID
	var archivedAt pgtype.Timestamptz
	if err := tx.QueryRow(ctx, `
		SELECT workspace_id, runtime_id, archived_at
		FROM agent
		WHERE id = $1
		FOR UPDATE`, agentID).Scan(&workspaceID, &runtimeID, &archivedAt); err != nil {
		if errorsIsNoRows(err) {
			if placementGeneration < 1 {
				placementGeneration = 1
			}
			return nil, &daemonws.ReminderOwnerGoneError{AgentID: payload.AgentID, RuntimeID: payload.RuntimeID, PlacementGeneration: placementGeneration}
		}
		return nil, err
	}
	if err := tx.QueryRow(ctx, `
		SELECT COALESCE(max(placement_generation), 0)
		FROM agent_reminder_daemon_owner_event
		WHERE agent_id = $1`, agentID).Scan(&placementGeneration); err != nil {
		return nil, err
	}
	if !daemonIdentityOwnsWorkspace(identity, workspaceID) {
		return nil, fmt.Errorf("reminder snapshot owner is outside daemon scope")
	}
	if runtimeID != requestRuntimeID || !daemonIdentityOwnsRuntime(identity, runtimeID) || archivedAt.Valid || payload.PlacementGeneration != placementGeneration {
		if placementGeneration < 1 {
			placementGeneration = 1
		}
		return nil, &daemonws.ReminderOwnerGoneError{AgentID: payload.AgentID, RuntimeID: payload.RuntimeID, PlacementGeneration: placementGeneration}
	}
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 210))`, uuidToString(runtimeID)); err != nil {
		return nil, err
	}
	var projectionWatermark int64
	if err := tx.QueryRow(ctx, `
		SELECT COALESCE((SELECT latest_seq FROM agent_reminder_daemon_projection_cursor WHERE runtime_id = $1), 0)`, runtimeID).Scan(&projectionWatermark); err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `SELECT `+reminderSelectColumns()+`
		FROM agent_reminder
		WHERE workspace_id = $1 AND agent_id = $2 AND status = 'scheduled'
		ORDER BY fire_at ASC, id ASC`, workspaceID, agentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	jobs := make([]protocol.ReminderTimerJob, 0)
	for rows.Next() {
		reminder, err := scanAgentReminder(rows)
		if err != nil {
			return nil, err
		}
		jobs = append(jobs, protocol.ReminderTimerJob{
			ReminderID:   uuidToString(reminder.ID),
			OwnerAgentID: payload.AgentID,
			Version:      reminder.Version,
			FireAt:       reminder.FireAt.Time.UTC().Format(time.RFC3339Nano),
		})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &protocol.ReminderSnapshotPayload{
		AgentID: payload.AgentID, RuntimeID: uuidToString(runtimeID), PlacementGeneration: placementGeneration,
		ProjectionWatermark: projectionWatermark, Reminders: jobs,
	}, nil
}

func (h *Handler) HandleDaemonReminderProjection(ctx context.Context, identity daemonws.ClientIdentity, payload protocol.ReminderProjectionRequestPayload) ([]protocol.ReminderProjectionEvent, protocol.ReminderProjectionReplayEndPayload, error) {
	allowed := make(map[string]bool, len(identity.RuntimeIDs))
	for _, runtimeID := range identity.RuntimeIDs {
		allowed[runtimeID] = true
	}
	events := make([]protocol.ReminderProjectionEvent, 0)
	endCursors := make(map[string]int64, len(payload.RuntimeCursors))
	resets := make(map[string]protocol.ReminderRuntimeReset)
	for runtimeID, required := range payload.RuntimeResetRequired {
		cursor, exists := payload.RuntimeCursors[runtimeID]
		if required && (!allowed[runtimeID] || !exists || cursor < 0) {
			return nil, protocol.ReminderProjectionReplayEndPayload{}, fmt.Errorf("reminder runtime reset outside daemon scope")
		}
	}
	for runtimeID, cursor := range payload.RuntimeCursors {
		if !allowed[runtimeID] || cursor < 0 {
			return nil, protocol.ReminderProjectionReplayEndPayload{}, fmt.Errorf("reminder projection cursor outside daemon scope")
		}
		var latest, ack int64
		if err := h.DB.QueryRow(ctx, `
			SELECT COALESCE((
			  SELECT cursor.latest_seq
			  FROM agent_reminder_daemon_projection_cursor cursor
			  JOIN agent_runtime runtime ON runtime.id = cursor.runtime_id
			  WHERE cursor.runtime_id::text = $1
			    AND ($2 = '' OR runtime.workspace_id::text = $2)
			), 0), COALESCE((
			  SELECT cursor.ack_seq
			  FROM agent_reminder_daemon_projection_cursor cursor
			  JOIN agent_runtime runtime ON runtime.id = cursor.runtime_id
			  WHERE cursor.runtime_id::text = $1
			    AND ($2 = '' OR runtime.workspace_id::text = $2)
			), 0)`, runtimeID, identity.WorkspaceID).Scan(&latest, &ack); err != nil {
			return nil, protocol.ReminderProjectionReplayEndPayload{}, err
		}
		if cursor > latest {
			return nil, protocol.ReminderProjectionReplayEndPayload{}, fmt.Errorf("reminder projection cursor exceeds server watermark")
		}
		forceReset := payload.RuntimeResetRequired[runtimeID]
		if cursor < ack || forceReset {
			reset, err := h.buildReminderRuntimeReset(ctx, identity, runtimeID, cursor, payload.RuntimeResidencies[runtimeID], forceReset)
			if err != nil {
				return nil, protocol.ReminderProjectionReplayEndPayload{}, err
			}
			resets[runtimeID] = reset
			endCursors[runtimeID] = reset.ProjectionWatermark
			continue
		}
		rows, err := h.DB.Query(ctx, `SELECT `+reminderProjectionSelectColumns()+`
			FROM agent_reminder_daemon_projection_event
			WHERE runtime_id::text = $1 AND seq > $2
			  AND ($3 = '' OR workspace_id::text = $3)
			ORDER BY seq ASC`, runtimeID, cursor, identity.WorkspaceID)
		if err != nil {
			return nil, protocol.ReminderProjectionReplayEndPayload{}, err
		}
		for rows.Next() {
			event, err := scanReminderProjection(rows)
			if err != nil {
				rows.Close()
				return nil, protocol.ReminderProjectionReplayEndPayload{}, err
			}
			events = append(events, event)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, protocol.ReminderProjectionReplayEndPayload{}, err
		}
		rows.Close()
		endCursors[runtimeID] = latest
	}
	return events, protocol.ReminderProjectionReplayEndPayload{RuntimeCursors: endCursors, RuntimeResets: resets}, nil
}

func (h *Handler) buildReminderRuntimeReset(ctx context.Context, identity daemonws.ClientIdentity, runtimeID string, cursor int64, residencies []protocol.ReminderRuntimeResidency, force bool) (protocol.ReminderRuntimeReset, error) {
	tx, err := h.TxStarter.Begin(ctx)
	if err != nil {
		return protocol.ReminderRuntimeReset{}, err
	}
	defer tx.Rollback(ctx)
	sort.Slice(residencies, func(i, j int) bool { return residencies[i].AgentID < residencies[j].AgentID })
	type lockedResidency struct {
		request          protocol.ReminderRuntimeResidency
		agentID          pgtype.UUID
		workspaceID      pgtype.UUID
		currentRuntimeID pgtype.UUID
		archivedAt       pgtype.Timestamptz
		exists           bool
	}
	seen := make(map[string]bool, len(residencies))
	locked := make([]lockedResidency, 0, len(residencies))
	for _, residency := range residencies {
		if residency.AgentID == "" || residency.PlacementGeneration < 1 || seen[residency.AgentID] {
			return protocol.ReminderRuntimeReset{}, fmt.Errorf("invalid reminder runtime reset residency")
		}
		seen[residency.AgentID] = true
		agentID, err := util.ParseUUID(residency.AgentID)
		if err != nil || !agentID.Valid {
			return protocol.ReminderRuntimeReset{}, fmt.Errorf("invalid reminder runtime reset agent")
		}
		entry := lockedResidency{request: residency, agentID: agentID}
		err = tx.QueryRow(ctx, `SELECT workspace_id, runtime_id, archived_at FROM agent WHERE id = $1 FOR UPDATE`, agentID).Scan(&entry.workspaceID, &entry.currentRuntimeID, &entry.archivedAt)
		if err != nil && !errorsIsNoRows(err) {
			return protocol.ReminderRuntimeReset{}, err
		}
		entry.exists = err == nil
		locked = append(locked, entry)
	}
	// Keep the established Agent -> runtime advisory order. Once every local
	// owner row is locked, the advisory lock makes the definitions read and
	// projection watermark one indivisible order-domain boundary.
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 210))`, runtimeID); err != nil {
		return protocol.ReminderRuntimeReset{}, err
	}
	owners := make([]protocol.ReminderRuntimeResetOwner, 0, len(locked))
	for _, entry := range locked {
		residency := entry.request
		owner := protocol.ReminderRuntimeResetOwner{AgentID: residency.AgentID, PlacementGeneration: residency.PlacementGeneration, Terminal: true}
		var currentGeneration int64
		if entry.exists {
			if err := tx.QueryRow(ctx, `SELECT COALESCE(max(placement_generation), 0) FROM agent_reminder_daemon_owner_event WHERE agent_id = $1`, entry.agentID).Scan(&currentGeneration); err != nil {
				return protocol.ReminderRuntimeReset{}, err
			}
			if currentGeneration > owner.PlacementGeneration {
				owner.PlacementGeneration = currentGeneration
			}
			owner.Terminal = entry.archivedAt.Valid || uuidToString(entry.currentRuntimeID) != runtimeID || currentGeneration != residency.PlacementGeneration || !daemonIdentityOwnsWorkspace(identity, entry.workspaceID)
			if !owner.Terminal {
				rows, err := tx.Query(ctx, `SELECT `+reminderSelectColumns()+` FROM agent_reminder WHERE workspace_id = $1 AND agent_id = $2 AND status = 'scheduled' ORDER BY fire_at ASC, id ASC`, entry.workspaceID, entry.agentID)
				if err != nil {
					return protocol.ReminderRuntimeReset{}, err
				}
				for rows.Next() {
					reminder, err := scanAgentReminder(rows)
					if err != nil {
						rows.Close()
						return protocol.ReminderRuntimeReset{}, err
					}
					owner.Reminders = append(owner.Reminders, protocol.ReminderTimerJob{ReminderID: uuidToString(reminder.ID), OwnerAgentID: residency.AgentID, Version: reminder.Version, FireAt: reminder.FireAt.Time.UTC().Format(time.RFC3339Nano)})
				}
				if err := rows.Err(); err != nil {
					rows.Close()
					return protocol.ReminderRuntimeReset{}, err
				}
				rows.Close()
			}
		}
		owners = append(owners, owner)
	}
	var watermark, ack int64
	if err := tx.QueryRow(ctx, `
		SELECT COALESCE(cursor.latest_seq, 0), COALESCE(cursor.ack_seq, 0)
		FROM agent_runtime runtime
		LEFT JOIN agent_reminder_daemon_projection_cursor cursor ON cursor.runtime_id = runtime.id
		WHERE runtime.id::text = $1 AND ($2 = '' OR runtime.workspace_id::text = $2)`, runtimeID, identity.WorkspaceID).Scan(&watermark, &ack); err != nil {
		return protocol.ReminderRuntimeReset{}, err
	}
	if !force && cursor >= ack {
		return protocol.ReminderRuntimeReset{}, fmt.Errorf("reminder runtime reset no longer required")
	}
	if err := tx.Commit(ctx); err != nil {
		return protocol.ReminderRuntimeReset{}, err
	}
	return protocol.ReminderRuntimeReset{ProjectionWatermark: watermark, Owners: owners}, nil
}

func (h *Handler) HandleDaemonReminderProjectionAck(ctx context.Context, identity daemonws.ClientIdentity, payload protocol.ReminderProjectionAckPayload) error {
	allowed := make(map[string]bool, len(identity.RuntimeIDs))
	for _, runtimeID := range identity.RuntimeIDs {
		allowed[runtimeID] = true
	}
	for runtimeID, seq := range payload.RuntimeCursors {
		if !allowed[runtimeID] || seq < 0 {
			return fmt.Errorf("reminder projection ack outside daemon scope")
		}
		tx, err := h.TxStarter.Begin(ctx)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 210))`, runtimeID); err != nil {
			_ = tx.Rollback(ctx)
			return err
		}
		result, err := tx.Exec(ctx, `
			INSERT INTO agent_reminder_daemon_projection_cursor (runtime_id, latest_seq, ack_seq)
			SELECT runtime.id, COALESCE(cursor.latest_seq, 0), $2
			FROM agent_runtime runtime
			LEFT JOIN agent_reminder_daemon_projection_cursor cursor ON cursor.runtime_id = runtime.id
			WHERE runtime.id::text = $1
			  AND ($3 = '' OR runtime.workspace_id::text = $3)
			  AND $2 <= COALESCE(cursor.latest_seq, 0)
			ON CONFLICT (runtime_id) DO UPDATE
			SET ack_seq = GREATEST(agent_reminder_daemon_projection_cursor.ack_seq, $2),
			    updated_at = now()
			WHERE $2 <= agent_reminder_daemon_projection_cursor.latest_seq`, runtimeID, seq, identity.WorkspaceID)
		if err != nil || result.RowsAffected() != 1 {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("invalid reminder projection ack")
		}
		if _, err := tx.Exec(ctx, `DELETE FROM agent_reminder_daemon_projection_event WHERE runtime_id::text = $1 AND seq <= $2`, runtimeID, seq); err != nil {
			_ = tx.Rollback(ctx)
			return err
		}
		if err := tx.Commit(ctx); err != nil {
			return err
		}
	}
	return nil
}

func (h *Handler) enqueueReminderFireResultTx(ctx context.Context, tx pgx.Tx, runtimeID, workspaceID, agentID, reminderID pgtype.UUID, placementGeneration, version int64, fireAt pgtype.Timestamptz, terminal bool) (protocol.ReminderProjectionEvent, error) {
	var seq int64
	var fireAtValue any
	if fireAt.Valid && !terminal {
		fireAtValue = fireAt
	}
	if err := tx.QueryRow(ctx, `SELECT enqueue_agent_reminder_daemon_projection($1,$2,$3,$4,$5,'fire_result',$6,$7,$8)`,
		runtimeID, workspaceID, agentID, reminderID, placementGeneration, version, fireAtValue, terminal).Scan(&seq); err != nil {
		return protocol.ReminderProjectionEvent{}, err
	}
	return scanReminderProjection(tx.QueryRow(ctx, `SELECT `+reminderProjectionSelectColumns()+` FROM agent_reminder_daemon_projection_event WHERE seq = $1`, seq))
}

func (h *Handler) HandleDaemonReminderFireAttempt(ctx context.Context, identity daemonws.ClientIdentity, payload protocol.ReminderFireAttemptPayload) (*protocol.ReminderFireResultPayload, error) {
	agentID, err := util.ParseUUID(strings.TrimSpace(payload.AgentID))
	if err != nil || !agentID.Valid {
		return nil, fmt.Errorf("invalid reminder fire agent_id")
	}
	requestRuntimeID, err := util.ParseUUID(strings.TrimSpace(payload.RuntimeID))
	if err != nil || !requestRuntimeID.Valid || payload.PlacementGeneration < 1 || !daemonIdentityOwnsRuntime(identity, requestRuntimeID) {
		return nil, fmt.Errorf("invalid reminder fire placement")
	}
	reminderID, err := util.ParseUUID(strings.TrimSpace(payload.ReminderID))
	if err != nil || !reminderID.Valid || payload.Version < 1 {
		return nil, fmt.Errorf("invalid reminder fire payload")
	}
	if raw := strings.TrimSpace(payload.FiredAtClient); raw != "" {
		if _, err := time.Parse(time.RFC3339Nano, raw); err != nil {
			return nil, fmt.Errorf("invalid reminder fire client timestamp")
		}
	}
	preliminary, preliminaryErr := scanAgentReminder(h.DB.QueryRow(ctx, `SELECT `+reminderSelectColumns()+` FROM agent_reminder WHERE id = $1`, reminderID))
	if preliminaryErr != nil && !errorsIsNoRows(preliminaryErr) {
		return nil, preliminaryErr
	}
	tx, err := h.TxStarter.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	// Keep the established channel -> agent -> reminder lock prefix. Agent
	// archive/move owns the agent row before its trigger touches definitions;
	// taking channel first also remains compatible with onboarding eligibility.
	if preliminaryErr == nil && preliminary.AnchorChannelID.Valid {
		var ignored pgtype.UUID
		if err := tx.QueryRow(ctx, `SELECT id FROM channel WHERE id = $1 AND workspace_id = $2 FOR UPDATE`, preliminary.AnchorChannelID, preliminary.WorkspaceID).Scan(&ignored); err != nil && !errorsIsNoRows(err) {
			return nil, err
		}
	}
	var runtimeID, workspaceID pgtype.UUID
	var archivedAt pgtype.Timestamptz
	if err := tx.QueryRow(ctx, `SELECT runtime_id, workspace_id, archived_at FROM agent WHERE id = $1 FOR UPDATE`, agentID).Scan(&runtimeID, &workspaceID, &archivedAt); err != nil {
		if errorsIsNoRows(err) {
			return nil, &daemonws.ReminderOwnerGoneError{AgentID: payload.AgentID, RuntimeID: payload.RuntimeID, PlacementGeneration: payload.PlacementGeneration}
		}
		return nil, err
	}
	var placementGeneration int64
	if err := tx.QueryRow(ctx, `SELECT COALESCE(max(placement_generation), 0) FROM agent_reminder_daemon_owner_event WHERE agent_id = $1`, agentID).Scan(&placementGeneration); err != nil {
		return nil, err
	}
	if archivedAt.Valid || runtimeID != requestRuntimeID || payload.PlacementGeneration != placementGeneration || !daemonIdentityOwnsWorkspace(identity, workspaceID) {
		if placementGeneration < 1 {
			placementGeneration = 1
		}
		return nil, &daemonws.ReminderOwnerGoneError{AgentID: payload.AgentID, RuntimeID: payload.RuntimeID, PlacementGeneration: placementGeneration}
	}
	var fireCapable bool
	if err := tx.QueryRow(ctx, `
		SELECT COALESCE((metadata->'capabilities') @> '["reminder_versioned_cache_v1","reminder_transient_owner_input_v1"]'::jsonb, false)
		FROM agent_runtime
		WHERE id = $1 AND workspace_id = $2
		FOR SHARE`, runtimeID, workspaceID).Scan(&fireCapable); err != nil {
		return nil, err
	}
	if !fireCapable {
		return nil, errReminderDaemonOutdated
	}
	reminder, err := scanAgentReminder(tx.QueryRow(ctx, `SELECT `+reminderSelectColumns()+` FROM agent_reminder WHERE id = $1 FOR UPDATE`, reminderID))
	if err != nil {
		if errorsIsNoRows(err) {
			event, eventErr := h.enqueueReminderFireResultTx(ctx, tx, runtimeID, workspaceID, agentID, reminderID, placementGeneration, payload.Version, pgtype.Timestamptz{}, true)
			if eventErr != nil {
				return nil, eventErr
			}
			if err := tx.Commit(ctx); err != nil {
				return nil, err
			}
			return &protocol.ReminderFireResultPayload{Projection: event}, nil
		}
		return nil, err
	}
	if reminder.AgentID != agentID || reminder.WorkspaceID != workspaceID {
		return nil, fmt.Errorf("reminder fire owner mismatch")
	}
	if reminder.Version != payload.Version || reminder.Status != "scheduled" || reminder.FireAt.Time.After(time.Now().UTC().Add(5*time.Second)) {
		terminal := reminder.Status != "scheduled"
		event, eventErr := h.enqueueReminderFireResultTx(ctx, tx, runtimeID, workspaceID, agentID, reminderID, placementGeneration, reminder.Version, reminder.FireAt, terminal)
		if eventErr != nil {
			return nil, eventErr
		}
		if err := tx.Commit(ctx); err != nil {
			return nil, err
		}
		return &protocol.ReminderFireResultPayload{Projection: event}, nil
	}
	cadenceSlot := reminder.FireAt
	if reminder.CadenceNextAt.Valid {
		cadenceSlot = reminder.CadenceNextAt
	}
	var occurrenceID pgtype.UUID
	err = tx.QueryRow(ctx, `
		INSERT INTO agent_reminder_occurrence (
			reminder_id, workspace_id, agent_id, fire_version, cadence_scheduled_for, due_at,
			status, title_snapshot, cadence_snapshot, timezone_snapshot, claimed_at
		) VALUES ($1, $2, $3, $4, $5, $6, 'claimed', $7, $8, $9, now())
		ON CONFLICT (reminder_id, fire_version) DO NOTHING
		RETURNING id`, reminder.ID, reminder.WorkspaceID, reminder.AgentID,
		payload.Version, cadenceSlot, reminder.FireAt, reminder.Title, nullableText(reminder.Cadence),
		reminderTimezoneValue(reminder.Cadence, reminder.ScheduleTimezone)).Scan(&occurrenceID)
	if errorsIsNoRows(err) {
		event, eventErr := h.enqueueReminderFireResultTx(ctx, tx, runtimeID, workspaceID, agentID, reminderID, placementGeneration, reminder.Version, reminder.FireAt, false)
		if eventErr != nil {
			return nil, eventErr
		}
		if err := tx.Commit(ctx); err != nil {
			return nil, err
		}
		return &protocol.ReminderFireResultPayload{Projection: event}, nil
	}
	if err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE agent_reminder
		SET status = 'firing', current_occurrence_id = $2, updated_at = now()
		WHERE id = $1 AND status = 'scheduled' AND version = $3`, reminder.ID, occurrenceID, payload.Version); err != nil {
		return nil, err
	}
	reminder.Status = "firing"
	reminder.CurrentOccurrenceID = occurrenceID
	committedFire, err := h.fireReminderOccurrenceWithTx(ctx, tx, reminder, occurrenceID, runtimeID, placementGeneration, payload.Version)
	if err != nil {
		return nil, err
	}
	if committedFire != nil {
		if h.ReminderOwnerInputNotifier != nil {
			h.ReminderOwnerInputNotifier.NotifyReminderOwnerInput(uuidToString(workspaceID), identity.DaemonID, committedFire.OwnerInput)
		}
		h.publishAgentReminderChanged(ctx, committedFire.Reminder.WorkspaceID, committedFire.Reminder.AgentID)
		if committedFire.Reminder.Status == "scheduled" {
			h.projectReminderUpsert(ctx, committedFire.Reminder)
		} else {
			h.projectReminderCancel(ctx, committedFire.Reminder)
		}
	}
	event, err := h.latestReminderProjection(ctx, reminder.ID)
	if err != nil {
		return nil, err
	}
	return &protocol.ReminderFireResultPayload{Projection: event}, nil
}

func (h *Handler) fireReminderOccurrenceWithTx(ctx context.Context, tx pgx.Tx, reminder agentReminder, occurrenceID, runtimeID pgtype.UUID, placementGeneration, fireVersion int64) (*reminderFireCommit, error) {
	var occurrenceStatus string
	var cadenceSlot pgtype.Timestamptz
	var persistedFireVersion int64
	if err := tx.QueryRow(ctx, `
		SELECT status, cadence_scheduled_for, fire_version
		FROM agent_reminder_occurrence
		WHERE id = $1 AND reminder_id = $2
		FOR UPDATE`, occurrenceID, reminder.ID).Scan(&occurrenceStatus, &cadenceSlot, &persistedFireVersion); err != nil {
		return nil, err
	}
	if occurrenceStatus == "fired" || occurrenceStatus == "cancelled" {
		return nil, tx.Commit(ctx)
	}
	if persistedFireVersion != fireVersion {
		return nil, fmt.Errorf("reminder occurrence fire version mismatch")
	}

	var channelName, channelKind string
	var channelArchivedAt pgtype.Timestamptz
	if err := tx.QueryRow(ctx, `
		SELECT name, kind, archived_at
		FROM channel
		WHERE id = $1 AND workspace_id = $2
		FOR UPDATE`, reminder.AnchorChannelID, reminder.WorkspaceID).Scan(&channelName, &channelKind, &channelArchivedAt); err != nil {
		if errorsIsNoRows(err) {
			return nil, h.terminalizeReminderOccurrence(ctx, tx, reminder, occurrenceID, "channel_deleted")
		}
		return nil, err
	}
	if channelArchivedAt.Valid {
		return nil, h.terminalizeReminderOccurrence(ctx, tx, reminder, occurrenceID, "channel_archived")
	}

	var agentRuntimeID pgtype.UUID
	var agentArchivedAt pgtype.Timestamptz
	if err := tx.QueryRow(ctx, `
		SELECT runtime_id, archived_at
		FROM agent
		WHERE id = $1 AND workspace_id = $2
		FOR UPDATE`, reminder.AgentID, reminder.WorkspaceID).Scan(&agentRuntimeID, &agentArchivedAt); err != nil {
		if errorsIsNoRows(err) {
			return nil, h.terminalizeReminderOccurrence(ctx, tx, reminder, occurrenceID, "agent_deleted")
		}
		return nil, err
	}
	if agentArchivedAt.Valid {
		return nil, h.terminalizeReminderOccurrence(ctx, tx, reminder, occurrenceID, "agent_archived")
	}
	if !agentRuntimeID.Valid || agentRuntimeID != runtimeID {
		return nil, h.terminalizeReminderOccurrence(ctx, tx, reminder, occurrenceID, "agent_runtime_unavailable")
	}
	// Lock eligibility in the fixed channel -> agent -> membership order and hold
	// every row through the same transaction that creates the occurrence and wake.
	// Non-SKIP channel-onboarding eligibility uses the same prefix before locking
	// onboarding, while the membership DELETE trigger uses the compatible
	// membership -> onboarding suffix. Either fire commits the complete occurrence
	// first, or an eligibility write wins and this transaction terminalizes with
	// no task or wake.
	var memberID pgtype.UUID
	if err := tx.QueryRow(ctx, `
		SELECT member_id
		FROM channel_member
		WHERE channel_id = $1 AND workspace_id = $2
		  AND member_type = 'agent' AND member_id = $3
		FOR UPDATE`, reminder.AnchorChannelID, reminder.WorkspaceID, reminder.AgentID).Scan(&memberID); err != nil {
		if errorsIsNoRows(err) {
			return nil, h.terminalizeReminderOccurrence(ctx, tx, reminder, occurrenceID, "agent_removed_from_anchor_channel")
		}
		return nil, err
	}
	ch := ChannelResponse{ID: uuidToString(reminder.AnchorChannelID), WorkspaceID: uuidToString(reminder.WorkspaceID), Name: channelName, Kind: channelKind}

	anchor, err := loadAuthorizedReminderAnchor(ctx, tx, reminder, pgtype.UUID{})
	if err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE agent_reminder_occurrence
		SET status = 'fired', fired_at = now(), updated_at = now()
		WHERE id = $1 AND status IN ('pending', 'claimed')`, occurrenceID); err != nil {
		return nil, err
	}

	resultingState := "fired"
	var nextFireAt any
	var lifecycleReason any
	lifecycleDetails := any([]byte(`{}`))
	if reminder.Cadence.Valid {
		cadence, parseErr := parseReminderCadence(reminder.Cadence.String, reminderCadenceTimezone(reminder))
		if parseErr != nil {
			return nil, parseErr
		}
		next, nextErr := nextReminderCadenceAfterSlot(cadence, cadenceSlot.Time, time.Now().UTC())
		if nextErr != nil {
			return nil, nextErr
		}
		resultingState = "scheduled"
		nextFireAt = next
		if err := tx.QueryRow(ctx, `
			UPDATE agent_reminder
			SET status = 'scheduled', fire_at = $2, cadence_next_at = $2,
			    current_occurrence_id = NULL, fired_at = now(),
			    version = version + 1, updated_at = now()
			WHERE id = $1 AND status = 'firing' AND current_occurrence_id = $3
			RETURNING version`, reminder.ID, next, occurrenceID).Scan(&reminder.Version); err != nil {
			return nil, err
		}
	} else {
		if err := tx.QueryRow(ctx, `
			UPDATE agent_reminder
			SET status = 'fired', current_occurrence_id = NULL, fired_at = now(),
			    version = version + 1, updated_at = now()
			WHERE id = $1 AND status = 'firing' AND current_occurrence_id = $2
			RETURNING version`, reminder.ID, occurrenceID).Scan(&reminder.Version); err != nil {
			return nil, err
		}
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO agent_reminder_lifecycle_event (
			reminder_id, workspace_id, agent_id, occurrence_id, event_type,
			actor_type, previous_fire_at, next_fire_at, title_snapshot,
			cadence_snapshot, timezone_snapshot, resulting_state, reason_code,
			details
		) VALUES ($1, $2, $3, $4, 'fired', 'system', $5, $6, $7, $8, $9, $10, $11, $12)`,
		reminder.ID, reminder.WorkspaceID, reminder.AgentID, occurrenceID, reminder.FireAt,
		nextFireAt, reminder.Title, nullableText(reminder.Cadence),
		reminderTimezoneValue(reminder.Cadence, reminder.ScheduleTimezone),
		resultingState, lifecycleReason, lifecycleDetails); err != nil {
		return nil, err
	}
	ownerInput := protocol.ReminderOwnerInputPayload{
		WorkspaceID:         uuidToString(reminder.WorkspaceID),
		AgentID:             uuidToString(reminder.AgentID),
		RuntimeID:           uuidToString(runtimeID),
		PlacementGeneration: placementGeneration,
		ReminderID:          uuidToString(reminder.ID),
		Version:             fireVersion,
		Title:               reminder.Title,
		Occurrence: protocol.ReminderOwnerInputOccurrence{
			OccurrenceID: uuidToString(occurrenceID),
			ScheduledFor: cadenceSlot.Time.UTC().Format(time.RFC3339Nano),
			DueAt:        reminder.FireAt.Time.UTC().Format(time.RFC3339Nano),
		},
	}
	if reminder.Cadence.Valid {
		ownerInput.Occurrence.Cadence = reminder.Cadence.String
	}
	if reminder.ScheduleTimezone.Valid {
		ownerInput.Occurrence.Timezone = reminder.ScheduleTimezone.String
	}
	if anchor.Available {
		message := ChannelMessageResponse{
			ID:                  anchor.MessageID,
			ChannelID:           ch.ID,
			WorkspaceID:         ch.WorkspaceID,
			ThreadRootMessageID: uuidPtr(reminder.AnchorThreadRootMessageID),
		}
		replyTarget, replyErr := canonicalMessageReplyTarget(ctx, tx, ch, message, reminder.AgentID)
		if replyErr != nil {
			return nil, replyErr
		}
		ownerInput.Anchor = protocol.ReminderOwnerInputAnchor{
			Available:           true,
			ChannelID:           ch.ID,
			MessageID:           anchor.MessageID,
			ThreadRootMessageID: stringValue(message.ThreadRootMessageID),
			Target:              canonicalMessageDeliveryTarget(ch, message),
			ReplyTarget:         replyTarget,
			Excerpt:             truncateReminderOwnerInputExcerpt(anchor.Content),
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	if resultingState == "scheduled" {
		reminder.Status = "scheduled"
		reminder.FireAt = pgtype.Timestamptz{Time: nextFireAt.(time.Time), Valid: true}
	} else {
		reminder.Status = "fired"
	}
	return &reminderFireCommit{Reminder: reminder, OwnerInput: ownerInput}, nil
}

// loadAuthorizedReminderAnchor is the single availability and authorization
// predicate for both private owner input and the human read projection. A null
// viewerUserID selects the already-locked owner-input path; a valid viewer also
// requires current user membership on the same surface.
func loadAuthorizedReminderAnchor(ctx context.Context, q reminderQueryRower, reminder agentReminder, viewerUserID pgtype.UUID) (authorizedReminderAnchor, error) {
	if !reminder.WorkspaceID.Valid || !reminder.AgentID.Valid || !reminder.AnchorChannelID.Valid || !reminder.AnchorMessageID.Valid {
		return authorizedReminderAnchor{}, nil
	}
	var anchor authorizedReminderAnchor
	err := q.QueryRow(ctx, `
		SELECT anchor.id::text, anchor.content, ch.name, ch.kind, ws.slug,
		       COALESCE((
		         SELECT COALESCE(NULLIF(u.display_name, ''), NULLIF(u.name, ''), u.email,
		                         NULLIF(peer_agent.display_name, ''), peer_agent.name, '')
		         FROM channel_member peer
		         LEFT JOIN "user" u
		           ON peer.member_type = 'user' AND u.id = peer.member_id
		         LEFT JOIN agent peer_agent
		           ON peer.member_type = 'agent' AND peer_agent.id = peer.member_id
		         WHERE peer.channel_id = ch.id
		           AND peer.workspace_id = ch.workspace_id
		           AND NOT (peer.member_type = 'user' AND peer.member_id = $6)
		         ORDER BY peer.created_at ASC
		         LIMIT 1
		       ), '')
		FROM channel_message anchor
		JOIN channel ch
		  ON ch.id = anchor.channel_id
		 AND ch.workspace_id = anchor.workspace_id
		 AND ch.archived_at IS NULL
		JOIN workspace ws ON ws.id = anchor.workspace_id
		JOIN agent owner_agent
		  ON owner_agent.id = $5
		 AND owner_agent.workspace_id = anchor.workspace_id
		 AND owner_agent.archived_at IS NULL
		WHERE anchor.id = $1
		  AND anchor.channel_id = $2
		  AND anchor.workspace_id = $3
		  AND anchor.deleted_at IS NULL
		  AND anchor.thread_root_message_id IS NOT DISTINCT FROM $4::uuid
		  AND ($4::uuid IS NULL OR EXISTS (
		      SELECT 1
		      FROM channel_message root
		      WHERE root.id = $4
		        AND root.channel_id = $2
		        AND root.workspace_id = $3
		        AND root.deleted_at IS NULL
		  ))
		  AND EXISTS (
		    SELECT 1
		    FROM channel_member owner_membership
		    WHERE owner_membership.channel_id = $2
		      AND owner_membership.workspace_id = $3
		      AND owner_membership.member_type = 'agent'
		      AND owner_membership.member_id = $5
		  )
		  AND ($6::uuid IS NULL OR EXISTS (
		    SELECT 1
		    FROM channel_member viewer_membership
		    WHERE viewer_membership.channel_id = $2
		      AND viewer_membership.workspace_id = $3
		      AND viewer_membership.member_type = 'user'
		      AND viewer_membership.member_id = $6
		  ))`, reminder.AnchorMessageID, reminder.AnchorChannelID, reminder.WorkspaceID,
		reminder.AnchorThreadRootMessageID, reminder.AgentID, viewerUserID).Scan(
		&anchor.MessageID, &anchor.Content, &anchor.ChannelName, &anchor.ChannelKind,
		&anchor.WorkspaceSlug, &anchor.DMPeerDisplay)
	if err != nil {
		if errorsIsNoRows(err) {
			return authorizedReminderAnchor{}, nil
		}
		return authorizedReminderAnchor{}, err
	}
	anchor.Available = true
	return anchor, nil
}

func (h *Handler) terminalizeReminderOccurrence(ctx context.Context, tx pgx.Tx, reminder agentReminder, occurrenceID pgtype.UUID, reason string) error {
	if _, err := tx.Exec(ctx, `
		UPDATE agent_reminder_occurrence
		SET status = 'cancelled', terminal_reason = $2, updated_at = now()
		WHERE id = $1 AND status IN ('pending', 'claimed')`, occurrenceID, reason); err != nil {
		return err
	}
	if err := tx.QueryRow(ctx, `
		UPDATE agent_reminder
		SET status = 'cancelled', terminal_reason = $2, current_occurrence_id = NULL,
		    version = version + 1, updated_at = now()
		WHERE id = $1
		RETURNING version`, reminder.ID, reason).Scan(&reminder.Version); err != nil {
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
	reminder.Status = "cancelled"
	h.projectReminderCancel(ctx, reminder)
	return nil
}

func reminderCadenceTimezone(reminder agentReminder) string {
	if !reminder.Cadence.Valid || (!strings.HasPrefix(reminder.Cadence.String, "daily@") && !strings.HasPrefix(reminder.Cadence.String, "weekly:")) {
		return ""
	}
	return reminder.ScheduleTimezone.String
}
