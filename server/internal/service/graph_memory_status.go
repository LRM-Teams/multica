package service

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/multica-ai/multica/server/internal/memorygraph"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// GraphMemoryStatusService backs the graph governance status API (spec §10).
// It reads only the canonical per-workspace layout; the daemon's query logs
// are read from the same shared workspaces volume.
type GraphMemoryStatusService struct {
	queries *db.Queries // may be nil in tests
	root    string      // workspaces root; empty resolves MULTICA_WORKSPACES_ROOT
}

func NewGraphMemoryStatusService(queries *db.Queries, root string) *GraphMemoryStatusService {
	return &GraphMemoryStatusService{queries: queries, root: root}
}

// GraphMemoryGraphStatus is the per-physical-graph view: versions/current
// pointer, staging depth, node count, last consolidation, and 24h recall
// quality.
type GraphMemoryGraphStatus struct {
	Kind                 string     `json:"kind"` // "project" | "channel" | "research"
	OwnerID              string     `json:"owner_id"`
	CurrentVersion       int        `json:"current_version"`
	Versions             []int      `json:"versions"`
	StagingSegments      int        `json:"staging_segments"`
	NodeCount            int        `json:"node_count"`
	LastConsolidatedAt   *time.Time `json:"last_consolidated_at,omitempty"`
	ConsolidationBackoff bool       `json:"consolidation_backoff"`
	RecallQueries24h     int        `json:"recall_queries_24h"`
	RecallHitRate24h     float64    `json:"recall_hit_rate_24h"`
}

// GraphMemoryStatus is the workspace-level governance view (spec §10).
type GraphMemoryStatus struct {
	WorkspaceID       string                   `json:"workspace_id"`
	MemoryType        string                   `json:"memory_type"`
	ScopedWriterReady bool                     `json:"scoped_writer_ready"`
	EmptyStart        bool                     `json:"empty_start"`
	Graphs            []GraphMemoryGraphStatus `json:"graphs"`
}

// graphConsolidationStateFile mirrors the scheduler's state file name
// (jobs_graph_memory.go): <workspaces root>/.consolidation_state.json.
const graphConsolidationStateFile = ".consolidation_state.json"

// graphDirStateView is the read-side view of the scheduler's per-directory
// consolidation state.
type graphDirStateView struct {
	LastConsolidated time.Time `json:"last_consolidated"`
	Backoff          bool      `json:"backoff,omitempty"`
}

func (s *GraphMemoryStatusService) Status(ctx context.Context, workspaceID string) (*GraphMemoryStatus, error) {
	root := s.root
	if root == "" {
		var err error
		root, err = graphMemoryWorkspacesRoot()
		if err != nil {
			return nil, err
		}
	}
	st := &GraphMemoryStatus{WorkspaceID: workspaceID, MemoryType: "legacy", EmptyStart: true}
	if s.queries != nil {
		if ws, err := util.ParseUUID(workspaceID); err == nil {
			if gate, err := s.queries.GetGraphMemoryScopedGate(ctx, ws); err == nil {
				st.MemoryType = gate.MemoryType
				st.ScopedWriterReady = gate.ScopedWriterReady
			}
		}
	}
	states := readConsolidationStates(root)
	cutoff := time.Now().UTC().Add(-24 * time.Hour)
	forEachWorkspaceGraph(root, workspaceID, func(kind memorygraph.GraphDirKind, ownerID, dir string) {
		g := GraphMemoryGraphStatus{Kind: string(kind), OwnerID: ownerID}
		store := memorygraph.NewStore(dir)
		if err := store.Init(); err != nil {
			return
		}
		g.CurrentVersion, _ = store.CurrentVersion()
		g.Versions, _ = store.ListVersions()
		sort.Ints(g.Versions)
		if segs, err := store.ListStagingSegments(); err == nil {
			g.StagingSegments = len(segs)
		}
		if g.CurrentVersion > 0 {
			if graph, err := memorygraph.LoadGraph(store, g.CurrentVersion); err == nil {
				g.NodeCount = len(graph.Nodes())
			}
		}
		if ds, ok := states[dir]; ok && !ds.LastConsolidated.IsZero() {
			ts := ds.LastConsolidated
			g.LastConsolidatedAt = &ts
			g.ConsolidationBackoff = ds.Backoff
		}
		queries, hits := recallStats24h(store, cutoff)
		g.RecallQueries24h = queries
		if queries > 0 {
			g.RecallHitRate24h = float64(hits) / float64(queries)
		}
		st.Graphs = append(st.Graphs, g)
		st.EmptyStart = false
	})
	return st, nil
}

// forEachWorkspaceGraph walks the canonical per-workspace layout
// (<root>/<ws>/memory_graph/{projects,channels}/<owner> plus the federated
// research graph research/<ws>, unification spec §4.4) and invokes fn for
// every directory whose identity marker matches the workspace scope.
// Directories with a mismatched or missing identity are skipped (fail
// closed, spec §3/§12).
func forEachWorkspaceGraph(root, workspaceID string, fn func(kind memorygraph.GraphDirKind, ownerID, dir string)) {
	for _, sub := range []struct {
		kind memorygraph.GraphDirKind
		dir  string
	}{
		{memorygraph.GraphDirKindProject, "projects"},
		{memorygraph.GraphDirKindChannel, "channels"},
		{memorygraph.GraphDirKindResearch, "research"},
	} {
		base := filepath.Join(root, workspaceID, "memory_graph", sub.dir)
		entries, err := os.ReadDir(base)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			// The research graph is owned by the workspace itself; only that
			// one directory is a sanctioned research scope.
			if sub.kind == memorygraph.GraphDirKindResearch && e.Name() != workspaceID {
				continue
			}
			dir := filepath.Join(base, e.Name())
			if err := memorygraph.VerifyGraphIdentity(dir, memorygraph.GraphIdentity{
				WorkspaceID: workspaceID, Kind: string(sub.kind), OwnerID: e.Name(),
			}); err != nil {
				continue
			}
			fn(sub.kind, e.Name(), dir)
		}
	}
}

func readConsolidationStates(root string) map[string]graphDirStateView {
	body, err := os.ReadFile(filepath.Join(root, graphConsolidationStateFile))
	if err != nil {
		return nil
	}
	var raw struct {
		Dirs map[string]graphDirStateView `json:"dirs"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil
	}
	return raw.Dirs
}

func recallStats24h(store *memorygraph.Store, cutoff time.Time) (queries, hits int) {
	windows, err := store.ListQueryLogWindows()
	if err != nil {
		return 0, 0
	}
	for _, w := range windows {
		entries, err := store.ReadQueryLog(w)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.Timestamp.Before(cutoff) {
				continue
			}
			queries++
			if e.Found {
				hits++
			}
		}
	}
	return queries, hits
}
