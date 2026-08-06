package handler

import (
	"context"
	"log/slog"
	"strings"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// scheduleCanonicalMessageDelivery attaches live Agent projection to the same
// committed channel:message boundary used by every canonical author type. This
// keeps the live side of the recovery fence aligned with the Server recovery
// query instead of covering only browser-authored human Messages.
func (h *Handler) scheduleCanonicalMessageDelivery(ctx context.Context, eventType string, payload any) {
	if h == nil || eventType != protocol.EventChannelMessage {
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

// deliverCanonicalMessageToChannelAgents persists and projects one committed
// canonical Message to exactly the Agents selected by the channel routing
// policy. Offline delivery is intentionally harmless: startup/reconnect
// recovery reads the same persisted Delivery mapping.
func (h *Handler) deliverCanonicalMessageToChannelAgents(ctx context.Context, ch ChannelResponse, message ChannelMessageResponse) {
	if h == nil || h.DB == nil || strings.TrimSpace(message.ID) == "" || message.Seq <= 0 {
		return
	}
	target := "channel:" + ch.ID
	if message.ThreadRootMessageID != nil && strings.TrimSpace(*message.ThreadRootMessageID) != "" {
		target = "thread:" + *message.ThreadRootMessageID
	}
	for _, recipient := range h.canonicalMessageDeliveryRecipients(ctx, ch, message) {
		if !recipient.RuntimeID.Valid {
			continue
		}
		agentIDString := uuidToString(recipient.ID)
		if _, err := h.DB.Exec(ctx, `
			INSERT INTO agent_message_delivery (workspace_id, agent_id, message_id, target, seq)
			VALUES ($1, $2, $3, $4, $5)
			ON CONFLICT (agent_id, message_id) DO NOTHING`,
			parseUUID(ch.WorkspaceID), recipient.ID, parseUUID(message.ID), target, message.Seq); err != nil {
			slog.Warn("persist canonical Agent Message delivery failed", "workspace_id", ch.WorkspaceID, "channel_id", ch.ID, "message_id", message.ID, "agent_id", agentIDString, "error", err)
			continue
		}
		delivery := protocol.AgentDeliverPayload{
			AgentID:    agentIDString,
			Target:     target,
			Seq:        message.Seq,
			DeliveryID: "message:" + message.ID + ":agent:" + agentIDString,
			Message: protocol.AgentMessageProjection{
				ID: message.ID, Target: target, Seq: message.Seq, Content: message.Content, Parts: message.Parts,
			},
		}
		if h.AgentDeliveryNotifier == nil || !h.AgentDeliveryNotifier.NotifyAgentDelivery(uuidToString(recipient.RuntimeID), delivery) {
			slog.Debug("Agent Message live delivery deferred to recovery", "workspace_id", ch.WorkspaceID, "agent_id", agentIDString, "runtime_id", uuidToString(recipient.RuntimeID), "message_id", message.ID, "delivery_id", delivery.DeliveryID)
		}
	}
}

// canonicalMessageDeliveryRecipients is the sole recipient policy for the
// canonical Message transport. It deliberately preserves channel semantics:
// normal channel messages wake every unmuted Agent, explicit @mentions wake
// only their targets, and thread replies wake only explicit targets or active
// thread participants. Agents never wake themselves from their own Messages.
func (h *Handler) canonicalMessageDeliveryRecipients(ctx context.Context, ch ChannelResponse, message ChannelMessageResponse) []db.Agent {
	mentioned := h.channelMentionedAgents(ctx, ch.WorkspaceID, ch.ID, message.Content, message.Parts)
	threadRootID := ""
	if message.ThreadRootMessageID != nil {
		threadRootID = strings.TrimSpace(*message.ThreadRootMessageID)
	}
	if threadRootID != "" {
		if len(mentioned) > 0 {
			return h.filterCanonicalMessageDeliveryRecipients(ctx, ch, message, mentioned, false)
		}
		return h.filterCanonicalMessageDeliveryRecipients(ctx, ch, message, h.channelThreadFollowerAgents(ctx, ch.WorkspaceID, ch.ID, threadRootID), true)
	}
	if channelMessageIsHumanAuthored(message.Type) && channelMessageIsGroupCommand(message.Content, message.Parts) {
		return h.filterCanonicalMessageDeliveryRecipients(ctx, ch, message, h.channelAgentMembers(ctx, ch.WorkspaceID, ch.ID), true)
	}
	if len(mentioned) > 0 {
		return h.filterCanonicalMessageDeliveryRecipients(ctx, ch, message, mentioned, false)
	}
	if message.Type == "system" {
		return nil
	}
	return h.filterCanonicalMessageDeliveryRecipients(ctx, ch, message, h.channelAgentMembers(ctx, ch.WorkspaceID, ch.ID), true)
}

func (h *Handler) filterCanonicalMessageDeliveryRecipients(ctx context.Context, ch ChannelResponse, message ChannelMessageResponse, candidates []db.Agent, respectMute bool) []db.Agent {
	unique := make(map[string]struct{}, len(candidates))
	result := make([]db.Agent, 0, len(candidates))
	for _, candidate := range candidates {
		if !candidate.RuntimeID.Valid {
			continue
		}
		candidateID := uuidToString(candidate.ID)
		if candidateID == "" {
			continue
		}
		if message.Type == "agent" && message.AuthorID != nil && candidateID == *message.AuthorID {
			continue
		}
		if respectMute && h.isChannelAgentMuted(ctx, parseUUID(ch.ID), parseUUID(ch.WorkspaceID), candidate.ID) {
			continue
		}
		if _, exists := unique[candidateID]; exists {
			continue
		}
		unique[candidateID] = struct{}{}
		result = append(result, candidate)
	}
	return result
}
