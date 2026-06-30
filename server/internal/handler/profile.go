package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/redact"
)

type MemberProfileResponse struct {
	MemberType     string                    `json:"member_type"`
	MemberID       string                    `json:"member_id"`
	Name           string                    `json:"name"`
	DisplayName    string                    `json:"display_name"`
	AvatarURL      *string                   `json:"avatar_url"`
	Description    string                    `json:"description"`
	Role           string                    `json:"role"`
	Status         *string                   `json:"status"`
	RecentActivity []AgentRecentActivityItem `json:"recent_activity"`
}

type AgentRecentActivityItem struct {
	ID         string  `json:"id"`
	Kind       string  `json:"kind"`
	Label      string  `json:"label"`
	Summary    *string `json:"summary,omitempty"`
	OccurredAt string  `json:"occurred_at"`
	Status     string  `json:"status"`
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
	})
}

func (h *Handler) getAgentMemberProfile(w http.ResponseWriter, r *http.Request, agentID string) {
	agent, ok := h.loadAgentForUser(w, r, agentID)
	if !ok {
		return
	}

	workspaceID := uuidToString(agent.WorkspaceID)
	actorType, actorID := h.resolveActor(r, requestUserID(r), workspaceID)
	if !h.canAccessPrivateAgent(r.Context(), agent, actorType, actorID, workspaceID) {
		writeError(w, http.StatusForbidden, "you do not have access to this agent")
		return
	}

	activity, err := h.listAgentRecentActivity(r.Context(), agent, 5)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load member profile")
		return
	}
	status := agent.Status
	writeJSON(w, http.StatusOK, MemberProfileResponse{
		MemberType:     "agent",
		MemberID:       uuidToString(agent.ID),
		Name:           agent.Name,
		DisplayName:    agentDisplayName(agent),
		AvatarURL:      textToPtr(agent.AvatarUrl),
		Description:    agent.Description,
		Role:           "Agent",
		Status:         &status,
		RecentActivity: activity,
	})
}

func (h *Handler) listAgentRecentActivity(ctx context.Context, agent db.Agent, limit int) ([]AgentRecentActivityItem, error) {
	rows, err := h.DB.Query(ctx, `
		SELECT atq.id, atq.status, atq.trigger_summary,
		       i.title, (w.issue_prefix || '-' || i.number::text) AS issue_identifier,
		       NULLIF(cs.title, '') AS chat_title,
		       COALESCE(atq.completed_at, atq.started_at, atq.dispatched_at, atq.created_at) AS occurred_at
		FROM agent_task_queue atq
		JOIN agent a ON a.id = atq.agent_id
		JOIN workspace w ON w.id = a.workspace_id
		LEFT JOIN issue i ON i.id = atq.issue_id
		LEFT JOIN chat_session cs ON cs.id = atq.chat_session_id
		WHERE atq.agent_id = $1
		  AND a.workspace_id = $2
		  AND atq.status IN ('queued', 'dispatched', 'running', 'waiting_local_directory', 'completed', 'failed', 'cancelled')
		ORDER BY occurred_at DESC, atq.id DESC
		LIMIT $3`,
		agent.ID, agent.WorkspaceID, int32(limit))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := make([]AgentRecentActivityItem, 0, limit)
	for rows.Next() {
		var (
			taskID          pgtype.UUID
			status          string
			triggerSummary  pgtype.Text
			issueTitle      pgtype.Text
			issueIdentifier pgtype.Text
			chatTitle       pgtype.Text
			occurredAt      pgtype.Timestamptz
		)
		if err := rows.Scan(&taskID, &status, &triggerSummary, &issueTitle, &issueIdentifier, &chatTitle, &occurredAt); err != nil {
			continue
		}
		items = append(items, AgentRecentActivityItem{
			ID:         uuidToString(taskID),
			Kind:       recentActivityFallbackKind(status),
			Label:      recentActivityFallbackLabel(status),
			Summary:    recentActivitySummary(issueIdentifier, issueTitle, chatTitle, triggerSummary),
			OccurredAt: timestampToString(occurredAt),
			Status:     status,
		})
		h.attachRecentExecutionActivity(ctx, taskID, status, &items[len(items)-1])
	}
	if err := rows.Err(); err != nil {
		return nil, err
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
	Input     []byte
	CreatedAt pgtype.Timestamptz
}

func (h *Handler) listRecentTaskActivityMessages(ctx context.Context, taskID pgtype.UUID, limit int32) ([]recentTaskActivityMessage, error) {
	rows, err := h.DB.Query(ctx, `
		SELECT id, task_id, seq, type, tool, content, input, created_at
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
			&msg.Input,
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
	input := parseTaskMessageInput(msg.Input)
	label, kind := toolActivityLabel(tool, status)
	if label == "" {
		return AgentRecentActivityItem{}, false
	}

	summary := summarizeToolInput(tool, input)
	return AgentRecentActivityItem{
		Kind:       kind,
		Label:      label,
		Summary:    stringPtrIfNotEmpty(summary),
		OccurredAt: timestampToString(msg.CreatedAt),
	}, true
}

func projectTextActivity(msg recentTaskActivityMessage, status string) (AgentRecentActivityItem, bool) {
	if !msg.Content.Valid || strings.TrimSpace(msg.Content.String) == "" {
		return AgentRecentActivityItem{}, false
	}
	summary := sanitizeTextActivitySummary(msg.Content.String, 160)
	return AgentRecentActivityItem{
		Kind:       "text",
		Label:      textActivityLabel(status),
		Summary:    stringPtrIfNotEmpty(summary),
		OccurredAt: timestampToString(msg.CreatedAt),
	}, true
}

func toolActivityLabel(tool, status string) (label string, kind string) {
	switch {
	case isCommandTool(tool):
		if isActiveTaskStatus(status) {
			return "Running command", "command"
		}
		return "Ran command", "command"
	case isFileEditTool(tool):
		return "Editing file", "tool_use"
	case isSearchTool(tool):
		if strings.Contains(tool, "issue") {
			return "Searching issues", "tool_use"
		}
		return "Reading context", "tool_use"
	case isFileReadTool(tool):
		return "Reading files", "tool_use"
	case tool != "":
		return "Reading context", "tool_use"
	default:
		return "", ""
	}
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
	return status == "queued" ||
		status == "dispatched" ||
		status == "running" ||
		status == "waiting_local_directory"
}

func summarizeToolInput(tool string, input map[string]any) string {
	if isCommandTool(tool) {
		return ""
	}
	if isFileEditTool(tool) || isFileReadTool(tool) {
		if summary := firstPathSummary(input, 80, "path", "file", "file_path", "filepath", "target_file", "files"); summary != "" {
			return summary
		}
	}
	return ""
}

func firstInputSummary(input map[string]any, max int, keys ...string) string {
	for _, key := range keys {
		if value, ok := input[key]; ok {
			if summary := inputValueSummary(value); summary != "" {
				return sanitizeActivitySummary(summary, max)
			}
		}
	}
	return ""
}

func firstPathSummary(input map[string]any, max int, keys ...string) string {
	for _, key := range keys {
		if value, ok := input[key]; ok {
			if summary := pathValueSummary(value); summary != "" {
				return sanitizeActivitySummary(summary, max)
			}
		}
	}
	return ""
}

func inputValueSummary(value any) string {
	switch v := value.(type) {
	case string:
		return v
	case []any:
		parts := make([]string, 0, len(v))
		for _, item := range v {
			if s := inputValueSummary(item); s != "" {
				parts = append(parts, s)
			}
		}
		return strings.Join(parts, " ")
	case map[string]any:
		if s := firstInputSummary(v, 160, "cmd", "command", "query", "q", "path", "file"); s != "" {
			return s
		}
	}
	return fmt.Sprint(value)
}

func pathValueSummary(value any) string {
	switch v := value.(type) {
	case string:
		return safePathLabel(v)
	case []any:
		parts := make([]string, 0, len(v))
		for _, item := range v {
			if s := pathValueSummary(item); s != "" {
				parts = append(parts, s)
			}
		}
		return strings.Join(parts, ", ")
	}
	return inputValueSummary(value)
}

func safePathLabel(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	base := filepath.Base(path)
	if base == "." || base == string(filepath.Separator) || base == "" {
		return path
	}
	return base
}

func parseTaskMessageInput(raw []byte) map[string]any {
	if len(raw) == 0 {
		return nil
	}
	var input map[string]any
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil
	}
	return input
}

func sanitizeActivitySummary(summary string, max int) string {
	summary = strings.Join(strings.Fields(redact.Text(summary)), " ")
	return truncateRunes(summary, max)
}

func sanitizeTextActivitySummary(summary string, max int) string {
	summary = sanitizeActivitySummary(summary, max)
	if !looksLikeNaturalLanguage(summary) {
		return ""
	}
	return summary
}

func looksLikeNaturalLanguage(summary string) bool {
	summary = strings.TrimSpace(summary)
	if summary == "" {
		return false
	}

	var letters, han, digits, spaces, other int
	for _, r := range summary {
		switch {
		case unicode.Is(unicode.Han, r):
			han++
			letters++
		case unicode.IsLetter(r):
			letters++
		case unicode.IsDigit(r):
			digits++
		case unicode.IsSpace(r):
			spaces++
		case strings.ContainsRune("-_()[]{}.,!?;:'\"/\\", r):
			other++
		default:
			other++
		}
	}

	if han >= 2 {
		return true
	}
	if letters == 0 {
		return false
	}
	if isTokenLikeSummary(summary, letters, digits, spaces, other) {
		return false
	}
	if spaces > 0 && letters >= 3 {
		return true
	}
	return hasSentencePunctuation(summary) && letters >= 3
}

func isTokenLikeSummary(summary string, letters, digits, spaces, other int) bool {
	if spaces > 0 {
		return false
	}
	trimmed := strings.Trim(summary, "()[]{}.,;:'\"")
	if trimmed == "" {
		return true
	}
	if digits > 0 && isASCIIIdentifierToken(trimmed) {
		return true
	}
	if utf8.RuneCountInString(trimmed) >= 8 && isHexishToken(trimmed) {
		return true
	}
	return letters+digits == 0 || (digits > 0 && other > 0)
}

func isASCIIIdentifierToken(s string) bool {
	for _, r := range s {
		if r > unicode.MaxASCII {
			return false
		}
		if !(unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' || r == '_') {
			return false
		}
	}
	return true
}

func isHexishToken(s string) bool {
	for _, r := range s {
		if r == '-' || r == '_' {
			continue
		}
		if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')) {
			return false
		}
	}
	return true
}

func hasSentencePunctuation(s string) bool {
	return strings.ContainsAny(s, ".!?。！？")
}

func stringPtrIfNotEmpty(s string) *string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	return &s
}

func recentActivityFallbackKind(status string) string {
	switch status {
	case "queued":
		return "queued"
	case "dispatched", "running", "waiting_local_directory":
		return "working"
	case "failed":
		return "failed"
	case "cancelled":
		return "cancelled"
	default:
		return "task"
	}
}

func recentActivityFallbackLabel(status string) string {
	switch status {
	case "queued":
		return "Thinking"
	case "dispatched", "running", "waiting_local_directory":
		return "Thinking"
	case "failed":
		return "Writing response"
	case "cancelled":
		return "Writing response"
	default:
		return "Writing response"
	}
}

func textActivityLabel(status string) string {
	if isActiveTaskStatus(status) {
		return "Replying"
	}
	return "Writing response"
}

func recentActivitySummary(issueIdentifier, issueTitle, chatTitle, triggerSummary pgtype.Text) *string {
	var summary string
	if issueIdentifier.Valid && issueIdentifier.String != "" {
		summary = issueIdentifier.String
		if issueTitle.Valid && issueTitle.String != "" {
			summary += " " + issueTitle.String
		}
	} else if chatTitle.Valid && chatTitle.String != "" {
		summary = chatTitle.String
	} else if triggerSummary.Valid && triggerSummary.String != "" {
		summary = triggerSummary.String
	}
	summary = strings.TrimSpace(summary)
	if summary == "" {
		return nil
	}
	summary = sanitizeActivitySummary(summary, 120)
	return &summary
}

func truncateRunes(s string, max int) string {
	if max <= 0 || utf8.RuneCountInString(s) <= max {
		return s
	}
	runes := []rune(s)
	if max <= 3 {
		return string(runes[:max])
	}
	return string(runes[:max-3]) + "..."
}
