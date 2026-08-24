package main

import (
	"context"
	"log/slog"
	"time"

	"github.com/multica-ai/multica/server/internal/handler"
)

func runMixedRLQuiescenceReaper(ctx context.Context, h *handler.Handler) {
	if h == nil {
		return
	}
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			if err := h.ReapMixedRLQuiescence(ctx, now); err != nil && ctx.Err() == nil {
				slog.Warn("mixed-RL quiescence reaper failed", "error", err)
			}
		}
	}
}
