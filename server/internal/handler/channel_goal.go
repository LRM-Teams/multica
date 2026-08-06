package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

type ChannelGoalResponse struct {
	ID                string                       `json:"id"`
	WorkspaceID       string                       `json:"workspace_id"`
	ChannelID         string                       `json:"channel_id"`
	Title             string                       `json:"title"`
	Objective         string                       `json:"objective"`
	SuccessCriteria   []string                     `json:"success_criteria"`
	Status            string                       `json:"status"`
	Version           int64                        `json:"version"`
	ProgressSummary   string                       `json:"progress_summary"`
	CurrentStep       string                       `json:"current_step"`
	Blocker           string                       `json:"blocker"`
	EvidenceRefs      []string                     `json:"evidence_refs"`
	CompletedCriteria []string                     `json:"completed_criteria"`
	CreatedByType     string                       `json:"created_by_type"`
	CreatedByID       string                       `json:"created_by_id"`
	UpdatedByType     string                       `json:"updated_by_type"`
	UpdatedByID       string                       `json:"updated_by_id"`
	CreatedAt         time.Time                    `json:"created_at"`
	UpdatedAt         time.Time                    `json:"updated_at"`
	CompletedAt       *time.Time                   `json:"completed_at,omitempty"`
	WorkGraph         *channelGoalWorkGraphSummary `json:"work_graph,omitempty"`
}

type channelGoalWorkGraphSummary struct {
	ID        string `json:"id"`
	Version   int64  `json:"version"`
	Status    string `json:"status"`
	Total     int    `json:"total"`
	Completed int    `json:"completed"`
	Running   int    `json:"running"`
	Waiting   int    `json:"waiting"`
	Stale     int    `json:"stale"`
}

type channelGoalEnvelope struct {
	Goal *ChannelGoalResponse `json:"goal"`
}

type createChannelGoalRequest struct {
	Title           string   `json:"title"`
	Objective       string   `json:"objective"`
	SuccessCriteria []string `json:"success_criteria"`
}

type updateChannelGoalRequest struct {
	ExpectedVersion   int64     `json:"expected_version"`
	Title             *string   `json:"title,omitempty"`
	Objective         *string   `json:"objective,omitempty"`
	SuccessCriteria   *[]string `json:"success_criteria,omitempty"`
	Status            *string   `json:"status,omitempty"`
	ProgressSummary   *string   `json:"progress_summary,omitempty"`
	CurrentStep       *string   `json:"current_step,omitempty"`
	Blocker           *string   `json:"blocker,omitempty"`
	EvidenceRefs      *[]string `json:"evidence_refs,omitempty"`
	CompletedCriteria *[]string `json:"completed_criteria,omitempty"`
}

type checkpointChannelGoalRequest struct {
	ExpectedVersion   int64    `json:"expected_version"`
	ProgressSummary   string   `json:"progress_summary"`
	CurrentStep       string   `json:"current_step"`
	Blocker           string   `json:"blocker"`
	EvidenceRefs      []string `json:"evidence_refs"`
	CompletedCriteria []string `json:"completed_criteria"`
}

func (h *Handler) publishChannelGoalUpdated(workspaceID, channelID, actorType, actorID string, goal ChannelGoalResponse) {
	h.publish(protocol.EventChannelUpdated, workspaceID, actorType, actorID, map[string]any{
		"id":         channelID,
		"goal":       goal,
		"goal_event": true,
	})
}

func normalizeGoalStrings(values []string, maxItems, maxLen int) ([]string, bool) {
	if len(values) == 0 || len(values) > maxItems {
		return nil, false
	}
	out := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || len(value) > maxLen {
			return nil, false
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out, len(out) > 0
}

func normalizeOptionalGoalStrings(values []string, maxItems, maxLen int) ([]string, bool) {
	if len(values) == 0 {
		return []string{}, true
	}
	return normalizeGoalStrings(values, maxItems, maxLen)
}

func validGoalProgress(progress, currentStep, blocker string) bool {
	return len(progress) <= 8000 && len(currentStep) <= 1000 && len(blocker) <= 4000
}

func scanChannelGoal(row pgx.Row) (ChannelGoalResponse, error) {
	var goal ChannelGoalResponse
	var successCriteria, evidenceRefs, completedCriteria []byte
	var workspaceID, channelID, createdByID, updatedByID pgtype.UUID
	var completedAt pgtype.Timestamptz
	err := row.Scan(
		&goal.ID, &workspaceID, &channelID, &goal.Title, &goal.Objective,
		&successCriteria, &goal.Status, &goal.Version, &goal.ProgressSummary,
		&goal.CurrentStep, &goal.Blocker, &evidenceRefs, &completedCriteria,
		&goal.CreatedByType, &createdByID, &goal.UpdatedByType, &updatedByID,
		&goal.CreatedAt, &goal.UpdatedAt, &completedAt,
	)
	if err != nil {
		return goal, err
	}
	goal.WorkspaceID = uuidToString(workspaceID)
	goal.ChannelID = uuidToString(channelID)
	goal.CreatedByID = uuidToString(createdByID)
	goal.UpdatedByID = uuidToString(updatedByID)
	_ = json.Unmarshal(successCriteria, &goal.SuccessCriteria)
	_ = json.Unmarshal(evidenceRefs, &goal.EvidenceRefs)
	_ = json.Unmarshal(completedCriteria, &goal.CompletedCriteria)
	if goal.SuccessCriteria == nil {
		goal.SuccessCriteria = []string{}
	}
	if goal.EvidenceRefs == nil {
		goal.EvidenceRefs = []string{}
	}
	if goal.CompletedCriteria == nil {
		goal.CompletedCriteria = []string{}
	}
	if completedAt.Valid {
		t := completedAt.Time
		goal.CompletedAt = &t
	}
	return goal, nil
}

const channelGoalColumns = `
	id::text, workspace_id, channel_id, title, objective, success_criteria,
	status, version, progress_summary, current_step, blocker, evidence_refs,
	completed_criteria, created_by_type, created_by_id, updated_by_type,
	updated_by_id, created_at, updated_at, completed_at`

func (h *Handler) currentChannelGoal(ctx context.Context, workspaceID, channelID pgtype.UUID) (ChannelGoalResponse, error) {
	return scanChannelGoal(h.DB.QueryRow(ctx, `
		SELECT `+channelGoalColumns+`
		FROM channel_goal
		WHERE workspace_id = $1 AND channel_id = $2
		  AND status IN ('active', 'paused')
		ORDER BY created_at DESC
		LIMIT 1`, workspaceID, channelID))
}

func channelGoalContextForClaim(goal ChannelGoalResponse) *protocol.ChannelGoalContext {
	if goal.Status != "active" {
		return nil
	}
	out := &protocol.ChannelGoalContext{
		ID:                goal.ID,
		Title:             goal.Title,
		Objective:         goal.Objective,
		SuccessCriteria:   goal.SuccessCriteria,
		Version:           goal.Version,
		ProgressSummary:   goal.ProgressSummary,
		CurrentStep:       goal.CurrentStep,
		Blocker:           goal.Blocker,
		EvidenceRefs:      goal.EvidenceRefs,
		CompletedCriteria: goal.CompletedCriteria,
	}
	if goal.WorkGraph != nil {
		out.WorkGraph = &protocol.ChannelWorkGraphContext{ID: goal.WorkGraph.ID, Version: goal.WorkGraph.Version, Status: goal.WorkGraph.Status, Completed: goal.WorkGraph.Completed, Running: goal.WorkGraph.Running, Waiting: goal.WorkGraph.Waiting, Stale: goal.WorkGraph.Stale}
	}
	return out
}

// Completing the main Goal must not cascade-close Issues / Needs You / other
// coordination records (LRM-1004). UpdateChannelGoal only mutates channel_goal.

func (h *Handler) GetChannelGoal(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	workspaceID := parseUUID(ctxWorkspaceID(r.Context()))
	channelID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "channelId"), "channel id")
	if !ok {
		return
	}
	if !h.requireChannelUserMember(w, r.Context(), uuidToString(workspaceID), channelID, parseUUID(userID)) {
		return
	}
	goal, err := h.currentChannelGoal(r.Context(), workspaceID, channelID)
	if errors.Is(err, pgx.ErrNoRows) {
		writeJSON(w, http.StatusOK, channelGoalEnvelope{})
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load channel goal")
		return
	}
	h.hydrateChannelGoalWorkGraph(r.Context(), &goal)
	writeJSON(w, http.StatusOK, channelGoalEnvelope{Goal: &goal})
}

func (h *Handler) hydrateChannelGoalWorkGraph(ctx context.Context, goal *ChannelGoalResponse) {
	if goal == nil {
		return
	}
	var s channelGoalWorkGraphSummary
	err := h.DB.QueryRow(ctx, `SELECT g.id::text,g.current_version,g.status,count(n.id)::int,count(n.id) FILTER(WHERE n.execution_status='succeeded' AND n.validity_status='valid')::int,count(n.id) FILTER(WHERE n.execution_status='running')::int,count(n.id) FILTER(WHERE n.execution_status IN('draft','queued','ready','waiting'))::int,count(n.id) FILTER(WHERE n.validity_status IN('stale','invalidated'))::int FROM work_graph g LEFT JOIN work_graph_node n ON n.graph_id=g.id WHERE g.workspace_id=$1::uuid AND g.anchor_kind='channel_goal' AND g.anchor_id=$2::uuid GROUP BY g.id,g.current_version,g.status`, goal.WorkspaceID, goal.ID).Scan(&s.ID, &s.Version, &s.Status, &s.Total, &s.Completed, &s.Running, &s.Waiting, &s.Stale)
	if err == nil {
		goal.WorkGraph = &s
	}
}

func (h *Handler) CreateChannelGoal(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	workspaceID := ctxWorkspaceID(r.Context())
	channelID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "channelId"), "channel id")
	if !ok {
		return
	}
	if !h.requireChannelWritable(w, r.Context(), workspaceID, channelID) ||
		!h.requireChannelManager(w, r, workspaceID, channelID, parseUUID(userID)) {
		return
	}
	var req createChannelGoalRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	req.Title = strings.TrimSpace(req.Title)
	req.Objective = strings.TrimSpace(req.Objective)
	criteria, valid := normalizeGoalStrings(req.SuccessCriteria, 50, 1000)
	if req.Title == "" || len(req.Title) > 160 || req.Objective == "" || len(req.Objective) > 8000 || !valid {
		writeError(w, http.StatusBadRequest, "title, objective, and success criteria are required")
		return
	}
	criteriaJSON, _ := json.Marshal(criteria)
	goal, err := scanChannelGoal(h.DB.QueryRow(r.Context(), `
		INSERT INTO channel_goal (
			workspace_id, channel_id, title, objective, success_criteria,
			created_by_type, created_by_id, updated_by_type, updated_by_id
		) VALUES ($1, $2, $3, $4, $5, 'user', $6, 'user', $6)
		RETURNING `+channelGoalColumns,
		parseUUID(workspaceID), channelID, req.Title, req.Objective, criteriaJSON, parseUUID(userID)))
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			writeError(w, http.StatusConflict, "channel already has a current goal")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to create channel goal")
		return
	}
	h.publishChannelGoalUpdated(workspaceID, uuidToString(channelID), "member", userID, goal)
	writeJSON(w, http.StatusCreated, channelGoalEnvelope{Goal: &goal})
}

func allGoalCriteriaCompleted(criteria, completed []string) bool {
	done := make(map[string]struct{}, len(completed))
	for _, criterion := range completed {
		done[criterion] = struct{}{}
	}
	for _, criterion := range criteria {
		if _, ok := done[criterion]; !ok {
			return false
		}
	}
	return true
}

// openSubgoalsBlockMainGoalComplete reports whether any non-terminal subgoals
// still block completing the parent Goal (LRM-1004 / design gate). This is a
// gate only — it never cascade-closes Issues or other coordination records.
func (h *Handler) openSubgoalsBlockMainGoalComplete(ctx context.Context, goalID string) bool {
	var openSubgoals int
	err := h.DB.QueryRow(ctx, `
		SELECT count(*) FROM channel_goal_subgoal
		WHERE goal_id = $1::uuid AND status IN ('captured','in_progress','waiting')`, goalID).Scan(&openSubgoals)
	return err == nil && openSubgoals > 0
}

func (h *Handler) openWorkGraphBlocksMainGoalComplete(ctx context.Context, goalID string) bool {
	var open int
	err := h.DB.QueryRow(ctx, `SELECT count(*) FROM work_graph_node n JOIN work_graph g ON g.id=n.graph_id WHERE g.anchor_kind='channel_goal' AND g.anchor_id=$1::uuid AND g.status NOT IN('completed','cancelled') AND (n.execution_status NOT IN('succeeded','cancelled') OR n.validity_status<>'valid' OR (n.role='verifier' AND n.review_status<>'accepted'))`, goalID).Scan(&open)
	return err == nil && open > 0
}

func (h *Handler) UpdateChannelGoal(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	workspaceID := ctxWorkspaceID(r.Context())
	channelID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "channelId"), "channel id")
	if !ok {
		return
	}
	if !h.requireChannelWritable(w, r.Context(), workspaceID, channelID) ||
		!h.requireChannelManager(w, r, workspaceID, channelID, parseUUID(userID)) {
		return
	}
	var req updateChannelGoalRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ExpectedVersion < 1 {
		writeError(w, http.StatusBadRequest, "expected_version is required")
		return
	}
	current, err := h.currentChannelGoal(r.Context(), parseUUID(workspaceID), channelID)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, "channel goal not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load channel goal")
		return
	}
	if current.Version != req.ExpectedVersion {
		writeError(w, http.StatusConflict, "channel goal version is stale")
		return
	}
	if req.Title != nil {
		current.Title = strings.TrimSpace(*req.Title)
	}
	if req.Objective != nil {
		current.Objective = strings.TrimSpace(*req.Objective)
	}
	if req.SuccessCriteria != nil {
		var valid bool
		current.SuccessCriteria, valid = normalizeGoalStrings(*req.SuccessCriteria, 50, 1000)
		if !valid {
			writeError(w, http.StatusBadRequest, "success criteria must be a non-empty list")
			return
		}
		if req.CompletedCriteria == nil {
			kept := make([]string, 0, len(current.CompletedCriteria))
			criteriaSet := make(map[string]struct{}, len(current.SuccessCriteria))
			for _, criterion := range current.SuccessCriteria {
				criteriaSet[criterion] = struct{}{}
			}
			for _, criterion := range current.CompletedCriteria {
				if _, exists := criteriaSet[criterion]; exists {
					kept = append(kept, criterion)
				}
			}
			current.CompletedCriteria = kept
		}
	}
	if req.ProgressSummary != nil {
		current.ProgressSummary = strings.TrimSpace(*req.ProgressSummary)
	}
	if req.CurrentStep != nil {
		current.CurrentStep = strings.TrimSpace(*req.CurrentStep)
	}
	if req.Blocker != nil {
		current.Blocker = strings.TrimSpace(*req.Blocker)
	}
	if !validGoalProgress(current.ProgressSummary, current.CurrentStep, current.Blocker) {
		writeError(w, http.StatusBadRequest, "goal progress fields are too long")
		return
	}
	if req.EvidenceRefs != nil {
		var valid bool
		current.EvidenceRefs, valid = normalizeOptionalGoalStrings(*req.EvidenceRefs, 100, 2000)
		if !valid {
			writeError(w, http.StatusBadRequest, "evidence references are invalid")
			return
		}
	}
	if req.CompletedCriteria != nil {
		var valid bool
		current.CompletedCriteria, valid = normalizeOptionalGoalStrings(*req.CompletedCriteria, 50, 1000)
		if !valid {
			writeError(w, http.StatusBadRequest, "completed criteria are invalid")
			return
		}
	}
	if req.Status != nil {
		switch *req.Status {
		case "active", "paused", "completed", "cancelled":
			current.Status = *req.Status
		default:
			writeError(w, http.StatusBadRequest, "invalid goal status")
			return
		}
	}
	if current.Title == "" || len(current.Title) > 160 || current.Objective == "" || len(current.Objective) > 8000 {
		writeError(w, http.StatusBadRequest, "title and objective are required")
		return
	}
	criteriaSet := make(map[string]struct{}, len(current.SuccessCriteria))
	for _, criterion := range current.SuccessCriteria {
		criteriaSet[criterion] = struct{}{}
	}
	for _, completed := range current.CompletedCriteria {
		if _, ok := criteriaSet[completed]; !ok {
			writeError(w, http.StatusBadRequest, "completed criterion is not in success criteria")
			return
		}
	}
	if current.Status == "completed" &&
		(!allGoalCriteriaCompleted(current.SuccessCriteria, current.CompletedCriteria) || len(current.EvidenceRefs) == 0) {
		writeError(w, http.StatusConflict, "all success criteria need evidence-backed completion")
		return
	}
	if current.Status == "completed" && h.openSubgoalsBlockMainGoalComplete(r.Context(), current.ID) {
		writeError(w, http.StatusConflict, "resolve or cancel open subgoals before completing the main goal")
		return
	}
	if current.Status == "completed" && h.openWorkGraphBlocksMainGoalComplete(r.Context(), current.ID) {
		writeError(w, http.StatusConflict, "complete and validate the work graph before completing the main goal")
		return
	}
	criteriaJSON, _ := json.Marshal(current.SuccessCriteria)
	evidenceJSON, _ := json.Marshal(current.EvidenceRefs)
	completedJSON, _ := json.Marshal(current.CompletedCriteria)
	goal, err := scanChannelGoal(h.DB.QueryRow(r.Context(), `
		UPDATE channel_goal
		SET title = $1, objective = $2, success_criteria = $3, status = $4,
		    progress_summary = $5, current_step = $6, blocker = $7,
		    evidence_refs = $8, completed_criteria = $9,
		    updated_by_type = 'user', updated_by_id = $10,
		    version = version + 1, updated_at = now(),
		    completed_at = CASE WHEN $4 = 'completed' THEN now() ELSE completed_at END
		WHERE id = $11 AND version = $12
		RETURNING `+channelGoalColumns,
		current.Title, current.Objective, criteriaJSON, current.Status,
		current.ProgressSummary, current.CurrentStep, current.Blocker,
		evidenceJSON, completedJSON, parseUUID(userID), parseUUID(current.ID), req.ExpectedVersion))
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusConflict, "channel goal version is stale")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update channel goal")
		return
	}
	h.publishChannelGoalUpdated(workspaceID, uuidToString(channelID), "member", userID, goal)
	if h.WorkGraph != nil {
		_ = h.WorkGraph.SyncGoalLifecycle(r.Context(), workspaceID, goal.ID, goal.Status)
	}
	h.hydrateChannelGoalWorkGraph(r.Context(), &goal)
	writeJSON(w, http.StatusOK, channelGoalEnvelope{Goal: &goal})
}

func (h *Handler) agentGoalScope(w http.ResponseWriter, r *http.Request) (pgtype.UUID, pgtype.UUID, pgtype.UUID, bool) {
	p, ok := h.requireAgentPrincipal(w, r)
	if !ok {
		return pgtype.UUID{}, pgtype.UUID{}, pgtype.UUID{}, false
	}
	agentID, ok := p.AgentUUID()
	if !ok {
		writeError(w, http.StatusForbidden, "access denied")
		return pgtype.UUID{}, pgtype.UUID{}, pgtype.UUID{}, false
	}
	channelID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "channelId"), "channel id")
	if !ok || !h.requireAgentSurfaceAccessHTTP(w, r, p, channelID) {
		return pgtype.UUID{}, pgtype.UUID{}, pgtype.UUID{}, false
	}
	return parseUUID(p.WorkspaceID), channelID, agentID, true
}

func (h *Handler) agentIsChannelManager(ctx context.Context, workspaceID, channelID, agentID pgtype.UUID) bool {
	var manager bool
	err := h.DB.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM agent execution_agent
			JOIN channel_member member
			  ON member.workspace_id = execution_agent.workspace_id
			 AND member.channel_id = $2
			 AND member.member_type = 'agent'
			 AND member.member_id = COALESCE(execution_agent.source_agent_id, execution_agent.id)
			 AND member.role = 'manager'
			WHERE execution_agent.workspace_id = $1 AND execution_agent.id = $3
		)`, workspaceID, channelID, agentID).Scan(&manager)
	return err == nil && manager
}

func (h *Handler) agentGoalChannelWritable(ctx context.Context, workspaceID, channelID pgtype.UUID) bool {
	var writable bool
	err := h.DB.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM channel
			WHERE workspace_id = $1 AND id = $2 AND kind = 'group' AND archived_at IS NULL
		)`, workspaceID, channelID).Scan(&writable)
	return err == nil && writable
}

func (h *Handler) GetAgentChannelGoal(w http.ResponseWriter, r *http.Request) {
	workspaceID, channelID, _, ok := h.agentGoalScope(w, r)
	if !ok {
		return
	}
	goal, err := h.currentChannelGoal(r.Context(), workspaceID, channelID)
	if errors.Is(err, pgx.ErrNoRows) {
		writeJSON(w, http.StatusOK, channelGoalEnvelope{})
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load channel goal")
		return
	}
	h.hydrateChannelGoalWorkGraph(r.Context(), &goal)
	writeJSON(w, http.StatusOK, channelGoalEnvelope{Goal: &goal})
}

func (h *Handler) CreateAgentChannelGoal(w http.ResponseWriter, r *http.Request) {
	workspaceID, channelID, agentID, ok := h.agentGoalScope(w, r)
	if !ok {
		return
	}
	if !h.agentIsChannelManager(r.Context(), workspaceID, channelID, agentID) {
		writeError(w, http.StatusForbidden, "only a channel manager can create a goal")
		return
	}
	if !h.agentGoalChannelWritable(r.Context(), workspaceID, channelID) {
		writeError(w, http.StatusConflict, "channel is archived")
		return
	}
	var req createChannelGoalRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	req.Title = strings.TrimSpace(req.Title)
	req.Objective = strings.TrimSpace(req.Objective)
	criteria, valid := normalizeGoalStrings(req.SuccessCriteria, 50, 1000)
	if req.Title == "" || len(req.Title) > 160 || req.Objective == "" || len(req.Objective) > 8000 || !valid {
		writeError(w, http.StatusBadRequest, "title, objective, and success criteria are required")
		return
	}
	criteriaJSON, _ := json.Marshal(criteria)
	goal, err := scanChannelGoal(h.DB.QueryRow(r.Context(), `
		INSERT INTO channel_goal (
			workspace_id, channel_id, title, objective, success_criteria,
			created_by_type, created_by_id, updated_by_type, updated_by_id
		) VALUES ($1, $2, $3, $4, $5, 'agent', $6, 'agent', $6)
		RETURNING `+channelGoalColumns,
		workspaceID, channelID, req.Title, req.Objective, criteriaJSON, agentID))
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			writeError(w, http.StatusConflict, "channel already has a current goal")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to create channel goal")
		return
	}
	h.publishChannelGoalUpdated(uuidToString(workspaceID), uuidToString(channelID), "agent", uuidToString(agentID), goal)
	writeJSON(w, http.StatusCreated, channelGoalEnvelope{Goal: &goal})
}

func (h *Handler) UpdateAgentChannelGoal(w http.ResponseWriter, r *http.Request) {
	workspaceID, channelID, agentID, ok := h.agentGoalScope(w, r)
	if !ok {
		return
	}
	if !h.agentIsChannelManager(r.Context(), workspaceID, channelID, agentID) {
		writeError(w, http.StatusForbidden, "only a channel manager can revise a goal")
		return
	}
	if !h.agentGoalChannelWritable(r.Context(), workspaceID, channelID) {
		writeError(w, http.StatusConflict, "channel is archived")
		return
	}
	var req updateChannelGoalRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ExpectedVersion < 1 {
		writeError(w, http.StatusBadRequest, "expected_version is required")
		return
	}
	current, err := h.currentChannelGoal(r.Context(), workspaceID, channelID)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, "channel goal not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load channel goal")
		return
	}
	if current.Version != req.ExpectedVersion {
		writeError(w, http.StatusConflict, "channel goal version is stale")
		return
	}
	if current.CreatedByType == "user" &&
		(req.Title != nil || req.Objective != nil || req.SuccessCriteria != nil) {
		writeError(w, http.StatusForbidden, "a human-authored goal can only be revised by a human")
		return
	}
	if req.Title != nil {
		current.Title = strings.TrimSpace(*req.Title)
	}
	if req.Objective != nil {
		current.Objective = strings.TrimSpace(*req.Objective)
	}
	if req.SuccessCriteria != nil {
		var valid bool
		current.SuccessCriteria, valid = normalizeGoalStrings(*req.SuccessCriteria, 50, 1000)
		if !valid {
			writeError(w, http.StatusBadRequest, "success criteria must be a non-empty list")
			return
		}
		kept := make([]string, 0, len(current.CompletedCriteria))
		criteriaSet := make(map[string]struct{}, len(current.SuccessCriteria))
		for _, criterion := range current.SuccessCriteria {
			criteriaSet[criterion] = struct{}{}
		}
		for _, criterion := range current.CompletedCriteria {
			if _, exists := criteriaSet[criterion]; exists {
				kept = append(kept, criterion)
			}
		}
		current.CompletedCriteria = kept
	}
	if req.Status != nil {
		switch *req.Status {
		case "active", "paused", "completed", "cancelled":
			current.Status = *req.Status
		default:
			writeError(w, http.StatusBadRequest, "invalid goal status")
			return
		}
	}
	if current.Title == "" || len(current.Title) > 160 || current.Objective == "" || len(current.Objective) > 8000 {
		writeError(w, http.StatusBadRequest, "title and objective are required")
		return
	}
	if current.Status == "completed" &&
		(!allGoalCriteriaCompleted(current.SuccessCriteria, current.CompletedCriteria) || len(current.EvidenceRefs) == 0) {
		writeError(w, http.StatusConflict, "all success criteria need evidence-backed completion")
		return
	}
	if current.Status == "completed" && h.openSubgoalsBlockMainGoalComplete(r.Context(), current.ID) {
		writeError(w, http.StatusConflict, "resolve or cancel open subgoals before completing the main goal")
		return
	}
	if current.Status == "completed" && h.openWorkGraphBlocksMainGoalComplete(r.Context(), current.ID) {
		writeError(w, http.StatusConflict, "complete and validate the work graph before completing the main goal")
		return
	}
	criteriaJSON, _ := json.Marshal(current.SuccessCriteria)
	completedJSON, _ := json.Marshal(current.CompletedCriteria)
	goal, err := scanChannelGoal(h.DB.QueryRow(r.Context(), `
		UPDATE channel_goal
		SET title = $1, objective = $2, success_criteria = $3, status = $4,
		    completed_criteria = $5, updated_by_type = 'agent', updated_by_id = $6,
		    version = version + 1, updated_at = now(),
		    completed_at = CASE WHEN $4 = 'completed' THEN now() ELSE completed_at END
		WHERE id = $7 AND version = $8
		RETURNING `+channelGoalColumns,
		current.Title, current.Objective, criteriaJSON, current.Status,
		completedJSON, agentID, parseUUID(current.ID), req.ExpectedVersion))
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusConflict, "channel goal version is stale")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update channel goal")
		return
	}
	h.publishChannelGoalUpdated(uuidToString(workspaceID), uuidToString(channelID), "agent", uuidToString(agentID), goal)
	if h.WorkGraph != nil {
		_ = h.WorkGraph.SyncGoalLifecycle(r.Context(), uuidToString(workspaceID), goal.ID, goal.Status)
	}
	writeJSON(w, http.StatusOK, channelGoalEnvelope{Goal: &goal})
}

func (h *Handler) CheckpointAgentChannelGoal(w http.ResponseWriter, r *http.Request) {
	workspaceID, channelID, agentID, ok := h.agentGoalScope(w, r)
	if !ok {
		return
	}
	if !h.agentGoalChannelWritable(r.Context(), workspaceID, channelID) {
		writeError(w, http.StatusConflict, "channel is archived")
		return
	}
	var req checkpointChannelGoalRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ExpectedVersion < 1 {
		writeError(w, http.StatusBadRequest, "expected_version is required")
		return
	}
	current, err := h.currentChannelGoal(r.Context(), workspaceID, channelID)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, "active channel goal not found")
		return
	}
	if err != nil || current.Status != "active" {
		writeError(w, http.StatusConflict, "channel goal is not active")
		return
	}
	req.ProgressSummary = strings.TrimSpace(req.ProgressSummary)
	req.CurrentStep = strings.TrimSpace(req.CurrentStep)
	req.Blocker = strings.TrimSpace(req.Blocker)
	if !validGoalProgress(req.ProgressSummary, req.CurrentStep, req.Blocker) {
		writeError(w, http.StatusBadRequest, "goal progress fields are too long")
		return
	}
	criteriaSet := make(map[string]struct{}, len(current.SuccessCriteria))
	for _, criterion := range current.SuccessCriteria {
		criteriaSet[criterion] = struct{}{}
	}
	for _, completed := range req.CompletedCriteria {
		if _, exists := criteriaSet[completed]; !exists {
			writeError(w, http.StatusBadRequest, "completed criterion is not in success criteria")
			return
		}
	}
	evidenceRefs, valid := normalizeOptionalGoalStrings(req.EvidenceRefs, 100, 2000)
	if !valid {
		writeError(w, http.StatusBadRequest, "evidence references are invalid")
		return
	}
	completedCriteria, valid := normalizeOptionalGoalStrings(req.CompletedCriteria, 50, 1000)
	if !valid {
		writeError(w, http.StatusBadRequest, "completed criteria are invalid")
		return
	}
	evidenceJSON, _ := json.Marshal(evidenceRefs)
	completedJSON, _ := json.Marshal(completedCriteria)
	goal, err := scanChannelGoal(h.DB.QueryRow(r.Context(), `
		UPDATE channel_goal
		SET progress_summary = $1, current_step = $2, blocker = $3,
		    evidence_refs = $4, completed_criteria = $5,
		    updated_by_type = 'agent', updated_by_id = $6,
		    version = version + 1, updated_at = now()
		WHERE id = $7 AND version = $8 AND status = 'active'
		RETURNING `+channelGoalColumns,
		req.ProgressSummary, req.CurrentStep, req.Blocker, evidenceJSON, completedJSON, agentID,
		parseUUID(current.ID), req.ExpectedVersion))
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusConflict, "channel goal version is stale")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to checkpoint channel goal")
		return
	}
	h.publishChannelGoalUpdated(uuidToString(workspaceID), uuidToString(channelID), "agent", uuidToString(agentID), goal)
	writeJSON(w, http.StatusOK, channelGoalEnvelope{Goal: &goal})
}
