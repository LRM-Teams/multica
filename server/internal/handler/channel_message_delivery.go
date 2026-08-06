package handler

import (
	"context"
	"log/slog"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// scheduleCanonicalMessageDelivery attaches live Agent projection to the same
// committed channel:message boundary used by every canonical author type. This
// keeps the live side of the recovery fence aligned with the Server recovery
// query instead of covering only browser-authored human Messages.
func (h *Handler) scheduleCanonicalMessageDelivery(ctx context.Context, eventType string, payload any) {
	if h == nil || h.AgentDeliveryNotifier == nil || eventType != protocol.EventChannelMessage {
		return
	}
	message, ok := payload.(ChannelMessageResponse)
	if !ok || strings.TrimSpace(message.ID) == "" || message.Seq <= 0 {
		return
	}
	h.runAfterChannelMessageAck(ctx, func(ctx context.Context) {
		channel, found := h.getChannel(ctx, message.WorkspaceID, parseUUID(message.ChannelID))
		if !found {
			slog.Warn("load canonical Message channel for Agent delivery failed", "workspace_id", message.WorkspaceID, "channel_id", message.ChannelID, "message_id", message.ID)
			return
		}
		h.deliverCanonicalMessageToChannelAgents(ctx, channel, message)
	})
}

// deliverCanonicalMessageToChannelAgents projects one committed canonical
// Message to every current Agent member with a runtime placement. Offline
// delivery is intentionally harmless: startup/reconnect recovery is the
// correctness path and reads the same canonical Message.
func (h *Handler) deliverCanonicalMessageToChannelAgents(ctx context.Context, ch ChannelResponse, message ChannelMessageResponse) {
	if h == nil || h.AgentDeliveryNotifier == nil || h.DB == nil || strings.TrimSpace(message.ID) == "" || message.Seq <= 0 {
		return
	}
	rows, err := h.DB.Query(ctx, `
		SELECT agent.id, agent.runtime_id
		FROM channel_member member
		JOIN agent ON agent.id = member.member_id
		WHERE member.workspace_id = $1
		  AND member.channel_id = $2
		  AND member.member_type = 'agent'
		  AND agent.archived_at IS NULL
		  AND agent.runtime_id IS NOT NULL
		  AND NOT ($3::text = 'agent' AND agent.id = $4)`,
		parseUUID(ch.WorkspaceID), parseUUID(ch.ID), message.Type, nullableUUIDString(message.AuthorID))
	if err != nil {
		slog.Warn("query Agent Message delivery recipients failed", "workspace_id", ch.WorkspaceID, "channel_id", ch.ID, "message_id", message.ID, "error", err)
		return
	}
	defer rows.Close()
	target := "channel:" + ch.ID
	if message.ThreadRootMessageID != nil && strings.TrimSpace(*message.ThreadRootMessageID) != "" {
		target = "thread:" + *message.ThreadRootMessageID
	}
	for rows.Next() {
		var agentID, runtimeID pgtype.UUID
		if err := rows.Scan(&agentID, &runtimeID); err != nil {
			slog.Warn("scan Agent Message delivery recipient failed", "workspace_id", ch.WorkspaceID, "channel_id", ch.ID, "message_id", message.ID, "error", err)
			continue
		}
		agentIDString := uuidToString(agentID)
		delivery := protocol.AgentDeliverPayload{
			AgentID:    agentIDString,
			Target:     target,
			Seq:        message.Seq,
			DeliveryID: "message:" + message.ID + ":agent:" + agentIDString,
			Message: protocol.AgentMessageProjection{
				ID: message.ID, Target: target, Seq: message.Seq, Content: message.Content, Parts: message.Parts,
			},
		}
		if !h.AgentDeliveryNotifier.NotifyAgentDelivery(uuidToString(runtimeID), delivery) {
			slog.Debug("Agent Message live delivery deferred to recovery", "workspace_id", ch.WorkspaceID, "agent_id", agentIDString, "runtime_id", uuidToString(runtimeID), "message_id", message.ID, "delivery_id", delivery.DeliveryID)
		}
	}
	if err := rows.Err(); err != nil {
		slog.Warn("iterate Agent Message delivery recipients failed", "workspace_id", ch.WorkspaceID, "channel_id", ch.ID, "message_id", message.ID, "error", err)
	}
}

func nullableUUIDString(value *string) pgtype.UUID {
	if value == nil || strings.TrimSpace(*value) == "" {
		return pgtype.UUID{}
	}
	return parseUUID(*value)
}
