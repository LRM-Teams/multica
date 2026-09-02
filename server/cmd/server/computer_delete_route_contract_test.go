package main

import (
	"net/http"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/multica-ai/multica/server/internal/analytics"
	"github.com/multica-ai/multica/server/internal/events"
	"github.com/multica-ai/multica/server/internal/realtime"
)

func TestComputerDeleteRoutesExposeOneOperation(t *testing.T) {
	router := NewRouter(nil, realtime.NewHub(), events.New(), analytics.NoopClient{}, nil)
	routes := make(map[string]bool)
	if err := chi.Walk(router, func(method string, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		routes[method+" "+route] = true
		return nil
	}); err != nil {
		t.Fatalf("walk router: %v", err)
	}

	for _, route := range []string{
		http.MethodGet + " /api/computers/{daemonId}/work-digest",
		http.MethodPatch + " /api/computers/{daemonId}/work-journal",
		http.MethodGet + " /api/computers/{daemonId}/collect-roots",
		http.MethodPatch + " /api/computers/{daemonId}/collect-roots",
		http.MethodDelete + " /api/computers/{daemonId}",
		http.MethodDelete + " /api/runtimes/by-daemon/{daemonId}",
	} {
		if !routes[route] {
			t.Fatalf("missing Computer delete route %s", route)
		}
	}

	for _, route := range []string{
		http.MethodPost + " /api/runtimes/by-daemon/{daemonId}/remove-agents",
		http.MethodDelete + " /api/computers/{daemonId}/workspace-connections/{workspaceId}",
		http.MethodDelete + " /api/daemons/{daemonId}/bindings/{workspaceId}",
	} {
		if routes[route] {
			t.Fatalf("retired alternate Computer removal route is still exposed: %s", route)
		}
	}
}
