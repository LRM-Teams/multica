package service

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/events"
	"github.com/multica-ai/multica/server/internal/researchrun"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// MirrorResearchChatReply copies an assistant chat reply into research_message
// when the chat session title is research:<sessionUUID>, so the Research UI
// drawer shows agent answers (not only process cards / CLI posts).
//
// Called from both TaskService.CompleteTask and the agent-inbox complete
// handler — production chat completions primarily use the inbox path.
// Also called after research-session Stop so partial streamed output is kept
// (LRM-820).
func (s *TaskService) MirrorResearchChatReply(ctx context.Context, task db.AgentInboxEvent, msg db.ChatMessage) {
	s.mirrorResearchChatReply(ctx, task, msg, false)
}

// MirrorResearchChatStoppedReply mirrors a stopped wake's assistant snapshot
// into the research drawer so already-streamed text survives Stop.
func (s *TaskService) MirrorResearchChatStoppedReply(ctx context.Context, task db.AgentInboxEvent, msg db.ChatMessage) {
	s.mirrorResearchChatReply(ctx, task, msg, true)
}

func (s *TaskService) mirrorResearchChatReply(ctx context.Context, task db.AgentInboxEvent, msg db.ChatMessage, stopped bool) {
	if s == nil || s.Queries == nil || !task.ChatSessionID.Valid {
		return
	}
	body := strings.TrimSpace(msg.Content)
	if body == "" {
		return
	}
	chatSession, err := s.Queries.GetChatSession(ctx, task.ChatSessionID)
	if err != nil {
		return
	}
	title := strings.TrimSpace(chatSession.Title)
	const prefix = "research:"
	if !strings.HasPrefix(title, prefix) {
		return
	}
	sessionIDStr := strings.TrimSpace(strings.TrimPrefix(title, prefix))
	sessionID, err := util.ParseUUID(sessionIDStr)
	if err != nil || !sessionID.Valid {
		return
	}
	researchSession, err := s.Queries.GetResearchSession(ctx, db.GetResearchSessionParams{
		ID:          sessionID,
		WorkspaceID: chatSession.WorkspaceID,
	})
	if err != nil {
		slog.Warn("research chat mirror: session missing",
			"chat_session_id", util.UUIDToString(task.ChatSessionID),
			"research_session_id", sessionIDStr,
			"error", err,
		)
		return
	}

	meta := map[string]any{"mirrored_from": "chat"}
	if stopped {
		meta["stopped"] = true
	}
	metaBytes := []byte(`{"mirrored_from":"chat"}`)
	if stopped {
		metaBytes = []byte(`{"mirrored_from":"chat","stopped":true}`)
	}

	if s.TxStarter == nil {
		slog.Warn("research chat mirror: transaction service unavailable", "research_session_id", sessionIDStr)
		return
	}
	tx, err := s.TxStarter.Begin(ctx)
	if err != nil {
		slog.Warn("research chat mirror: begin transaction failed", "research_session_id", sessionIDStr, "error", err)
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()

	row, err := s.Queries.WithTx(tx).CreateResearchMessage(ctx, db.CreateResearchMessageParams{
		WorkspaceID:   researchSession.WorkspaceID,
		SessionID:     researchSession.ID,
		SenderType:    "agent",
		SenderID:      task.AgentID,
		TargetAgentID: pgtype.UUID{},
		Body:          body,
		CardKind:      "chat",
		Meta:          metaBytes,
	})
	if err != nil {
		slog.Warn("research chat mirror: create message failed",
			"research_session_id", sessionIDStr,
			"error", err,
		)
		return
	}
	if err = researchrun.RegisterProductionResearchMessageTx(
		ctx, tx, util.UUIDToString(researchSession.WorkspaceID),
		util.UUIDToString(researchSession.ID), util.UUIDToString(row.ID),
	); err != nil {
		slog.Warn("research chat mirror: register message passport failed",
			"research_session_id", sessionIDStr,
			"error", err,
		)
		return
	}
	if err = tx.Commit(ctx); err != nil {
		slog.Warn("research chat mirror: commit message failed",
			"research_session_id", sessionIDStr,
			"error", err,
		)
		return
	}

	createdAt := ""
	if row.CreatedAt.Valid {
		createdAt = row.CreatedAt.Time.UTC().Format(time.RFC3339Nano)
	}
	payload := map[string]any{
		"session_id": util.UUIDToString(researchSession.ID),
		"message": map[string]any{
			"id":              util.UUIDToString(row.ID),
			"session_id":      util.UUIDToString(row.SessionID),
			"sender_type":     row.SenderType,
			"sender_id":       util.UUIDToString(row.SenderID),
			"target_agent_id": nil,
			"body":            row.Body,
			"card_kind":       row.CardKind,
			"meta":            meta,
			"created_at":      createdAt,
		},
	}
	if s.Bus != nil {
		s.Bus.Publish(events.Event{
			Type:        protocol.EventResearchSessionMessage,
			WorkspaceID: util.UUIDToString(researchSession.WorkspaceID),
			ActorType:   "agent",
			ActorID:     util.UUIDToString(task.AgentID),
			Payload:     payload,
		})
	}
}

// coalesceTaskMessageText joins user-facing text fragments from a task's
// execution transcript (same order as chat live TimelineView).
func coalesceTaskMessageText(messages []db.TaskMessage) string {
	var b strings.Builder
	for _, m := range messages {
		if m.Type != "text" {
			continue
		}
		if m.Visibility == "diagnostic_only" {
			continue
		}
		if !m.Content.Valid {
			continue
		}
		chunk := m.Content.String
		if chunk == "" {
			continue
		}
		b.WriteString(chunk)
	}
	return strings.TrimSpace(b.String())
}
