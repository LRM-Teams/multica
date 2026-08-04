package handler

import (
	"testing"
	"time"
)

func TestAgentTaskSnapshotCacheTTL(t *testing.T) {
	resetAgentTaskSnapshotCacheForTest()
	t.Cleanup(resetAgentTaskSnapshotCacheForTest)

	ws := "ws-snapshot-cache"
	tasks := []AgentTaskResponse{{ID: "t1", AgentID: "a1", Status: "running"}}
	putCachedAgentTaskSnapshot(ws, tasks)

	got, hit := getCachedAgentTaskSnapshot(ws)
	if !hit {
		t.Fatal("expected cache hit")
	}
	if len(got) != 1 || got[0].ID != "t1" {
		t.Fatalf("cached tasks = %+v", got)
	}
	// Mutating the returned slice must not corrupt the cache entry.
	got[0].ID = "mutated"
	got2, hit := getCachedAgentTaskSnapshot(ws)
	if !hit || got2[0].ID != "t1" {
		t.Fatalf("cache entry leaked mutation: %+v", got2)
	}

	agentTaskSnapshotCache.mu.Lock()
	entry := agentTaskSnapshotCache.entries[ws]
	entry.expiresAt = time.Now().Add(-time.Second)
	agentTaskSnapshotCache.entries[ws] = entry
	agentTaskSnapshotCache.mu.Unlock()

	if _, hit := getCachedAgentTaskSnapshot(ws); hit {
		t.Fatal("expected expired cache miss")
	}
}
