package main

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/multica-ai/multica/server/internal/analytics"
	"github.com/multica-ai/multica/server/internal/events"
	"github.com/multica-ai/multica/server/internal/realtime"
)

func TestMainRouterDoesNotExposePrometheusMetrics(t *testing.T) {
	router := NewRouter(nil, realtime.NewHub(), events.New(), analytics.NoopClient{}, nil)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("main API /metrics status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestMainRouterHasChannelMessageEditRouteAndNoDeleteRoute(t *testing.T) {
	router := NewRouter(nil, realtime.NewHub(), events.New(), analytics.NoopClient{}, nil)
	const editRoute = "PATCH /api/channels/{channelId}/messages/{messageId}"
	const deleteRoute = "DELETE /api/channels/{channelId}/messages/{messageId}"
	editFound := false
	deleteFound := false
	if err := chi.Walk(router, func(method string, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		key := method + " " + route
		if key == editRoute {
			editFound = true
		}
		if key == deleteRoute {
			deleteFound = true
		}
		return nil
	}); err != nil {
		t.Fatalf("walk router: %v", err)
	}
	if !editFound {
		t.Fatalf("missing route %s", editRoute)
	}
	if deleteFound {
		t.Fatalf("unexpected route %s", deleteRoute)
	}
}

func TestMainRouterHasPublicVoiceCallProviderRoutes(t *testing.T) {
	router := NewRouter(nil, realtime.NewHub(), events.New(), analytics.NoopClient{}, nil)
	want := map[string]bool{
		http.MethodGet + " " + voiceCallCallbackPath:  false,
		http.MethodPost + " " + voiceCallCallbackPath: false,
		http.MethodPost + " " + voiceCallLLMPath:      false,
	}
	if err := chi.Walk(router, func(method string, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		key := method + " " + route
		if _, ok := want[key]; ok {
			want[key] = true
		}
		return nil
	}); err != nil {
		t.Fatalf("walk router: %v", err)
	}
	for route, found := range want {
		if !found {
			t.Fatalf("missing route %s", route)
		}
	}
}
