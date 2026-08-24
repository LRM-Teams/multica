package main

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/multica-ai/multica/server/internal/daemonws"
	"github.com/multica-ai/multica/server/internal/handler"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// runnerPresenceRefreshInterval is how often the server refreshes Redis
// liveness and DB last_seen_at for currently-connected Workspace Runners.
//
// It participates in a three-constant freshness chain that MUST keep its
// ordering: runnerPresenceRefreshInterval (45s) < runtimeLivenessTTL (90s,
// handler/daemon.go) < staleThresholdSeconds (150s, runtime_sweeper.go).
// The tick must be below the Redis TTL so a connected daemon never expires
// its liveness record, and the TTL must stay below the sweeper's stale
// window so a disconnected daemon degrades to offline at the normal cadence.
// If you tune any of the three, recompute the whole chain.
const runnerPresenceRefreshInterval = 45 * time.Second

// runnerIDCacheTTL prunes daemon/workspace -> runtimeID entries that have not
// been refreshed (i.e. whose socket is no longer being listed) for this long,
// so a disconnected daemon's cache entry does not linger in memory forever.
// Multiple of the refresh interval; a few minutes is plenty.
const runnerIDCacheTTL = 5 * time.Minute

// runRunnerPresenceLivenessTicker implements LRM-1571's "liveness driven by
// WS connection state": while a Workspace Runner socket is connected, the
// server keeps the runtime's Redis liveness record and DB last_seen_at fresh
// on the daemon's behalf — new daemons stop sending heartbeat frames, and the
// sweeper / curation / UI consumers all keep working unchanged. When the
// socket drops, refreshes stop and the existing TTL + stale-window machinery
// degrades the runtime to offline at the normal cadence.
//
// Legacy daemons that still send heartbeat frames are unaffected: their own
// writes win on the hot path; this ticker only touches rows already online.
func runRunnerPresenceLivenessTicker(ctx context.Context, queries *db.Queries, liveness handler.LivenessStore, hub *daemonws.Hub) {
	if hub == nil {
		return
	}
	ticker := time.NewTicker(runnerPresenceRefreshInterval)
	defer ticker.Stop()
	cache := newRunnerIDCache()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			refreshRunnerPresenceLiveness(ctx, queries, liveness, hub, cache)
		}
	}
}

// runnerIDCache remembers daemon/workspace -> runtimeID so the per-tick
// refresh does not re-query for every connected runner when nothing changed.
// Keys are "daemonID|workspaceID". Entries carry the last refresh time and
// are pruned once they stop being refreshed (socket disconnected), keeping
// the map bounded to currently-seen runners.
type runnerIDCache struct {
	mu sync.Mutex
	m  map[string]runnerIDEntry
}

type runnerIDEntry struct {
	runtimeID   string
	refreshedAt time.Time
}

func newRunnerIDCache() *runnerIDCache {
	return &runnerIDCache{m: make(map[string]runnerIDEntry)}
}

func (c *runnerIDCache) get(key string) (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.m[key]
	if !ok {
		return "", false
	}
	return e.runtimeID, true
}

func (c *runnerIDCache) set(key, runtimeID string, now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.m[key] = runnerIDEntry{runtimeID: runtimeID, refreshedAt: now}
}

// prune drops entries not refreshed within ttl. Call it every tick so a
// daemon whose socket disconnected stops occupying cache memory.
func (c *runnerIDCache) prune(now time.Time, ttl time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	cutoff := now.Add(-ttl)
	for k, e := range c.m {
		if e.refreshedAt.Before(cutoff) {
			delete(c.m, k)
		}
	}
}

// refreshRunnerPresenceLiveness resolves every connected Workspace Runner to
// its online runtime(s) and bumps liveness + last_seen_at for each.
func refreshRunnerPresenceLiveness(ctx context.Context, queries *db.Queries, liveness handler.LivenessStore, hub *daemonws.Hub, cache *runnerIDCache) {
	runners := hub.ListWorkspaceDaemons()
	if len(runners) == 0 {
		// No sockets: nothing to refresh; let the cache age out naturally.
		return
	}
	now := time.Now()
	for _, r := range runners {
		key := r.DaemonID + "|" + r.WorkspaceID
		runtimeID, ok := cache.get(key)
		if !ok {
			wsID, err := util.ParseUUID(r.WorkspaceID)
			if err != nil {
				continue
			}
			rows, err := queries.ListAgentRuntimesByDaemonID(ctx, db.ListAgentRuntimesByDaemonIDParams{
				DaemonID:    r.DaemonID,
				WorkspaceID: wsID,
				RuntimeMode: "",
			})
			if err != nil {
				slog.Debug("runner presence liveness: runtime lookup failed", "daemon_id", r.DaemonID, "error", err)
				continue
			}
			// Prefer the first online row for the daemon/workspace pair.
			runtimeID = ""
			for i := range rows {
				if rows[i].Status == "online" {
					runtimeID = util.UUIDToString(rows[i].ID)
					break
				}
			}
			if runtimeID == "" {
				continue
			}
			cache.set(key, runtimeID, now)
		}
		touchRuntimePresence(ctx, queries, liveness, runtimeID)
	}
	cache.prune(now, runnerIDCacheTTL)
}

// touchRuntimePresence refreshes the liveness record and DB last_seen_at for
// one online runtime. Both writes are idempotent and online-guarded, so they
// never resurrect a sweeper-flipped offline row.
func touchRuntimePresence(ctx context.Context, queries *db.Queries, liveness handler.LivenessStore, runtimeID string) {
	if liveness.Available() {
		if err := liveness.Touch(ctx, runtimeID, runnerPresenceRefreshInterval*2); err != nil {
			slog.Debug("runner presence liveness: Redis touch failed", "runtime_id", runtimeID, "error", err)
		}
	}
	id, err := util.ParseUUID(runtimeID)
	if err != nil {
		return
	}
	if _, err := queries.TouchAgentRuntimeLastSeen(ctx, id); err != nil {
		slog.Debug("runner presence liveness: last_seen bump failed", "runtime_id", runtimeID, "error", err)
	}
}
