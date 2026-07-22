package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/internal/service"
)

func TestGetMemberProfile_AgentMemoryGrowth(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	agentID := createHandlerTestAgent(t, "memory-growth-"+uuid.NewString()[:8], []byte(`{}`))
	t.Cleanup(func() {
		testPool.Exec(ctx, `DELETE FROM agent_memory_write_event WHERE agent_id = $1`, agentID)
	})

	w := httptest.NewRecorder()
	req := withRouteParams(
		newRequest(http.MethodGet, "/api/member-profiles/agent/"+agentID, nil),
		"memberType", "agent",
		"memberId", agentID,
	)
	testHandler.GetMemberProfile(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GetMemberProfile zero writes: status=%d body=%s", w.Code, w.Body.String())
	}
	var emptyProfile MemberProfileResponse
	if err := json.Unmarshal(w.Body.Bytes(), &emptyProfile); err != nil {
		t.Fatalf("decode profile: %v", err)
	}
	if emptyProfile.MemoryGrowth != nil {
		t.Fatalf("memory_growth = %#v, want nil for zero writes", emptyProfile.MemoryGrowth)
	}

	insertAgentMemoryWriteEvent(t, agentID, 5)

	w = httptest.NewRecorder()
	testHandler.GetMemberProfile(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GetMemberProfile with writes: status=%d body=%s", w.Code, w.Body.String())
	}
	var profile MemberProfileResponse
	if err := json.Unmarshal(w.Body.Bytes(), &profile); err != nil {
		t.Fatalf("decode profile: %v", err)
	}
	if profile.MemoryGrowth == nil {
		t.Fatalf("memory_growth missing (body=%s)", w.Body.String())
	}
	if profile.MemoryGrowth.TotalWrites != 5 {
		t.Fatalf("total_writes = %d, want 5", profile.MemoryGrowth.TotalWrites)
	}
	if profile.MemoryGrowth.Tier != service.MemoryGrowthTierSilver {
		t.Fatalf("tier = %q, want silver", profile.MemoryGrowth.Tier)
	}
	if profile.MemoryGrowth.ProgressTarget != 6 {
		t.Fatalf("progress_target = %d, want 6", profile.MemoryGrowth.ProgressTarget)
	}
	if len(profile.MemoryGrowth.Segments) != 4 {
		t.Fatalf("segments = %#v, want 4 items", profile.MemoryGrowth.Segments)
	}
}

func insertAgentMemoryWriteEvent(t *testing.T, agentID string, count int) {
	t.Helper()
	ctx := context.Background()
	agentUUID := parseUUID(agentID)
	for i := 0; i < count; i++ {
		if _, err := testPool.Exec(ctx, `
			INSERT INTO agent_memory_write_event (
				workspace_id, agent_id, rel_path, scope_type, file_key, content_hash, delta_chars
			) VALUES ($1, $2, $3, 'agent_global', 'MEMORY.md', $4, 10)
		`, parseUUID(testWorkspaceID), agentUUID, "memory/MEMORY.md", uuid.NewString()); err != nil {
			t.Fatalf("insert memory write event %d: %v", i, err)
		}
	}
}

func TestLoadAgentMemoryGrowth_CountsEvents(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	agentID := createHandlerTestAgent(t, "memory-growth-load-"+uuid.NewString()[:8], []byte(`{}`))
	t.Cleanup(func() {
		testPool.Exec(ctx, `DELETE FROM agent_memory_write_event WHERE agent_id = $1`, agentID)
	})
	insertAgentMemoryWriteEvent(t, agentID, 2)

	got, err := testHandler.loadAgentMemoryGrowth(ctx, parseUUID(agentID))
	if err != nil {
		t.Fatalf("loadAgentMemoryGrowth: %v", err)
	}
	if got == nil || got.TotalWrites != 2 || got.Tier != service.MemoryGrowthTierBronze {
		t.Fatalf("growth = %#v, want bronze at 2 writes", got)
	}
}
