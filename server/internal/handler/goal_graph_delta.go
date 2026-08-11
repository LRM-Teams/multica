package handler

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// wakeGoalCoordinatorForGraphDelta turns a durable review verdict into a
// directed coordinator run. The prompt contains no producer conversation and
// the normal Goal hydration adds the current server-owned graph summary.
func (h *Handler) wakeGoalCoordinatorForGraphDelta(ctx context.Context, workspaceID, graphID, eventType string) {
	var channelID, coordinatorID pgtype.UUID
	err := h.DB.QueryRow(ctx, `
		SELECT goal.channel_id,graph.created_by_id
		FROM work_graph graph
		JOIN channel_goal goal ON graph.anchor_kind='channel_goal' AND goal.id=graph.anchor_id
		WHERE graph.workspace_id=$1::uuid AND graph.id=$2::uuid
		  AND graph.created_by_type='agent' AND graph.created_by_id IS NOT NULL
		  AND graph.status NOT IN('completed','cancelled')
	`, workspaceID, graphID).Scan(&channelID, &coordinatorID)
	if err != nil {
		slog.Warn("goal graph delta coordinator lookup failed", "graph_id", graphID, "event_type", eventType, "error", err)
		return
	}
	agent, err := h.Queries.GetAgent(ctx, coordinatorID)
	if err != nil || agent.ArchivedAt.Valid || !agent.RuntimeID.Valid {
		return
	}
	channel, ok := h.getChannel(ctx, workspaceID, channelID)
	if !ok || channel.ArchivedAt != nil {
		return
	}
	prompt := fmt.Sprintf("Goal Work Graph delta `%s` for graph `%s`. Reconcile the current server-owned graph state now: integrate accepted work, respond to rework or blockers, add only genuinely new tasks through an incremental revision, and close the Goal only when every current node is satisfied.", eventType, graphID)
	trigger := ChannelMessageResponse{
		ChannelID: channel.ID, WorkspaceID: workspaceID, Type: "system",
		Content: prompt, Source: protocol.AgentInboxReasonGoalGraphDelta,
	}
	if _, err = h.enqueueChannelAgentPrompt(ctx, channel, agent, trigger, agent.OwnerID, prompt, "goal graph delta", false, protocol.AgentInboxReasonGoalGraphDelta, channelDirectedWakePriority); err != nil {
		slog.Warn("goal graph delta coordinator wake failed", "graph_id", graphID, "event_type", eventType, "error", err)
	}
}
