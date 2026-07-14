package handler

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/radar"
	"github.com/multica-ai/multica/server/internal/service"
	"github.com/multica-ai/multica/server/internal/util"
	"github.com/multica-ai/multica/server/internal/workgraph"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

const wendyAmbientDispatchLimit = int32(5)

func (h *Handler) DispatchDueWendyAmbientReviews(ctx context.Context, limit int32) (int, error) {
	if h.WorkGraph == nil || h.TaskService == nil || limit <= 0 {
		return 0, nil
	}
	if limit > wendyAmbientDispatchLimit {
		limit = wendyAmbientDispatchLimit
	}
	// Re-arm reviews whose run never completed (daemon gone, crash) so a review
	// is not lost forever (#2).
	if reclaimed, err := h.WorkGraph.ReclaimStaleChannelAmbient(ctx); err != nil {
		slog.Warn("Wendy ambient stale reclaim failed", "error", err)
	} else if reclaimed > 0 {
		slog.Info("Wendy ambient reclaimed stale reviews", "count", reclaimed)
	}
	watches, claimToken, err := h.WorkGraph.ClaimDueChannelAmbient(ctx, limit)
	if err != nil {
		return 0, err
	}
	enqueued := 0
	for _, watch := range watches {
		ok, err := h.dispatchClaimedWendyAmbient(ctx, watch, claimToken)
		if err != nil {
			slog.Warn("Wendy ambient dispatch failed",
				"channel_id", uuidToString(watch.ChannelID),
				"error", err,
			)
			_ = h.WorkGraph.ReturnChannelAmbientForRetry(ctx, watch.ChannelID, claimToken)
			continue
		}
		if ok {
			enqueued++
		}
	}
	return enqueued, nil
}

func (h *Handler) dispatchClaimedWendyAmbient(ctx context.Context, watch workgraph.ChannelAmbientWatch, claimToken pgtype.UUID) (bool, error) {
	channel, found := h.getChannel(ctx, uuidToString(watch.WorkspaceID), watch.ChannelID)
	if !found || channel.Kind != "group" || channel.ArchivedAt != nil {
		return false, h.WorkGraph.CancelChannelAmbientClaim(ctx, watch.ChannelID, claimToken, "channel unavailable")
	}
	if !h.channelHasAgentMember(ctx, watch.WorkspaceID, watch.ChannelID, watch.WendyAgentID) {
		return false, h.WorkGraph.CancelChannelAmbientClaim(ctx, watch.ChannelID, claimToken, "Wendy left channel")
	}
	supervisor, err := h.Queries.GetAgentInWorkspace(ctx, db.GetAgentInWorkspaceParams{
		ID:          watch.WendyAgentID,
		WorkspaceID: watch.WorkspaceID,
	})
	if err != nil || supervisor.ArchivedAt.Valid || !supervisor.RuntimeID.Valid {
		return false, h.WorkGraph.CancelChannelAmbientClaim(ctx, watch.ChannelID, claimToken, "Wendy unavailable")
	}

	markdown, err := h.buildWendyAmbientChannelMarkdown(ctx, watch, channel)
	if err != nil {
		return false, err
	}
	prompt := radar.BuildAmbientChannelPrompt(markdown)
	triggerRef := fmt.Sprintf("ambient:%s:%d", uuidToString(watch.ChannelID), watch.LastHumanMessageAt.Unix())
	run, _, err := h.TaskService.EnqueueAgentRadarRun(ctx, service.EnqueueAgentRadarRunParams{
		WorkspaceID:    watch.WorkspaceID,
		AgentID:        watch.WendyAgentID,
		TriggerKind:    "event",
		TriggerRef:     triggerRef,
		CooldownKey:    "wendy_ambient:" + uuidToString(watch.ChannelID),
		ContextSummary: "Wendy ambient review for channel " + channel.Name,
		ScheduledFor:   time.Now().UTC(),
		Prompt:         prompt,
	})
	if err != nil {
		if errors.Is(err, service.ErrAgentRadarRunActive) || errors.Is(err, service.ErrAgentRadarNotReady) {
			return false, h.WorkGraph.ReturnChannelAmbientForRetry(ctx, watch.ChannelID, claimToken)
		}
		return false, err
	}
	if err := h.WorkGraph.MarkChannelAmbientRunning(ctx, watch.ChannelID, claimToken, watch.LastHumanMessageAt, run.ID); err != nil {
		return false, err
	}
	return true, nil
}

func (h *Handler) buildWendyAmbientChannelMarkdown(ctx context.Context, watch workgraph.ChannelAmbientWatch, channel ChannelResponse) (string, error) {
	var b strings.Builder
	fmt.Fprintf(&b, "## Channel\n\n- channel_id=%s\n- name=%s\n\n", channel.ID, channel.Name)

	b.WriteString("## Open Work Nodes In Channel\n\n")
	rows, err := h.DB.Query(ctx, `
		SELECT id, title, status, owner_type, owner_id, linked_issue_id
		FROM work_node
		WHERE workspace_id = $1
		  AND primary_channel_id = $2
		  AND status NOT IN ('done', 'cancelled')
		ORDER BY updated_at DESC
		LIMIT 30
	`, watch.WorkspaceID, watch.ChannelID)
	if err != nil {
		return "", fmt.Errorf("list ambient work nodes: %w", err)
	}
	nodeCount := 0
	for rows.Next() {
		var id, ownerID, issueID pgtype.UUID
		var title, status, ownerType string
		if err := rows.Scan(&id, &title, &status, &ownerType, &ownerID, &issueID); err != nil {
			rows.Close()
			return "", err
		}
		fmt.Fprintf(&b, "- work_node_id=%s title=%q status=%s owner_type=%s owner_id=%s linked_issue_id=%s\n",
			uuidToString(id), title, status, ownerType, uuidToString(ownerID), uuidToString(issueID))
		nodeCount++
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return "", err
	}
	if nodeCount == 0 {
		b.WriteString("- (none)\n")
	}

	b.WriteString("\n## Channel Agents\n\n")
	agentRows, err := h.DB.Query(ctx, `
		SELECT a.id, a.name
		FROM channel_member cm
		JOIN agent a ON a.id = cm.member_id AND a.workspace_id = cm.workspace_id
		WHERE cm.workspace_id = $1
		  AND cm.channel_id = $2
		  AND cm.member_type = 'agent'
		  AND a.archived_at IS NULL
		ORDER BY a.name ASC
		LIMIT 50
	`, watch.WorkspaceID, watch.ChannelID)
	if err != nil {
		return "", fmt.Errorf("list ambient channel agents: %w", err)
	}
	for agentRows.Next() {
		var id pgtype.UUID
		var name string
		if err := agentRows.Scan(&id, &name); err != nil {
			agentRows.Close()
			return "", err
		}
		fmt.Fprintf(&b, "- agent_id=%s name=%q\n", uuidToString(id), name)
	}
	agentRows.Close()
	if err := agentRows.Err(); err != nil {
		return "", err
	}

	b.WriteString("\n## Open Workspace Issues\n\n")
	// #4: issue-backed work_nodes carry no primary_channel_id, so the
	// channel-scoped section above never surfaces them. Give the review the
	// workspace's open issues (with assignee) so it can spot missing tracking,
	// stalled owners, or who should start/stop — instead of judging on chat text
	// alone. Best-effort: a transient error must not sink the whole review.
	issueRows, err := h.DB.Query(ctx, `
		SELECT i.number, i.title, i.status, COALESCE(i.assignee_type, '') AS assignee_type, i.assignee_id,
		       COALESCE(a.name, '') AS agent_name
		FROM issue i
		LEFT JOIN agent a
		  ON a.id = i.assignee_id
		 AND i.assignee_type = 'agent'
		WHERE i.workspace_id = $1
		  AND i.status NOT IN ('done', 'cancelled', 'closed')
		ORDER BY i.updated_at DESC
		LIMIT 30
	`, watch.WorkspaceID)
	if err != nil {
		b.WriteString("- (issues unavailable)\n")
		slog.Warn("ambient markdown: list open issues failed", "workspace_id", uuidToString(watch.WorkspaceID), "error", err)
	} else {
		issueCount := 0
		for issueRows.Next() {
			var number int64
			var assigneeID pgtype.UUID
			var title, status, assigneeType, agentName string
			if err := issueRows.Scan(&number, &title, &status, &assigneeType, &assigneeID, &agentName); err != nil {
				issueRows.Close()
				return "", err
			}
			assignee := "unassigned"
			if assigneeType == "agent" && agentName != "" {
				assignee = "agent:" + agentName
			} else if assigneeType != "" && assigneeID.Valid {
				assignee = assigneeType + ":" + uuidToString(assigneeID)
			}
			fmt.Fprintf(&b, "- issue #%d title=%q status=%s assignee=%s\n", number, trimAmbientContent(title), status, assignee)
			issueCount++
		}
		issueRows.Close()
		if err := issueRows.Err(); err != nil {
			return "", err
		}
		if issueCount == 0 {
			b.WriteString("- (none)\n")
		}
	}

	b.WriteString("\n## Recent Group Messages\n\n")
	msgRows, err := h.DB.Query(ctx, `
		SELECT id, author_type, author_id, author_name, content, created_at
		FROM channel_message
		WHERE workspace_id = $1 AND channel_id = $2
		ORDER BY created_at DESC
		LIMIT 40
	`, watch.WorkspaceID, watch.ChannelID)
	if err != nil {
		return "", fmt.Errorf("list ambient channel messages: %w", err)
	}
	type ambientMsg struct {
		id, authorID                    pgtype.UUID
		authorType, authorName, content string
		createdAt                       time.Time
	}
	var messages []ambientMsg
	for msgRows.Next() {
		var msg ambientMsg
		if err := msgRows.Scan(&msg.id, &msg.authorType, &msg.authorID, &msg.authorName, &msg.content, &msg.createdAt); err != nil {
			msgRows.Close()
			return "", err
		}
		messages = append(messages, msg)
	}
	msgRows.Close()
	if err := msgRows.Err(); err != nil {
		return "", err
	}
	for i := len(messages) - 1; i >= 0; i-- {
		msg := messages[i]
		fmt.Fprintf(&b, "- message_id=%s at=%s author_type=%s author_id=%s author_name=%q content=%q\n",
			uuidToString(msg.id), msg.createdAt.UTC().Format(time.RFC3339), msg.authorType, uuidToString(msg.authorID), msg.authorName, trimAmbientContent(msg.content))
	}
	if len(messages) == 0 {
		b.WriteString("- (none)\n")
	}
	return b.String(), nil
}

func trimAmbientContent(content string) string {
	content = strings.TrimSpace(content)
	runes := []rune(content)
	if len(runes) > 400 {
		return string(runes[:400]) + "…"
	}
	return content
}

func (h *Handler) touchWendyChannelAmbient(ctx context.Context, ch ChannelResponse, msg ChannelMessageResponse) {
	if h.WorkGraph == nil || ch.Kind != "group" {
		return
	}
	wendyID, ok := h.resolveWendyAmbientAgentForChannel(ctx, parseUUID(ch.WorkspaceID), parseUUID(ch.ID))
	if !ok {
		return
	}
	messageAt := time.Now()
	if msg.CreatedAt != "" {
		if parsed, parseErr := time.Parse(time.RFC3339, msg.CreatedAt); parseErr == nil {
			messageAt = parsed
		} else if parsed, parseErr := time.Parse(time.RFC3339Nano, msg.CreatedAt); parseErr == nil {
			messageAt = parsed
		}
	}
	var messageID pgtype.UUID
	if msg.ID != "" {
		if parsed, parseErr := util.ParseUUID(msg.ID); parseErr == nil {
			messageID = parsed
		}
	}
	if err := h.WorkGraph.TouchChannelAmbient(ctx, parseUUID(ch.WorkspaceID), parseUUID(ch.ID), wendyID, messageID, messageAt); err != nil {
		slog.Warn("touch Wendy channel ambient failed", "channel_id", ch.ID, "message_id", msg.ID, "error", err)
	}
}

// resolveWendyAmbientAgentForChannel picks which Wendy watches this group.
// Prefer the workspace supervisor (the canonical signal from
// workspace_radar_state) when she is a member; otherwise fall back to a named
// Wendy already in the channel (personal Wendy clones in multi-owner
// workspaces). The fallback is deterministic — prefer a runtime-ready,
// non-archived agent with a stable (created_at, id) tie-break — so a channel
// with several clones always resolves to the same watcher instead of racing on
// insert order. Name matching remains a transitional signal until a managed
// group-manager agent role lands.
func (h *Handler) resolveWendyAmbientAgentForChannel(ctx context.Context, workspaceID, channelID pgtype.UUID) (pgtype.UUID, bool) {
	if supervisorID, err := h.Queries.GetWorkspaceSupervisorAgentID(ctx, workspaceID); err == nil {
		if h.channelHasAgentMember(ctx, workspaceID, channelID, supervisorID) {
			return supervisorID, true
		}
	}
	rows, err := h.DB.Query(ctx, `
		SELECT a.id, COALESCE(NULLIF(a.display_name, ''), a.name) AS display_name
		FROM channel_member cm
		JOIN agent a
		  ON a.id = cm.member_id
		 AND a.workspace_id = cm.workspace_id
		WHERE cm.workspace_id = $1
		  AND cm.channel_id = $2
		  AND cm.member_type = 'agent'
		  AND a.archived_at IS NULL
		ORDER BY (a.runtime_id IS NOT NULL) DESC, a.created_at ASC, a.id ASC
	`, workspaceID, channelID)
	if err != nil {
		return pgtype.UUID{}, false
	}
	defer rows.Close()
	for rows.Next() {
		var agentID pgtype.UUID
		var displayName string
		if err := rows.Scan(&agentID, &displayName); err != nil {
			return pgtype.UUID{}, false
		}
		if isWindyAgentName(displayName) {
			return agentID, true
		}
	}
	return pgtype.UUID{}, false
}
