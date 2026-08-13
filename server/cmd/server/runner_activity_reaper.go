package main

import (
	"context"
	"log/slog"
	"time"

	"github.com/multica-ai/multica/server/internal/handler"
)

// runnerActivityReapInterval keeps the five-second probe deadline bounded
// without making the normal activity hot path depend on a scheduler tick.
const runnerActivityReapInterval = time.Second

// runRunnerActivityReaper owns the server-side liveness arbitration for
// Workspace Runner activity. The Runner only publishes observations; the
// server alone decides when a stale working state needs a probe or a fenced
// disconnect projection.
func runRunnerActivityReaper(ctx context.Context, h *handler.Handler) {
	if h == nil {
		return
	}
	ticker := time.NewTicker(runnerActivityReapInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			if err := h.ReapStaleRunnerActivity(ctx, now); err != nil && ctx.Err() == nil {
				slog.Warn("runner Activity reaper failed", "error", err)
			}
			if err := h.ReapMixedRLQuiescence(ctx, now); err != nil && ctx.Err() == nil {
				slog.Warn("mixed-RL quiescence reaper failed", "error", err)
			}
		}
	}
}
