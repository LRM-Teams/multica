package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// Per-manager process Markdown is a long-form working document under the
// channel's single current goal. It is intentionally separate from the
// authoritative short-status fields on channel_goal
// (progress_summary / current_step / blocker). Process endpoints never mutate
// those fields, and checkpoint/goal updates never mutate process Markdown.
//
// Auth contract:
//   - Read (human): channel member (same as GET /goal).
//   - Read (agent): agent channel surface access (same as GET /agent/.../goal).
//   - Write (human): channel owner / workspace owner|admin (same as POST|PATCH /goal).
//     May upsert any channel-manager agent's document.
//   - Write (agent): channel manager only; may upsert only the document keyed by
//     their roster identity (source_agent_id or self). Stricter than checkpoint
//     (any participating agent may checkpoint shared short status).
//
// Errors (no silent fallback / LRM-238):
//   - no current goal → 404 "channel goal not found"
//   - missing process doc on GET-by-agent → 404 "process markdown not found"
//   - target agent is not a channel manager → 400
//   - unauthorized → 403
//   - stale expected_version → 409

const maxGoalProcessMarkdownBytes = 200000

type ChannelGoalProcessMarkdownResponse struct {
	ID             string    `json:"id"`
	WorkspaceID    string    `json:"workspace_id"`
	ChannelID      string    `json:"channel_id"`
	GoalID         string    `json:"goal_id"`
	ManagerAgentID string    `json:"manager_agent_id"`
	Content        string    `json:"content"`
	Version        int64     `json:"version"`
	UpdatedByType  string    `json:"updated_by_type"`
	UpdatedByID    string    `json:"updated_by_id"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

type channelGoalProcessEnvelope struct {
	Process *ChannelGoalProcessMarkdownResponse `json:"process"`
}

type channelGoalProcessListEnvelope struct {
	GoalID    string                               `json:"goal_id"`
	Processes []ChannelGoalProcessMarkdownResponse `json:"processes"`
}

type upsertChannelGoalProcessRequest struct {
	Content         string `json:"content"`
	ExpectedVersion int64  `json:"expected_version"`
}

func (h *Handler) publishChannelGoalProcessUpdated(
	workspaceID, channelID, actorType, actorID string,
	process ChannelGoalProcessMarkdownResponse,
) {
	h.publish(protocol.EventChannelUpdated, workspaceID, actorType, actorID, map[string]any{
		"id":                 channelID,
		"goal_process":       process,
		"goal_process_event": true,
	})
}

type goalProcessScanner interface {
	Scan(dest ...any) error
}

func scanChannelGoalProcess(row goalProcessScanner) (ChannelGoalProcessMarkdownResponse, error) {
	var doc ChannelGoalProcessMarkdownResponse
	var workspaceID, channelID, goalID, managerAgentID, updatedByID pgtype.UUID
	err := row.Scan(
		&doc.ID, &workspaceID, &channelID, &goalID, &managerAgentID,
		&doc.Content, &doc.Version, &doc.UpdatedByType, &updatedByID,
		&doc.CreatedAt, &doc.UpdatedAt,
	)
	if err != nil {
		return doc, err
	}
	doc.WorkspaceID = uuidToString(workspaceID)
	doc.ChannelID = uuidToString(channelID)
	doc.GoalID = uuidToString(goalID)
	doc.ManagerAgentID = uuidToString(managerAgentID)
	doc.UpdatedByID = uuidToString(updatedByID)
	return doc, nil
}

const channelGoalProcessColumns = `
	id::text, workspace_id, channel_id, goal_id, manager_agent_id,
	content, version, updated_by_type, updated_by_id, created_at, updated_at`

func validGoalProcessContent(content string) bool {
	return utf8.ValidString(content) && len(content) <= maxGoalProcessMarkdownBytes
}

func (h *Handler) agentRosterIdentity(ctx context.Context, workspaceID, agentID pgtype.UUID) (pgtype.UUID, error) {
	var rosterID pgtype.UUID
	err := h.DB.QueryRow(ctx, `
		SELECT COALESCE(source_agent_id, id)
		FROM agent
		WHERE workspace_id = $1 AND id = $2`, workspaceID, agentID).Scan(&rosterID)
	return rosterID, err
}

func (h *Handler) agentIsChannelManagerRoster(ctx context.Context, workspaceID, channelID, managerAgentID pgtype.UUID) bool {
	var manager bool
	err := h.DB.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM channel_member
			WHERE workspace_id = $1 AND channel_id = $2
			  AND member_type = 'agent' AND member_id = $3 AND role = 'manager'
		)`, workspaceID, channelID, managerAgentID).Scan(&manager)
	return err == nil && manager
}

func (h *Handler) listChannelGoalProcesses(
	ctx context.Context, workspaceID, channelID, goalID pgtype.UUID,
) ([]ChannelGoalProcessMarkdownResponse, error) {
	rows, err := h.DB.Query(ctx, `
		SELECT `+channelGoalProcessColumns+`
		FROM channel_goal_process_markdown
		WHERE workspace_id = $1 AND channel_id = $2 AND goal_id = $3
		ORDER BY updated_at DESC, manager_agent_id ASC`, workspaceID, channelID, goalID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]ChannelGoalProcessMarkdownResponse, 0)
	for rows.Next() {
		doc, err := scanChannelGoalProcess(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, doc)
	}
	return out, rows.Err()
}

func (h *Handler) getChannelGoalProcess(
	ctx context.Context, workspaceID, channelID, goalID, managerAgentID pgtype.UUID,
) (ChannelGoalProcessMarkdownResponse, error) {
	return scanChannelGoalProcess(h.DB.QueryRow(ctx, `
		SELECT `+channelGoalProcessColumns+`
		FROM channel_goal_process_markdown
		WHERE workspace_id = $1 AND channel_id = $2 AND goal_id = $3 AND manager_agent_id = $4`,
		workspaceID, channelID, goalID, managerAgentID))
}

func (h *Handler) upsertChannelGoalProcess(
	ctx context.Context,
	workspaceID, channelID, goalID, managerAgentID pgtype.UUID,
	content string,
	expectedVersion int64,
	actorType string,
	actorID pgtype.UUID,
) (ChannelGoalProcessMarkdownResponse, error) {
	if expectedVersion == 0 {
		// Create-only path: reject if a row already exists.
		return scanChannelGoalProcess(h.DB.QueryRow(ctx, `
			INSERT INTO channel_goal_process_markdown (
				workspace_id, channel_id, goal_id, manager_agent_id, content,
				updated_by_type, updated_by_id
			) VALUES ($1, $2, $3, $4, $5, $6, $7)
			RETURNING `+channelGoalProcessColumns,
			workspaceID, channelID, goalID, managerAgentID, content, actorType, actorID))
	}
	return scanChannelGoalProcess(h.DB.QueryRow(ctx, `
		UPDATE channel_goal_process_markdown
		SET content = $1, updated_by_type = $2, updated_by_id = $3,
		    version = version + 1, updated_at = now()
		WHERE workspace_id = $4 AND channel_id = $5 AND goal_id = $6
		  AND manager_agent_id = $7 AND version = $8
		RETURNING `+channelGoalProcessColumns,
		content, actorType, actorID, workspaceID, channelID, goalID, managerAgentID, expectedVersion))
}

func (h *Handler) requireCurrentGoalOrNotFound(
	w http.ResponseWriter, ctx context.Context, workspaceID, channelID pgtype.UUID,
) (ChannelGoalResponse, bool) {
	goal, err := h.currentChannelGoal(ctx, workspaceID, channelID)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, "channel goal not found")
		return ChannelGoalResponse{}, false
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load channel goal")
		return ChannelGoalResponse{}, false
	}
	return goal, true
}

func (h *Handler) parseManagerAgentParam(w http.ResponseWriter, r *http.Request) (pgtype.UUID, bool) {
	return parseUUIDOrBadRequest(w, chi.URLParam(r, "agentId"), "manager agent id")
}

func (h *Handler) ListChannelGoalProcesses(w http.ResponseWriter, r *http.Request) {
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
	goal, ok := h.requireCurrentGoalOrNotFound(w, r.Context(), workspaceID, channelID)
	if !ok {
		return
	}
	processes, err := h.listChannelGoalProcesses(r.Context(), workspaceID, channelID, parseUUID(goal.ID))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list process markdown")
		return
	}
	writeJSON(w, http.StatusOK, channelGoalProcessListEnvelope{GoalID: goal.ID, Processes: processes})
}

func (h *Handler) GetChannelGoalProcess(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	workspaceID := parseUUID(ctxWorkspaceID(r.Context()))
	channelID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "channelId"), "channel id")
	if !ok {
		return
	}
	managerAgentID, ok := h.parseManagerAgentParam(w, r)
	if !ok {
		return
	}
	if !h.requireChannelUserMember(w, r.Context(), uuidToString(workspaceID), channelID, parseUUID(userID)) {
		return
	}
	goal, ok := h.requireCurrentGoalOrNotFound(w, r.Context(), workspaceID, channelID)
	if !ok {
		return
	}
	doc, err := h.getChannelGoalProcess(r.Context(), workspaceID, channelID, parseUUID(goal.ID), managerAgentID)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, "process markdown not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load process markdown")
		return
	}
	writeJSON(w, http.StatusOK, channelGoalProcessEnvelope{Process: &doc})
}

func (h *Handler) PutChannelGoalProcess(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	workspaceID := ctxWorkspaceID(r.Context())
	channelID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "channelId"), "channel id")
	if !ok {
		return
	}
	managerAgentID, ok := h.parseManagerAgentParam(w, r)
	if !ok {
		return
	}
	if !h.requireChannelWritable(w, r.Context(), workspaceID, channelID) ||
		!h.requireChannelManager(w, r, workspaceID, channelID, parseUUID(userID)) {
		return
	}
	h.writeChannelGoalProcess(w, r, parseUUID(workspaceID), channelID, managerAgentID, "user", parseUUID(userID))
}

func (h *Handler) ListAgentChannelGoalProcesses(w http.ResponseWriter, r *http.Request) {
	workspaceID, channelID, _, ok := h.agentGoalScope(w, r)
	if !ok {
		return
	}
	goal, ok := h.requireCurrentGoalOrNotFound(w, r.Context(), workspaceID, channelID)
	if !ok {
		return
	}
	processes, err := h.listChannelGoalProcesses(r.Context(), workspaceID, channelID, parseUUID(goal.ID))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list process markdown")
		return
	}
	writeJSON(w, http.StatusOK, channelGoalProcessListEnvelope{GoalID: goal.ID, Processes: processes})
}

func (h *Handler) GetAgentChannelGoalProcess(w http.ResponseWriter, r *http.Request) {
	workspaceID, channelID, _, ok := h.agentGoalScope(w, r)
	if !ok {
		return
	}
	managerAgentID, ok := h.parseManagerAgentParam(w, r)
	if !ok {
		return
	}
	goal, ok := h.requireCurrentGoalOrNotFound(w, r.Context(), workspaceID, channelID)
	if !ok {
		return
	}
	doc, err := h.getChannelGoalProcess(r.Context(), workspaceID, channelID, parseUUID(goal.ID), managerAgentID)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, "process markdown not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load process markdown")
		return
	}
	writeJSON(w, http.StatusOK, channelGoalProcessEnvelope{Process: &doc})
}

func (h *Handler) PutAgentChannelGoalProcess(w http.ResponseWriter, r *http.Request) {
	workspaceID, channelID, agentID, ok := h.agentGoalScope(w, r)
	if !ok {
		return
	}
	if !h.agentIsChannelManager(r.Context(), workspaceID, channelID, agentID) {
		writeError(w, http.StatusForbidden, "only a channel manager can write process markdown")
		return
	}
	if !h.agentGoalChannelWritable(r.Context(), workspaceID, channelID) {
		writeError(w, http.StatusConflict, "channel is archived")
		return
	}
	rosterID, err := h.agentRosterIdentity(r.Context(), workspaceID, agentID)
	if err != nil {
		writeError(w, http.StatusForbidden, "access denied")
		return
	}
	// Optional path param: if present, must equal roster identity (no cross-manager writes).
	if raw := chi.URLParam(r, "agentId"); raw != "" {
		requested, ok := parseUUIDOrBadRequest(w, raw, "manager agent id")
		if !ok {
			return
		}
		if uuidToString(requested) != uuidToString(rosterID) {
			writeError(w, http.StatusForbidden, "agents may only write their own process markdown")
			return
		}
	}
	h.writeChannelGoalProcess(w, r, workspaceID, channelID, rosterID, "agent", agentID)
}

func (h *Handler) writeChannelGoalProcess(
	w http.ResponseWriter, r *http.Request,
	workspaceID, channelID, managerAgentID pgtype.UUID,
	actorType string, actorID pgtype.UUID,
) {
	var req upsertChannelGoalProcessRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.ExpectedVersion < 0 {
		writeError(w, http.StatusBadRequest, "expected_version must be >= 0")
		return
	}
	if !validGoalProcessContent(req.Content) {
		writeError(w, http.StatusBadRequest, "process markdown is invalid or too long")
		return
	}
	if !h.agentIsChannelManagerRoster(r.Context(), workspaceID, channelID, managerAgentID) {
		writeError(w, http.StatusBadRequest, "manager agent is not a channel manager")
		return
	}
	goal, ok := h.requireCurrentGoalOrNotFound(w, r.Context(), workspaceID, channelID)
	if !ok {
		return
	}
	doc, err := h.upsertChannelGoalProcess(
		r.Context(), workspaceID, channelID, parseUUID(goal.ID), managerAgentID,
		req.Content, req.ExpectedVersion, actorType, actorID,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusConflict, "process markdown version is stale")
			return
		}
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			writeError(w, http.StatusConflict, "process markdown already exists; pass expected_version")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to save process markdown")
		return
	}
	actorKind := "member"
	if actorType == "agent" {
		actorKind = "agent"
	}
	h.publishChannelGoalProcessUpdated(uuidToString(workspaceID), uuidToString(channelID), actorKind, uuidToString(actorID), doc)
	status := http.StatusOK
	if doc.Version == 1 && req.ExpectedVersion == 0 {
		status = http.StatusCreated
	}
	writeJSON(w, status, channelGoalProcessEnvelope{Process: &doc})
}
