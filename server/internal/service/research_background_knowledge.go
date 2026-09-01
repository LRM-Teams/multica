// SPDX-License-Identifier: Apache-2.0

package service

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/multica-ai/multica/server/internal/memorygraph"
	"github.com/multica-ai/multica/server/internal/researchrun"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// Phase 2 (unification spec §5) bounds: one bounded retrieval per graph, each
// entry summarized at a rune cap, so the Brief's size increment is independent
// of graph size. The marker fits inside the cap so a truncated summary never
// exceeds it.
const (
	researchBackgroundTopKPerGraph = 5
	researchBackgroundSummaryRunes = 400
)

// ResearchBackgroundKnowledgeService recalls Director background knowledge
// from the workspace research graph and, for project-bound sessions, the
// bound project's graph. It implements researchrun.V6BackgroundKnowledgeProvider.
type ResearchBackgroundKnowledgeService struct {
	pool *pgxpool.Pool
	root string
}

func NewResearchBackgroundKnowledgeService(pool *pgxpool.Pool, root string) *ResearchBackgroundKnowledgeService {
	return &ResearchBackgroundKnowledgeService{pool: pool, root: root}
}

// BackgroundKnowledge returns the bounded entry list for one Director cycle.
// Legacy-memory workspaces get zero behavior, and every graph-level failure
// degrades to that graph contributing nothing — background knowledge is
// additive and never blocks the cycle (spec §5).
func (s *ResearchBackgroundKnowledgeService) BackgroundKnowledge(ctx context.Context, workspaceID, runID, goal, projectID string) ([]researchrun.V6BackgroundKnowledgeEntry, error) {
	entries := []researchrun.V6BackgroundKnowledgeEntry{}
	if strings.TrimSpace(goal) == "" {
		return entries, nil
	}
	wsUUID, err := util.ParseUUID(workspaceID)
	if err != nil {
		slog.Warn("research background: invalid workspace id", "workspace_id", workspaceID, "run_id", runID, "error", err)
		return entries, nil
	}
	if resolveGraphMemoryType(ctx, db.New(s.pool), wsUUID, graphMemoryEnvMemoryType()) != "graph" {
		return entries, nil
	}
	root := s.root
	if root == "" {
		if root, err = graphMemoryWorkspacesRoot(); err != nil {
			slog.Warn("research background: workspaces root unavailable", "run_id", runID, "error", err)
			return entries, nil
		}
	}
	entries = append(entries, s.recallBackgroundGraph(ctx, root, workspaceID, memorygraph.GraphDirKindResearch, workspaceID,
		memorygraph.GraphView{AllowResearch: true}, "research", runID, goal)...)
	if projectID = strings.TrimSpace(projectID); projectID != "" {
		entries = append(entries, s.recallBackgroundGraph(ctx, root, workspaceID, memorygraph.GraphDirKindProject, projectID,
			memorygraph.GraphView{AllowProject: true}, "project", runID, goal)...)
	}
	return entries, nil
}

// recallBackgroundGraph runs one bounded BM25 retrieval over a single scoped
// graph under its view, filters rejected/superseded nodes, and summarizes
// bodies. Any failure degrades to no entries from this graph.
func (s *ResearchBackgroundKnowledgeService) recallBackgroundGraph(ctx context.Context, root, workspaceID string, kind memorygraph.GraphDirKind, ownerID string, view memorygraph.GraphView, graphTag, runID, query string) []researchrun.V6BackgroundKnowledgeEntry {
	dir, err := memorygraph.DirForScope(root, workspaceID, kind, ownerID)
	if err != nil {
		slog.Warn("research background: scope unresolved", "run_id", runID, "kind", string(kind), "error", err)
		return nil
	}
	if err := memorygraph.VerifyGraphIdentity(dir, memorygraph.GraphIdentity{WorkspaceID: workspaceID, Kind: string(kind), OwnerID: ownerID}); err != nil {
		slog.Warn("research background: graph identity mismatch", "run_id", runID, "kind", string(kind), "error", err)
		return nil
	}
	store := memorygraph.NewStore(dir)
	version, err := store.CurrentVersion()
	if err != nil {
		return nil
	}
	cfg := memorygraph.DefaultRetrievalConfig()
	cfg.View = view
	cfg.TopK = researchBackgroundTopKPerGraph
	retriever := memorygraph.NewHybridRetriever(store, nil, cfg)
	if err := retriever.RebuildForVersion(ctx, version); err != nil {
		slog.Warn("research background: retriever unavailable", "run_id", runID, "kind", string(kind), "error", err)
		return nil
	}
	docs, err := retriever.Search(ctx, query)
	if err != nil || len(docs) == 0 {
		return nil
	}
	graph, err := memorygraph.LoadGraph(store, version)
	if err != nil {
		return nil
	}
	entries := make([]researchrun.V6BackgroundKnowledgeEntry, 0, len(docs))
	for _, doc := range docs {
		node := graph.Node(doc.ID)
		if node == nil {
			continue
		}
		if node.Epistemic == memorygraph.StatusRejected || node.Epistemic == memorygraph.StatusSuperseded {
			continue
		}
		entries = append(entries, researchrun.V6BackgroundKnowledgeEntry{
			NodeID:     node.NodeID,
			Graph:      graphTag,
			Epistemic:  node.Epistemic,
			ObservedAt: node.ObservedAt,
			Summary:    researchBackgroundSummary(node.Body),
		})
	}
	// Adopted recalls are usage signals (spec §4.5): the maintenance trigger
	// counts query-log entries across windows, so this window drives the
	// graph's maintenance rounds as Director adoption grows. A write failure
	// only costs the signal, never the entries.
	if len(entries) > 0 {
		nodeIDs := make([]string, 0, len(entries))
		for _, entry := range entries {
			nodeIDs = append(nodeIDs, entry.NodeID)
		}
		if err := memorygraph.NewQueryRecorder(store, "director").RecordRecall(memorygraph.QueryLogEntry{
			TraceID:   uuid.NewString(),
			Query:     query,
			Timestamp: time.Now().UTC(),
			Version:   version,
			NodeIDs:   nodeIDs,
			Found:     true,
		}); err != nil {
			slog.Warn("research background: query log append failed", "run_id", runID, "kind", string(kind), "error", err)
		}
	}
	return entries
}

// researchBackgroundSummary trims a node body to the summary rune cap, with
// the truncation marker kept inside the cap.
func researchBackgroundSummary(body string) string {
	body = strings.TrimSpace(body)
	runes := []rune(body)
	if len(runes) <= researchBackgroundSummaryRunes {
		return body
	}
	marker := []rune(graphMemoryRecallTruncationMarker)
	return string(runes[:researchBackgroundSummaryRunes-len(marker)]) + graphMemoryRecallTruncationMarker
}
