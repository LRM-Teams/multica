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
	ActorID           string `json:"actor_id,omitempty"`
	ActorType         string `json:"actor_type,omitempty"`
	ActorHandle       string `json:"actor_handle,omitempty"`
	ActorDisplayName  string `json:"actor_display_name,omitempty"`
	ActorName         string `json:"actor_name,omitempty"`
	TargetID          string `json:"target_id"`
	TargetType        string `json:"target_type"`
	TargetHandle      string `json:"target_handle,omitempty"`
	TargetDisplayName string `json:"target_display_name,omitempty"`
	TargetName        string `json:"target_name"`
}

type channelMemberSystemEventPart struct {
	Event  string                         `json:"event"`
	Params channelMemberSystemEventParams `json:"params"`
}

func (h *Handler) emitChannelMemberSystemEvent(ctx context.Context, workspaceID string, channelID pgtype.UUID, event string, actorID pgtype.UUID, targetType string, targetID pgtype.UUID) {
	actorRef := h.channelMemberSystemEventActorRef(ctx, workspaceID, "user", actorID)
	targetRef := h.channelMemberSystemEventActorRef(ctx, workspaceID, targetType, targetID)
	params := channelMemberSystemEventParams{
		ActorID:           uuidToString(actorID),
		ActorType:         actorRef.Type,
		ActorHandle:       actorRef.Handle,
		ActorDisplayName:  actorRef.DisplayName,
		ActorName:         actorRef.DisplayName,
		TargetID:          uuidToString(targetID),
		TargetType:        targetRef.Type,
		TargetHandle:      targetRef.Handle,
		TargetDisplayName: targetRef.DisplayName,
		TargetName:        targetRef.DisplayName,
	}
	content := channelMemberSystemEventCanonicalContent(event, actorRef.DisplayName, targetRef.DisplayName)
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

type channelMemberSystemEventActorRef struct {
	Type        string
	Handle      string
	DisplayName string
}

func (h *Handler) channelMemberSystemEventActorRef(ctx context.Context, workspaceID, memberType string, memberID pgtype.UUID) channelMemberSystemEventActorRef {
	ref := channelMemberSystemEventActorRef{Type: memberType}
	var err error
	switch memberType {
	case "agent":
		err = h.DB.QueryRow(ctx, `
			SELECT COALESCE(NULLIF(name, ''), 'agent'),
			       COALESCE(NULLIF(display_name, ''), NULLIF(name, ''), 'Agent')
			FROM agent
			WHERE workspace_id = $1 AND id = $2`, parseUUID(workspaceID), memberID).Scan(&ref.Handle, &ref.DisplayName)
	default:
		ref.Type = "user"
		err = h.DB.QueryRow(ctx, `
			SELECT COALESCE(NULLIF(u.name, ''), NULLIF(u.email, ''), 'user'),
			       COALESCE(NULLIF(u.display_name, ''), NULLIF(u.name, ''), NULLIF(u.email, ''), 'User')
			FROM "user" u
			JOIN member m ON m.user_id = u.id AND m.workspace_id = $1
			WHERE u.id = $2`, parseUUID(workspaceID), memberID).Scan(&ref.Handle, &ref.DisplayName)
	}
	if err != nil || ref.DisplayName == "" {
		if ref.Type == "agent" {
			ref.Handle = firstNonEmpty(ref.Handle, "agent")
			ref.DisplayName = "Agent"
			return ref
		}
		ref.Handle = firstNonEmpty(ref.Handle, "user")
		ref.DisplayName = "User"
	}
	return ref
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
