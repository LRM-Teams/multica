package handler

import (
	"context"
	"log/slog"

	"github.com/jackc/pgx/v5/pgtype"
)

func (h *Handler) incrementNudgeLadder(
	ctx context.Context,
	workspaceID, channelID, agentID pgtype.UUID,
) {
	if _, err := h.DB.Exec(ctx, `
		INSERT INTO wendy_nudge_ladder (
		  workspace_id, channel_id, agent_id, nudge_count,
		  last_nudged_at, updated_at
		)
		VALUES ($1, $2, $3, 1, now(), now())
		ON CONFLICT (channel_id, agent_id) DO UPDATE SET
		  nudge_count = wendy_nudge_ladder.nudge_count + 1,
		  last_nudged_at = now(),
		  updated_at = now()
	`, workspaceID, channelID, agentID); err != nil {
		slog.Warn("increment nudge ladder failed",
			"channel_id", uuidToString(channelID),
			"agent_id", uuidToString(agentID),
			"error", err,
		)
	}
}

func (h *Handler) resetNudgeLadderForAgent(
	ctx context.Context,
	workspaceID, agentID pgtype.UUID,
) {
	if _, err := h.DB.Exec(ctx, `
		UPDATE wendy_nudge_ladder
		SET nudge_count = 0, last_progress_seen_at = now(), updated_at = now()
		WHERE workspace_id = $1 AND agent_id = $2 AND nudge_count > 0
	`, workspaceID, agentID); err != nil {
		slog.Warn("reset nudge ladder failed",
			"agent_id", uuidToString(agentID),
			"error", err,
		)
	}
}

func (h *Handler) channelNudgeLadder(
	ctx context.Context,
	channelID pgtype.UUID,
) map[string]int {
	out := map[string]int{}
	rows, err := h.DB.Query(ctx, `
		SELECT agent_id, nudge_count
		FROM wendy_nudge_ladder
		WHERE channel_id = $1 AND nudge_count > 0
	`, channelID)
	if err != nil {
		slog.Warn("load nudge ladder failed",
			"channel_id", uuidToString(channelID),
			"error", err,
		)
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var agentID pgtype.UUID
		var count int
		if err := rows.Scan(&agentID, &count); err != nil {
			return out
		}
		out[uuidToString(agentID)] = count
	}
	return out
}
