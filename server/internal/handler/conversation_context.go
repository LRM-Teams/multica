package handler

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

const (
	conversationContextVersion = "ctx_v1"

	dmRecentMessageLimit      = 20
	channelRecentMessageLimit = 30
	threadRecentReplyLimit    = 30
)

type conversationSurface struct {
	Type       string
	SurfaceKey string
	SessionID  string
}

func buildConversationSurface(workspaceID, agentID string, chatSessionID pgtype.UUID, channelID string, threadRootMessageID *string, issueID string) conversationSurface {
	surfaceType := "direct"
	surfaceKey := "direct:" + issueID
	sessionID := issueID

	if chatSessionID.Valid {
		chatID := uuidToString(chatSessionID)
		surfaceType = "dm"
		surfaceKey = "dm:" + chatID
		sessionID = chatID
		if strings.TrimSpace(channelID) != "" {
			surfaceType = "channel"
			surfaceKey = "channel:" + strings.TrimSpace(channelID)
		}
		if threadRootMessageID != nil && strings.TrimSpace(*threadRootMessageID) != "" {
			surfaceType = "thread"
			surfaceKey = "thread:" + strings.TrimSpace(*threadRootMessageID)
			sessionID = strings.TrimSpace(*threadRootMessageID)
		}
	} else if issueID != "" {
		surfaceType = "issue"
		surfaceKey = "issue:" + issueID
		sessionID = issueID
	}

	if surfaceKey == "direct:" || surfaceKey == "issue:" || surfaceKey == "issue:00000000-0000-0000-0000-000000000000" || surfaceKey == "" {
		surfaceKey = fmt.Sprintf("task:%s:%s", workspaceID, agentID)
	}
	if sessionID == "" {
		sessionID = surfaceKey
	}

	return conversationSurface{Type: surfaceType, SurfaceKey: surfaceKey, SessionID: sessionID}
}

func buildAgentRunCacheKey(workspaceID, agentID string, surface conversationSurface) string {
	return strings.Join([]string{
		"agent-run",
		workspaceID,
		agentID,
		surface.Type,
		surface.SurfaceKey,
		surface.SessionID,
		conversationContextVersion,
	}, ":")
}

func buildChatContextSummary(msgs []db.ChatMessage, totalTokens int64, reason, workspaceID, agentID string, surface conversationSurface) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Conversation context surface: %s (%s).\n", surface.Type, surface.SurfaceKey)
	fmt.Fprintf(&b, "Conversation session: %s. Context version: %s.\n", surface.SessionID, conversationContextVersion)
	fmt.Fprintf(&b, "Agent run cache key: %s.\n", buildAgentRunCacheKey(workspaceID, agentID, surface))
	if strings.TrimSpace(reason) != "" {
		fmt.Fprintf(&b, "Native session resume was intentionally skipped because %s.\n", reason)
	}
	fmt.Fprintf(&b, "Full chat history is preserved by Multica, but only the latest %d surface-scoped messages are included here to avoid carrying old tool outputs into every new turn.\n", chatResumeRecentMessageLimit)
	if totalTokens > 0 {
		fmt.Fprintf(&b, "Recorded token usage for this chat so far: %d.\n", totalTokens)
	}
	if len(msgs) == 0 {
		return b.String()
	}
	b.WriteString("Recent surface messages:\n")
	for _, m := range recentChatMessages(msgs, chatResumeRecentMessageLimit) {
		fmt.Fprintf(&b, "- %s: %s\n", m.Role, compactChatLine(m.Content))
	}
	b.WriteString("Older tool outputs/log dumps are not included. If exact older details matter, re-read the referenced files/logs instead of assuming they are in context.\n")
	return b.String()
}

func (h *Handler) threadRootIDForChatSession(ctx context.Context, chatSessionID pgtype.UUID) *string {
	if !chatSessionID.Valid || h == nil || h.DB == nil {
		return nil
	}
	var rootID pgtype.UUID
	if err := h.DB.QueryRow(ctx, `
		SELECT channel_thread_root_message_id
		FROM chat_message
		WHERE chat_session_id = $1 AND channel_thread_root_message_id IS NOT NULL
		ORDER BY created_at DESC, id DESC
		LIMIT 1`, chatSessionID).Scan(&rootID); err != nil || !rootID.Valid {
		return nil
	}
	root := uuidToString(rootID)
	return &root
}

func (h *Handler) channelIDForChatSession(ctx context.Context, chatSessionID pgtype.UUID) string {
	if !chatSessionID.Valid || h == nil || h.DB == nil {
		return ""
	}
	var channelID pgtype.UUID
	if err := h.DB.QueryRow(ctx, `SELECT channel_id FROM channel_agent_session WHERE chat_session_id = $1`, chatSessionID).Scan(&channelID); err != nil {
		return ""
	}
	return uuidToString(channelID)
}

func formatChannelMessageLine(msg ChannelMessageResponse) string {
	return fmt.Sprintf("[%s] %s (%s): %s", msg.CreatedAt, msg.AuthorName, msg.AuthorType, msg.Content)
}
