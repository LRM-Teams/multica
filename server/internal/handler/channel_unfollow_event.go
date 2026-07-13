package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

type threadUnfollowedSystemEventPart struct {
	Event  string                            `json:"event"`
	Params threadUnfollowedSystemEventParams `json:"params"`
}

type threadUnfollowedSystemEventParams struct {
	AgentID          string `json:"agent_id"`
	AgentName        string `json:"agent_name"`
	ActorID          string `json:"actor_id"`
	ActorType        string `json:"actor_type"`
	ActorHandle      string `json:"actor_handle,omitempty"`
	ActorDisplayName string `json:"actor_display_name"`
	ActorName        string `json:"actor_name"`
}

// emitAgentThreadUnfollowedEvent inserts a system message in the thread when
// an agent explicitly unfollows it. The message is type=system with a
// structured event part so the FE can i18n-localize the display text.
//
// #329: thread unfollow is the only attention-management action that
// emits a system notification. Channel mute/unmute is silent (no
// broadcast, visibility via profile + Activity). Re-follow emits nothing.
// Co-ship constraint: this event must be live before (or in the same
// batch as) any daemon release that wires the CLI to the agent-mute
// endpoints, so agents never go silently mute-unresponsive with no
// visible signal.
func (h *Handler) emitAgentThreadUnfollowedEvent(w http.ResponseWriter, ctx context.Context, workspaceID string, channelID pgtype.UUID, rootID pgtype.UUID, agentID pgtype.UUID) {
	actorRef := h.channelMemberSystemEventActorRef(ctx, workspaceID, "agent", agentID)
	displayName := firstNonEmpty(actorRef.DisplayName, "Agent")
	agentIDText := uuidToString(agentID)
	params := threadUnfollowedSystemEventParams{
		AgentID:          agentIDText,
		AgentName:        displayName,
		ActorID:          agentIDText,
		ActorType:        actorRef.Type,
		ActorHandle:      actorRef.Handle,
		ActorDisplayName: displayName,
		ActorName:        displayName,
	}
	rawPart, err := json.Marshal(threadUnfollowedSystemEventPart{
		Event:  "thread_unfollowed",
		Params: params,
	})
	if err != nil {
		slog.Warn("emitAgentThreadUnfollowedEvent: failed to marshal system part", "thread", rootID.String(), "agent", agentIDText, "error", err)
		return
	}

	canonical := fmt.Sprintf("%s unfollowed this thread", mentionLink("agent", agentIDText, displayName))

	msg, err := h.insertChannelMessageWithParts(
		ctx,
		channelID,
		parseUUID(workspaceID),
		"system",
		pgtype.UUID{},
		"system",
		canonical,
		[]protocol.MessagePart{{Text: string(rawPart)}},
		"multica",
		nil,
		pgtype.UUID{},
		rootID,
		nil,
		0,
	)
	if err != nil {
		slog.Warn("emitAgentThreadUnfollowedEvent: failed to insert system message", "thread", rootID.String(), "agent", agentIDText, "error", err)
		return
	}
	h.publishChannelToMembers(ctx, protocol.EventChannelMessage, workspaceID, "system", "", channelID, msg)
}

func mentionLink(mentionType, id, label string) string {
	label = strings.ReplaceAll(label, "]", "")
	label = strings.TrimSpace(label)
	if label == "" {
		label = mentionType
	}
	return fmt.Sprintf("[%s](mention://%s/%s)", label, mentionType, id)
}
