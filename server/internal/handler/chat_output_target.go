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

func (h *Handler) validateChatOutputTarget(ctx context.Context, task db.AgentTaskQueue, rawTarget string) error {
	origin, ok := h.chatOutputOriginForTask(ctx, task)
	if !ok {
		return errChatOutputInvalidTarget
	}
	_, err := h.resolveChatOutputTarget(ctx, origin, rawTarget)
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

func (h *Handler) resolveChatOutputTarget(ctx context.Context, origin chatOutputOrigin, rawTarget string) (resolvedChatOutputTarget, error) {
	target := strings.TrimSpace(rawTarget)
	if target == "" {
		ch, found := h.getChannel(ctx, uuidToString(origin.workspaceID), origin.channelID)
		if !found || ch.ArchivedAt != nil {
			return resolvedChatOutputTarget{}, errChatOutputInvalidTarget
		}
		return resolvedChatOutputTarget{kind: chatOutputTargetChannel, channel: ch}, nil
	}
	if strings.HasPrefix(target, "dm:@") {
		handle, rawMessageID, hasMessageID := splitDMOutputTarget(strings.TrimPrefix(target, "dm:@"))
		recipientID, err := h.resolveHumanDMOutputTarget(ctx, origin, handle)
		if err != nil {
			return resolvedChatOutputTarget{}, err
		}
		if hasMessageID {
			ch, ok := h.agentHumanDMChannel(ctx, origin.workspaceID, origin.agentID, recipientID, false)
			if !ok {
				return resolvedChatOutputTarget{}, errChatOutputInvalidTarget
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
		return resolvedChatOutputTarget{kind: chatOutputTargetDM, recipientID: recipientID}, nil
	}
	if strings.HasPrefix(target, "#") {
		return h.resolveChannelOutputTarget(ctx, origin, strings.TrimPrefix(target, "#"))
	}
	return resolvedChatOutputTarget{}, errChatOutputInvalidTarget
}

func splitDMOutputTarget(raw string) (handle, rawMessageID string, hasMessageID bool) {
	handle = strings.TrimSpace(raw)
	rawMessageID = ""
	hasMessageID = false
	if before, after, ok := strings.Cut(handle, ":"); ok {
		if strings.Contains(after, ":") {
			return "", "", true
		}
		handle = strings.TrimSpace(before)
		rawMessageID = strings.TrimSpace(after)
		hasMessageID = true
	}
	return handle, rawMessageID, hasMessageID
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
	rows, err := h.DB.Query(ctx, `
		SELECT m.user_id
		FROM member m
		JOIN "user" u ON u.id = m.user_id
		WHERE m.workspace_id = $1 AND lower(u.name) = lower($2)
		ORDER BY m.created_at ASC
		LIMIT 2`, origin.workspaceID, handle)
	if err != nil {
		return pgtype.UUID{}, errChatOutputInvalidTarget
	}
	defer rows.Close()
	var matches []pgtype.UUID
	for rows.Next() {
		if err := rows.Scan(&userID); err != nil {
			return pgtype.UUID{}, errChatOutputInvalidTarget
		}
		matches = append(matches, userID)
	}
	if rows.Err() != nil || len(matches) != 1 {
		return pgtype.UUID{}, errChatOutputInvalidTarget
	}
	userID = matches[0]
	agent, err := h.Queries.GetAgentInWorkspace(ctx, db.GetAgentInWorkspaceParams{ID: origin.agentID, WorkspaceID: origin.workspaceID})
	if err != nil || agent.ArchivedAt.Valid {
		return pgtype.UUID{}, errChatOutputInvalidTarget
	}
	if !h.canAccessPrivateAgent(ctx, agent, "user", uuidToString(userID), uuidToString(origin.workspaceID)) {
		return pgtype.UUID{}, errChatOutputInvalidTarget
	}
	return userID, nil
}

func (h *Handler) resolveChannelOutputTarget(ctx context.Context, origin chatOutputOrigin, raw string) (resolvedChatOutputTarget, error) {
	channelName, rawMessageID, hasMessageID := strings.Cut(raw, ":")
	if strings.Contains(rawMessageID, ":") {
		return resolvedChatOutputTarget{}, errChatOutputInvalidTarget
	}
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
		SELECT id, workspace_id, name, description, lark_chat_id, project_id, created_by, created_at, updated_at, kind, system_key, archived_at, archived_by
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

func (h *Handler) loadChannelThreadRootForOutputTarget(ctx context.Context, workspaceID, channelID, msgID pgtype.UUID) (ChannelMessageResponse, error) {
	// First, look up the message to check whether it is a root or a reply.
	// If the caller passed a thread reply id (#channel:<replyid>), flatten to
	// the thread root so a natural reply target is not silently suppressed.
	var threadRootID pgtype.UUID
	err := h.DB.QueryRow(ctx, `
		SELECT thread_root_message_id
		FROM channel_message
		WHERE id = $1 AND channel_id = $2 AND workspace_id = $3 AND author_type <> 'system'`,
		msgID, channelID, workspaceID).Scan(&threadRootID)
	if err != nil {
		return ChannelMessageResponse{}, err
	}
	lookupID := msgID
	if threadRootID.Valid {
		// The passed id is itself a thread reply — flatten to the root.
		lookupID = threadRootID
	}
	row := h.DB.QueryRow(ctx, `
		SELECT id, channel_id, workspace_id, author_type, author_id, author_name, content, parts, source, external_message_id, client_message_id, reply_to_message_id, quote_message_id, quote_snapshot, thread_root_message_id, thread_id, trigger_depth, seq, created_at, edited_at, deleted_at
		FROM channel_message
		WHERE id = $1 AND channel_id = $2 AND workspace_id = $3 AND thread_root_message_id IS NULL AND author_type <> 'system'`,
		lookupID, channelID, workspaceID)
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

// handleResolvedChannelChatDone is the sole visible-message path for a channel
// chat completion. It resolves the final destination before deriving any
// message facts, so a cross-channel output cannot borrow mention candidates
// from the channel that originally woke the agent.
func (h *Handler) handleResolvedChannelChatDone(ctx context.Context, origin chatOutputOrigin, payload protocol.ChatDonePayload, content string, parts []protocol.MessagePart, initiatorID, defaultThreadRootMessageID pgtype.UUID, defaultThreadID *string, defaultTriggerDepth int) {
	target, err := h.resolveChatOutputTarget(ctx, origin, payload.Target)
	if err != nil {
		slog.Warn("channel bridge: suppressing invalid chat output target", "chat_session_id", payload.ChatSessionID, "target", payload.Target, "error", err)
		return
	}
	if archived, found := h.channelIsArchived(ctx, uuidToString(origin.workspaceID), origin.channelID); !found || archived {
		return
	}
	if target.kind == chatOutputTargetDM {
		ch, ok := h.ensureAgentHumanDMChannel(ctx, origin.workspaceID, origin.agentID, target.recipientID)
		if !ok {
			slog.Warn("channel bridge: create targeted dm failed", "chat_session_id", payload.ChatSessionID, "target", payload.Target)
			return
		}
		target.channel = ch
	}
	switch target.kind {
	case chatOutputTargetDM:
		h.insertAgentChatOutputMessage(ctx, target.channel, origin.agentID, content, parts, pgtype.UUID{}, nil, 0, initiatorID)
	case chatOutputTargetThread:
		threadID := target.threadRoot.ThreadID
		if threadID == nil || strings.TrimSpace(*threadID) == "" {
			fresh := uuid.NewString()
			threadID = &fresh
		}
		h.insertAgentChatOutputMessage(ctx, target.channel, origin.agentID, content, parts, parseUUID(target.threadRoot.ID), threadID, target.threadRoot.TriggerDepth+1, initiatorID)
	case chatOutputTargetChannel:
		threadRootMessageID := pgtype.UUID{}
		threadID := (*string)(nil)
		triggerDepth := 0
		if strings.TrimSpace(payload.Target) == "" {
			threadRootMessageID = defaultThreadRootMessageID
			threadID = defaultThreadID
			triggerDepth = defaultTriggerDepth + 1
		}
		h.insertAgentChatOutputMessage(ctx, target.channel, origin.agentID, content, parts, threadRootMessageID, threadID, triggerDepth, initiatorID)
	}
}

func (h *Handler) insertAgentChatOutputMessage(ctx context.Context, ch ChannelResponse, agentID pgtype.UUID, content string, parts []protocol.MessagePart, threadRootMessageID pgtype.UUID, threadID *string, triggerDepth int, initiatorID pgtype.UUID) (ChannelMessageResponse, bool) {
	content, parts, err := h.finalizeAgentChannelMessage(ctx, ch, content, parts)
	if err != nil {
		slog.Warn("channel bridge: invalid agent message output", "channel_id", ch.ID, "agent_id", uuidToString(agentID), "error", err)
		return ChannelMessageResponse{}, false
	}
	channelID := parseUUID(ch.ID)
	workspaceID := parseUUID(ch.WorkspaceID)
	agentName := h.agentName(ctx, agentID)
	msg, err := h.insertChannelMessageWithParts(ctx, channelID, workspaceID, "agent", agentID, agentName, content, parts, "multica", nil, pgtype.UUID{}, threadRootMessageID, threadID, triggerDepth)
	if err != nil {
		slog.Warn("channel bridge: insert targeted agent message failed", "channel_id", ch.ID, "agent_id", uuidToString(agentID), "error", err)
		return ChannelMessageResponse{}, false
	}
	if threadRootMessageID.Valid {
		h.followChannelThreadAgent(ctx, channelID, threadRootMessageID, agentID)
	}
	messages := []ChannelMessageResponse{msg}
	h.attachChannelMessageAuthorAvatars(ctx, ch.WorkspaceID, messages)
	msg = messages[0]
	_, _ = h.DB.Exec(ctx, `UPDATE channel SET updated_at = now() WHERE id = $1`, channelID)
	h.clearDMHiddenForChannelMembers(ctx, ch.WorkspaceID, channelID)
	h.publishChannelToMembers(ctx, protocol.EventChannelMessage, ch.WorkspaceID, "agent", uuidToString(agentID), channelID, msg)
	// Same LRM-272 / LRM-297 contract as agent transport send: publish first,
	// then wake/Feishu off the critical path so channel visibility is not gated
	// on O(agents) fanout.
	if ch.Kind == "group" {
		h.runAfterChannelMessageAck(ctx, func(ctx context.Context) {
			h.ingestWendyAgentGroupMessage(ctx, ch, msg, agentID)
			if threadRootMessageID.Valid {
				h.dispatchChannelThreadReplyMentions(ctx, ch, msg, initiatorID)
			} else {
				h.dispatchChannelMentions(ctx, ch, msg, initiatorID)
			}
			h.sendChannelMessageToFeishu(ctx, ch, agentName, content)
		})
	}
	return msg, true
}
