package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/multica-ai/multica/server/internal/service"
)

func TestGetGraphMemoryChannelLineage(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("test database unavailable")
	}
	ctx := context.Background()
	workspaceID := createGraphMemoryTestWorkspace(t)
	mustGraphMemoryMember(t, workspaceID, "member")
	channelID := createGraphMemoryTestChannel(t, workspaceID)
	// No route yet: stable empty answer, not an error.
	req := withRouteParams(newRequest(http.MethodGet,
		"/api/workspaces/"+workspaceID.String()+"/graph-memory/channels/"+channelID.String()+"/lineage", nil),
		"id", workspaceID.String(), "channelId", channelID.String())
	rec := httptest.NewRecorder()
	testHandler.GetGraphMemoryChannelLineage(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"lineage":[]`) {
		t.Fatalf("empty lineage: status=%d body=%s", rec.Code, rec.Body.String())
	}
	// After resolution the current route and generation appear.
	if _, err := service.ResolveChannelRoute(ctx, testPool, workspaceID.String(), channelID.String()); err != nil {
		t.Fatal(err)
	}
	rec = httptest.NewRecorder()
	testHandler.GetGraphMemoryChannelLineage(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"routing_mode":"standalone"`) {
		t.Fatalf("resolved lineage: status=%d body=%s", rec.Code, rec.Body.String())
	}
}
