package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
	"github.com/multica-ai/multica/server/pkg/redact"
)

const (
	agentActivityDefaultLimit = 30
	agentActivityMaxLimit     = 100
	agentActivityStepLimit    = 50
	agentActivityMaxStepLimit = 200

	agentActivityVisibleSummary = "summary"
	agentActivityVisibleDetail  = "detail"

	agentActivityRealtimeStepPayload = "AgentActivityStep"

	agentActivityTriggerAssign       = "assign"
	agentActivityTriggerAmbient      = "ambient"
	agentActivityTriggerAutopilot    = "autopilot"
	agentActivityTriggerContinuation = "continuation"
	agentActivityTriggerDM           = "dm"
	agentActivityTriggerIssue        = "issue"
	agentActivityTriggerManual       = "manual"
	agentActivityTriggerMention      = "mention"
	agentActivityTriggerQuickCreate  = "quick_create"
	agentActivityTriggerTimeTrigger  = "time_trigger"
	agentActivityTriggerUnknown      = "unknown"
)

type AgentActivityCursor struct {
	CreatedAt string `json:"created_at"`
	Kind      string `json:"kind"`
	ID        string `json:"id"`
}

type AgentActivityStepCursor struct {
	Seq int32 `json:"seq"`
}

type AgentActivityTargetRef struct {
	Kind string  `json:"kind"`
	ID   *string `json:"id,omitempty"`
	Slug *string `json:"slug,omitempty"`
}

type AgentActivityRunSummary struct {
	Status      string                  `json:"status"`
	CreatedAt   string                  `json:"created_at"`
	StartedAt   *string                 `json:"started_at,omitempty"`
	EndedAt     *string                 `json:"ended_at,omitempty"`
	DurationMS  *int64                  `json:"duration_ms,omitempty"`
	Trigger     AgentActivityTrigger    `json:"trigger"`
	Result      AgentActivityRunResult  `json:"result"`
	Tokens      []AgentActivityTokenUse `json:"tokens,omitempty"`
	StepCount   int64                   `json:"step_count"`
	ResultState *string                 `json:"result_state,omitempty"`
}

type AgentActivityTrigger struct {
	Kind    string  `json:"kind"`
	Summary *string `json:"summary,omitempty"`
}

type AgentActivityTokenUse struct {
	Provider         string `json:"provider"`
	Model            string `json:"model"`
	InputTokens      int64  `json:"input_tokens"`
	OutputTokens     int64  `json:"output_tokens"`
	CacheReadTokens  int64  `json:"cache_read_tokens"`
	CacheWriteTokens int64  `json:"cache_write_tokens"`
}

type AgentActivityRunResult struct {
	Action                 *string                  `json:"action,omitempty"`
	MessageRef             *AgentActivityMessageRef `json:"message_ref,omitempty"`
	OutputSuppressedReason *string                  `json:"output_suppressed_reason,omitempty"`
}

type AgentActivityMessageRef struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
}

type AgentActivityEventSummary struct {
	EventType  string         `json:"event_type"`
	Severity   string         `json:"severity"`
	ReasonCode string         `json:"reason_code,omitempty"`
	Message    *string        `json:"message,omitempty"`
	Details    map[string]any `json:"details,omitempty"`
}

type AgentActivityTimelineEvent struct {
	ID         string                   `json:"id"`
	AgentID    string                   `json:"agent_id"`
	RuntimeID  *string                  `json:"runtime_id,omitempty"`
	TaskID     *string                  `json:"task_id,omitempty"`
	Kind       string                   `json:"kind"`
	EventType  string                   `json:"event_type"`
	OccurredAt string                   `json:"occurred_at"`
	Visibility string                   `json:"visibility"`
	Text       *string                  `json:"text,omitempty"`
	Tool       *string                  `json:"tool,omitempty"`
	ToolTarget *string                  `json:"tool_target,omitempty"`
	Status     *string                  `json:"status,omitempty"`
	ReasonCode string                   `json:"reason_code,omitempty"`
	TargetRef  AgentActivityTargetRef   `json:"target_ref"`
	SourceRefs []AgentActivitySourceRef `json:"source_refs,omitempty"`
}

type AgentActivitySourceRef struct {
	Kind string `json:"kind"`
	ID   string `json:"id,omitempty"`
	Seq  *int64 `json:"seq,omitempty"`
}

type AgentActivityEventsPageResponse struct {
	Events     []AgentActivityTimelineEvent  `json:"events"`
	Limit      int                           `json:"limit"`
	HasMore    bool                          `json:"has_more"`
	NextCursor *AgentActivityCursor          `json:"next_cursor,omitempty"`
	Realtime   AgentActivityRealtimeContract `json:"realtime"`
}

type AgentActivityEventRealtimePayload struct {
	AgentID string                      `json:"agent_id"`
	EventID string                      `json:"event_id"`
	Event   *AgentActivityTimelineEvent `json:"event,omitempty"`
}

type AgentActivityRealtimeContract struct {
	Scope     string `json:"scope"`
	ID        string `json:"id"`
	EventType string `json:"event_type"`
	Payload   string `json:"payload"`
}

type AgentActivityItem struct {
	ID           string                         `json:"id"`
	Kind         string                         `json:"kind"`
	AgentID      string                         `json:"agent_id"`
	RuntimeID    *string                        `json:"runtime_id,omitempty"`
	CreatedAt    string                         `json:"created_at"`
	TargetRef    AgentActivityTargetRef         `json:"target_ref"`
	VisibleLevel string                         `json:"visible_level"`
	Run          *AgentActivityRunSummary       `json:"run,omitempty"`
	Event        *AgentActivityEventSummary     `json:"event,omitempty"`
	Realtime     *AgentActivityRealtimeContract `json:"realtime,omitempty"`
}

type AgentActivityPageResponse struct {
	Activities []AgentActivityItem  `json:"activities"`
	Limit      int                  `json:"limit"`
	HasMore    bool                 `json:"has_more"`
	NextCursor *AgentActivityCursor `json:"next_cursor,omitempty"`
}

type AgentActivityDetailResponse struct {
	Activity AgentActivityItem `json:"activity"`
}

type AgentActivityStep struct {
	ID         string         `json:"id"`
	ActivityID string         `json:"activity_id"`
	Seq        int32          `json:"seq"`
	StepType   string         `json:"step_type"`
	Tool       *string        `json:"tool,omitempty"`
	CreatedAt  string         `json:"created_at"`
	Payload    map[string]any `json:"payload,omitempty"`
}

type AgentActivityStepPageResponse struct {
	Steps      []AgentActivityStep           `json:"steps"`
	Limit      int                           `json:"limit"`
	HasMore    bool                          `json:"has_more"`
	NextCursor *AgentActivityStepCursor      `json:"next_cursor,omitempty"`
	Realtime   AgentActivityRealtimeContract `json:"realtime"`
}

type AgentActivityDiagnosticResponse struct {
	ActivityID   string                 `json:"activity_id"`
	Kind         string                 `json:"kind"`
	VisibleLevel string                 `json:"visible_level"`
	TargetRef    AgentActivityTargetRef `json:"target_ref"`
	Diagnostic   map[string]any         `json:"diagnostic"`
}

type agentActivityRequestContext struct {
	workspaceID string
	userID      string
	member      db.Member
	agent       db.Agent
}

type agentActivityRawRow struct {
	Kind              string
	ID                pgtype.UUID
	AgentID           pgtype.UUID
	RuntimeID         pgtype.UUID
	EventTaskID       pgtype.UUID
	IssueID           pgtype.UUID
	ChatSessionID     pgtype.UUID
	EventType         pgtype.Text
	Severity          pgtype.Text
	TargetKind        pgtype.Text
	TargetID          pgtype.UUID
	TargetSlug        pgtype.Text
	ReasonCode        pgtype.Text
	Message           pgtype.Text
	Details           []byte
	Visibility        pgtype.Text
	Status            pgtype.Text
	TriggerSummary    pgtype.Text
	Result            []byte
	FailureReason     pgtype.Text
	CreatedAt         pgtype.Timestamptz
	StartedAt         pgtype.Timestamptz
	CompletedAt       pgtype.Timestamptz
	StepCount         int64
	UsageJSON         []byte
	ResultMessageID   pgtype.UUID
	ResultMessageKind pgtype.Text
}

type agentActivityStepRawRow struct {
	ID        pgtype.UUID
	TaskID    pgtype.UUID
	Seq       int32
	Type      string
	Tool      pgtype.Text
	Content   pgtype.Text
	Input     []byte
	Output    pgtype.Text
	CreatedAt pgtype.Timestamptz
}

func (h *Handler) ListAgentActivity(w http.ResponseWriter, r *http.Request) {
	reqCtx, ok := h.prepareAgentActivityRequest(w, r)
	if !ok {
		return
	}
	limit, ok := parseActivityLimit(w, r, agentActivityDefaultLimit, agentActivityMaxLimit)
	if !ok {
		return
	}
	beforeAt, beforeKind, beforeID, ok := parseActivityCursor(w, r)
	if !ok {
		return
	}

	rows, err := h.queryAgentActivityRows(r.Context(), reqCtx, beforeAt, beforeKind, beforeID, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list agent activity")
		return
	}

	items := make([]AgentActivityItem, 0, limit)
	var next *AgentActivityCursor
	for _, row := range rows {
		item, visible, err := h.agentActivityRowToItem(r.Context(), reqCtx, row)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to resolve agent activity visibility")
			return
		}
		if !visible {
			continue
		}
		if len(items) == limit {
			last := items[len(items)-1]
			next = &AgentActivityCursor{CreatedAt: last.CreatedAt, Kind: last.Kind, ID: last.ID}
			break
		}
		items = append(items, item)
	}

	writeJSON(w, http.StatusOK, AgentActivityPageResponse{
		Activities: items,
		Limit:      limit,
		HasMore:    next != nil,
		NextCursor: next,
	})
}

func (h *Handler) ListAgentActivityEvents(w http.ResponseWriter, r *http.Request) {
	reqCtx, ok := h.prepareAgentActivityRequest(w, r)
	if !ok {
		return
	}
	limit, ok := parseActivityLimit(w, r, agentActivityDefaultLimit, agentActivityMaxLimit)
	if !ok {
		return
	}
	beforeAt, beforeID, ok := parseActivityEventCursor(w, r)
	if !ok {
		return
	}

	rows, err := h.queryAgentActivityEventRows(r.Context(), reqCtx, beforeAt, beforeID, limit*3+1)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list agent activity events")
		return
	}

	events := make([]AgentActivityTimelineEvent, 0, limit)
	var next *AgentActivityCursor
	for _, row := range rows {
		targetRef, visible, err := h.agentTimelineTargetVisibility(r.Context(), reqCtx.workspaceID, reqCtx.userID, row)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to resolve agent activity visibility")
			return
		}
		if !visible {
			continue
		}
		if len(events) == limit {
			last := events[len(events)-1]
			next = &AgentActivityCursor{CreatedAt: last.OccurredAt, Kind: "event", ID: last.ID}
			break
		}
		events = append(events, agentActivityTimelineEvent(row, targetRef))
	}

	writeJSON(w, http.StatusOK, AgentActivityEventsPageResponse{
		Events:     events,
		Limit:      limit,
		HasMore:    next != nil,
		NextCursor: next,
		Realtime:   agentActivityEventRealtime(uuidToString(reqCtx.agent.ID)),
	})
}

func (h *Handler) GetAgentActivity(w http.ResponseWriter, r *http.Request) {
	reqCtx, ok := h.prepareAgentActivityRequest(w, r)
	if !ok {
		return
	}
	activityID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "activityId"), "activity id")
	if !ok {
		return
	}
	row, err := h.queryAgentActivityRow(r.Context(), reqCtx, activityID)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, "activity not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load agent activity")
		return
	}
	item, visible, err := h.agentActivityRowToItem(r.Context(), reqCtx, row)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to resolve agent activity visibility")
		return
	}
	if !visible {
		writeError(w, http.StatusNotFound, "activity not found")
		return
	}
	writeJSON(w, http.StatusOK, AgentActivityDetailResponse{Activity: item})
}

func (h *Handler) ListAgentActivitySteps(w http.ResponseWriter, r *http.Request) {
	reqCtx, ok := h.prepareAgentActivityRequest(w, r)
	if !ok {
		return
	}
	limit, ok := parseActivityLimit(w, r, agentActivityStepLimit, agentActivityMaxStepLimit)
	if !ok {
		return
	}
	afterSeq, ok := parseActivityAfterSeq(w, r)
	if !ok {
		return
	}
	activityID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "activityId"), "activity id")
	if !ok {
		return
	}
	row, err := h.queryAgentActivityRow(r.Context(), reqCtx, activityID)
	if errors.Is(err, pgx.ErrNoRows) || row.Kind != "run" {
		writeError(w, http.StatusNotFound, "activity not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load agent activity")
		return
	}
	_, visible, detailAllowed, err := h.agentActivityVisibility(r.Context(), reqCtx, row)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to resolve agent activity visibility")
		return
	}
	if !visible {
		writeError(w, http.StatusNotFound, "activity not found")
		return
	}
	if !detailAllowed {
		writeError(w, http.StatusForbidden, "insufficient permissions")
		return
	}

	steps, err := h.queryAgentActivitySteps(r.Context(), activityID, afterSeq, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list agent activity steps")
		return
	}
	respSteps := make([]AgentActivityStep, 0, min(len(steps), limit))
	for i, step := range steps {
		if i == limit {
			break
		}
		respSteps = append(respSteps, agentActivityStepToResponse(step))
	}
	var next *AgentActivityStepCursor
	if len(steps) > limit && len(respSteps) > 0 {
		next = &AgentActivityStepCursor{Seq: respSteps[len(respSteps)-1].Seq}
	}
	writeJSON(w, http.StatusOK, AgentActivityStepPageResponse{
		Steps:      respSteps,
		Limit:      limit,
		HasMore:    next != nil,
		NextCursor: next,
		Realtime:   agentActivityRealtime(uuidToString(activityID)),
	})
}

func (h *Handler) GetAgentActivityDiagnostic(w http.ResponseWriter, r *http.Request) {
	reqCtx, ok := h.prepareAgentActivityRequest(w, r)
	if !ok {
		return
	}
	activityID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "activityId"), "activity id")
	if !ok {
		return
	}
	row, err := h.queryAgentActivityRow(r.Context(), reqCtx, activityID)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, "activity not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load agent activity")
		return
	}
	targetRef, visible, detailAllowed, err := h.agentActivityVisibility(r.Context(), reqCtx, row)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to resolve agent activity visibility")
		return
	}
	if !visible {
		writeError(w, http.StatusNotFound, "activity not found")
		return
	}
	if !detailAllowed {
		writeError(w, http.StatusForbidden, "insufficient permissions")
		return
	}

	writeJSON(w, http.StatusOK, AgentActivityDiagnosticResponse{
		ActivityID:   uuidToString(row.ID),
		Kind:         row.Kind,
		VisibleLevel: agentActivityVisibleDetail,
		TargetRef:    targetRef,
		Diagnostic:   agentActivityDiagnostic(row),
	})
}

func (h *Handler) prepareAgentActivityRequest(w http.ResponseWriter, r *http.Request) (agentActivityRequestContext, bool) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return agentActivityRequestContext{}, false
	}
	workspaceID := ctxWorkspaceID(r.Context())
	if workspaceID == "" {
		workspaceID = h.resolveWorkspaceID(r)
	}
	if workspaceID == "" {
		writeError(w, http.StatusBadRequest, "workspace_id is required")
		return agentActivityRequestContext{}, false
	}
	agent, ok := h.loadAgentForUser(w, r, chi.URLParam(r, "id"))
	if !ok {
		return agentActivityRequestContext{}, false
	}
	member, ok := h.workspaceMember(w, r, workspaceID)
	if !ok {
		return agentActivityRequestContext{}, false
	}
	actorType, actorID := h.resolveActor(r, userID, workspaceID)
	if !h.canAccessPrivateAgent(r.Context(), agent, actorType, actorID, workspaceID) {
		writeError(w, http.StatusForbidden, "you do not have access to this agent")
		return agentActivityRequestContext{}, false
	}
	return agentActivityRequestContext{
		workspaceID: workspaceID,
		userID:      userID,
		member:      member,
		agent:       agent,
	}, true
}

func parseActivityLimit(w http.ResponseWriter, r *http.Request, defaultLimit, maxLimit int) (int, bool) {
	limit := defaultLimit
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed <= 0 {
			writeError(w, http.StatusBadRequest, "invalid limit")
			return 0, false
		}
		limit = parsed
	}
	if limit > maxLimit {
		limit = maxLimit
	}
	return limit, true
}

func parseActivityCursor(w http.ResponseWriter, r *http.Request) (pgtype.Timestamptz, pgtype.Text, pgtype.UUID, bool) {
	rawAt := strings.TrimSpace(r.URL.Query().Get("before_created_at"))
	rawKind := strings.TrimSpace(r.URL.Query().Get("before_kind"))
	rawID := strings.TrimSpace(r.URL.Query().Get("before_id"))
	if rawAt == "" && rawKind == "" && rawID == "" {
		return pgtype.Timestamptz{}, pgtype.Text{}, pgtype.UUID{}, true
	}
	if rawAt == "" || rawKind == "" || rawID == "" {
		writeError(w, http.StatusBadRequest, "invalid cursor")
		return pgtype.Timestamptz{}, pgtype.Text{}, pgtype.UUID{}, false
	}
	at, err := time.Parse(time.RFC3339Nano, rawAt)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid cursor")
		return pgtype.Timestamptz{}, pgtype.Text{}, pgtype.UUID{}, false
	}
	id, ok := parseUUIDOrBadRequest(w, rawID, "before_id")
	if !ok {
		return pgtype.Timestamptz{}, pgtype.Text{}, pgtype.UUID{}, false
	}
	return pgtype.Timestamptz{Time: at, Valid: true}, strToText(rawKind), id, true
}

func parseActivityEventCursor(w http.ResponseWriter, r *http.Request) (pgtype.Timestamptz, pgtype.UUID, bool) {
	rawAt := strings.TrimSpace(r.URL.Query().Get("before_created_at"))
	rawID := strings.TrimSpace(r.URL.Query().Get("before_id"))
	if rawAt == "" && rawID == "" {
		return pgtype.Timestamptz{}, pgtype.UUID{}, true
	}
	if rawAt == "" || rawID == "" {
		writeError(w, http.StatusBadRequest, "invalid cursor")
		return pgtype.Timestamptz{}, pgtype.UUID{}, false
	}
	at, err := time.Parse(time.RFC3339Nano, rawAt)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid cursor")
		return pgtype.Timestamptz{}, pgtype.UUID{}, false
	}
	id, ok := parseUUIDOrBadRequest(w, rawID, "before_id")
	if !ok {
		return pgtype.Timestamptz{}, pgtype.UUID{}, false
	}
	return pgtype.Timestamptz{Time: at, Valid: true}, id, true
}

func parseActivityAfterSeq(w http.ResponseWriter, r *http.Request) (int32, bool) {
	raw := strings.TrimSpace(r.URL.Query().Get("after_seq"))
	if raw == "" {
		return 0, true
	}
	seq, err := strconv.ParseInt(raw, 10, 32)
	if err != nil || seq < 0 {
		writeError(w, http.StatusBadRequest, "invalid cursor")
		return 0, false
	}
	return int32(seq), true
}

func (h *Handler) queryAgentActivityRows(ctx context.Context, reqCtx agentActivityRequestContext, beforeAt pgtype.Timestamptz, beforeKind pgtype.Text, beforeID pgtype.UUID, limit int) ([]agentActivityRawRow, error) {
	queryLimit := limit*3 + 1
	if queryLimit > agentActivityMaxLimit*3+1 {
		queryLimit = agentActivityMaxLimit*3 + 1
	}
	rows, err := h.DB.Query(ctx, agentActivityListSQL, parseUUID(reqCtx.workspaceID), reqCtx.agent.ID, nullableTimeArg(beforeAt), nullableTextArg(beforeKind), nullableUUIDArg(beforeID), queryLimit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []agentActivityRawRow{}
	for rows.Next() {
		row, err := scanAgentActivityRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

func (h *Handler) queryAgentActivityEventRows(ctx context.Context, reqCtx agentActivityRequestContext, beforeAt pgtype.Timestamptz, beforeID pgtype.UUID, limit int) ([]agentActivityRawRow, error) {
	rows, err := h.DB.Query(ctx, `
		WITH timeline AS (
		SELECT
			aae.event_kind AS kind,
			aae.id,
			aae.agent_id,
			aae.runtime_id,
			aae.task_id,
			NULL::uuid AS issue_id,
			NULL::uuid AS chat_session_id,
			aae.event_type,
			aae.severity,
			aae.target_kind,
			aae.target_id,
			aae.target_slug,
			aae.reason_code,
			aae.message,
			aae.details,
			aae.visibility,
			NULL::text AS status,
			NULL::text AS trigger_summary,
			NULL::jsonb AS result,
			NULL::text AS failure_reason,
			aae.created_at,
			NULL::timestamptz AS started_at,
			NULL::timestamptz AS completed_at,
			0::bigint AS step_count,
			'[]'::jsonb AS usage_json,
			NULL::uuid AS result_message_id,
			NULL::text AS result_message_kind
		FROM agent_activity_event aae
		WHERE aae.workspace_id = $1
		  AND aae.agent_id = $2

		UNION ALL

		SELECT
			CASE
				WHEN tm.type = 'thinking' THEN 'thinking'
				WHEN tm.type = 'tool_use' THEN 'tool_call'
				WHEN tm.type = 'tool_result' THEN 'tool_output'
				WHEN tm.type = 'error' THEN 'error'
				WHEN tm.type = 'log' THEN 'custom'
				ELSE 'text'
			END AS kind,
			tm.id,
			atq.agent_id,
			atq.runtime_id,
			atq.id AS task_id,
			atq.issue_id,
			atq.chat_session_id,
			CASE WHEN tm.type = 'log' THEN 'runtime_text' ELSE tm.type END AS event_type,
			CASE WHEN tm.type = 'error' THEN 'error' ELSE 'info' END AS severity,
			NULL::text AS target_kind,
			NULL::uuid AS target_id,
			NULL::text AS target_slug,
			''::text AS reason_code,
			CASE
				WHEN tm.type IN ('thinking', 'text', 'error', 'log') THEN COALESCE(NULLIF(tm.content, ''), NULLIF(tm.output, ''), '')
				ELSE ''
			END AS message,
			jsonb_strip_nulls(jsonb_build_object(
				'task_message_id', tm.id,
				'task_id', tm.task_id,
				'seq', tm.seq,
				'tool', tm.tool,
				'tool_target',
					CASE
						WHEN tm.type <> 'tool_use' THEN NULL
						WHEN tm.input ? 'path' THEN regexp_replace(tm.input->>'path', '^.*/', '')
						WHEN tm.input ? 'file_path' THEN regexp_replace(tm.input->>'file_path', '^.*/', '')
						WHEN tm.input ? 'filepath' THEN regexp_replace(tm.input->>'filepath', '^.*/', '')
						WHEN tm.input ? 'cmd' THEN regexp_replace(trim(both '"' from split_part(btrim(tm.input->>'cmd'), ' ', 1)), '^.*/', '')
						WHEN tm.input ? 'command' THEN regexp_replace(trim(both '"' from split_part(btrim(tm.input->>'command'), ' ', 1)), '^.*/', '')
						WHEN tm.input ? 'shell_command' THEN regexp_replace(trim(both '"' from split_part(btrim(tm.input->>'shell_command'), ' ', 1)), '^.*/', '')
						WHEN tm.input ? 'query' THEN left(tm.input->>'query', 80)
						WHEN tm.input ? 'pattern' THEN left(tm.input->>'pattern', 80)
						WHEN tm.input ? 'url' THEN left(tm.input->>'url', 80)
						ELSE NULL
					END
			)) AS details,
			tm.visibility,
			atq.status AS status,
			NULL::text AS trigger_summary,
			NULL::jsonb AS result,
			NULL::text AS failure_reason,
			tm.created_at,
			NULL::timestamptz AS started_at,
			NULL::timestamptz AS completed_at,
			0::bigint AS step_count,
			'[]'::jsonb AS usage_json,
			NULL::uuid AS result_message_id,
			NULL::text AS result_message_kind
		FROM task_message tm
		JOIN agent_task_queue atq ON atq.id = tm.task_id
		LEFT JOIN issue i ON i.id = atq.issue_id
		LEFT JOIN chat_session cs ON cs.id = atq.chat_session_id
		WHERE atq.agent_id = $2
		  AND (i.workspace_id = $1 OR cs.workspace_id = $1)
		)
		SELECT *
		FROM timeline
		WHERE ($3::timestamptz IS NULL OR (created_at, id) < ($3::timestamptz, $4::uuid))
		ORDER BY created_at DESC, id DESC
		LIMIT $5
	`, parseUUID(reqCtx.workspaceID), reqCtx.agent.ID, nullableTimeArg(beforeAt), nullableUUIDArg(beforeID), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []agentActivityRawRow{}
	for rows.Next() {
		row, err := scanAgentActivityRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

func (h *Handler) hydrateAgentActivityTimelineEvent(ctx context.Context, workspaceID string, agentID, eventID pgtype.UUID) *AgentActivityTimelineEvent {
	if h == nil || h.DB == nil {
		return nil
	}
	row, err := scanAgentActivityRow(h.DB.QueryRow(ctx, `
		SELECT
			aae.event_kind AS kind,
			aae.id,
			aae.agent_id,
			aae.runtime_id,
			aae.task_id,
			NULL::uuid AS issue_id,
			NULL::uuid AS chat_session_id,
			aae.event_type,
			aae.severity,
			aae.target_kind,
			aae.target_id,
			aae.target_slug,
			aae.reason_code,
			aae.message,
			aae.details,
			aae.visibility,
			NULL::text AS status,
			NULL::text AS trigger_summary,
			NULL::jsonb AS result,
			NULL::text AS failure_reason,
			aae.created_at,
			NULL::timestamptz AS started_at,
			NULL::timestamptz AS completed_at,
			0::bigint AS step_count,
			'[]'::jsonb AS usage_json,
			NULL::uuid AS result_message_id,
			NULL::text AS result_message_kind
		FROM agent_activity_event aae
		WHERE aae.workspace_id = $1
		  AND aae.agent_id = $2
		  AND aae.id = $3
	`, parseUUID(workspaceID), agentID, eventID))
	if err != nil {
		return nil
	}
	targetRef := h.agentTimelineTargetRef(ctx, workspaceID, row)
	event := agentActivityTimelineEvent(row, targetRef)
	return &event
}

func (h *Handler) queryAgentActivityRow(ctx context.Context, reqCtx agentActivityRequestContext, activityID pgtype.UUID) (agentActivityRawRow, error) {
	row := h.DB.QueryRow(ctx, agentActivityUnionSQL+`
		SELECT *
		FROM activity
		WHERE id = $3
		ORDER BY kind DESC
		LIMIT 1`, parseUUID(reqCtx.workspaceID), reqCtx.agent.ID, activityID)
	return scanAgentActivityRow(row)
}

const agentActivityListSQL = `
	WITH run_candidates AS (
		SELECT atq.*
		FROM agent_task_queue atq
		WHERE atq.agent_id = $2
		  AND ($3::timestamptz IS NULL OR (atq.created_at, 'run'::text, atq.id) < ($3::timestamptz, $4::text, $5::uuid))
		ORDER BY atq.created_at DESC, 'run'::text DESC, atq.id DESC
		LIMIT $6
	),
	event_candidates AS (
		SELECT aae.*
		FROM agent_activity_event aae
		WHERE aae.workspace_id = $1
		  AND aae.agent_id = $2
		  AND ($3::timestamptz IS NULL OR (aae.created_at, aae.event_kind, aae.id) < ($3::timestamptz, $4::text, $5::uuid))
		ORDER BY aae.created_at DESC, aae.event_kind DESC, aae.id DESC
		LIMIT $6
	),
	activity AS (
		SELECT
			'run'::text AS kind,
			atq.id,
			atq.agent_id,
			atq.runtime_id,
			NULL::uuid AS task_id,
			atq.issue_id,
			atq.chat_session_id,
			NULL::text AS event_type,
			NULL::text AS severity,
			NULL::text AS target_kind,
			NULL::uuid AS target_id,
			NULL::text AS target_slug,
			NULL::text AS reason_code,
			NULL::text AS message,
			'{}'::jsonb AS details,
			'user_facing'::text AS visibility,
			atq.status,
			atq.trigger_summary,
			atq.result,
			atq.failure_reason,
			atq.created_at,
			atq.started_at,
			atq.completed_at,
			COALESCE(steps.step_count, 0)::bigint AS step_count,
			COALESCE(usage.usage_json, '[]'::jsonb) AS usage_json,
			result_message.id AS result_message_id,
			result_message.kind AS result_message_kind
		FROM run_candidates atq
		LEFT JOIN LATERAL (
			SELECT count(*)::bigint AS step_count
			FROM task_message tm
			WHERE tm.task_id = atq.id
		) steps ON true
		LEFT JOIN LATERAL (
			SELECT jsonb_agg(jsonb_build_object(
				       'provider', provider,
				       'model', model,
				       'input_tokens', input_tokens,
				       'output_tokens', output_tokens,
				       'cache_read_tokens', cache_read_tokens,
				       'cache_write_tokens', cache_write_tokens
			       ) ORDER BY provider, model) AS usage_json
			FROM task_usage tu
			WHERE tu.task_id = atq.id
		) usage ON true
		LEFT JOIN LATERAL (
			SELECT cm.id, 'chat_message'::text AS kind
			FROM chat_message cm
			WHERE cm.task_id = atq.id
			  AND cm.role = 'assistant'
			  AND cm.content <> ''
			ORDER BY cm.created_at DESC
			LIMIT 1
		) result_message ON true

		UNION ALL

		SELECT
			aae.event_kind AS kind,
			aae.id,
			aae.agent_id,
			aae.runtime_id,
			aae.task_id,
			NULL::uuid AS issue_id,
			NULL::uuid AS chat_session_id,
			aae.event_type,
			aae.severity,
			aae.target_kind,
			aae.target_id,
			aae.target_slug,
			aae.reason_code,
			aae.message,
			aae.details,
			aae.visibility,
			NULL::text AS status,
			NULL::text AS trigger_summary,
			NULL::jsonb AS result,
			NULL::text AS failure_reason,
			aae.created_at,
			NULL::timestamptz AS started_at,
			NULL::timestamptz AS completed_at,
			0::bigint AS step_count,
			'[]'::jsonb AS usage_json,
			NULL::uuid AS result_message_id,
			NULL::text AS result_message_kind
		FROM event_candidates aae
	)
	SELECT *
	FROM activity
	ORDER BY created_at DESC, kind DESC, id DESC
	LIMIT $6`

const agentActivityUnionSQL = `
	WITH activity AS (
		SELECT
			'run'::text AS kind,
			atq.id,
			atq.agent_id,
			atq.runtime_id,
			NULL::uuid AS task_id,
			atq.issue_id,
			atq.chat_session_id,
			NULL::text AS event_type,
			NULL::text AS severity,
			NULL::text AS target_kind,
			NULL::uuid AS target_id,
			NULL::text AS target_slug,
			NULL::text AS reason_code,
			NULL::text AS message,
			'{}'::jsonb AS details,
			'user_facing'::text AS visibility,
			atq.status,
			atq.trigger_summary,
			atq.result,
			atq.failure_reason,
			atq.created_at,
			atq.started_at,
			atq.completed_at,
			COALESCE(steps.step_count, 0)::bigint AS step_count,
			COALESCE(usage.usage_json, '[]'::jsonb) AS usage_json,
			result_message.id AS result_message_id,
			result_message.kind AS result_message_kind
		FROM agent_task_queue atq
		LEFT JOIN (
			SELECT task_id, count(*)::bigint AS step_count
			FROM task_message
			GROUP BY task_id
		) steps ON steps.task_id = atq.id
		LEFT JOIN (
			SELECT task_id,
			       jsonb_agg(jsonb_build_object(
				       'provider', provider,
				       'model', model,
				       'input_tokens', input_tokens,
				       'output_tokens', output_tokens,
				       'cache_read_tokens', cache_read_tokens,
				       'cache_write_tokens', cache_write_tokens
			       ) ORDER BY provider, model) AS usage_json
			FROM task_usage
			GROUP BY task_id
		) usage ON usage.task_id = atq.id
		LEFT JOIN LATERAL (
			SELECT cm.id, 'chat_message'::text AS kind
			FROM chat_message cm
			WHERE cm.task_id = atq.id
			  AND cm.role = 'assistant'
			  AND cm.content <> ''
			ORDER BY cm.created_at DESC
			LIMIT 1
		) result_message ON true
		WHERE atq.agent_id = $2

		UNION ALL

		SELECT
			aae.event_kind AS kind,
			aae.id,
			aae.agent_id,
			aae.runtime_id,
			aae.task_id,
			NULL::uuid AS issue_id,
			NULL::uuid AS chat_session_id,
			aae.event_type,
			aae.severity,
			aae.target_kind,
			aae.target_id,
			aae.target_slug,
			aae.reason_code,
			aae.message,
			aae.details,
			aae.visibility,
			NULL::text AS status,
			NULL::text AS trigger_summary,
			NULL::jsonb AS result,
			NULL::text AS failure_reason,
			aae.created_at,
			NULL::timestamptz AS started_at,
			NULL::timestamptz AS completed_at,
			0::bigint AS step_count,
			'[]'::jsonb AS usage_json,
			NULL::uuid AS result_message_id,
			NULL::text AS result_message_kind
		FROM agent_activity_event aae
		WHERE aae.workspace_id = $1
		  AND aae.agent_id = $2
	)`

type scanner interface {
	Scan(dest ...any) error
}

func scanAgentActivityRow(row scanner) (agentActivityRawRow, error) {
	var out agentActivityRawRow
	err := row.Scan(
		&out.Kind,
		&out.ID,
		&out.AgentID,
		&out.RuntimeID,
		&out.EventTaskID,
		&out.IssueID,
		&out.ChatSessionID,
		&out.EventType,
		&out.Severity,
		&out.TargetKind,
		&out.TargetID,
		&out.TargetSlug,
		&out.ReasonCode,
		&out.Message,
		&out.Details,
		&out.Visibility,
		&out.Status,
		&out.TriggerSummary,
		&out.Result,
		&out.FailureReason,
		&out.CreatedAt,
		&out.StartedAt,
		&out.CompletedAt,
		&out.StepCount,
		&out.UsageJSON,
		&out.ResultMessageID,
		&out.ResultMessageKind,
	)
	return out, err
}

func (h *Handler) queryAgentActivitySteps(ctx context.Context, taskID pgtype.UUID, afterSeq int32, limit int) ([]agentActivityStepRawRow, error) {
	rows, err := h.DB.Query(ctx, `
		SELECT id, task_id, seq, type, tool, content, input, output, created_at
		FROM task_message
		WHERE task_id = $1
		  AND ($2::int = 0 OR seq > $2::int)
		ORDER BY seq ASC
		LIMIT $3`, taskID, afterSeq, limit+1)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []agentActivityStepRawRow{}
	for rows.Next() {
		var row agentActivityStepRawRow
		if err := rows.Scan(&row.ID, &row.TaskID, &row.Seq, &row.Type, &row.Tool, &row.Content, &row.Input, &row.Output, &row.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

func (h *Handler) agentActivityRowToItem(ctx context.Context, reqCtx agentActivityRequestContext, row agentActivityRawRow) (AgentActivityItem, bool, error) {
	targetRef, visible, detailAllowed, err := h.agentActivityVisibility(ctx, reqCtx, row)
	if err != nil || !visible {
		return AgentActivityItem{}, visible, err
	}
	visibleLevel := agentActivityVisibleSummary
	if detailAllowed {
		visibleLevel = agentActivityVisibleDetail
	}

	item := AgentActivityItem{
		ID:           uuidToString(row.ID),
		Kind:         row.Kind,
		AgentID:      uuidToString(row.AgentID),
		RuntimeID:    uuidToPtr(row.RuntimeID),
		CreatedAt:    timestampToString(row.CreatedAt),
		TargetRef:    targetRef,
		VisibleLevel: visibleLevel,
	}
	if row.Kind == "run" {
		item.Run = agentActivityRunSummary(row)
		if detailAllowed {
			realtime := agentActivityRealtime(uuidToString(row.ID))
			item.Realtime = &realtime
		}
	} else {
		item.Event = agentActivityEventSummary(row)
	}
	return item, true, nil
}

func (h *Handler) agentActivityVisibility(ctx context.Context, reqCtx agentActivityRequestContext, row agentActivityRawRow) (AgentActivityTargetRef, bool, bool, error) {
	targetRef, visible, err := h.agentActivityTargetVisibility(ctx, reqCtx.workspaceID, reqCtx.userID, row)
	if err != nil || !visible {
		return targetRef, visible, false, err
	}
	detailAllowed := roleAllowed(reqCtx.member.Role, "owner", "admin") || uuidToString(reqCtx.agent.OwnerID) == reqCtx.userID
	return targetRef, true, detailAllowed, nil
}

func (h *Handler) agentActivityTargetVisibility(ctx context.Context, workspaceID, userID string, row agentActivityRawRow) (AgentActivityTargetRef, bool, error) {
	if row.Kind == "run" {
		return h.agentRunTargetVisibility(ctx, workspaceID, userID, row)
	}
	return h.agentEventTargetVisibility(ctx, workspaceID, userID, row)
}

func (h *Handler) agentRunTargetVisibility(ctx context.Context, workspaceID, userID string, row agentActivityRawRow) (AgentActivityTargetRef, bool, error) {
	if row.IssueID.Valid {
		ref := AgentActivityTargetRef{Kind: "issue", ID: stringPtr(uuidToString(row.IssueID))}
		ok, err := h.issueBelongsToWorkspace(ctx, workspaceID, row.IssueID)
		return ref, ok, err
	}
	if row.ChatSessionID.Valid {
		channelID := h.channelIDForChatSession(ctx, row.ChatSessionID)
		if channelID != "" {
			channelUUID, err := util.ParseUUID(channelID)
			if err != nil {
				return AgentActivityTargetRef{Kind: "channel", ID: stringPtr(channelID)}, false, err
			}
			userUUID, err := util.ParseUUID(userID)
			if err != nil {
				return AgentActivityTargetRef{Kind: "channel", ID: stringPtr(channelID)}, false, err
			}
			if root := h.threadRootIDForChatSession(ctx, row.ChatSessionID); root != nil {
				ref := AgentActivityTargetRef{Kind: "thread", ID: root, Slug: stringPtr(channelID)}
				return ref, h.channelUserIsMember(ctx, workspaceID, channelUUID, userUUID), nil
			}
			ref := AgentActivityTargetRef{Kind: "channel", ID: stringPtr(channelID)}
			return ref, h.channelUserIsMember(ctx, workspaceID, channelUUID, userUUID), nil
		}
		ref := AgentActivityTargetRef{Kind: "dm", ID: stringPtr(uuidToString(row.ChatSessionID))}
		ok, err := h.chatSessionCreatedByUser(ctx, workspaceID, row.ChatSessionID, userID)
		return ref, ok, err
	}
	return AgentActivityTargetRef{Kind: "none"}, true, nil
}

func (h *Handler) agentEventTargetVisibility(ctx context.Context, workspaceID, userID string, row agentActivityRawRow) (AgentActivityTargetRef, bool, error) {
	kind := textOrDefault(row.TargetKind, "none")
	ref := AgentActivityTargetRef{Kind: kind, ID: uuidToPtr(row.TargetID), Slug: textToPtr(row.TargetSlug)}
	switch kind {
	case "none", "agent":
		return ref, true, nil
	case "issue":
		if !row.TargetID.Valid {
			return ref, false, nil
		}
		ok, err := h.issueBelongsToWorkspace(ctx, workspaceID, row.TargetID)
		return ref, ok, err
	case "dm":
		if !row.TargetID.Valid {
			return ref, false, nil
		}
		ok, err := h.chatSessionCreatedByUser(ctx, workspaceID, row.TargetID, userID)
		return ref, ok, err
	case "channel":
		if !row.TargetID.Valid {
			return ref, false, nil
		}
		userUUID, err := util.ParseUUID(userID)
		if err != nil {
			return ref, false, err
		}
		return ref, h.channelUserIsMember(ctx, workspaceID, row.TargetID, userUUID), nil
	case "thread":
		if !row.TargetID.Valid {
			return ref, false, nil
		}
		channelID, err := h.channelIDForThreadRoot(ctx, workspaceID, row.TargetID)
		if err != nil {
			return ref, false, err
		}
		if !channelID.Valid {
			return ref, false, nil
		}
		userUUID, err := util.ParseUUID(userID)
		if err != nil {
			return ref, false, err
		}
		return ref, h.channelUserIsMember(ctx, workspaceID, channelID, userUUID), nil
	default:
		return AgentActivityTargetRef{Kind: "none"}, false, nil
	}
}

func (h *Handler) agentTimelineTargetVisibility(ctx context.Context, workspaceID, userID string, row agentActivityRawRow) (AgentActivityTargetRef, bool, error) {
	if row.IssueID.Valid || row.ChatSessionID.Valid {
		return h.agentRunTargetVisibility(ctx, workspaceID, userID, row)
	}
	return h.agentEventTargetVisibility(ctx, workspaceID, userID, row)
}

func (h *Handler) agentTimelineTargetRef(ctx context.Context, workspaceID string, row agentActivityRawRow) AgentActivityTargetRef {
	if row.IssueID.Valid {
		return AgentActivityTargetRef{Kind: "issue", ID: stringPtr(uuidToString(row.IssueID))}
	}
	if row.ChatSessionID.Valid {
		channelID := h.channelIDForChatSession(ctx, row.ChatSessionID)
		if channelID != "" {
			if root := h.threadRootIDForChatSession(ctx, row.ChatSessionID); root != nil {
				return AgentActivityTargetRef{Kind: "thread", ID: root, Slug: stringPtr(channelID)}
			}
			return AgentActivityTargetRef{Kind: "channel", ID: stringPtr(channelID)}
		}
		return AgentActivityTargetRef{Kind: "dm", ID: stringPtr(uuidToString(row.ChatSessionID))}
	}
	kind := textOrDefault(row.TargetKind, "none")
	ref := AgentActivityTargetRef{Kind: kind, ID: uuidToPtr(row.TargetID), Slug: textToPtr(row.TargetSlug)}
	if ref.Kind == "thread" && ref.Slug == nil && row.TargetID.Valid {
		if channelID, err := h.channelIDForThreadRoot(ctx, workspaceID, row.TargetID); err == nil && channelID.Valid {
			ref.Slug = stringPtr(uuidToString(channelID))
		}
	}
	return ref
}

func (h *Handler) issueBelongsToWorkspace(ctx context.Context, workspaceID string, issueID pgtype.UUID) (bool, error) {
	var exists bool
	err := h.DB.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM issue
			WHERE id = $1 AND workspace_id = $2
		)`, issueID, parseUUID(workspaceID)).Scan(&exists)
	return exists, err
}

func (h *Handler) chatSessionCreatedByUser(ctx context.Context, workspaceID string, chatSessionID pgtype.UUID, userID string) (bool, error) {
	var exists bool
	err := h.DB.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM chat_session
			WHERE id = $1 AND workspace_id = $2 AND creator_id = $3
		)`, chatSessionID, parseUUID(workspaceID), parseUUID(userID)).Scan(&exists)
	return exists, err
}

func (h *Handler) channelIDForThreadRoot(ctx context.Context, workspaceID string, rootID pgtype.UUID) (pgtype.UUID, error) {
	var channelID pgtype.UUID
	err := h.DB.QueryRow(ctx, `
		SELECT channel_id
		FROM channel_message
		WHERE id = $1 AND workspace_id = $2`, rootID, parseUUID(workspaceID)).Scan(&channelID)
	if errors.Is(err, pgx.ErrNoRows) {
		return pgtype.UUID{}, nil
	}
	return channelID, err
}

func (h *Handler) publishAgentActivityRealtimeEvent(ctx context.Context, workspaceID, agentID, eventID string, event *AgentActivityTimelineEvent, targetRef AgentActivityTargetRef) {
	if h == nil || h.Bus == nil {
		return
	}
	payload := AgentActivityEventRealtimePayload{AgentID: agentID, EventID: eventID}
	if event != nil {
		payload.Event = event
		targetRef = event.TargetRef
	}
	if recipientIDs, scoped := h.agentActivityRealtimeRecipientUserIDs(ctx, workspaceID, targetRef); scoped {
		h.publishToUsers(protocol.EventAgentActivityEvent, workspaceID, "system", "", recipientIDs, payload)
		return
	}
	h.publish(protocol.EventAgentActivityEvent, workspaceID, "system", "", payload)
}

func (h *Handler) agentActivityRealtimeRecipientUserIDs(ctx context.Context, workspaceID string, targetRef AgentActivityTargetRef) ([]string, bool) {
	switch targetRef.Kind {
	case "dm":
		if targetRef.ID == nil {
			return []string{}, true
		}
		chatSessionID, err := util.ParseUUID(*targetRef.ID)
		if err != nil {
			return []string{}, true
		}
		creatorID, ok := h.chatSessionCreatorUserID(ctx, workspaceID, chatSessionID)
		if !ok {
			return []string{}, true
		}
		return []string{creatorID}, true
	case "channel":
		if targetRef.ID == nil {
			return []string{}, true
		}
		return recipientUserIDsFromSet(h.channelHumanMemberIDs(ctx, workspaceID, *targetRef.ID)), true
	case "thread":
		channelID := ""
		if targetRef.Slug != nil {
			channelID = *targetRef.Slug
		} else if targetRef.ID != nil {
			rootID, err := util.ParseUUID(*targetRef.ID)
			if err == nil {
				if id, err := h.channelIDForThreadRoot(ctx, workspaceID, rootID); err == nil && id.Valid {
					channelID = uuidToString(id)
				}
			}
		}
		if channelID == "" {
			return []string{}, true
		}
		return recipientUserIDsFromSet(h.channelHumanMemberIDs(ctx, workspaceID, channelID)), true
	default:
		return nil, false
	}
}

func (h *Handler) chatSessionCreatorUserID(ctx context.Context, workspaceID string, chatSessionID pgtype.UUID) (string, bool) {
	if !chatSessionID.Valid || h == nil || h.DB == nil {
		return "", false
	}
	var creatorID pgtype.UUID
	err := h.DB.QueryRow(ctx, `
		SELECT creator_id
		FROM chat_session
		WHERE id = $1 AND workspace_id = $2`, chatSessionID, parseUUID(workspaceID)).Scan(&creatorID)
	if err != nil || !creatorID.Valid {
		return "", false
	}
	return uuidToString(creatorID), true
}

func agentActivityRunSummary(row agentActivityRawRow) *AgentActivityRunSummary {
	status := textOrDefault(row.Status, "queued")
	resultState := agentActivityResultState(row.Result)
	if resultState != nil && *resultState == "no_reply" && status == "completed" {
		status = "no_reply"
	}
	return &AgentActivityRunSummary{
		Status:      status,
		CreatedAt:   timestampToString(row.CreatedAt),
		StartedAt:   timestampToPtr(row.StartedAt),
		EndedAt:     timestampToPtr(row.CompletedAt),
		DurationMS:  activityDurationMS(row.StartedAt, row.CompletedAt),
		Trigger:     agentActivityTrigger(row),
		Result:      agentActivityRunResult(row),
		Tokens:      parseActivityTokenUse(row.UsageJSON),
		StepCount:   row.StepCount,
		ResultState: resultState,
	}
}

func agentActivityTrigger(row agentActivityRawRow) AgentActivityTrigger {
	kind := agentActivityResultString(row.Result, "trigger_kind", "trigger")
	if !isKnownAgentActivityTrigger(kind) {
		kind = agentActivityTriggerManual
	}
	if kind == agentActivityTriggerManual {
		if row.IssueID.Valid {
			kind = agentActivityTriggerIssue
		} else if row.ChatSessionID.Valid {
			kind = agentActivityTriggerDM
		}
	}
	return AgentActivityTrigger{Kind: kind, Summary: textToPtr(row.TriggerSummary)}
}

func isKnownAgentActivityTrigger(kind string) bool {
	switch kind {
	case agentActivityTriggerAssign,
		agentActivityTriggerAmbient,
		agentActivityTriggerAutopilot,
		agentActivityTriggerContinuation,
		agentActivityTriggerDM,
		agentActivityTriggerIssue,
		agentActivityTriggerManual,
		agentActivityTriggerMention,
		agentActivityTriggerQuickCreate,
		agentActivityTriggerTimeTrigger,
		agentActivityTriggerUnknown:
		return true
	default:
		return false
	}
}

func agentActivityRunResult(row agentActivityRawRow) AgentActivityRunResult {
	action := agentActivityResultState(row.Result)
	var suppressedReason *string
	if reason := agentActivityResultString(row.Result, "output_suppressed_reason"); reason != "" {
		suppressedReason = stringPtr(reason)
	}
	return AgentActivityRunResult{
		Action:                 action,
		MessageRef:             agentActivityResultMessageRef(row),
		OutputSuppressedReason: suppressedReason,
	}
}

func agentActivityResultMessageRef(row agentActivityRawRow) *AgentActivityMessageRef {
	if row.ResultMessageID.Valid {
		return &AgentActivityMessageRef{
			Kind: textOrDefault(row.ResultMessageKind, "chat_message"),
			ID:   uuidToString(row.ResultMessageID),
		}
	}
	if ref := agentActivityResultMessageRefFromPayload(row.Result); ref != nil {
		return ref
	}
	return nil
}

func agentActivityResultMessageRefFromPayload(raw []byte) *AgentActivityMessageRef {
	if len(raw) == 0 {
		return nil
	}
	var obj map[string]any
	if err := json.Unmarshal(raw, &obj); err != nil {
		return nil
	}
	if ref, ok := objectMap(obj["message_ref"]); ok {
		return agentActivityMessageRefFromMap(ref)
	}
	if ref, ok := objectMap(obj["result_message_ref"]); ok {
		return agentActivityMessageRefFromMap(ref)
	}
	for _, key := range []string{"chat_message_id", "channel_message_id", "comment_id", "message_id"} {
		id, ok := obj[key].(string)
		if !ok || strings.TrimSpace(id) == "" {
			continue
		}
		kind := strings.TrimSuffix(key, "_id")
		if key == "message_id" {
			kind = "message"
		}
		return &AgentActivityMessageRef{Kind: kind, ID: strings.TrimSpace(id)}
	}
	return nil
}

func objectMap(v any) (map[string]any, bool) {
	obj, ok := v.(map[string]any)
	return obj, ok
}

func agentActivityMessageRefFromMap(obj map[string]any) *AgentActivityMessageRef {
	id, _ := obj["id"].(string)
	if strings.TrimSpace(id) == "" {
		id, _ = obj["message_id"].(string)
	}
	if strings.TrimSpace(id) == "" {
		return nil
	}
	kind, _ := obj["kind"].(string)
	if strings.TrimSpace(kind) == "" {
		kind, _ = obj["type"].(string)
	}
	if strings.TrimSpace(kind) == "" {
		kind = "message"
	}
	return &AgentActivityMessageRef{Kind: strings.TrimSpace(kind), ID: strings.TrimSpace(id)}
}

func agentActivityResultString(raw []byte, keys ...string) string {
	if len(raw) == 0 {
		return ""
	}
	var obj map[string]any
	if err := json.Unmarshal(raw, &obj); err != nil {
		return ""
	}
	for _, key := range keys {
		switch value := obj[key].(type) {
		case string:
			if trimmed := strings.TrimSpace(value); trimmed != "" {
				return trimmed
			}
		case map[string]any:
			for _, nestedKey := range []string{"kind", "type"} {
				if nested, ok := value[nestedKey].(string); ok && strings.TrimSpace(nested) != "" {
					return strings.TrimSpace(nested)
				}
			}
		}
	}
	return ""
}

func agentActivityEventDetails(eventType string, raw []byte) map[string]any {
	lower := strings.ToLower(strings.TrimSpace(eventType))
	if !strings.Contains(lower, "restart") && !strings.Contains(lower, "resume") {
		return nil
	}
	out := map[string]any{"replay_count": int64(0)}
	if len(raw) == 0 {
		return out
	}
	var obj map[string]any
	if err := json.Unmarshal(raw, &obj); err != nil {
		return out
	}
	if replayCount, ok := int64FromJSONValue(obj["replay_count"]); ok {
		out["replay_count"] = replayCount
	}
	return out
}

func int64FromJSONValue(value any) (int64, bool) {
	switch v := value.(type) {
	case float64:
		return int64(v), true
	case int64:
		return v, true
	case int:
		return int64(v), true
	case json.Number:
		i, err := v.Int64()
		return i, err == nil
	case string:
		i, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
		return i, err == nil
	default:
		return 0, false
	}
}

func agentActivityEventSummary(row agentActivityRawRow) *AgentActivityEventSummary {
	eventType := textOrDefault(row.EventType, row.Kind)
	return &AgentActivityEventSummary{
		EventType:  eventType,
		Severity:   textOrDefault(row.Severity, "info"),
		ReasonCode: textOrDefault(row.ReasonCode, ""),
		Message:    textToPtr(row.Message),
		Details:    agentActivityEventDetails(eventType, row.Details),
	}
}

func (h *Handler) taskMessageActivityTimelineEvent(ctx context.Context, workspaceID string, task db.AgentTaskQueue, message db.TaskMessage) *AgentActivityTimelineEvent {
	details := map[string]any{
		"task_message_id": uuidToString(message.ID),
		"task_id":         uuidToString(message.TaskID),
		"seq":             message.Seq,
	}
	if message.Tool.Valid && strings.TrimSpace(message.Tool.String) != "" {
		rawTool := strings.TrimSpace(message.Tool.String)
		canonicalTool, known := taskMessageCanonicalToolName(rawTool, jsonObject(message.Input))
		if known {
			details["tool"] = canonicalTool
		} else {
			details["unmapped_tool_name"] = rawTool
		}
		if canonicalTool != rawTool {
			details["raw_tool"] = rawTool
		}
	}
	if target, summaryKind := taskMessageActivityToolTarget(message); target != "" {
		details["tool_target"] = target
		details["summary_kind"] = summaryKind
	}
	detailsJSON, _ := json.Marshal(details)
	row := agentActivityRawRow{
		Kind:          taskMessageActivityKind(message.Type),
		ID:            message.ID,
		AgentID:       task.AgentID,
		RuntimeID:     task.RuntimeID,
		EventTaskID:   task.ID,
		IssueID:       task.IssueID,
		ChatSessionID: task.ChatSessionID,
		EventType:     pgtype.Text{String: taskMessageActivityEventType(message.Type), Valid: strings.TrimSpace(message.Type) != ""},
		Severity:      pgtype.Text{String: "info", Valid: true},
		ReasonCode:    pgtype.Text{String: "", Valid: true},
		Message:       taskMessageActivityText(message),
		Details:       detailsJSON,
		Visibility:    pgtype.Text{String: message.Visibility, Valid: strings.TrimSpace(message.Visibility) != ""},
		Status:        pgtype.Text{String: task.Status, Valid: strings.TrimSpace(task.Status) != ""},
		CreatedAt:     message.CreatedAt,
		UsageJSON:     []byte("[]"),
	}
	if message.Type == "error" {
		row.Severity = pgtype.Text{String: "error", Valid: true}
	}
	targetRef := h.agentTimelineTargetRef(ctx, workspaceID, row)
	event := agentActivityTimelineEvent(row, targetRef)
	return &event
}

func taskMessageActivityKind(messageType string) string {
	switch messageType {
	case "thinking":
		return activityKindThinking
	case "tool_use":
		return activityKindToolCall
	case "tool_result":
		return activityKindToolOutput
	case "error":
		return activityKindError
	case "log":
		return activityKindCustom
	default:
		return activityKindText
	}
}

func taskMessageActivityEventType(messageType string) string {
	if messageType == "log" {
		return "runtime_text"
	}
	return messageType
}

func taskMessageActivityText(message db.TaskMessage) pgtype.Text {
	switch message.Type {
	case "thinking", "text", "error", "log":
		if message.Content.Valid && strings.TrimSpace(message.Content.String) != "" {
			return message.Content
		}
		if message.Output.Valid && strings.TrimSpace(message.Output.String) != "" {
			return message.Output
		}
	}
	return pgtype.Text{}
}

func agentActivityTimelineEvent(row agentActivityRawRow, targetRef AgentActivityTargetRef) AgentActivityTimelineEvent {
	details := jsonObject(row.Details)
	tool := stringPtrFromMap(details, "tool")
	if tool != nil {
		canonical := agentActivityCanonicalToolName(*tool)
		tool = &canonical
	}
	return AgentActivityTimelineEvent{
		ID:         uuidToString(row.ID),
		AgentID:    uuidToString(row.AgentID),
		RuntimeID:  uuidToPtr(row.RuntimeID),
		TaskID:     agentActivityTimelineTaskID(row),
		Kind:       row.Kind,
		EventType:  textOrDefault(row.EventType, row.Kind),
		OccurredAt: timestampToString(row.CreatedAt),
		Visibility: textOrDefault(row.Visibility, "user_facing"),
		Text:       textToPtr(row.Message),
		Tool:       tool,
		ToolTarget: stringPtrFromMap(details, "tool_target"),
		Status:     textToPtr(row.Status),
		ReasonCode: textOrDefault(row.ReasonCode, ""),
		TargetRef:  targetRef,
		SourceRefs: agentActivitySourceRefs(row),
	}
}

func taskMessageActivityToolTarget(message db.TaskMessage) (string, string) {
	if message.Type != "tool_use" {
		return "", ""
	}
	input := jsonObject(message.Input)
	rawTool := ""
	if message.Tool.Valid {
		rawTool = message.Tool.String
	}
	canonicalTool, known := taskMessageCanonicalToolName(rawTool, input)
	if !known {
		canonicalTool = ""
	}
	return agentActivitySafeToolTargetForTool(canonicalTool, input)
}

func agentActivitySafeToolTarget(input map[string]any) (string, string) {
	for _, key := range []string{"path", "file_path", "filepath"} {
		if value := basenameFromMap(input, key); value != "" {
			return value, "file_path"
		}
	}
	for _, key := range []string{"cmd", "command", "shell_command"} {
		if value := commandNameFromMap(input, key); value != "" {
			return value, "command"
		}
	}
	for _, key := range []string{"query", "pattern"} {
		if value := clippedStringFromMap(input, key, 80); value != "" {
			return value, key
		}
	}
	if value := clippedStringFromMap(input, "url", 80); value != "" {
		return value, "url"
	}
	return "", ""
}

func agentActivitySafeToolTargetForTool(canonicalTool string, input map[string]any) (string, string) {
	if isFileActivityTool(canonicalTool) {
		for _, key := range []string{"path", "file_path", "filepath"} {
			if value := sourcePathFromMap(input, key); value != "" {
				return value, "file_path"
			}
		}
	}
	return agentActivitySafeToolTarget(input)
}

func isFileActivityTool(tool string) bool {
	switch strings.ToLower(strings.TrimSpace(tool)) {
	case "write_file", "edit_file", "read_file":
		return true
	default:
		return false
	}
}

func agentActivityCanonicalToolName(raw string) string {
	canonical, _ := agentActivityCanonicalToolNameKnown(raw)
	return canonical
}

func agentActivityCanonicalToolNameKnown(raw string) (string, bool) {
	tool := strings.ToLower(strings.TrimSpace(raw))
	tool = strings.TrimPrefix(tool, "mcp_chat_")
	if strings.HasPrefix(tool, "mcp__") {
		parts := strings.Split(tool, "__")
		if len(parts) > 0 {
			tool = parts[len(parts)-1]
		}
	}
	tool = strings.TrimSpace(strings.TrimPrefix(tool, "tool:"))
	if canonical, ok := agentActivityToolAliases[tool]; ok {
		return canonical, true
	}
	return tool, false
}

func taskMessageCanonicalToolName(raw string, input map[string]any) (string, bool) {
	canonical, known := agentActivityCanonicalToolNameKnown(raw)
	if known {
		return canonical, true
	}
	if isStatusLikeToolName(canonical) && hasShellCommandInput(input) {
		return "bash", true
	}
	return canonical, false
}

func isStatusLikeToolName(tool string) bool {
	switch strings.ToLower(strings.TrimSpace(tool)) {
	case "running", "in_progress", "pending":
		return true
	default:
		return false
	}
}

func hasShellCommandInput(input map[string]any) bool {
	for _, key := range []string{"cmd", "command", "shell_command"} {
		if value := clippedStringFromMap(input, key, 1024); value != "" {
			return true
		}
	}
	return false
}

func taskMessageToolIsMapped(messageType, tool string, input map[string]any) bool {
	if messageType != "tool_use" || strings.TrimSpace(tool) == "" {
		return true
	}
	_, known := taskMessageCanonicalToolName(tool, input)
	return known
}

var agentActivityToolAliases = map[string]string{
	"send_message": "send_message",
	"message_send": "send_message",

	"check_messages":     "check_messages",
	"wait_for_message":   "wait_for_message",
	"receive_message":    "receive_message",
	"read_history":       "read_history",
	"search_messages":    "search_messages",
	"list_server":        "list_server",
	"list_tasks":         "list_tasks",
	"create_tasks":       "create_tasks",
	"claim_tasks":        "claim_tasks",
	"unclaim_task":       "unclaim_task",
	"update_task_status": "update_task_status",
	"join_channel":       "join_channel",
	"leave_channel":      "leave_channel",
	"upload_file":        "upload_file",
	"view_file":          "view_file",

	"bash":                 "bash",
	"shell":                "bash",
	"sh":                   "bash",
	"zsh":                  "bash",
	"exec":                 "bash",
	"exec_command":         "bash",
	"command":              "bash",
	"command_execution":    "bash",
	"run_terminal_command": "bash",
	"run_shell_command":    "bash",
	"terminal":             "bash",

	"read":      "read_file",
	"readfile":  "read_file",
	"read_file": "read_file",
	"file_read": "read_file",
	"open":      "read_file",
	"cat":       "read_file",

	"write":      "write_file",
	"writefile":  "write_file",
	"write_file": "write_file",
	"file_write": "write_file",

	"edit":           "edit_file",
	"editfile":       "edit_file",
	"edit_file":      "edit_file",
	"file_edit":      "edit_file",
	"file_change":    "edit_file",
	"strreplacefile": "edit_file",
	"str_replace":    "edit_file",
	"multi_edit":     "edit_file",
	"patch_apply":    "edit_file",

	"glob":         "glob",
	"search_files": "glob",

	"grep":        "grep",
	"rg":          "grep",
	"search":      "grep",
	"search_code": "grep",

	"web_fetch":  "web_fetch",
	"webfetch":   "web_fetch",
	"fetchurl":   "web_fetch",
	"fetch_url":  "web_fetch",
	"web_search": "web_search",
	"websearch":  "web_search",
	"searchweb":  "web_search",
	"search_web": "web_search",

	"todowrite":         "todo_write",
	"todo_write":        "todo_write",
	"set_todo_list":     "todo_write",
	"settodolist":       "todo_write",
	"schedule_reminder": "schedule_reminder",
	"list_reminders":    "list_reminders",
	"cancel_reminder":   "cancel_reminder",
	"collab_tool_call":  "collab_tool_call",
}

func agentActivityTimelineTaskID(row agentActivityRawRow) *string {
	if !row.EventTaskID.Valid || (!row.IssueID.Valid && textOrDefault(row.TargetKind, "") != "issue") {
		return nil
	}
	return uuidToPtr(row.EventTaskID)
}

func agentActivitySourceRefs(row agentActivityRawRow) []AgentActivitySourceRef {
	details := jsonObject(row.Details)
	refs := make([]AgentActivitySourceRef, 0, 4)
	for _, spec := range []struct {
		key  string
		kind string
	}{
		{key: "inbox_event_id", kind: "inbox_event"},
		{key: "delivery_id", kind: "delivery"},
		{key: "source_message_id", kind: "message"},
		{key: "message_id", kind: "message"},
		{key: "trigger_message_id", kind: "message"},
		{key: "task_message_id", kind: "task_message"},
		{key: "agent_session_id", kind: "agent_session"},
	} {
		if id := stringFromMap(details, spec.key); id != "" {
			refs = append(refs, AgentActivitySourceRef{Kind: spec.kind, ID: id})
		}
	}
	if seq, ok := int64FromJSONValue(details["message_seq"]); ok {
		refs = append(refs, AgentActivitySourceRef{Kind: "message_seq", Seq: &seq})
	}
	if seq, ok := int64FromJSONValue(details["seq"]); ok {
		refs = append(refs, AgentActivitySourceRef{Kind: "seq", Seq: &seq})
	}
	return refs
}

func jsonObject(raw []byte) map[string]any {
	if len(raw) == 0 {
		return map[string]any{}
	}
	var obj map[string]any
	if err := json.Unmarshal(raw, &obj); err != nil {
		return map[string]any{}
	}
	return obj
}

func stringFromMap(obj map[string]any, key string) string {
	value, ok := obj[key]
	if !ok {
		return ""
	}
	switch v := value.(type) {
	case string:
		return strings.TrimSpace(v)
	default:
		return strings.TrimSpace(fmt.Sprint(v))
	}
}

func agentActivityRealtime(activityID string) AgentActivityRealtimeContract {
	return AgentActivityRealtimeContract{
		Scope:     "task",
		ID:        activityID,
		EventType: protocol.EventAgentActivityStep,
		Payload:   agentActivityRealtimeStepPayload,
	}
}

func agentActivityEventRealtime(agentID string) AgentActivityRealtimeContract {
	return AgentActivityRealtimeContract{
		Scope:     "agent",
		ID:        agentID,
		EventType: protocol.EventAgentActivityEvent,
		Payload:   "AgentActivityTimelineEvent",
	}
}

func agentActivityDiagnostic(row agentActivityRawRow) map[string]any {
	out := map[string]any{
		"activity_id": uuidToString(row.ID),
		"kind":        row.Kind,
		"agent_id":    uuidToString(row.AgentID),
		"created_at":  timestampToString(row.CreatedAt),
		"redaction":   "diagnostic_v1",
	}
	if row.RuntimeID.Valid {
		out["runtime_id"] = uuidToString(row.RuntimeID)
	}
	if row.Kind == "run" {
		out["status"] = textOrDefault(row.Status, "")
		out["created_at"] = timestampToString(row.CreatedAt)
		if started := timestampToPtr(row.StartedAt); started != nil {
			out["started_at"] = *started
		}
		if ended := timestampToPtr(row.CompletedAt); ended != nil {
			out["ended_at"] = *ended
		}
		if state := agentActivityResultState(row.Result); state != nil {
			out["result_state"] = *state
		}
		if reason := agentActivityResultString(row.Result, "output_suppressed_reason"); reason != "" {
			out["output_suppressed_reason"] = reason
		}
		out["step_count"] = row.StepCount
		return out
	}
	out["event_type"] = textOrDefault(row.EventType, row.Kind)
	out["severity"] = textOrDefault(row.Severity, "info")
	if reason := textOrDefault(row.ReasonCode, ""); reason != "" {
		out["reason_code"] = reason
	}
	return out
}

func stringPtrFromMap(m map[string]any, key string) *string {
	if value := stringFromMap(m, key); value != "" {
		return &value
	}
	return nil
}

func basenameFromMap(m map[string]any, key string) string {
	value := stringFromMap(m, key)
	if value == "" {
		return ""
	}
	parts := strings.FieldsFunc(value, func(r rune) bool {
		return r == '/' || r == '\\'
	})
	if len(parts) == 0 {
		return value
	}
	return parts[len(parts)-1]
}

func sourcePathFromMap(m map[string]any, key string) string {
	return stringFromMap(m, key)
}

func commandNameFromMap(m map[string]any, key string) string {
	value := stringFromMap(m, key)
	if value == "" {
		return ""
	}
	value = redact.Text(value)
	fields := strings.Fields(value)
	if len(fields) == 0 {
		return ""
	}
	cmd := strings.Trim(fields[0], `"'`)
	if cmd == "" {
		return ""
	}
	parts := strings.FieldsFunc(cmd, func(r rune) bool {
		return r == '/' || r == '\\'
	})
	if len(parts) == 0 {
		return truncateForActivity(cmd, 40)
	}
	return truncateForActivity(parts[len(parts)-1], 40)
}

func clippedStringFromMap(m map[string]any, key string, limit int) string {
	value := stringFromMap(m, key)
	if value == "" {
		return ""
	}
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit])
}

func agentActivityStepToResponse(row agentActivityStepRawRow) AgentActivityStep {
	stepType := normalizeActivityStepType(row.Type)
	payload := map[string]any{}
	if content := textToPtr(row.Content); content != nil {
		payload["content"] = *content
	}
	if input := rawJSONOrString(row.Input); input != nil {
		payload["input"] = input
	}
	if output := textToPtr(row.Output); output != nil {
		payload["output"] = *output
	}
	if len(payload) == 0 {
		payload = nil
	}
	return AgentActivityStep{
		ID:         uuidToString(row.ID),
		ActivityID: uuidToString(row.TaskID),
		Seq:        row.Seq,
		StepType:   stepType,
		Tool:       textToPtr(row.Tool),
		CreatedAt:  timestampToString(row.CreatedAt),
		Payload:    payload,
	}
}

func normalizeActivityStepType(raw string) string {
	lower := strings.ToLower(strings.TrimSpace(raw))
	switch {
	case strings.Contains(lower, "thinking"):
		return activityKindThinking
	case strings.Contains(lower, "tool") && strings.Contains(lower, "result"):
		return activityKindToolOutput
	case strings.Contains(lower, "tool"):
		return activityKindToolCall
	case strings.Contains(lower, "error"):
		return activityKindError
	default:
		return activityKindText
	}
}

func parseActivityTokenUse(raw []byte) []AgentActivityTokenUse {
	if len(raw) == 0 {
		return nil
	}
	var usage []AgentActivityTokenUse
	if err := json.Unmarshal(raw, &usage); err != nil {
		return nil
	}
	if len(usage) == 0 {
		return nil
	}
	return usage
}

func agentActivityResultState(raw []byte) *string {
	if len(raw) == 0 {
		return nil
	}
	var obj map[string]any
	if err := json.Unmarshal(raw, &obj); err != nil {
		return nil
	}
	if action, ok := obj["action"].(string); ok && strings.TrimSpace(action) != "" {
		return stringPtr(action)
	}
	if state, ok := obj["state"].(string); ok && strings.TrimSpace(state) != "" {
		return stringPtr(state)
	}
	return nil
}

func activityDurationMS(start, end pgtype.Timestamptz) *int64 {
	if !start.Valid || !end.Valid || end.Time.Before(start.Time) {
		return nil
	}
	ms := end.Time.Sub(start.Time).Milliseconds()
	return &ms
}

func rawJSONOrString(raw []byte) any {
	if len(raw) == 0 {
		return nil
	}
	var obj any
	if err := json.Unmarshal(raw, &obj); err == nil {
		return obj
	}
	return string(raw)
}

func textOrDefault(t pgtype.Text, fallback string) string {
	if !t.Valid {
		return fallback
	}
	return t.String
}

func stringPtr(s string) *string {
	return &s
}

func nullableTimeArg(t pgtype.Timestamptz) any {
	if !t.Valid {
		return nil
	}
	return t.Time
}

func nullableTextArg(t pgtype.Text) any {
	if !t.Valid {
		return nil
	}
	return t.String
}

func nullableUUIDArg(u pgtype.UUID) any {
	if !u.Valid {
		return nil
	}
	return u
}
