package handler

import (
	"context"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
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

	activity, err := h.listAgentRecentActivity(r.Context(), agent, 3)
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
			Kind:       recentActivityKind(status),
			Label:      recentActivityLabel(status),
			Summary:    recentActivitySummary(issueIdentifier, issueTitle, chatTitle, triggerSummary),
			OccurredAt: timestampToString(occurredAt),
			Status:     status,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return items, nil
}

func recentActivityKind(status string) string {
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

func recentActivityLabel(status string) string {
	switch status {
	case "queued":
		return "Queued"
	case "dispatched", "running", "waiting_local_directory":
		return "Working"
	case "failed":
		return "Failed task"
	case "cancelled":
		return "Cancelled task"
	default:
		return "Completed task"
	}
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
	summary = truncateRunes(summary, 120)
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
