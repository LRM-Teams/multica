package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func insertTestMemoryWriteEvents(t *testing.T, agentID string, count int) {
	t.Helper()
	if testHandler == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	agentUUID := parseUUID(agentID)
	wsUUID := parseUUID(testWorkspaceID)
	for i := 0; i < count; i++ {
		if _, err := testHandler.Queries.InsertAgentMemoryWriteEvent(ctx, db.InsertAgentMemoryWriteEventParams{
			WorkspaceID: wsUUID,
			AgentID:     agentUUID,
			RelPath:     "memory/MEMORY.md",
			ScopeType:   "agent_global",
			FileKey:     "MEMORY",
			ContentHash: "hash-" + string(rune('a'+i)),
			DeltaChars:  10,
		}); err != nil {
			t.Fatalf("insert memory write event %d: %v", i, err)
		}
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(ctx, `DELETE FROM agent_memory_write_event WHERE agent_id = $1`, agentUUID)
	})
}

func TestGetAgent_MemoryGrowthZeroWritesOmitted(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	agentID := createHandlerTestAgent(t, "memory-growth-zero", []byte(`{}`))

	w := httptest.NewRecorder()
	req := withRouteParams(
		newRequest(http.MethodGet, "/api/agents/"+agentID, nil),
		"id", agentID,
	)
	testHandler.GetAgent(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GetAgent: status=%d body=%s", w.Code, w.Body.String())
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(w.Body.Bytes(), &raw); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if _, ok := raw["memory_growth"]; ok {
		t.Fatalf("expected memory_growth omitted for zero writes, body=%s", w.Body.String())
	}
}

func TestGetAgent_MemoryGrowthSilverProgress(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	agentID := createHandlerTestAgent(t, "memory-growth-silver", []byte(`{}`))
	insertTestMemoryWriteEvents(t, agentID, 5)

	w := httptest.NewRecorder()
	req := withRouteParams(
		newRequest(http.MethodGet, "/api/agents/"+agentID, nil),
		"id", agentID,
	)
	testHandler.GetAgent(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GetAgent: status=%d body=%s", w.Code, w.Body.String())
	}

	var resp AgentResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.MemoryGrowth == nil {
		t.Fatal("expected memory_growth payload")
	}
	if resp.MemoryGrowth.TotalWrites != 5 || resp.MemoryGrowth.Tier != "silver" {
		t.Fatalf("growth=%#v", resp.MemoryGrowth)
	}
	if resp.MemoryGrowth.Next == nil || resp.MemoryGrowth.Next.Tier != "gold" || resp.MemoryGrowth.Next.Current != 5 || resp.MemoryGrowth.Next.Required != 6 {
		t.Fatalf("next=%#v", resp.MemoryGrowth.Next)
	}
	if len(resp.MemoryGrowth.Segments) != 4 {
		t.Fatalf("segments=%d", len(resp.MemoryGrowth.Segments))
	}
}

func TestGetMemberProfile_MemoryGrowthOnFullAccess(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	agentID := createHandlerTestAgent(t, "memory-growth-profile", []byte(`{}`))
	insertTestMemoryWriteEvents(t, agentID, 3)

	w := httptest.NewRecorder()
	req := withRouteParams(
		newRequest(http.MethodGet, "/api/member-profiles/agent/"+agentID, nil),
		"memberType", "agent",
		"memberId", agentID,
	)
	testHandler.GetMemberProfile(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GetMemberProfile: status=%d body=%s", w.Code, w.Body.String())
	}

	var profile MemberProfileResponse
	if err := json.NewDecoder(w.Body).Decode(&profile); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if profile.MemoryGrowth == nil || profile.MemoryGrowth.Tier != "silver" || profile.MemoryGrowth.TotalWrites != 3 {
		t.Fatalf("memory_growth=%#v", profile.MemoryGrowth)
	}
}

func TestGetMemberProfile_MemoryGrowthOmittedForIdentityOnly(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	agentID, _, memberID := privateAgentTestFixture(t)
	insertTestMemoryWriteEvents(t, agentID, 4)

	w := httptest.NewRecorder()
	req := withRouteParams(
		newRequestAs(memberID, http.MethodGet, "/api/member-profiles/agent/"+agentID, nil),
		"memberType", "agent",
		"memberId", agentID,
	)
	testHandler.GetMemberProfile(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GetMemberProfile: status=%d body=%s", w.Code, w.Body.String())
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(w.Body.Bytes(), &raw); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if _, ok := raw["memory_growth"]; ok {
		t.Fatalf("identity_only profile must not expose memory_growth, body=%s", w.Body.String())
	}
}

func TestLoadAgentMemoryGrowth_CountsPersistedEvents(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	agentID := createHandlerTestAgent(t, "memory-growth-count", []byte(`{}`))
	insertTestMemoryWriteEvents(t, agentID, 2)

	growth, err := testHandler.loadAgentMemoryGrowth(context.Background(), parseUUID(agentID))
	if err != nil {
		t.Fatalf("loadAgentMemoryGrowth: %v", err)
	}
	if growth == nil || growth.TotalWrites != 2 || growth.Tier != "bronze" {
		t.Fatalf("growth=%#v", growth)
	}
}
