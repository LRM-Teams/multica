package main

import (
	"net/http"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/multica-ai/multica/server/internal/analytics"
	"github.com/multica-ai/multica/server/internal/events"
	"github.com/multica-ai/multica/server/internal/realtime"
)

// Spec §6/D6: the Dive Judge is the sole judge. The legacy Outcome Judge
// report endpoint is retired; the server-authoritative recall endpoint
// replaces it as the daemon's graph-memory entry point.
func TestGraphMemoryJudgeRouteRetired(t *testing.T) {
	router := NewRouter(nil, realtime.NewHub(), events.New(), analytics.NoopClient{}, nil)
	routes := make(map[string]bool)
	if err := chi.Walk(router, func(method string, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		routes[method+" "+route] = true
		return nil
	}); err != nil {
		t.Fatalf("walk router: %v", err)
	}

	if !routes[http.MethodPost+" /api/daemon/graph-memory/recalls"] {
		t.Fatal("missing daemon graph-memory recall route")
	}
	if routes[http.MethodPost+" /api/daemon/graph-memory/judge"] {
		t.Fatal("retired Outcome Judge route is still exposed: POST /api/daemon/graph-memory/judge")
	}
}
