package handler

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/radar"
	"github.com/multica-ai/multica/server/internal/service"
	"github.com/multica-ai/multica/server/internal/workgraph"
)

// IdleNudgeDebounce rate-limits idle nudges per channel so a stuck group is
// re-checked periodically without spamming. Tests may shorten it.
var IdleNudgeDebounce = 15 * time.Minute

// IdleProgressWindow is how far back "real progress" is looked for. If a group
// has no in-flight real work AND no progress event within this window, it counts
// as stalled — even if agents chatted (chat is not progress). Tests may shorten it.
var IdleProgressWindow = 15 * time.Minute

const idleNudgeDispatchLimit = int32(10)

const idleNudgeActiveTaskStatusList = "'queued','dispatched','running','waiting_local_directory'"

// DispatchIdleNudges wakes Beckham for every managed group that has STALLED —
// meaning no member is doing real work (an active issue-linked task) AND there
// has been no measurable progress recently (a completed issue task, an issue
// status change / task-completion in activity_log, or a new agent issue comment).
// A chat reply is NOT progress, so a group where agents only acknowledge without
// advancing an issue still counts as stalled and gets nudged. The rule: at least
// one agent should be doing real work unless the goal is genuinely complete;
// Beckham decides who to nudge (or @the product manager to decompose+assign), or
// stays silent only if everything is truly done.
func (h *Handler) DispatchIdleNudges(ctx context.Context, limit int32) (int, error) {
	if h.WorkGraph == nil || h.TaskService == nil || limit <= 0 {
		return 0, nil
	}
	if limit > idleNudgeDispatchLimit {
		limit = idleNudgeDispatchLimit
	}

	rows, err := h.DB.Query(ctx, `
		SELECT c.id, c.workspace_id, c.group_manager_agent_id, c.name
		FROM channel c
		JOIN agent mgr
		  ON mgr.id = c.group_manager_agent_id
		 AND mgr.workspace_id = c.workspace_id
		 AND mgr.archived_at IS NULL
		 AND mgr.runtime_id IS NOT NULL
		WHERE c.kind = 'group'
		  AND c.archived_at IS NULL
		  AND c.group_manager_agent_id IS NOT NULL
		  -- there is a team to nudge (at least one non-manager agent member)
		  AND EXISTS (
		    SELECT 1 FROM channel_member cm
		    JOIN agent a ON a.id = cm.member_id AND a.workspace_id = cm.workspace_id AND a.archived_at IS NULL
		    WHERE cm.channel_id = c.id AND cm.member_type = 'agent' AND cm.member_id <> c.group_manager_agent_id
		  )
		  -- no real work in flight: no ACTIVE issue-linked task for a non-manager
		  -- member (a chat reply has no issue_id, so chatting does not count).
		  AND NOT EXISTS (
		    SELECT 1 FROM channel_member cm
		    JOIN agent_task_queue t ON t.agent_id = cm.member_id
		    WHERE cm.channel_id = c.id AND cm.member_type = 'agent' AND cm.member_id <> c.group_manager_agent_id
		      AND t.issue_id IS NOT NULL
		      AND t.status IN (`+idleNudgeActiveTaskStatusList+`)
		  )
		  -- no recent PROGRESS: no completed issue task by a member in the window
		  AND NOT EXISTS (
		    SELECT 1 FROM channel_member cm
		    JOIN agent_task_queue t ON t.agent_id = cm.member_id
		    WHERE cm.channel_id = c.id AND cm.member_type = 'agent' AND cm.member_id <> c.group_manager_agent_id
		      AND t.issue_id IS NOT NULL AND t.status = 'completed'
		      AND t.completed_at > now() - make_interval(secs => $1)
		  )
		  -- ... and no issue activity (status change / task completed / created) by a member
		  AND NOT EXISTS (
		    SELECT 1 FROM channel_member cm
		    JOIN activity_log al ON al.actor_id = cm.member_id AND al.actor_type = 'agent'
		    WHERE cm.channel_id = c.id AND cm.member_type = 'agent' AND cm.member_id <> c.group_manager_agent_id
		      AND al.workspace_id = c.workspace_id
		      AND al.created_at > now() - make_interval(secs => $1)
		  )
		  -- ... and no new issue comment by a member
		  AND NOT EXISTS (
		    SELECT 1 FROM channel_member cm
		    JOIN comment cmt ON cmt.author_id = cm.member_id AND cmt.author_type = 'agent'
		    WHERE cm.channel_id = c.id AND cm.member_type = 'agent' AND cm.member_id <> c.group_manager_agent_id
		      AND cmt.workspace_id = c.workspace_id
		      AND cmt.created_at > now() - make_interval(secs => $1)
		  )
		  -- debounce: not idle-nudged for this channel recently
		  AND NOT EXISTS (
		    SELECT 1 FROM agent_radar_run r
		    WHERE r.workspace_id = c.workspace_id
		      AND r.agent_id = c.group_manager_agent_id
		      AND r.trigger_ref LIKE 'idle_nudge:' || c.id::text || ':%'
		      AND r.created_at > now() - make_interval(secs => $2)
		  )
		ORDER BY c.updated_at ASC
		LIMIT $3
	`, IdleProgressWindow.Seconds(), IdleNudgeDebounce.Seconds(), limit)
	if err != nil {
		return 0, fmt.Errorf("list idle-nudge candidates: %w", err)
	}
	type candidate struct {
		channelID, workspaceID, managerID pgtype.UUID
		name                              string
	}
	var candidates []candidate
	for rows.Next() {
		var c candidate
		if err := rows.Scan(&c.channelID, &c.workspaceID, &c.managerID, &c.name); err != nil {
			rows.Close()
			return 0, fmt.Errorf("scan idle-nudge candidate: %w", err)
		}
		candidates = append(candidates, c)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}

	nudged := 0
	for _, c := range candidates {
		channel, found := h.getChannel(ctx, uuidToString(c.workspaceID), c.channelID)
		if !found || channel.Kind != "group" || channel.ArchivedAt != nil {
			continue
		}
		watch := workgraph.ChannelAmbientWatch{WorkspaceID: c.workspaceID, ChannelID: c.channelID}
		markdown, err := h.buildWendyAmbientChannelMarkdown(ctx, watch, channel)
		if err != nil {
			slog.Warn("idle nudge: build markdown failed", "channel_id", uuidToString(c.channelID), "error", err)
			continue
		}
		prompt := radar.BuildIdleNudgeChannelPrompt(markdown)
		triggerRef := fmt.Sprintf("idle_nudge:%s:%d", uuidToString(c.channelID), time.Now().Unix())
		_, _, err = h.TaskService.EnqueueAgentRadarRun(ctx, service.EnqueueAgentRadarRunParams{
			WorkspaceID:    c.workspaceID,
			AgentID:        c.managerID,
			TriggerKind:    "event",
			TriggerRef:     triggerRef,
			CooldownKey:    "wendy_ambient:" + uuidToString(c.channelID),
			ContextSummary: "Beckham idle nudge for channel " + c.name,
			ScheduledFor:   time.Now().UTC(),
			Prompt:         prompt,
		})
		if err != nil {
			// A review is already active for this channel, or the manager is not
			// runnable right now — try again on a later sweep.
			if errors.Is(err, service.ErrAgentRadarRunActive) || errors.Is(err, service.ErrAgentRadarNotReady) {
				continue
			}
			slog.Warn("idle nudge: enqueue failed", "channel_id", uuidToString(c.channelID), "error", err)
			continue
		}
		nudged++
	}
	return nudged, nil
}
