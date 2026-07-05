package handler

import (
	"context"
	"errors"
	"log/slog"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

var errChatOutputInvalidTarget = errors.New("invalid chat output target")

type chatOutputTargetKind string

const (
	chatOutputTargetChannel chatOutputTargetKind = "channel"
	chatOutputTargetThread  chatOutputTargetKind = "thread"
	chatOutputTargetDM      chatOutputTargetKind = "dm"
)

type chatOutputOrigin struct {
	channelID   pgtype.UUID
	workspaceID pgtype.UUID
	agentID     pgtype.UUID
}

type resolvedChatOutputTarget struct {
	kind        chatOutputTargetKind
	channel     ChannelResponse
	recipientID pgtype.UUID
	threadRoot  ChannelMessageResponse
}

func (h *Handler) validateChatOutputTarget(ctx context.Context, task db.AgentTaskQueue, rawTarget string, options *protocol.ChatOutputOptions) error {
	origin, ok := h.chatOutputOriginForTask(ctx, task)
	if !ok {
		return errChatOutputInvalidTarget
	}
	_, err := h.resolveChatOutputTarget(ctx, origin, rawTarget, options)
	return err
}

func (h *Handler) chatOutputOriginForTask(ctx context.Context, task db.AgentTaskQueue) (chatOutputOrigin, bool) {
	if !task.ChatSessionID.Valid {
		return chatOutputOrigin{}, false
	}
	channelID, workspaceID, agentID, ok := h.channelAgentForChatSession(ctx, uuidToString(task.ChatSessionID))
	if !ok {
		return chatOutputOrigin{}, false
	}
	return chatOutputOrigin{channelID: channelID, workspaceID: workspaceID, agentID: agentID}, true
}

func (h *Handler) resolveChatOutputTarget(ctx context.Context, origin chatOutputOrigin, rawTarget string, options *protocol.ChatOutputOptions) (resolvedChatOutputTarget, error) {
	target := strings.TrimSpace(rawTarget)
	if target == "" {
		if chatOutputOptionsPresent(options) {
			return resolvedChatOutputTarget{}, errChatOutputInvalidTarget
		}
		ch, found := h.getChannel(ctx, uuidToString(origin.workspaceID), origin.channelID)
		if !found || ch.ArchivedAt != nil {
			return resolvedChatOutputTarget{}, errChatOutputInvalidTarget
		}
		return resolvedChatOutputTarget{kind: chatOutputTargetChannel, channel: ch}, nil
	}
	if strings.HasPrefix(target, "dm:@") {
		if chatOutputOptionsPresent(options) {
			return resolvedChatOutputTarget{}, errChatOutputInvalidTarget
		}
		recipientID, err := h.resolveHumanDMOutputTarget(ctx, origin, strings.TrimPrefix(target, "dm:@"))
		if err != nil {
			return resolvedChatOutputTarget{}, err
		}
		return resolvedChatOutputTarget{kind: chatOutputTargetDM, recipientID: recipientID}, nil
	}
	if strings.HasPrefix(target, "#") {
		return h.resolveChannelOutputTarget(ctx, origin, strings.TrimPrefix(target, "#"), options)
	}
	return resolvedChatOutputTarget{}, errChatOutputInvalidTarget
}

func chatOutputOptionsPresent(options *protocol.ChatOutputOptions) bool {
	return options.HasChannelDisplayOption()
}

func (h *Handler) resolveHumanDMOutputTarget(ctx context.Context, origin chatOutputOrigin, handle string) (pgtype.UUID, error) {
	handle = strings.TrimSpace(handle)
	if handle == "" {
		return pgtype.UUID{}, errChatOutputInvalidTarget
	}
	var agentName string
	if err := h.DB.QueryRow(ctx, `
		SELECT name
		FROM agent
		WHERE id = $1 AND workspace_id = $2`, origin.agentID, origin.workspaceID).Scan(&agentName); err == nil && strings.EqualFold(strings.TrimSpace(agentName), handle) {
		return pgtype.UUID{}, errChatOutputInvalidTarget
	}
	var userID pgtype.UUID
	err := h.DB.QueryRow(ctx, `
		SELECT m.user_id
		FROM member m
		JOIN "user" u ON u.id = m.user_id
		WHERE m.workspace_id = $1 AND lower(u.name) = lower($2)
		LIMIT 1`, origin.workspaceID, handle).Scan(&userID)
	if err != nil {
		return pgtype.UUID{}, errChatOutputInvalidTarget
	}
	agent, err := h.Queries.GetAgentInWorkspace(ctx, db.GetAgentInWorkspaceParams{ID: origin.agentID, WorkspaceID: origin.workspaceID})
	if err != nil || agent.ArchivedAt.Valid {
		return pgtype.UUID{}, errChatOutputInvalidTarget
	}
	if !h.canAccessPrivateAgent(ctx, agent, "user", uuidToString(userID), uuidToString(origin.workspaceID)) {
		return pgtype.UUID{}, errChatOutputInvalidTarget
	}
	return userID, nil
}

func (h *Handler) resolveChannelOutputTarget(ctx context.Context, origin chatOutputOrigin, raw string, options *protocol.ChatOutputOptions) (resolvedChatOutputTarget, error) {
	channelName, rawMessageID, hasMessageID := strings.Cut(raw, ":")
	channelName = strings.TrimSpace(channelName)
	if channelName == "" {
		return resolvedChatOutputTarget{}, errChatOutputInvalidTarget
	}
	ch, err := h.groupChannelByName(ctx, origin.workspaceID, channelName)
	if err != nil {
		return resolvedChatOutputTarget{}, errChatOutputInvalidTarget
	}
	if !h.channelHasAgentMember(ctx, origin.workspaceID, parseUUID(ch.ID), origin.agentID) {
		return resolvedChatOutputTarget{}, errChatOutputInvalidTarget
	}
	if !hasMessageID {
		if chatOutputOptionsPresent(options) {
			return resolvedChatOutputTarget{}, errChatOutputInvalidTarget
		}
		return resolvedChatOutputTarget{kind: chatOutputTargetChannel, channel: ch}, nil
	}
	rootID, err := util.ParseUUID(strings.TrimSpace(rawMessageID))
	if err != nil {
		return resolvedChatOutputTarget{}, errChatOutputInvalidTarget
	}
	root, err := h.loadChannelThreadRootForOutputTarget(ctx, origin.workspaceID, parseUUID(ch.ID), rootID)
	if err != nil {
		return resolvedChatOutputTarget{}, errChatOutputInvalidTarget
	}
	return resolvedChatOutputTarget{kind: chatOutputTargetThread, channel: ch, threadRoot: root}, nil
}

func (h *Handler) groupChannelByName(ctx context.Context, workspaceID pgtype.UUID, name string) (ChannelResponse, error) {
	row := h.DB.QueryRow(ctx, `
		SELECT id, workspace_id, name, description, lark_chat_id, created_by, created_at, updated_at, kind, archived_at, archived_by
		FROM channel
		WHERE workspace_id = $1 AND name = $2 AND kind = 'group' AND archived_at IS NULL
		LIMIT 1`, workspaceID, name)
	return scanChannel(row)
}

func (h *Handler) channelHasAgentMember(ctx context.Context, workspaceID, channelID, agentID pgtype.UUID) bool {
	var exists bool
	err := h.DB.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM channel_member
			WHERE workspace_id = $1 AND channel_id = $2 AND member_type = 'agent' AND member_id = $3
		)`, workspaceID, channelID, agentID).Scan(&exists)
	return err == nil && exists
}

func (h *Handler) loadChannelThreadRootForOutputTarget(ctx context.Context, workspaceID, channelID, rootID pgtype.UUID) (ChannelMessageResponse, error) {
	row := h.DB.QueryRow(ctx, `
		SELECT id, channel_id, workspace_id, author_type, author_id, author_name, content, parts, source, external_message_id, client_message_id, reply_to_message_id, thread_root_message_id, thread_id, trigger_depth, seq, created_at, edited_at, deleted_at
		FROM channel_message
		WHERE id = $1 AND channel_id = $2 AND workspace_id = $3 AND thread_root_message_id IS NULL AND author_type <> 'system'`,
		rootID, channelID, workspaceID)
	return scanChannelMessage(row)
}

func (h *Handler) ensureAgentHumanDMChannel(ctx context.Context, workspaceID, agentID, userID pgtype.UUID) (ChannelResponse, bool) {
	workspaceIDText := uuidToString(workspaceID)
	canonical := dmCanonicalName("user", uuidToString(userID), "agent", uuidToString(agentID))
	if ch, found := h.findDMChannel(ctx, workspaceIDText, canonical); found {
		h.clearDMPeerHidden(ctx, workspaceIDText, uuidToString(userID), dmPeerRef{Type: "agent", ID: agentID})
		return ch, true
	}
	ch, created := h.createDMChannel(ctx, nil, workspaceIDText, uuidToString(userID), canonical, []dmMember{
		{memberType: "user", memberID: userID},
		{memberType: "agent", memberID: agentID},
	})
	if !created {
		return ChannelResponse{}, false
	}
	h.clearDMPeerHidden(ctx, workspaceIDText, uuidToString(userID), dmPeerRef{Type: "agent", ID: agentID})
	return ch, true
}

func (h *Handler) handleTargetedChannelChatDone(ctx context.Context, origin chatOutputOrigin, payload protocol.ChatDonePayload, content string, parts []protocol.MessagePart, initiatorID pgtype.UUID) bool {
	if strings.TrimSpace(payload.Target) == "" && !chatOutputOptionsPresent(payload.Options) {
		return false
	}
	target, err := h.resolveChatOutputTarget(ctx, origin, payload.Target, payload.Options)
	if err != nil {
		slog.Warn("channel bridge: suppressing invalid targeted chat output", "chat_session_id", payload.ChatSessionID, "target", payload.Target, "error", err)
		return true
	}
	switch target.kind {
	case chatOutputTargetDM:
		ch, ok := h.ensureAgentHumanDMChannel(ctx, origin.workspaceID, origin.agentID, target.recipientID)
		if !ok {
			slog.Warn("channel bridge: create targeted dm failed", "chat_session_id", payload.ChatSessionID, "target", payload.Target)
			return true
		}
		h.insertAgentChatOutputMessage(ctx, ch, origin.agentID, content, parts, pgtype.UUID{}, nil, 0, initiatorID, false)
	case chatOutputTargetThread:
		threadID := target.threadRoot.ThreadID
		if threadID == nil || strings.TrimSpace(*threadID) == "" {
			fresh := uuid.NewString()
			threadID = &fresh
		}
		mainTimelineVisible := payload.Options.ShowInChannelValue()
		h.insertAgentChatOutputMessage(ctx, target.channel, origin.agentID, content, parts, parseUUID(target.threadRoot.ID), threadID, target.threadRoot.TriggerDepth+1, initiatorID, mainTimelineVisible)
	case chatOutputTargetChannel:
		h.insertAgentChatOutputMessage(ctx, target.channel, origin.agentID, content, parts, pgtype.UUID{}, nil, 0, initiatorID, false)
	}
	return true
}

func (h *Handler) insertAgentChatOutputMessage(ctx context.Context, ch ChannelResponse, agentID pgtype.UUID, content string, parts []protocol.MessagePart, threadRootMessageID pgtype.UUID, threadID *string, triggerDepth int, initiatorID pgtype.UUID, mainTimelineVisible bool) (ChannelMessageResponse, bool) {
	channelID := parseUUID(ch.ID)
	workspaceID := parseUUID(ch.WorkspaceID)
	agentName := h.agentName(ctx, agentID)
	msg, err := h.insertChannelMessageWithPartsMainProjection(ctx, channelID, workspaceID, "agent", agentID, agentName, content, parts, "multica", nil, pgtype.UUID{}, threadRootMessageID, threadID, triggerDepth, mainTimelineVisible)
	if err != nil {
		slog.Warn("channel bridge: insert targeted agent message failed", "channel_id", ch.ID, "agent_id", uuidToString(agentID), "error", err)
		return ChannelMessageResponse{}, false
	}
	if mainTimelineVisible {
		messages := []ChannelMessageResponse{msg}
		h.attachChannelMessageThreadRootSummaries(ctx, ch.WorkspaceID, messages)
		msg = messages[0]
	}
	_, _ = h.DB.Exec(ctx, `UPDATE channel SET updated_at = now() WHERE id = $1`, channelID)
	if ch.Kind == "dm" {
		h.clearDMHiddenForChannelMembers(ctx, ch.WorkspaceID, channelID)
	}
	h.publishChannelToMembers(ctx, protocol.EventChannelMessage, ch.WorkspaceID, "agent", uuidToString(agentID), channelID, msg)
	if ch.Kind == "group" {
		if threadRootMessageID.Valid {
			h.dispatchChannelThreadReplyMentions(ctx, ch, msg, initiatorID)
		} else {
			h.dispatchChannelMentions(ctx, ch, msg, initiatorID)
		}
		h.sendChannelMessageToFeishu(ctx, ch, agentName, content)
	}
	return msg, true
}
