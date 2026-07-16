package main

import (
	"testing"

	"github.com/multica-ai/multica/server/internal/analytics"
	"github.com/multica-ai/multica/server/internal/events"
	"github.com/multica-ai/multica/server/internal/realtime"
)

func TestRouterWiresEphemeralSandboxManagerAtStartup(t *testing.T) {
	if testPool == nil {
		t.Skip("database not available")
	}
	_, h := NewRouterWithOptions(
		testPool,
		realtime.NewHub(),
		events.New(),
		analytics.NoopClient{},
		nil,
		RouterOptions{},
	)
	if h.TaskService == nil || h.TaskService.EphemeralSandboxManager == nil {
		t.Fatal("ephemeral sandbox manager was not wired at router startup")
	}
}
