package handler

import (
	"context"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/memorygrowth"
)

// AgentMemoryGrowthResponse is the profile/card Memory growth block (LRM-303).
// Omitted from JSON when nil (zero valid writes).
type AgentMemoryGrowthResponse = memorygrowth.Snapshot

func (h *Handler) loadAgentMemoryGrowth(ctx context.Context, agentID pgtype.UUID) (*AgentMemoryGrowthResponse, error) {
	count, err := h.Queries.CountAgentMemoryWriteEventsByAgent(ctx, agentID)
	if err != nil {
		return nil, err
	}
	return memorygrowth.Compute(int(count), memorygrowth.DefaultBase, memorygrowth.DefaultRatio), nil
}
