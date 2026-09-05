package main

import (
	"context"
	"log/slog"
	"time"

	"github.com/multica-ai/multica/server/internal/handler"
)

// problemEvolutionReapInterval is well under the cancellation deadline so a
// stopping run is cancelled close to its deadline rather than a tick later.
const problemEvolutionReapInterval = 10 * time.Second

func runProblemEvolutionReaper(ctx context.Context, h *handler.Handler) {
	if h == nil {
		return
	}
	ticker := time.NewTicker(problemEvolutionReapInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			if err := h.ReapProblemEvolutionRuns(ctx, now); err != nil && ctx.Err() == nil {
				slog.Warn("problem evolution reaper failed", "error", err)
			}
		}
	}
}
