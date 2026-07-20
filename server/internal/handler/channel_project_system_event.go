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
	channelProjectBoundEvent   = "channel_project_bound"
	channelProjectChangedEvent = "channel_project_changed"
	channelProjectUnboundEvent = "channel_project_unbound"
)

// channelProjectSystemEventParams is intentionally a typed fact rather than
// display prose. The channel message is the durable channel projection; the
// frontend can localize it (and attach the project route) without having to
// parse its fallback content.
type channelProjectSystemEventParams struct {
	ActorID              string `json:"actor_id"`
	ActorType            string `json:"actor_type"`
	ActorHandle          string `json:"actor_handle,omitempty"`
	ActorDisplayName     string `json:"actor_display_name"`
	ActorName            string `json:"actor_name"`
	ProjectID            string `json:"project_id,omitempty"`
	ProjectTitle         string `json:"project_title,omitempty"`
	PreviousProjectID    string `json:"previous_project_id,omitempty"`
	PreviousProjectTitle string `json:"previous_project_title,omitempty"`
}

func (h *Handler) emitChannelProjectSystemEvent(ctx context.Context, workspaceID string, channelID, actorID, previousProjectID, projectID pgtype.UUID) {
	if uuidToString(previousProjectID) == uuidToString(projectID) {
		return
	}
	actorRef := h.channelMemberSystemEventActorRef(ctx, workspaceID, "user", actorID)
	params := channelProjectSystemEventParams{
		ActorID:          uuidToString(actorID),
		ActorType:        actorRef.Type,
		ActorHandle:      actorRef.Handle,
		ActorDisplayName: actorRef.DisplayName,
		ActorName:        actorRef.DisplayName,
	}
	if previousProjectID.Valid {
		params.PreviousProjectID = uuidToString(previousProjectID)
		if err := h.DB.QueryRow(ctx, `SELECT title FROM project WHERE id = $1 AND workspace_id = $2`, previousProjectID, parseUUID(workspaceID)).Scan(&params.PreviousProjectTitle); err != nil {
			slog.Warn("channel project system event: load previous project title", "channel", channelID.String(), "project", previousProjectID.String(), "error", err)
			return
		}
	}
	if projectID.Valid {
		params.ProjectID = uuidToString(projectID)
		if err := h.DB.QueryRow(ctx, `SELECT title FROM project WHERE id = $1 AND workspace_id = $2`, projectID, parseUUID(workspaceID)).Scan(&params.ProjectTitle); err != nil {
			slog.Warn("channel project system event: load project title", "channel", channelID.String(), "project", projectID.String(), "error", err)
			return
		}
	}

	event := channelProjectBoundEvent
	if !projectID.Valid {
		event = channelProjectUnboundEvent
	} else if previousProjectID.Valid {
		event = channelProjectChangedEvent
	}
	content := channelProjectSystemEventContent(event, actorRef.DisplayName, params.ProjectTitle, params.PreviousProjectTitle)
	paramsJSON, err := json.Marshal(params)
	if err != nil {
		slog.Warn("channel project system event: marshal event params", "channel", channelID.String(), "event", event, "error", err)
		return
	}
	msg, err := h.insertChannelMessageWithParts(ctx, channelID, parseUUID(workspaceID), "system", pgtype.UUID{}, "system", content,
		[]protocol.MessagePart{{Type: protocol.MessagePartTypeSystemEvent, Event: event, EventParams: paramsJSON}},
		"multica", nil, pgtype.UUID{}, pgtype.UUID{}, nil, 0)
	if err != nil {
		slog.Warn("channel project system event: insert", "channel", channelID.String(), "event", event, "error", err)
		return
	}
	h.publishChannelToMembers(ctx, protocol.EventChannelMessage, workspaceID, "system", "", channelID, msg)
}

func channelProjectSystemEventContent(event, actorName, projectTitle, previousProjectTitle string) string {
	switch event {
	case channelProjectUnboundEvent:
		return fmt.Sprintf("%s removed this channel's project %s", actorName, previousProjectTitle)
	case channelProjectChangedEvent:
		return fmt.Sprintf("%s changed this channel's project from %s to %s", actorName, previousProjectTitle, projectTitle)
	default:
		return fmt.Sprintf("%s linked this channel to %s", actorName, projectTitle)
	}
}
