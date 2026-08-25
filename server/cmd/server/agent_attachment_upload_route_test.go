package main

import (
	"net/http"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/multica-ai/multica/server/internal/analytics"
	"github.com/multica-ai/multica/server/internal/events"
	"github.com/multica-ai/multica/server/internal/realtime"
)

func TestAgentAttachmentUploadObjectRouteOnlyMatchesPut(t *testing.T) {
	router := NewRouter(nil, realtime.NewHub(), events.New(), analytics.NoopClient{}, nil)
	const path = "/api/agent/attachment-upload-sessions/00000000-0000-0000-0000-000000000000/object"

	for _, test := range []struct {
		method string
		want   bool
	}{
		{method: http.MethodPut, want: true},
		{method: http.MethodPost, want: false},
		{method: http.MethodHead, want: false},
		{method: http.MethodOptions, want: false},
	} {
		t.Run(test.method, func(t *testing.T) {
			routeContext := chi.NewRouteContext()
			if got := router.Match(routeContext, test.method, path); got != test.want {
				t.Fatalf("router.Match(%q, %q) = %v, want %v", test.method, path, got, test.want)
			}
		})
	}
}
