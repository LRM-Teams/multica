package handler

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"
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
	if channelMessageHasPendingVoiceTranscription(message) {
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

func channelMessageHasPendingVoiceTranscription(message ChannelMessageResponse) bool {
	for _, part := range message.Parts {
		if part.Type == protocol.MessagePartTypeVoice && part.TranscriptionStatus == protocol.VoiceTranscriptionPending {
			return true
		}
	}
	return false
}

// deliverCanonicalMessageToChannelAgents persists and projects one committed
// canonical Message to exactly the Agents selected by the channel routing
// policy. Offline delivery is intentionally harmless: startup/reconnect
// recovery reads the same persisted Delivery mapping.
func (h *Handler) deliverCanonicalMessageToChannelAgents(ctx context.Context, ch ChannelResponse, message ChannelMessageResponse) {
	if h == nil || h.DB == nil || strings.TrimSpace(message.ID) == "" || message.Seq <= 0 {
		return
	}
	for _, recipient := range h.canonicalMessageDeliveryRecipients(ctx, ch, message) {
		delivery, ok, err := persistCanonicalMessageDelivery(ctx, h.DB, ch, message, recipient)
		if err != nil {
			slog.Warn("persist canonical Agent Message delivery failed", "workspace_id", ch.WorkspaceID, "channel_id", ch.ID, "message_id", message.ID, "agent_id", uuidToString(recipient.ID), "error", err)
			continue
		}
		if ok {
			h.notifyCanonicalMessageDelivery(ctx, ch, recipient, delivery)
		}
	}
}

// persistCanonicalMessageDelivery records one explicitly selected recipient.
// Normal channel traffic supplies recipients through canonicalMessageDeliveryRecipients;
// system primitives such as Reminder use this same durable mapping with their own
// product-specific recipient rule.
func persistCanonicalMessageDelivery(ctx context.Context, exec dbExecutor, ch ChannelResponse, message ChannelMessageResponse, recipient db.Agent) (protocol.AgentDeliverPayload, bool, error) {
	if exec == nil || !recipient.RuntimeID.Valid || strings.TrimSpace(ch.WorkspaceID) == "" || strings.TrimSpace(message.ID) == "" || message.Seq <= 0 {
		return protocol.AgentDeliverPayload{}, false, nil
	}
	agentID := uuidToString(recipient.ID)
	if agentID == "" {
		return protocol.AgentDeliverPayload{}, false, nil
	}
	target := canonicalMessageDeliveryTarget(ch, message)
	replyTarget, err := canonicalMessageReplyTarget(ctx, exec, ch, message, recipient.ID)
	if err != nil {
		return protocol.AgentDeliverPayload{}, false, err
	}
	tag, err := exec.Exec(ctx, `
		INSERT INTO agent_message_delivery (workspace_id, agent_id, message_id, target, seq)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (agent_id, message_id) DO NOTHING`,
		parseUUID(ch.WorkspaceID), recipient.ID, parseUUID(message.ID), target, message.Seq)
	if err != nil {
		return protocol.AgentDeliverPayload{}, false, err
	}
	if tag.RowsAffected() != 1 {
		return protocol.AgentDeliverPayload{}, false, nil
	}
	return protocol.AgentDeliverPayload{
		AgentID:    agentID,
		Target:     target,
		Seq:        message.Seq,
		DeliveryID: "message:" + message.ID + ":agent:" + agentID,
		Message: protocol.AgentMessageProjection{
			ID: message.ID, Target: target, ReplyTarget: replyTarget, Seq: message.Seq, Content: message.Content, Parts: message.Parts,
		},
	}, true, nil
}

func canonicalMessageDeliveryTarget(ch ChannelResponse, message ChannelMessageResponse) string {
	if message.ThreadRootMessageID != nil && strings.TrimSpace(*message.ThreadRootMessageID) != "" {
		return "thread:" + *message.ThreadRootMessageID
	}
	return "channel:" + ch.ID
}

// canonicalMessageReplyTarget projects the internal delivery key into the
// human-facing target syntax accepted by `multica message send`. The internal
// target remains stable for coordinator boundaries; the reply target is safe
// to expose to a runtime and can be reused verbatim.
func canonicalMessageReplyTarget(ctx context.Context, exec dbExecutor, ch ChannelResponse, message ChannelMessageResponse, recipientID pgtype.UUID) (string, error) {
	var base string
	switch strings.TrimSpace(ch.Kind) {
	case "", "group":
		name := strings.TrimSpace(ch.Name)
		if name == "" {
			return "", fmt.Errorf("canonical Message group channel name is empty")
		}
		base = "#" + name
	case "dm":
		var handle string
		if err := exec.QueryRow(ctx, `
			SELECT COALESCE(NULLIF(u.name, ''), NULLIF(a.name, ''), '')
			FROM channel_member cm
			LEFT JOIN "user" u ON cm.member_type = 'user' AND u.id = cm.member_id
			LEFT JOIN agent a ON cm.member_type = 'agent' AND a.id = cm.member_id AND a.archived_at IS NULL
			WHERE cm.channel_id = $1
			  AND cm.workspace_id = $2
			  AND NOT (cm.member_type = 'agent' AND cm.member_id = $3)
			ORDER BY cm.created_at ASC
			LIMIT 1`, parseUUID(ch.ID), parseUUID(ch.WorkspaceID), recipientID).Scan(&handle); err != nil {
			return "", fmt.Errorf("resolve canonical Message DM peer: %w", err)
		}
		handle = strings.TrimSpace(handle)
		if handle == "" {
			return "", fmt.Errorf("canonical Message DM peer handle is empty")
		}
		base = "dm:@" + handle
	default:
		return "", fmt.Errorf("unsupported canonical Message channel kind %q", ch.Kind)
	}
	if message.ThreadRootMessageID != nil {
		rootID := strings.TrimSpace(*message.ThreadRootMessageID)
		if rootID != "" {
			if len(rootID) > 8 {
				rootID = rootID[:8]
			}
			base += ":" + rootID
		}
	}
	return base, nil
}

func (h *Handler) notifyCanonicalMessageDelivery(ctx context.Context, ch ChannelResponse, recipient db.Agent, delivery protocol.AgentDeliverPayload) {
	if h == nil || h.DB == nil || h.AgentDeliveryNotifier == nil || !recipient.RuntimeID.Valid {
		return
	}
	var daemonID *string
	if err := h.DB.QueryRow(ctx, `SELECT daemon_id FROM agent_runtime WHERE id = $1`, recipient.RuntimeID).Scan(&daemonID); err != nil {
		slog.Warn("load Agent Message delivery daemon failed", "workspace_id", ch.WorkspaceID, "agent_id", delivery.AgentID, "runtime_id", uuidToString(recipient.RuntimeID), "message_id", delivery.Message.ID, "error", err)
		return
	}
	if daemonID == nil || strings.TrimSpace(*daemonID) == "" || !h.AgentDeliveryNotifier.NotifyWorkspaceAgentDelivery(ch.WorkspaceID, *daemonID, delivery) {
		slog.Debug("Agent Message live delivery deferred to recovery", "workspace_id", ch.WorkspaceID, "agent_id", delivery.AgentID, "daemon_id", daemonID, "message_id", delivery.Message.ID, "delivery_id", delivery.DeliveryID)
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
