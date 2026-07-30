package service

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/events"
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
func (s *TaskService) MirrorResearchChatReply(ctx context.Context, task db.AgentInboxEvent, msg db.ChatMessage) {
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

	row, err := s.Queries.CreateResearchMessage(ctx, db.CreateResearchMessageParams{
		WorkspaceID:   researchSession.WorkspaceID,
		SessionID:     researchSession.ID,
		SenderType:    "agent",
		SenderID:      task.AgentID,
		TargetAgentID: pgtype.UUID{},
		Body:          body,
		CardKind:      "chat",
		Meta:          []byte(`{"mirrored_from":"chat"}`),
	})
	if err != nil {
		slog.Warn("research chat mirror: create message failed",
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
			"meta":            map[string]any{"mirrored_from": "chat"},
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
