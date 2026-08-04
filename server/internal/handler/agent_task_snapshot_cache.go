package handler

import (
	"sync"
	"time"

	"golang.org/x/sync/singleflight"
)

// Short in-process TTL for GET /api/agent-task-snapshot (LRM-1261).
// Presence is patched by WS and FE already uses 30s staleTime; this only
// collapses concurrent/burst refetches against the same workspace so the
// LATERAL latest-outcome query is not paid N times in the same second.
const agentTaskSnapshotCacheTTL = 2 * time.Second

type agentTaskSnapshotCacheEntry struct {
	tasks     []AgentTaskResponse
	expiresAt time.Time
}

var agentTaskSnapshotCache = struct {
	mu      sync.Mutex
	entries map[string]agentTaskSnapshotCacheEntry
	group   singleflight.Group
}{
	entries: map[string]agentTaskSnapshotCacheEntry{},
}

func getCachedAgentTaskSnapshot(workspaceID string) ([]AgentTaskResponse, bool) {
	now := time.Now()
	agentTaskSnapshotCache.mu.Lock()
	defer agentTaskSnapshotCache.mu.Unlock()
	entry, ok := agentTaskSnapshotCache.entries[workspaceID]
	if !ok || now.After(entry.expiresAt) {
		if ok {
			delete(agentTaskSnapshotCache.entries, workspaceID)
		}
		return nil, false
	}
	return cloneAgentTaskSnapshot(entry.tasks), true
}

func putCachedAgentTaskSnapshot(workspaceID string, tasks []AgentTaskResponse) {
	agentTaskSnapshotCache.mu.Lock()
	defer agentTaskSnapshotCache.mu.Unlock()
	agentTaskSnapshotCache.entries[workspaceID] = agentTaskSnapshotCacheEntry{
		tasks:     cloneAgentTaskSnapshot(tasks),
		expiresAt: time.Now().Add(agentTaskSnapshotCacheTTL),
	}
}

func cloneAgentTaskSnapshot(tasks []AgentTaskResponse) []AgentTaskResponse {
	if tasks == nil {
		return nil
	}
	out := make([]AgentTaskResponse, len(tasks))
	copy(out, tasks)
	for i := range out {
		if out[i].Actor != nil {
			actor := *out[i].Actor
			out[i].Actor = &actor
		}
		if out[i].AvatarURL != nil {
			avatar := *out[i].AvatarURL
			out[i].AvatarURL = &avatar
		}
		if out[i].Handle != nil {
			handle := *out[i].Handle
			out[i].Handle = &handle
		}
		if out[i].Error != nil {
			errText := *out[i].Error
			out[i].Error = &errText
		}
		if out[i].TriggerSummary != nil {
			summary := *out[i].TriggerSummary
			out[i].TriggerSummary = &summary
		}
		if out[i].ParentTaskID != nil {
			parent := *out[i].ParentTaskID
			out[i].ParentTaskID = &parent
		}
		if out[i].TriggerCommentID != nil {
			trigger := *out[i].TriggerCommentID
			out[i].TriggerCommentID = &trigger
		}
		if out[i].DispatchedAt != nil {
			dispatched := *out[i].DispatchedAt
			out[i].DispatchedAt = &dispatched
		}
		if out[i].StartedAt != nil {
			started := *out[i].StartedAt
			out[i].StartedAt = &started
		}
		if out[i].CompletedAt != nil {
			completed := *out[i].CompletedAt
			out[i].CompletedAt = &completed
		}
	}
	return out
}

// resetAgentTaskSnapshotCacheForTest clears the in-process cache between tests.
func resetAgentTaskSnapshotCacheForTest() {
	agentTaskSnapshotCache.mu.Lock()
	defer agentTaskSnapshotCache.mu.Unlock()
	agentTaskSnapshotCache.entries = map[string]agentTaskSnapshotCacheEntry{}
}
