package handler

import (
	"context"
	"errors"
)

func (h *Handler) ProcessDueResearchRuns(ctx context.Context, limit int) (int, error) {
	if h == nil || h.ResearchRun == nil {
		return 0, errors.New("research run engine is unavailable")
	}
	return h.ResearchRun.ReconcileDue(ctx, limit)
}
