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
	channelMemberAddedEvent          = "channel_member_added"
	channelMemberRemovedEvent        = "channel_member_removed"
	channelMemberLeftEvent           = "channel_member_left"
	channelMemberRoleChangedEvent    = "channel_member_role_changed"
	channelOwnershipTransferredEvent = "channel_ownership_transferred"
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
	PreviousRole      string `json:"previous_role,omitempty"`
	NewRole           string `json:"new_role,omitempty"`
}

func (h *Handler) insertChannelMemberRoleChangedSystemEventExec(
	ctx context.Context,
	exec dbExecutor,
	workspaceID string,
	channelID pgtype.UUID,
	actorID pgtype.UUID,
	targetType string,
	targetID pgtype.UUID,
	previousRole, newRole string,
) (ChannelMessageResponse, error) {
	actorRef := h.channelMemberSystemEventActorRefWithExec(ctx, exec, workspaceID, "user", actorID)
	targetRef := h.channelMemberSystemEventActorRefWithExec(ctx, exec, workspaceID, targetType, targetID)
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
		PreviousRole:      previousRole,
		NewRole:           newRole,
	}
	paramsJSON, err := json.Marshal(params)
	if err != nil {
		return ChannelMessageResponse{}, fmt.Errorf("marshal channel member role event: %w", err)
	}
	content := fmt.Sprintf(
		"%s changed %s's role from %s to %s",
		actorRef.DisplayName,
		targetRef.DisplayName,
		previousRole,
		newRole,
	)
	inserted, err := insertChannelMessageWithPartsExec(
		ctx, exec, channelID, parseUUID(workspaceID),
		"system", pgtype.UUID{}, "system", content,
		[]protocol.MessagePart{{
			Type:   protocol.MessagePartTypeSystemEvent,
			Event:  channelMemberRoleChangedEvent,
			Params: paramsJSON,
		}},
		"multica", nil, nil, pgtype.UUID{}, pgtype.UUID{}, nil, pgtype.UUID{}, nil, 0,
	)
	if err != nil {
		return ChannelMessageResponse{}, fmt.Errorf("insert channel member role event: %w", err)
	}
	return inserted.Message, nil
}

type channelMemberSystemEventPart struct {
	Event  string                         `json:"event"`
	Params channelMemberSystemEventParams `json:"params"`
}

func (h *Handler) emitChannelMemberSystemEvent(ctx context.Context, workspaceID string, channelID pgtype.UUID, event string, actorType string, actorID pgtype.UUID, targetType string, targetID pgtype.UUID) {
	actorRef := h.channelMemberSystemEventActorRef(ctx, workspaceID, actorType, actorID)
	targetRef := h.channelMemberSystemEventActorRef(ctx, workspaceID, targetType, targetID)
	if actorRef.Type == "" || targetRef.Type == "" {
		slog.Warn(
			"channel member system event: actor or target is not in workspace",
			"event", event,
			"channel", channelID.String(),
			"actor_type", actorType,
			"actor_id", uuidToString(actorID),
			"target_type", targetType,
			"target_id", uuidToString(targetID),
		)
		return
	}
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
	paramsJSON, err := json.Marshal(params)
	if err != nil {
		slog.Warn("channel member system event: marshal event params failed", "event", event, "channel", channelID.String(), "error", err)
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
		[]protocol.MessagePart{{Type: protocol.MessagePartTypeSystemEvent, Event: event, EventParams: paramsJSON}},
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

// insertChannelMemberSystemEventExec writes the durable system row on exec.
// Callers must publish the returned message only after a successful commit.
// Fail-closed: any insert/marshal error is returned (no soft log-and-continue).
func (h *Handler) insertChannelMemberSystemEventExec(
	ctx context.Context,
	exec dbExecutor,
	workspaceID string,
	channelID pgtype.UUID,
	event string,
	actorType string,
	actorID pgtype.UUID,
	targetType string,
	targetID pgtype.UUID,
) (ChannelMessageResponse, error) {
	actorRef := h.channelMemberSystemEventActorRefWithExec(ctx, exec, workspaceID, actorType, actorID)
	targetRef := h.channelMemberSystemEventActorRefWithExec(ctx, exec, workspaceID, targetType, targetID)
	if actorRef.Type == "" {
		return ChannelMessageResponse{}, fmt.Errorf(
			"channel member system event actor %s/%s is not in workspace",
			actorType,
			uuidToString(actorID),
		)
	}
	if targetRef.Type == "" {
		return ChannelMessageResponse{}, fmt.Errorf(
			"channel member system event target %s/%s is not in workspace",
			targetType,
			uuidToString(targetID),
		)
	}
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
	paramsJSON, err := json.Marshal(params)
	if err != nil {
		return ChannelMessageResponse{}, fmt.Errorf("marshal channel member system event: %w", err)
	}
	inserted, err := insertChannelMessageWithPartsExec(
		ctx, exec, channelID, parseUUID(workspaceID),
		"system", pgtype.UUID{}, "system", content,
		[]protocol.MessagePart{{Type: protocol.MessagePartTypeSystemEvent, Event: event, EventParams: paramsJSON}},
		"multica", nil, nil, pgtype.UUID{}, pgtype.UUID{}, nil, pgtype.UUID{}, nil, 0,
	)
	if err != nil {
		return ChannelMessageResponse{}, fmt.Errorf("insert channel member system event: %w", err)
	}
	return inserted.Message, nil
}

func (h *Handler) channelMemberSystemEventActorRefWithExec(
	ctx context.Context,
	exec dbExecutor,
	workspaceID, memberType string,
	memberID pgtype.UUID,
) channelMemberSystemEventActorRef {
	publicType := channelMemberSystemEventPublicType(memberType)
	if publicType == "" {
		return channelMemberSystemEventActorRef{}
	}
	if memberType == channelMemberActorSystem {
		if memberID.Valid {
			return channelMemberSystemEventActorRef{}
		}
		return channelMemberSystemEventActorRef{
			Type:        "system",
			Handle:      "system",
			DisplayName: "System",
		}
	}
	if !validChannelMemberActorID(memberID) {
		return channelMemberSystemEventActorRef{}
	}
	ref := channelMemberSystemEventActorRef{Type: publicType}
	var err error
	switch memberType {
	case channelMemberActorAgent:
		err = exec.QueryRow(ctx, `
			SELECT COALESCE(NULLIF(name, ''), 'agent'),
			       COALESCE(NULLIF(display_name, ''), NULLIF(name, ''), 'Agent')
			FROM agent
			WHERE workspace_id = $1 AND id = $2`, parseUUID(workspaceID), memberID).Scan(&ref.Handle, &ref.DisplayName)
	case channelMemberActorUser:
		err = exec.QueryRow(ctx, `
			SELECT COALESCE(NULLIF(u.name, ''), NULLIF(u.email, ''), 'user'),
			       COALESCE(NULLIF(u.display_name, ''), NULLIF(u.name, ''), NULLIF(u.email, ''), 'User')
			FROM "user" u
			JOIN member m ON m.user_id = u.id AND m.workspace_id = $1
			WHERE u.id = $2`, parseUUID(workspaceID), memberID).Scan(&ref.Handle, &ref.DisplayName)
	}
	if err != nil || ref.DisplayName == "" {
		return channelMemberSystemEventActorRef{}
	}
	return ref
}

type channelMemberSystemEventActorRef struct {
	Type        string
	Handle      string
	DisplayName string
}

func (h *Handler) channelMemberSystemEventActorRef(ctx context.Context, workspaceID, memberType string, memberID pgtype.UUID) channelMemberSystemEventActorRef {
	return h.channelMemberSystemEventActorRefWithExec(ctx, h.DB, workspaceID, memberType, memberID)
}

func channelMemberSystemEventPublicType(memberType string) string {
	switch memberType {
	case channelMemberActorAgent:
		return "agent"
	case channelMemberActorUser, "member":
		return "human"
	case channelMemberActorSystem:
		return "system"
	default:
		return ""
	}
}

func channelMemberSystemEventCanonicalContent(event, actorName, targetName string) string {
	switch event {
	case channelMemberAddedEvent:
		return fmt.Sprintf("%s added %s to the channel", actorName, targetName)
	case channelMemberRemovedEvent:
		return fmt.Sprintf("%s removed %s from the channel", actorName, targetName)
	case channelMemberLeftEvent:
		return fmt.Sprintf("%s left the channel", targetName)
	case channelOwnershipTransferredEvent:
		// actor = previous owner (transfer initiator), target = new owner.
		return fmt.Sprintf("%s transferred channel ownership to %s", actorName, targetName)
	default:
		return targetName
	}
}
