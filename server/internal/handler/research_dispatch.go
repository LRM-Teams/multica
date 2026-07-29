package handler

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/service"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func researchChatSessionTitle(sessionID pgtype.UUID) string {
	return "research:" + uuidToString(sessionID)
}

func buildResearchWakePrompt(session db.ResearchSession, body string, senderType string) string {
	var b strings.Builder
	b.WriteString("## Research Fleet assignment\n\n")
	b.WriteString(fmt.Sprintf("- Session ID: `%s`\n", uuidToString(session.ID)))
	b.WriteString(fmt.Sprintf("- Title: %s\n", session.Title))
	b.WriteString(fmt.Sprintf("- Goal: %s\n", session.Goal))
	b.WriteString(fmt.Sprintf("- Status: %s\n", session.Status))
	b.WriteString(fmt.Sprintf("- Current stage: %s\n\n", session.CurrentStage))
	b.WriteString("Use `multica research` CLI tools (graph-append, source-upsert, report-patch, stage-eval, hire, optimize).\n")
	b.WriteString("Do not treat a single generic web_search dump as completion.\n")
	b.WriteString("Record probes / findings / dead_ends / pivots on the exploration graph.\n\n")
	if senderType == "user" {
		b.WriteString("### User message\n\n")
	} else {
		b.WriteString("### Dispatch note\n\n")
	}
	b.WriteString(strings.TrimSpace(body))
	b.WriteString("\n")
	return b.String()
}

// enqueueResearchAgentWake creates/reuses a chat session for the target fleet
// agent, writes a structured prompt, and enqueues a chat inbox task so the
// daemon wakes the agent.
func (h *Handler) enqueueResearchAgentWake(
	ctx context.Context,
	workspaceID pgtype.UUID,
	session db.ResearchSession,
	targetAgentID pgtype.UUID,
	initiatorUserID pgtype.UUID,
	body string,
	senderType string,
) error {
	if h.TaskService == nil {
		return fmt.Errorf("task service unavailable")
	}
	if !targetAgentID.Valid {
		return fmt.Errorf("target agent required")
	}

	member, err := h.Queries.GetResearchFleetMemberByAgent(ctx, db.GetResearchFleetMemberByAgentParams{
		WorkspaceID: workspaceID,
		AgentID:     targetAgentID,
	})
	if err != nil {
		return fmt.Errorf("target is not a research fleet member")
	}
	if member.Status == "pending_prompt_review" {
		return fmt.Errorf("fleet member pending prompt review cannot accept tasks")
	}
	if member.Status == "archived" {
		return fmt.Errorf("fleet member is archived")
	}

	agent, err := h.Queries.GetAgentInWorkspace(ctx, db.GetAgentInWorkspaceParams{
		ID:          targetAgentID,
		WorkspaceID: workspaceID,
	})
	if err != nil {
		return fmt.Errorf("load agent: %w", err)
	}
	if agent.ArchivedAt.Valid {
		return fmt.Errorf("agent is archived")
	}
	if !agent.RuntimeID.Valid {
		return fmt.Errorf("agent has no runtime")
	}

	chatSession, err := h.ensureResearchAgentChatSession(ctx, workspaceID, session, targetAgentID, initiatorUserID)
	if err != nil {
		return err
	}

	prompt := buildResearchWakePrompt(session, body, senderType)
	msg, err := h.Queries.CreateChatMessage(ctx, db.CreateChatMessageParams{
		ChatSessionID: chatSession.ID,
		Role:          "user",
		Content:       prompt,
		Parts:         []byte("[]"),
	})
	if err != nil {
		return fmt.Errorf("create research wake prompt: %w", err)
	}

	task, err := h.TaskService.EnqueueChatTask(ctx, chatSession, initiatorUserID)
	if err != nil {
		if errors.Is(err, service.ErrChatTaskAgentArchived) || errors.Is(err, service.ErrChatTaskAgentNoRuntime) {
			return err
		}
		return fmt.Errorf("enqueue research wake: %w", err)
	}
	if linkErr := h.Queries.LinkChatMessageToTask(ctx, db.LinkChatMessageToTaskParams{
		ID:     msg.ID,
		TaskID: task.ID,
	}); linkErr != nil {
		slog.Warn("link research wake message to task failed",
			"message_id", uuidToString(msg.ID),
			"task_id", uuidToString(task.ID),
			"error", linkErr,
		)
	}
	_ = h.Queries.TouchChatSession(ctx, chatSession.ID)
	return nil
}

func (h *Handler) ensureResearchAgentChatSession(
	ctx context.Context,
	workspaceID pgtype.UUID,
	session db.ResearchSession,
	agentID pgtype.UUID,
	creatorID pgtype.UUID,
) (db.ChatSession, error) {
	title := researchChatSessionTitle(session.ID)
	var existingID pgtype.UUID
	err := h.DB.QueryRow(ctx, `
		SELECT id FROM chat_session
		WHERE workspace_id = $1 AND agent_id = $2 AND title = $3 AND status = 'active'
		ORDER BY created_at DESC
		LIMIT 1
	`, workspaceID, agentID, title).Scan(&existingID)
	if err == nil && existingID.Valid {
		return h.Queries.GetChatSession(ctx, existingID)
	}
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return db.ChatSession{}, fmt.Errorf("lookup research chat session: %w", err)
	}

	created, err := h.Queries.CreateChatSession(ctx, db.CreateChatSessionParams{
		WorkspaceID: workspaceID,
		AgentID:     agentID,
		CreatorID:   creatorID,
		Title:       title,
	})
	if err != nil {
		return db.ChatSession{}, fmt.Errorf("create research chat session: %w", err)
	}
	return created, nil
}
