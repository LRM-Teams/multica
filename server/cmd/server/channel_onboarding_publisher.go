package main

import (
	"context"
	"log/slog"
	"time"

	"github.com/multica-ai/multica/server/internal/handler"
)

const channelOnboardingPublicationInterval = 250 * time.Millisecond

func runChannelOnboardingPublisher(ctx context.Context, h *handler.Handler) {
	ticker := time.NewTicker(channelOnboardingPublicationInterval)
	defer ticker.Stop()

	publish := func() {
		if _, err := h.PublishPendingChannelOnboardingSystemMessages(ctx, 100); err != nil && ctx.Err() == nil {
			slog.Warn("channel onboarding publication failed", "error", err)
		}
	}
	publish()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			publish()
		}
	}
}
