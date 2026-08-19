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
// It must stay below both runtimeLivenessTTL (90s in handler/daemon.go) and
// the sweeper's stale threshold minus flush slack (150s), so a connected new
// daemon never looks stale to any consumer while its socket is up.
const runnerPresenceRefreshInterval = 45 * time.Second

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
// Keys are "daemonID|workspaceID".
type runnerIDCache struct {
	mu sync.Mutex
	m  map[string]string
}

func newRunnerIDCache() *runnerIDCache {
	return &runnerIDCache{m: make(map[string]string)}
}

func (c *runnerIDCache) get(key string) (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	id, ok := c.m[key]
	return id, ok
}

func (c *runnerIDCache) set(key, runtimeID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.m[key] = runtimeID
}

// refreshRunnerPresenceLiveness resolves every connected Workspace Runner to
// its online runtime(s) and bumps liveness + last_seen_at for each.
func refreshRunnerPresenceLiveness(ctx context.Context, queries *db.Queries, liveness handler.LivenessStore, hub *daemonws.Hub, cache *runnerIDCache) {
	runners := hub.ListWorkspaceRunners()
	if len(runners) == 0 {
		return
	}
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
			cache.set(key, runtimeID)
		}
		touchRuntimePresence(ctx, queries, liveness, runtimeID)
	}
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
