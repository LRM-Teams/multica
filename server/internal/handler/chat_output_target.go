package handler

import (
	"context"
	"encoding/json"
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

// errReminderSendOutsideAnchor is returned when a reminder-wake task tries to
// post outside the anchor message surface (msg-id hard bind). Not prompt-only.
// thread → that thread; main-channel message → main timeline. Not "any post in
// the anchor channel".
var errReminderSendOutsideAnchor = errors.New("reminder wake can only send to the anchor message surface")

type chatOutputTargetKind string

const (
	chatOutputTargetChannel chatOutputTargetKind = "channel"
	chatOutputTargetThread  chatOutputTargetKind = "thread"
	chatOutputTargetDM      chatOutputTargetKind = "dm"
)

// reminderAnchorSurface returns the hard-bound surface for a reminder wake:
// channel id + optional thread root (empty = main timeline). ok=false if not a
// reminder task with a channel.
func reminderAnchorSurface(task db.AgentInboxEvent) (channelID string, threadRootID string, ok bool) {
	if strings.TrimSpace(task.Reason) != "reminder" || !task.ChannelID.Valid {
		return "", "", false
	}
	channelID = uuidToString(task.ChannelID)
	if len(task.Context) > 0 {
		var wake channelWakeContext
		if err := json.Unmarshal(task.Context, &wake); err == nil && wake.Type == channelWakeContextType {
			threadRootID = strings.TrimSpace(wake.ThreadRootMessageID)
		}
	}
	return channelID, threadRootID, true
}

// enforceReminderAnchorSurface rejects sends that leave the msg-id surface.
// - Same channel required.
// - If anchor is a thread: target must be that thread (not main, not other thread).
// - If anchor is main timeline: target must be channel main (not a thread).
// - Cross-channel / unrelated DM: reject.
func enforceReminderAnchorSurface(task db.AgentInboxEvent, channelID string, kind chatOutputTargetKind, targetThreadRootID string) error {
	anchorChannelID, anchorThreadRoot, ok := reminderAnchorSurface(task)
	if !ok {
		return nil
	}
	channelID = strings.TrimSpace(channelID)
	if channelID == "" || channelID != anchorChannelID {
		return errReminderSendOutsideAnchor
	}
	targetThreadRootID = strings.TrimSpace(targetThreadRootID)
	if anchorThreadRoot != "" {
		// Thread-anchored: only that thread.
		if kind != chatOutputTargetThread || targetThreadRootID == "" || targetThreadRootID != anchorThreadRoot {
			return errReminderSendOutsideAnchor
		}
		return nil
	}
	// Main-timeline / DM-channel anchor: stay on that surface (no other thread).
	// chatOutputTargetDM is OK only when channelID already matches (DM materializes
	// to the same channel id as the anchor DM).
	if kind == chatOutputTargetThread {
		return errReminderSendOutsideAnchor
	}
	return nil
}

type chatOutputOrigin struct {
	channelID   pgtype.UUID
	workspaceID pgtype.UUID
	agentID     pgtype.UUID
}

type resolvedChatOutputTarget struct {
	kind          chatOutputTargetKind
	channel       ChannelResponse
	recipientType string
	recipientID   pgtype.UUID
	threadRoot    ChannelMessageResponse
}

func (h *Handler) validateChatOutputTarget(ctx context.Context, task db.AgentInboxEvent, rawTarget string) error {
	origin, ok := h.chatOutputOriginForTask(ctx, task)
	if !ok {
		return errChatOutputInvalidTarget
	}
	resolved, err := h.resolveChatOutputTarget(ctx, origin, rawTarget)
	if err != nil {
		return err
	}
	// Reminder: msg-id surface hard bind (Frank: 从哪来回哪去).
	threadRootID := ""
	if resolved.kind == chatOutputTargetThread {
		threadRootID = strings.TrimSpace(resolved.threadRoot.ID)
	}
	return enforceReminderAnchorSurface(task, resolved.channel.ID, resolved.kind, threadRootID)
}

func (h *Handler) chatOutputOriginForTask(ctx context.Context, task db.AgentInboxEvent) (chatOutputOrigin, bool) {
	// Prefer the chat-session mapping when present (mention/DM/reminder fires).
	if task.ChatSessionID.Valid {
		channelID, workspaceID, agentID, ok := h.channelAgentForChatSession(ctx, uuidToString(task.ChatSessionID))
		if ok {
			return chatOutputOrigin{channelID: channelID, workspaceID: workspaceID, agentID: agentID}, true
		}
	}
	// LRM-1055: ambient / channel_role_changed wakes bind a channel without a
	// chat session. Allow channel-anchored transport when the agent still has
	// direct surface membership on that channel.
	if task.ChannelID.Valid && task.WorkspaceID.Valid && task.AgentID.Valid &&
		h.agentHasSurfaceAccess(ctx, task.WorkspaceID, task.AgentID, task.ChannelID) {
		return chatOutputOrigin{
			channelID:   task.ChannelID,
			workspaceID: task.WorkspaceID,
			agentID:     task.AgentID,
		}, true
	}
	return chatOutputOrigin{}, false
}

func (h *Handler) resolveChatOutputTarget(ctx context.Context, origin chatOutputOrigin, rawTarget string) (resolvedChatOutputTarget, error) {
	target := strings.TrimSpace(rawTarget)
	if target == "" {
		ch, found := h.getChannel(ctx, uuidToString(origin.workspaceID), origin.channelID)
		if !found || ch.ArchivedAt != nil {
			return resolvedChatOutputTarget{}, errChatOutputInvalidTarget
		}
		// Default origin still requires current agent surface access (no stale
		// channel_agent_session after membership removal).
		if !h.agentHasSurfaceAccess(ctx, origin.workspaceID, origin.agentID, parseUUID(ch.ID)) {
			return resolvedChatOutputTarget{}, errChatOutputInvalidTarget
		}
		return resolvedChatOutputTarget{kind: chatOutputTargetChannel, channel: ch}, nil
	}
	if strings.HasPrefix(target, "dm:@") {
		handle, rawMessageID, hasMessageID := splitDMOutputTarget(strings.TrimPrefix(target, "dm:@"))
		recipientType, recipientID, err := h.resolveDMOutputTarget(ctx, origin, handle)
		if err != nil {
			return resolvedChatOutputTarget{}, err
		}
		if hasMessageID {
			ch, ok := h.agentDMChannel(ctx, origin.workspaceID, origin.agentID, recipientType, recipientID, pgtype.UUID{}, false)
			if !ok {
				return resolvedChatOutputTarget{}, errChatOutputInvalidTarget
			}
			rootID, err := h.resolveOutputThreadMessageID(ctx, origin.workspaceID, parseUUID(ch.ID), rawMessageID)
			if err != nil {
				return resolvedChatOutputTarget{}, errChatOutputInvalidTarget
			}
			root, err := h.loadChannelThreadRootForOutputTarget(ctx, origin.workspaceID, parseUUID(ch.ID), rootID)
			if err != nil {
				return resolvedChatOutputTarget{}, errChatOutputInvalidTarget
			}
			return resolvedChatOutputTarget{
				kind:          chatOutputTargetThread,
				channel:       ch,
				recipientType: recipientType,
				recipientID:   recipientID,
				threadRoot:    root,
			}, nil
		}
		return resolvedChatOutputTarget{kind: chatOutputTargetDM, recipientType: recipientType, recipientID: recipientID}, nil
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

func (h *Handler) resolveDMOutputTarget(ctx context.Context, origin chatOutputOrigin, handle string) (string, pgtype.UUID, error) {
	handle = strings.TrimSpace(handle)
	if handle == "" {
		return "", pgtype.UUID{}, errChatOutputInvalidTarget
	}

	rows, err := h.DB.Query(ctx, `
		SELECT actor_type, actor_id
		FROM (
		  SELECT 'user'::text AS actor_type, m.user_id AS actor_id, m.created_at
		  FROM member m
		  JOIN "user" u ON u.id = m.user_id
		  WHERE m.workspace_id = $1 AND lower(u.name) = lower($2)
		  UNION ALL
		  SELECT 'agent'::text AS actor_type, a.id AS actor_id, a.created_at
		  FROM agent a
		  WHERE a.workspace_id = $1
		    AND a.archived_at IS NULL
		    AND lower(a.name) = lower($2)
		) matches
		ORDER BY created_at ASC
		LIMIT 2`, origin.workspaceID, handle)
	if err != nil {
		return "", pgtype.UUID{}, errChatOutputInvalidTarget
	}
	defer rows.Close()
	type actorRef struct {
		typ string
		id  pgtype.UUID
	}
	var matches []actorRef
	for rows.Next() {
		var match actorRef
		if err := rows.Scan(&match.typ, &match.id); err != nil {
			return "", pgtype.UUID{}, errChatOutputInvalidTarget
		}
		matches = append(matches, match)
	}
	if rows.Err() != nil || len(matches) != 1 {
		return "", pgtype.UUID{}, errChatOutputInvalidTarget
	}
	match := matches[0]
	if match.typ == "agent" {
		if match.id == origin.agentID {
			return "", pgtype.UUID{}, errChatOutputInvalidTarget
		}
		return match.typ, match.id, nil
	}

	var userID pgtype.UUID
	err = h.DB.QueryRow(ctx, `
		SELECT m.user_id
		FROM member m
		JOIN "user" u ON u.id = m.user_id
		WHERE m.workspace_id = $1 AND m.user_id = $2`,
		origin.workspaceID, match.id).Scan(&userID)
	if err != nil {
		return "", pgtype.UUID{}, errChatOutputInvalidTarget
	}
	agent, err := h.Queries.GetAgentInWorkspace(ctx, db.GetAgentInWorkspaceParams{ID: origin.agentID, WorkspaceID: origin.workspaceID})
	if err != nil || agent.ArchivedAt.Valid {
		return "", pgtype.UUID{}, errChatOutputInvalidTarget
	}
	return "user", userID, nil
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
	// A direct channel_member match is the common case. Env-dispatch is a narrow
	// exception: derived execution agents are not channel_members (source agent
	// keeps the @alias). #801: source_agent fallback may ONLY target the current
	// inbox/origin channel — never other channels where the source is also a
	// member (that was a cross-channel borrow hole).
	targetID := parseUUID(ch.ID)
	if h.channelHasAgentMember(ctx, origin.workspaceID, targetID, origin.agentID) {
		// ok: direct member
	} else if origin.channelID.Valid && targetID == origin.channelID &&
		h.channelHasSourceAgentMember(ctx, origin.workspaceID, targetID, origin.agentID) {
		// ok: derived agent on this env-dispatch origin surface only
	} else {
		return resolvedChatOutputTarget{}, errChatOutputInvalidTarget
	}
	if !hasMessageID {
		return resolvedChatOutputTarget{kind: chatOutputTargetChannel, channel: ch}, nil
	}
	rootID, err := h.resolveOutputThreadMessageID(ctx, origin.workspaceID, parseUUID(ch.ID), rawMessageID)
	if err != nil {
		return resolvedChatOutputTarget{}, errChatOutputInvalidTarget
	}
	root, err := h.loadChannelThreadRootForOutputTarget(ctx, origin.workspaceID, parseUUID(ch.ID), rootID)
	if err != nil {
		return resolvedChatOutputTarget{}, errChatOutputInvalidTarget
	}
	return resolvedChatOutputTarget{kind: chatOutputTargetThread, channel: ch, threadRoot: root}, nil
}

func (h *Handler) resolveOutputThreadMessageID(ctx context.Context, workspaceID, channelID pgtype.UUID, raw string) (pgtype.UUID, error) {
	raw = strings.TrimSpace(raw)
	if id, err := util.ParseUUID(raw); err == nil {
		return id, nil
	}
	if !shortAgentMessageIDPattern.MatchString(raw) {
		return pgtype.UUID{}, errChatOutputInvalidTarget
	}
	rows, err := h.DB.Query(ctx, `
		SELECT id
		FROM channel_message
		WHERE workspace_id = $1
		  AND channel_id = $2
		  AND deleted_at IS NULL
		  AND LOWER(id::text) LIKE LOWER($3) || '%'
		ORDER BY created_at ASC
		LIMIT 2`, workspaceID, channelID, raw)
	if err != nil {
		return pgtype.UUID{}, err
	}
	defer rows.Close()
	var matches []pgtype.UUID
	for rows.Next() {
		var id pgtype.UUID
		if err := rows.Scan(&id); err != nil {
			return pgtype.UUID{}, err
		}
		matches = append(matches, id)
	}
	if err := rows.Err(); err != nil {
		return pgtype.UUID{}, err
	}
	if len(matches) != 1 {
		return pgtype.UUID{}, errChatOutputInvalidTarget
	}
	return matches[0], nil
}

func (h *Handler) groupChannelByName(ctx context.Context, workspaceID pgtype.UUID, name string) (ChannelResponse, error) {
	row := h.DB.QueryRow(ctx, `
		SELECT id, workspace_id, name, description, lark_chat_id, project_id, created_by, created_at, updated_at, kind, system_key, archived_at, archived_by, avatar_url
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

// channelHasSourceAgentMember reports whether agentID is an env-dispatch derived
// execution agent whose *source* agent is a member of the channel. Env-dispatch
// keeps the source agent as the stable channel_member alias (see
// ReplaceDispatchChannelMember) while the derived agent actually executes the
// run, so a derived agent replying with `#channel` would otherwise be rejected
// by channelHasAgentMember even though it legitimately owns the channel's
// env-dispatch task. Scope is deliberately narrow: only an agent that has a
// non-null source_agent_id whose source is a channel member is admitted, so
// ordinary (non-derived) agents gain no additional access.
func (h *Handler) channelHasSourceAgentMember(ctx context.Context, workspaceID, channelID, agentID pgtype.UUID) bool {
	var exists bool
	err := h.DB.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM agent a
			JOIN channel_member cm
			  ON cm.workspace_id = a.workspace_id
			 AND cm.channel_id = $2
			 AND cm.member_type = 'agent'
			 AND cm.member_id = a.source_agent_id
			WHERE a.id = $3 AND a.workspace_id = $1 AND a.source_agent_id IS NOT NULL
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
	// Agent-to-agent DMs must use the agent message transport. That is the
	// canonical write boundary where exchange and frequency accounting are
	// committed atomically with the visible message.
	if target.recipientType == "agent" {
		slog.Warn(
			"channel bridge: agent dm requires canonical transport",
			"chat_session_id", payload.ChatSessionID,
			"target", payload.Target,
		)
		return
	}
	if archived, found := h.channelIsArchived(ctx, uuidToString(origin.workspaceID), origin.channelID); !found || archived {
		return
	}
	if target.kind == chatOutputTargetDM {
		ch, ok := h.agentDMChannel(ctx, origin.workspaceID, origin.agentID, target.recipientType, target.recipientID, initiatorID, true)
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
