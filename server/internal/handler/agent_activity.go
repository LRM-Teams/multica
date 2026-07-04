package handler

import (
	"context"
	"encoding/json"
	"errors"
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
	Action     *string                  `json:"action,omitempty"`
	MessageRef *AgentActivityMessageRef `json:"message_ref,omitempty"`
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
	rows, err := h.DB.Query(ctx, agentActivityUnionSQL+`
		SELECT *
		FROM activity
		WHERE ($3::timestamptz IS NULL OR (created_at, kind, id) < ($3::timestamptz, $4::text, $5::uuid))
		ORDER BY created_at DESC, kind DESC, id DESC
		LIMIT $6`, parseUUID(reqCtx.workspaceID), reqCtx.agent.ID, nullableTimeArg(beforeAt), nullableTextArg(beforeKind), nullableUUIDArg(beforeID), queryLimit)
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

func (h *Handler) queryAgentActivityRow(ctx context.Context, reqCtx agentActivityRequestContext, activityID pgtype.UUID) (agentActivityRawRow, error) {
	row := h.DB.QueryRow(ctx, agentActivityUnionSQL+`
		SELECT *
		FROM activity
		WHERE id = $3
		ORDER BY kind DESC
		LIMIT 1`, parseUUID(reqCtx.workspaceID), reqCtx.agent.ID, activityID)
	return scanAgentActivityRow(row)
}

const agentActivityUnionSQL = `
	WITH activity AS (
		SELECT
			'run'::text AS kind,
			atq.id,
			atq.agent_id,
			atq.runtime_id,
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
	return AgentActivityRunResult{
		Action:     action,
		MessageRef: agentActivityResultMessageRef(row),
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

func agentActivityRealtime(activityID string) AgentActivityRealtimeContract {
	return AgentActivityRealtimeContract{
		Scope:     "task",
		ID:        activityID,
		EventType: protocol.EventAgentActivityStep,
		Payload:   agentActivityRealtimeStepPayload,
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

func agentActivityStepToResponse(row agentActivityStepRawRow) AgentActivityStep {
	stepType := normalizeActivityStepType(row.Type)
	payload := map[string]any{}
	if content := textToPtr(row.Content); content != nil && stepType != "thinking_marker" {
		payload["content"] = *content
	}
	if input := rawJSONOrString(row.Input); input != nil && stepType != "thinking_marker" {
		payload["input"] = input
	}
	if output := textToPtr(row.Output); output != nil && stepType != "thinking_marker" {
		payload["output"] = *output
	}
	if stepType == "thinking_marker" {
		payload["redacted"] = true
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
		return "thinking_marker"
	case strings.Contains(lower, "tool") && strings.Contains(lower, "result"):
		return "output"
	case strings.Contains(lower, "tool"):
		return "command"
	case strings.Contains(lower, "error"):
		return "output"
	default:
		return "progress"
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
