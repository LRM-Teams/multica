package handler

import (
	"context"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

type MemberProfileResponse struct {
	MemberType     string                     `json:"member_type"`
	MemberID       string                     `json:"member_id"`
	Name           string                     `json:"name"`
	DisplayName    string                     `json:"display_name"`
	AvatarURL      *string                    `json:"avatar_url"`
	Description    string                     `json:"description"`
	Role           string                     `json:"role"`
	Status         *string                    `json:"status"`
	RecentActivity []AgentRecentActivityItem  `json:"recent_activity"`
	ProfileAccess  string                     `json:"profile_access"`
	MemoryGrowth   *AgentMemoryGrowthResponse `json:"memory_growth,omitempty"`
	Honor          *honorSnapshotResponse     `json:"honor,omitempty"`
}

type AgentRecentActivityItem struct {
	ID           string `json:"id"`
	Kind         string `json:"kind"`
	Label        string `json:"label"`
	DisplayLabel string `json:"display_label"`
	LabelKey     string `json:"label_key"`
	ActivityKind string `json:"activity_kind"`
	DetailKind   string `json:"detail_kind"`
	OccurredAt   string `json:"occurred_at"`
	Status       string `json:"status"`
}

func (h *Handler) GetMemberProfile(w http.ResponseWriter, r *http.Request) {
	memberType := chi.URLParam(r, "memberType")
	memberID := chi.URLParam(r, "memberId")
	memberUUID, ok := parseUUIDOrBadRequest(w, memberID, "member id")
	if !ok {
		return
	}

	switch memberType {
	case "user":
		h.getUserMemberProfile(w, r, memberUUID)
	case "agent":
		h.getAgentMemberProfile(w, r, memberID)
	default:
		writeError(w, http.StatusBadRequest, "member_type must be user or agent")
	}
}

func (h *Handler) getUserMemberProfile(w http.ResponseWriter, r *http.Request, userID pgtype.UUID) {
	workspaceID := h.resolveWorkspaceID(r)
	member, err := h.Queries.GetMemberByUserAndWorkspace(r.Context(), db.GetMemberByUserAndWorkspaceParams{
		UserID:      userID,
		WorkspaceID: parseUUID(workspaceID),
	})
	if err != nil {
		if err == pgx.ErrNoRows {
			writeError(w, http.StatusNotFound, "member not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to load member profile")
		return
	}

	user, err := h.Queries.GetUser(r.Context(), userID)
	if err != nil {
		if err == pgx.ErrNoRows {
			writeError(w, http.StatusNotFound, "member not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to load member profile")
		return
	}

	writeJSON(w, http.StatusOK, MemberProfileResponse{
		MemberType:     "user",
		MemberID:       uuidToString(user.ID),
		Name:           user.Name,
		DisplayName:    userDisplayName(user),
		AvatarURL:      textToPtr(user.AvatarUrl),
		Description:    user.ProfileDescription,
		Role:           member.Role,
		Status:         nil,
		RecentActivity: []AgentRecentActivityItem{},
		ProfileAccess:  "full",
		Honor:          h.userHonorSnapshot(r, user),
	})
}

func (h *Handler) userHonorSnapshot(r *http.Request, user db.User) *honorSnapshotResponse {
	if h.HonorService == nil {
		return nil
	}
	wall, err := h.HonorService.GetPublicWall(r.Context(), user)
	if err != nil {
		return nil
	}
	return &honorSnapshotResponse{
		Level:     wall.Level,
		NameStyle: wall.NameStyle,
		Badge:     wall.EquippedBadge,
	}
}

func (h *Handler) getAgentMemberProfile(w http.ResponseWriter, r *http.Request, agentID string) {
	agent, ok := h.loadAgentForUser(w, r, agentID)
	if !ok {
		return
	}

	// Task #908: identity + presence status (name/avatar/description/status
	// — "is it around, can it work") are unconditional for every workspace
	// member. RecentActivity/MemoryGrowth are internal-construction/history
	// data and stay gated by canAccessAgentInternals, same as GetAgent.
	workspaceID := uuidToString(agent.WorkspaceID)
	actorType, actorID := h.resolveActor(r, requestUserID(r), workspaceID)
	status := agent.Status
	if !h.canAccessAgentInternals(r.Context(), agent, actorType, actorID, workspaceID) {
		writeJSON(w, http.StatusOK, MemberProfileResponse{
			MemberType:     "agent",
			MemberID:       uuidToString(agent.ID),
			Name:           agent.Name,
			DisplayName:    agentDisplayName(agent),
			AvatarURL:      util.StringToPtr(agent.AvatarUrl),
			Description:    agent.Description,
			Role:           "Agent",
			Status:         &status,
			RecentActivity: []AgentRecentActivityItem{},
			ProfileAccess:  "identity_only",
		})
		return
	}

	activity, err := h.listAgentRecentActivity(r.Context(), agent, 5)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load member profile")
		return
	}
	growth, err := h.loadAgentMemoryGrowth(r.Context(), agent.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load member profile")
		return
	}
	writeJSON(w, http.StatusOK, MemberProfileResponse{
		MemberType:     "agent",
		MemberID:       uuidToString(agent.ID),
		Name:           agent.Name,
		DisplayName:    agentDisplayName(agent),
		AvatarURL:      util.StringToPtr(agent.AvatarUrl),
		Description:    agent.Description,
		Role:           "Agent",
		Status:         &status,
		RecentActivity: activity,
		ProfileAccess:  "full",
		MemoryGrowth:   growth,
	})
}

func (h *Handler) listAgentRecentActivity(ctx context.Context, agent db.Agent, limit int) ([]AgentRecentActivityItem, error) {
	rows, err := h.DB.Query(ctx, `
		SELECT atq.id,
		       CASE
		         WHEN atq.status IN ('pending', 'failed') THEN 'queued'
		         WHEN atq.status = 'draining' THEN 'running'
		         WHEN atq.status = 'suppressed' THEN 'cancelled'
		         ELSE COALESCE(atq.terminal_outcome, 'completed')
		       END AS status,
		       COALESCE(atq.completed_at, atq.started_at, atq.dispatched_at, atq.created_at) AS occurred_at
		FROM agent_inbox_event atq
		JOIN agent a ON a.id = atq.agent_id
		WHERE atq.agent_id = $1
		  AND a.workspace_id = $2
		  AND atq.status IN ('pending', 'draining', 'failed', 'acked', 'suppressed')
		ORDER BY occurred_at DESC, atq.id DESC
		LIMIT $3`,
		agent.ID, agent.WorkspaceID, int32(limit))
	if err != nil {
		return nil, err
	}
	// Scan every row into memory and close the cursor BEFORE calling
	// attachRecentExecutionActivity: that helper acquires its own pool
	// connection (task_message lookup), and doing so while this cursor is
	// still open can deadlock a bounded pool under concurrent requests (same
	// shape as the #1803 attachAgentRuntimeNames bug).
	taskIDs := make([]pgtype.UUID, 0, limit)
	items := make([]AgentRecentActivityItem, 0, limit)
	for rows.Next() {
		var (
			taskID     pgtype.UUID
			status     string
			occurredAt pgtype.Timestamptz
		)
		if err := rows.Scan(&taskID, &status, &occurredAt); err != nil {
			continue
		}
		taskIDs = append(taskIDs, taskID)
		items = append(items, AgentRecentActivityItem{
			ID:           uuidToString(taskID),
			Kind:         recentActivityFallbackKind(status),
			Label:        recentActivityFallbackLabel(status),
			DisplayLabel: recentActivityFallbackDisplayLabel(status),
			LabelKey:     recentActivityFallbackLabelKey(status),
			ActivityKind: recentActivityFallbackActivityKind(status),
			DetailKind:   recentActivityFallbackDetailKind(status),
			OccurredAt:   timestampToString(occurredAt),
			Status:       status,
		})
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	for i := range items {
		h.attachRecentExecutionActivity(ctx, taskIDs[i], items[i].Status, &items[i])
	}
	return items, nil
}

func (h *Handler) attachRecentExecutionActivity(ctx context.Context, taskID pgtype.UUID, status string, item *AgentRecentActivityItem) {
	messages, err := h.listRecentTaskActivityMessages(ctx, taskID, 5)
	if err != nil || len(messages) == 0 {
		return
	}

	var textFallback *AgentRecentActivityItem
	for _, msg := range messages {
		switch msg.Type {
		case "tool_use":
			if projected, ok := projectToolUseActivity(msg, status); ok {
				occurredAt := item.OccurredAt
				*item = projected
				item.ID = uuidToString(taskID)
				if item.OccurredAt == "" {
					item.OccurredAt = occurredAt
				}
				item.Status = status
				return
			}
		case "text":
			if textFallback == nil {
				if projected, ok := projectTextActivity(msg, status); ok {
					textFallback = &projected
				}
			}
		}
	}
	if textFallback != nil {
		textFallback.ID = uuidToString(taskID)
		textFallback.Status = status
		if textFallback.OccurredAt == "" {
			textFallback.OccurredAt = item.OccurredAt
		}
		*item = *textFallback
	}
}

type recentTaskActivityMessage struct {
	ID        pgtype.UUID
	TaskID    pgtype.UUID
	Seq       int32
	Type      string
	Tool      pgtype.Text
	Content   pgtype.Text
	CreatedAt pgtype.Timestamptz
}

func (h *Handler) listRecentTaskActivityMessages(ctx context.Context, taskID pgtype.UUID, limit int32) ([]recentTaskActivityMessage, error) {
	rows, err := h.DB.Query(ctx, `
		SELECT id, task_id, seq, type, tool, content, created_at
		FROM task_message
		WHERE task_id = $1
		  AND type IN ('tool_use', 'text')
		ORDER BY seq DESC
		LIMIT $2`, taskID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	messages := make([]recentTaskActivityMessage, 0, limit)
	for rows.Next() {
		var msg recentTaskActivityMessage
		if err := rows.Scan(
			&msg.ID,
			&msg.TaskID,
			&msg.Seq,
			&msg.Type,
			&msg.Tool,
			&msg.Content,
			&msg.CreatedAt,
		); err != nil {
			return nil, err
		}
		messages = append(messages, msg)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return messages, nil
}

func projectToolUseActivity(msg recentTaskActivityMessage, status string) (AgentRecentActivityItem, bool) {
	tool := strings.ToLower(strings.TrimSpace(msg.Tool.String))
	label, displayLabel, labelKey, kind := toolActivityLabel(tool, status)
	if label == "" {
		return AgentRecentActivityItem{}, false
	}

	return AgentRecentActivityItem{
		Kind:         kind,
		Label:        label,
		DisplayLabel: displayLabel,
		LabelKey:     labelKey,
		ActivityKind: "tool_call",
		DetailKind:   "tool_use",
		OccurredAt:   timestampToString(msg.CreatedAt),
	}, true
}

func projectTextActivity(msg recentTaskActivityMessage, status string) (AgentRecentActivityItem, bool) {
	if !msg.Content.Valid || strings.TrimSpace(msg.Content.String) == "" {
		return AgentRecentActivityItem{}, false
	}
	return AgentRecentActivityItem{
		Kind:         "text",
		Label:        textActivityLabel(status),
		DisplayLabel: "Output",
		LabelKey:     "output",
		ActivityKind: "text",
		DetailKind:   "text",
		OccurredAt:   timestampToString(msg.CreatedAt),
	}, true
}

func toolActivityLabel(tool, status string) (label string, displayLabel string, labelKey string, kind string) {
	active := isActiveTaskStatus(status)
	switch {
	case isCommandTool(tool):
		return activeActivityLabel("Running command…", active), "Running command", "running_command", "command"
	case strings.Contains(tool, "send_message"):
		return activeActivityLabel("Sending message…", active), "Sending message", "sending_message", "tool_use"
	case strings.Contains(tool, "write"):
		return activeActivityLabel("Writing file…", active), "Writing file", "writing_file", "tool_use"
	case isFileEditTool(tool):
		return activeActivityLabel("Editing file…", active), "Editing file", "editing_file", "tool_use"
	case strings.Contains(tool, "web_search") || strings.Contains(tool, "websearch"):
		return activeActivityLabel("Searching web…", active), "Searching web", "searching_web", "tool_use"
	case strings.Contains(tool, "glob"):
		return activeActivityLabel("Searching files…", active), "Searching files", "searching_files", "tool_use"
	case isSearchTool(tool):
		return activeActivityLabel("Searching code…", active), "Searching code", "searching_code", "tool_use"
	case isFileReadTool(tool):
		return activeActivityLabel("Reading file…", active), "Reading file", "reading_file", "tool_use"
	case tool != "":
		return activeActivityLabel("Working…", active), "Working", "working", "tool_use"
	default:
		return "", "", "", ""
	}
}

func activeActivityLabel(label string, active bool) string {
	if active {
		return label
	}
	return strings.TrimSuffix(label, "…")
}

func isCommandTool(tool string) bool {
	return strings.Contains(tool, "exec") ||
		strings.Contains(tool, "command") ||
		strings.Contains(tool, "shell") ||
		strings.Contains(tool, "terminal") ||
		strings.Contains(tool, "bash")
}

func isFileEditTool(tool string) bool {
	return strings.Contains(tool, "edit") ||
		strings.Contains(tool, "write") ||
		strings.Contains(tool, "patch") ||
		strings.Contains(tool, "apply")
}

func isFileReadTool(tool string) bool {
	return strings.Contains(tool, "read") ||
		strings.Contains(tool, "open") ||
		strings.Contains(tool, "file") ||
		strings.Contains(tool, "list") ||
		strings.Contains(tool, "cat")
}

func isSearchTool(tool string) bool {
	return strings.Contains(tool, "search") ||
		strings.Contains(tool, "query") ||
		strings.Contains(tool, "grep") ||
		strings.Contains(tool, "rg") ||
		strings.Contains(tool, "find") ||
		strings.Contains(tool, "issue") ||
		strings.Contains(tool, "context")
}

func isActiveTaskStatus(status string) bool {
	return status == "pending" || status == "draining" || status == "failed"
}

func recentActivityFallbackKind(status string) string {
	switch status {
	case "pending":
		return "queued"
	case "draining":
		return "working"
	case "failed":
		return "failed"
	case "suppressed":
		return "cancelled"
	default:
		return "task"
	}
}

func recentActivityFallbackLabel(status string) string {
	switch status {
	case "pending":
		return "Thinking"
	case "draining":
		return "Thinking"
	case "failed":
		return "Failed"
	case "suppressed":
		return "Cancelled"
	default:
		return "Output"
	}
}

func recentActivityFallbackDisplayLabel(status string) string {
	return recentActivityFallbackLabel(status)
}

func recentActivityFallbackLabelKey(status string) string {
	switch status {
	case "pending", "draining":
		return "thinking"
	case "failed":
		return "failed"
	case "suppressed":
		return "cancelled"
	default:
		return "output"
	}
}

func recentActivityFallbackActivityKind(status string) string {
	switch status {
	case "pending", "draining":
		return "thinking"
	case "failed":
		return "error"
	case "suppressed":
		return "turn_end"
	default:
		return "text"
	}
}

func recentActivityFallbackDetailKind(status string) string {
	switch status {
	case "pending", "draining":
		return "runtime_phase"
	case "failed":
		return "agent_inbox_failed"
	case "suppressed":
		return "cancelled"
	default:
		return "output"
	}
}

func textActivityLabel(status string) string {
	return "Output"
}
