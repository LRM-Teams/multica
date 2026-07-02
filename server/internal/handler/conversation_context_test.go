package handler

import (
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
)

func TestBuildConversationSurfaceIsolationKeys(t *testing.T) {
	dm1 := buildConversationSurface("ws-1", "agent-1", parseUUID("00000000-0000-0000-0000-000000000001"), "", nil, "")
	dm2 := buildConversationSurface("ws-1", "agent-1", parseUUID("00000000-0000-0000-0000-000000000002"), "", nil, "")
	if dm1.Type != "dm" || dm1.SurfaceKey != "dm:00000000-0000-0000-0000-000000000001" {
		t.Fatalf("dm surface = %+v", dm1)
	}
	if buildAgentRunCacheKey("ws-1", "agent-1", dm1) == buildAgentRunCacheKey("ws-1", "agent-1", dm2) {
		t.Fatal("different DMs must not share an agent run cache key")
	}

	root := "00000000-0000-0000-0000-0000000000aa"
	thread := buildConversationSurface("ws-1", "agent-1", parseUUID("00000000-0000-0000-0000-000000000001"), "channel-1", &root, "")
	if thread.Type != "thread" || thread.SurfaceKey != "thread:"+root || thread.SessionID != root {
		t.Fatalf("thread surface = %+v", thread)
	}
	if buildAgentRunCacheKey("ws-1", "agent-1", dm1) == buildAgentRunCacheKey("ws-1", "agent-1", thread) {
		t.Fatal("DM and thread surfaces must not share an agent run cache key")
	}

	channel := buildConversationSurface("ws-1", "agent-1", parseUUID("00000000-0000-0000-0000-000000000001"), "channel-1", nil, "")
	if channel.Type != "channel" || channel.SurfaceKey != "channel:channel-1" || channel.SessionID != "00000000-0000-0000-0000-000000000001" {
		t.Fatalf("channel surface = %+v", channel)
	}
	if buildAgentRunCacheKey("ws-1", "agent-1", dm1) == buildAgentRunCacheKey("ws-1", "agent-1", channel) {
		t.Fatal("DM and channel surfaces must not share an agent run cache key")
	}

	issue := buildConversationSurface("ws-1", "agent-1", pgtype.UUID{}, "", nil, "MUL-123")
	if issue.Type != "issue" || issue.SurfaceKey != "issue:MUL-123" {
		t.Fatalf("issue surface = %+v", issue)
	}
}

func TestBuildAgentRunCacheKeyContainsSurfaceSessionAndVersion(t *testing.T) {
	surface := conversationSurface{Type: "thread", SurfaceKey: "thread:t-1", SessionID: "sess-1"}
	key := buildAgentRunCacheKey("ws-1", "agent-1", surface)
	newSession := surface
	newSession.SessionID = "sess-2"
	if key == buildAgentRunCacheKey("ws-1", "agent-1", newSession) {
		t.Fatal("same surface with a new session must get a different cache key")
	}
	for _, want := range []string{"agent-run", "ws-1", "agent-1", "thread", "thread:t-1", "sess-1", conversationContextVersion} {
		if !strings.Contains(key, want) {
			t.Fatalf("cache key %q missing %q", key, want)
		}
	}
	if key == "agent-run:agent-1" {
		t.Fatal("cache key must not collapse to agent id only")
	}
}
