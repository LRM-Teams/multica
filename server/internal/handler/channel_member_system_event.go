package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

const (
	channelMemberAddedEvent   = "channel_member_added"
	channelMemberRemovedEvent = "channel_member_removed"
	channelMemberLeftEvent    = "channel_member_left"
)

type channelMemberSystemEventParams struct {
	ActorID    string `json:"actor_id,omitempty"`
	ActorName  string `json:"actor_name,omitempty"`
	TargetID   string `json:"target_id"`
	TargetName string `json:"target_name"`
}

type channelMemberSystemEventPart struct {
	Event  string                         `json:"event"`
	Params channelMemberSystemEventParams `json:"params"`
}

func (h *Handler) emitChannelMemberSystemEvent(ctx context.Context, workspaceID string, channelID pgtype.UUID, event string, actorID pgtype.UUID, targetType string, targetID pgtype.UUID) {
	actorName := h.channelMemberSystemEventDisplayName(ctx, workspaceID, "user", actorID)
	targetName := h.channelMemberSystemEventDisplayName(ctx, workspaceID, targetType, targetID)
	params := channelMemberSystemEventParams{
		ActorID:    uuidToString(actorID),
		ActorName:  actorName,
		TargetID:   uuidToString(targetID),
		TargetName: targetName,
	}
	content := channelMemberSystemEventCanonicalContent(event, actorName, targetName)
	rawPart, err := json.Marshal(channelMemberSystemEventPart{Event: event, Params: params})
	if err != nil {
		slog.Warn("channel member system event: marshal part failed", "event", event, "channel", channelID.String(), "error", err)
		return
	}
	msg, err := h.insertChannelMessageWithParts(
		ctx,
		channelID,
		parseUUID(workspaceID),
		"system",
		pgtype.UUID{},
		"system",
		content,
		[]protocol.MessagePart{{Text: string(rawPart)}},
		"multica",
		nil,
		pgtype.UUID{},
		pgtype.UUID{},
		nil,
		0,
	)
	if err != nil {
		slog.Warn("channel member system event: insert failed", "event", event, "channel", channelID.String(), "error", err)
		return
	}
	h.publishChannelToMembers(ctx, protocol.EventChannelMessage, workspaceID, "system", "", channelID, msg)
}

func (h *Handler) channelMemberSystemEventDisplayName(ctx context.Context, workspaceID, memberType string, memberID pgtype.UUID) string {
	var displayName string
	var err error
	switch memberType {
	case "agent":
		err = h.DB.QueryRow(ctx, `
			SELECT COALESCE(NULLIF(display_name, ''), name, 'Agent')
			FROM agent
			WHERE workspace_id = $1 AND id = $2`, parseUUID(workspaceID), memberID).Scan(&displayName)
	default:
		err = h.DB.QueryRow(ctx, `
			SELECT COALESCE(NULLIF(u.display_name, ''), u.name, u.email, 'User')
			FROM "user" u
			JOIN member m ON m.user_id = u.id AND m.workspace_id = $1
			WHERE u.id = $2`, parseUUID(workspaceID), memberID).Scan(&displayName)
	}
	if err != nil || displayName == "" {
		if memberType == "agent" {
			return "Agent"
		}
		return "User"
	}
	return displayName
}

func channelMemberSystemEventCanonicalContent(event, actorName, targetName string) string {
	switch event {
	case channelMemberAddedEvent:
		return fmt.Sprintf("%s added %s to the channel", actorName, targetName)
	case channelMemberRemovedEvent:
		return fmt.Sprintf("%s removed %s from the channel", actorName, targetName)
	case channelMemberLeftEvent:
		return fmt.Sprintf("%s left the channel", targetName)
	default:
		return targetName
	}
}
