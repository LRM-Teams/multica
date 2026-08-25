package daemon

import (
	"context"
	"log/slog"
	"strings"

	"github.com/google/uuid"

	"github.com/multica-ai/multica/server/internal/daemon/execenv"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// graphExecutionMemories asks the server to resolve and execute one bounded
// graph-memory recall. The daemon never resolves graph paths or runs Explore
// locally. Any unsuccessful recall remains non-fatal and never restores the
// legacy project/channel/daily/workspace/team memory paths.
func (d *Daemon) graphExecutionMemories(ctx context.Context, task Task, log *slog.Logger) []execenv.MemoryContextForEnv {
	profile := effectiveGraphProfile(d.cfg, task)
	if profile.memoryType != MemoryTypeGraph {
		return nil
	}
	query := graphRecallQuery(task)
	if query == "" {
		return nil
	}
	if d.client == nil {
		if log != nil {
			log.Warn("graph memory recall failed; injecting no graph memory", "task_id", task.ID, "error", "daemon client is not configured")
		}
		return nil
	}

	response, err := d.client.RequestGraphMemoryRecall(ctx, task.WorkspaceID, protocol.GraphMemoryRecallRequest{
		TraceID: uuid.NewString(), TaskID: task.ID, RuntimeID: task.RuntimeID, Query: query,
	})
	if err != nil {
		if log != nil {
			log.Warn("graph memory recall failed; injecting no graph memory", "task_id", task.ID, "error", err)
		}
		return nil
	}
	if response == nil || !response.Found || strings.TrimSpace(response.Injection) == "" {
		if log != nil {
			status := ""
			if response != nil {
				status = response.Status
			}
			log.Info("graph memory recall returned no injection", "task_id", task.ID, "status", status)
		}
		return nil
	}
	return []execenv.MemoryContextForEnv{{
		Name: "Graph memory recall", Content: response.Injection, Scope: "workspace",
	}}
}

// graphRecallQuery picks the user-authored text of the task as the recall
// query.
func graphRecallQuery(task Task) string {
	for _, candidate := range []string{
		task.ChatMessage,
		task.TriggerCommentContent,
		task.QuickCreatePrompt,
		task.AgentRadarPrompt,
		task.AutopilotDescription,
	} {
		if trimmed := strings.TrimSpace(candidate); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

// memoizedGraphExecutionMemories coalesces identical graph recall queries
// within one resident message batch: the first message pays the recall and
// later messages whose normalized query matches reuse the result (spec P0
// §4.2). nil results are memoized too - a recall failure is non-fatal
// data, not a retryable error, within a near-simultaneous batch.
func (d *Daemon) memoizedGraphExecutionMemories(
	ctx context.Context, task Task,
	memo map[string][]execenv.MemoryContextForEnv, log *slog.Logger,
) []execenv.MemoryContextForEnv {
	key := normalizeGraphRecallKey(graphRecallQuery(task))
	if key == "" {
		return nil
	}
	if cached, ok := memo[key]; ok {
		if log != nil {
			log.Info("graph memory recall coalesced within batch", "task_id", task.ID)
		}
		return cached
	}
	memories := d.graphExecutionMemories(ctx, task, log)
	memo[key] = memories
	return memories
}

// normalizeGraphRecallKey canonicalizes a recall query for exact-match
// coalescing: case-folding with whitespace runs collapsed.
func normalizeGraphRecallKey(query string) string {
	return strings.Join(strings.Fields(strings.ToLower(query)), " ")
}

// effectiveMemoryType resolves the reviewer type for one task (design
// §1/A4): a valid task-scoped override from the server payload wins over
// the process-level env default (cfg.MemoryType, already validated by
// LoadConfig); anything unrecognized falls back to the env default.
func effectiveMemoryType(configured, taskScoped string) string {
	switch strings.ToLower(strings.TrimSpace(taskScoped)) {
	case MemoryTypeLegacy:
		return MemoryTypeLegacy
	case MemoryTypeGraph:
		return MemoryTypeGraph
	}
	if configured == MemoryTypeGraph {
		return MemoryTypeGraph
	}
	return MemoryTypeLegacy
}

// graphMemoryEffectiveProfile is the per-task effective graph profile:
// valid task-scoped values from the server win; anything absent falls back
// to the daemon process env config (spec §10: env values are defaults only).
type graphMemoryEffectiveProfile struct {
	memoryType       string
	exploreAgents    int
	exploreMaxRounds int
}

func effectiveGraphProfile(cfg Config, task Task) graphMemoryEffectiveProfile {
	p := graphMemoryEffectiveProfile{
		memoryType:       effectiveMemoryType(cfg.MemoryType, task.MemoryType),
		exploreAgents:    cfg.GraphExploreAgents,
		exploreMaxRounds: cfg.GraphExploreMaxRounds,
	}
	if task.ExploreAgents > 0 {
		p.exploreAgents = task.ExploreAgents
	}
	if task.ExploreMaxRounds > 0 {
		p.exploreMaxRounds = task.ExploreMaxRounds
	}
	return p
}

// rememberGraphProfile caches the server-delivered effective graph profile
// for one workspace (spec §10). Empty memory types never clobber a cached
// entry: an old server simply leaves the previous value (or the env
// default) in effect.
func (d *Daemon) rememberGraphProfile(workspaceID, memoryType string, exploreAgents, exploreMaxRounds int) {
	switch memoryType {
	case MemoryTypeLegacy, MemoryTypeGraph:
	default:
		return // empty or unrecognized: never clobber the cache
	}
	if strings.TrimSpace(workspaceID) == "" {
		return
	}
	d.graphProfileMu.Lock()
	defer d.graphProfileMu.Unlock()
	if d.graphProfiles == nil {
		d.graphProfiles = map[string]graphMemoryEffectiveProfile{}
	}
	d.graphProfiles[workspaceID] = graphMemoryEffectiveProfile{
		memoryType:       memoryType,
		exploreAgents:    exploreAgents,
		exploreMaxRounds: exploreMaxRounds,
	}
}

func (d *Daemon) graphProfileForWorkspace(workspaceID string) (graphMemoryEffectiveProfile, bool) {
	d.graphProfileMu.Lock()
	defer d.graphProfileMu.Unlock()
	p, ok := d.graphProfiles[workspaceID]
	return p, ok
}
