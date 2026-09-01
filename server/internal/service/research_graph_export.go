// SPDX-License-Identifier: Apache-2.0

package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/multica-ai/multica/server/internal/memorygraph"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// Research→memory exporter (unification spec §4.2): polls the research
// ledgers (V5 research_graph_node/edge plus the V6 insight/result tables)
// and writes knowledge nodes into the workspace's research-scope memory
// graph. Execution bookkeeping (agent supernodes, stage gates, roster
// changes) never crosses over; agents dissolve into SourceAgentIDs
// provenance. Writes go through the GraphMutationCoordinator lock and are
// idempotent by deterministic node ids plus export keys, so the research
// graph directory can be wiped and replayed from the ledgers.
//
// Deviation from the plan's cursor sketch, recorded here deliberately:
// research rows carry no monotonic id (UUID PKs), so the watermark is
// max(updated_at) with a >= poll and export-key hash comparison as the
// miss-proof dedup; the V6 tables are re-scanned within the batch budget
// for the same reason. This preserves the plan's guarantees (resumable,
// replay-safe, no skipped rows) without inventing new monotonic columns.

// Source kinds recorded in export keys and on node frontmatter (spec §4.2).
const (
	ResearchSourceKindNode    = "research_node"
	ResearchSourceKindInsight = "research_insight"
	ResearchSourceKindResult  = "research_result"
)

// researchKnowledgeNodeTypes is the V5 node-type whitelist: knowledge nodes
// only, execution bookkeeping excluded (unification decision 3).
var researchKnowledgeNodeTypes = map[string]bool{
	"goal": true, "subquestion": true, "probe": true, "finding": true,
	"conflict": true, "dead_end": true, "refuted": true, "pivot": true,
	"conclusion": true, "insight": true,
}

// researchSameNameEdgeTypes crosses over verbatim into relation edges; every
// other research edge type (and any hierarchy structure) is dropped.
var researchSameNameEdgeTypes = map[string]bool{
	"supports": true, "contradicts": true, "supersedes": true,
	"derived_from": true, "merged_from": true,
}

func researchNodeTypeAdmitted(nodeType string) bool {
	return researchKnowledgeNodeTypes[nodeType]
}

func researchEdgeTypeAdmitted(edgeType string) bool {
	return researchSameNameEdgeTypes[edgeType]
}

// researchEpistemic maps a V5 node onto the memory epistemic ladder
// (spec §4.2): the lifecycle status overrides the type mapping because a
// terminal research state is stronger evidence than the node's kind.
func researchEpistemic(nodeType, status string) string {
	switch status {
	case "superseded", "restarted":
		return memorygraph.StatusSuperseded
	case "invalidated", "deprecated", "abandoned":
		return memorygraph.StatusRejected
	}
	switch nodeType {
	case "conclusion":
		return memorygraph.StatusAccepted
	case "insight":
		return memorygraph.StatusSupported
	case "conflict":
		return memorygraph.StatusContested
	case "dead_end", "refuted":
		return memorygraph.StatusRejected
	default: // goal, subquestion, probe, finding, pivot
		return memorygraph.StatusProposed
	}
}

// researchInsightEpistemic maps research_insight.status.
func researchInsightEpistemic(status string) string {
	switch status {
	case "accepted":
		return memorygraph.StatusAccepted
	case "stale":
		return memorygraph.StatusContested
	case "superseded", "obsolete":
		return memorygraph.StatusSuperseded
	default:
		return memorygraph.StatusProposed
	}
}

// researchResultEpistemic maps research_result_node.conclusion_state.
func researchResultEpistemic(state string) string {
	switch state {
	case "accepted":
		return memorygraph.StatusAccepted
	case "challenged":
		return memorygraph.StatusContested
	case "refuted", "invalid":
		return memorygraph.StatusRejected
	default:
		return memorygraph.StatusProposed
	}
}

// researchNodeRow is the V5 poll projection of one research_graph_node.
type researchNodeRow struct {
	ID           pgtype.UUID
	WorkspaceID  pgtype.UUID
	SessionID    pgtype.UUID
	NodeType     string
	Title        string
	Summary      string
	Status       string
	ActorAgentID pgtype.UUID
	SupersededBy pgtype.UUID
	DerivedFrom  pgtype.UUID
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// mapResearchNodeToMemory renders one research node as a memory node with
// deterministic id, research scope, dissolved agent provenance, and the
// mapped epistemic state.
func mapResearchNodeToMemory(row researchNodeRow) *memorygraph.Node {
	body := row.Title
	if row.Summary != "" {
		body += "\n\n" + row.Summary
	}
	n := &memorygraph.Node{
		NodeID:          ResearchSourceKindNode + ":" + util.UUIDToString(row.ID),
		Body:            body,
		Level:           0,
		Epistemic:       researchEpistemic(row.NodeType, row.Status),
		TemporalStatus:  memorygraph.TemporalCurrent,
		Tags:            []string{"research", row.NodeType},
		Visibility:      "research",
		SourceKind:      ResearchSourceKindNode,
		SourceSessionID: util.UUIDToString(row.SessionID),
		ObservedAt:      row.CreatedAt.UTC(),
		CreatedBy:       memorygraph.CreatorIngester,
	}
	if row.ActorAgentID.Valid {
		n.SourceAgentIDs = []string{util.UUIDToString(row.ActorAgentID)}
	}
	return n
}

// researchExportItem is one source row mapped for writing.
type researchExportItem struct {
	kind         string
	sourceID     pgtype.UUID
	sourceHash   string // ledger-side content hash, "" = compute from body
	node         *memorygraph.Node
	mergeInto    string // non-empty: fold into this existing node (spec §4.2 dedup)
	supersededBy pgtype.UUID
	derivedFrom  pgtype.UUID
}

// ResearchSimilarityFunc scores a candidate body against the existing
// research-graph nodes (dir) and returns the best match and its similarity
// in [0,1]. excludeNodeID keeps a node from matching itself. The production
// wiring (embedder cosine) lands with the scheduler slice; tests inject a
// mock.
type ResearchSimilarityFunc func(ctx context.Context, dir string, body string, excludeNodeID string) (string, float64, error)

// ResearchGraphExporterConfig shapes one exporter instance.
type ResearchGraphExporterConfig struct {
	Batch          int     // max V5 rows per poll (default: limits research batch)
	DedupThreshold float64 // import-time merge threshold (default: limits)
}

// ResearchGraphExporter polls the research ledgers and maintains the
// research-scope memory graph.
type ResearchGraphExporter struct {
	pool       *pgxpool.Pool
	queries    *db.Queries
	coord      *GraphMutationCoordinator
	cfg        ResearchGraphExporterConfig
	limits     GraphMemoryLimits
	similarity ResearchSimilarityFunc
}

// NewResearchGraphExporter wires an exporter. pool backs the ledger polls,
// state transactions and the mutation lock; queries resolves the graph dir.
func NewResearchGraphExporter(pool *pgxpool.Pool, queries *db.Queries, cfg ResearchGraphExporterConfig) *ResearchGraphExporter {
	limits := LoadGraphMemoryLimits(func(string) string { return "" })
	if cfg.Batch <= 0 {
		cfg.Batch = limits.Research.ExportBatch
	}
	if cfg.DedupThreshold <= 0 {
		cfg.DedupThreshold = limits.Research.DedupThreshold
	}
	return &ResearchGraphExporter{
		pool:    pool,
		queries: queries,
		coord:   NewGraphMutationCoordinator(pool),
		cfg:     cfg,
		limits:  limits,
	}
}

// SetSimilarity installs the import-time dedup scorer; nil (the default)
// disables deterministic merging.
func (e *ResearchGraphExporter) SetSimilarity(fn ResearchSimilarityFunc) { e.similarity = fn }

// ResearchGraphExportResult reports one poll's write outcome.
type ResearchGraphExportResult struct {
	NodesWritten int
	EdgesWritten int
	Cursor       time.Time
}

// ExportWorkspace runs one export poll for a workspace. Non-graph
// workspaces fail closed via researchGraphDir.
func (e *ResearchGraphExporter) ExportWorkspace(ctx context.Context, workspaceID pgtype.UUID) (*ResearchGraphExportResult, error) {
	if !workspaceID.Valid {
		return nil, fmt.Errorf("research export: invalid workspace id")
	}
	dir, err := researchGraphDir(ctx, e.queries, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("research export: %w", err)
	}
	// Wipe recovery (plan line 18): export keys exist but the on-disk graph
	// is empty → the directory was deleted after exports ran. Restart the
	// watermark so V5 rows before it re-poll and the graph rebuilds from
	// the ledgers; the presence-checked skip filter re-materializes every
	// key's node with its deterministic id.
	if err := e.recoverWipedGraph(ctx, workspaceID, dir); err != nil {
		return nil, err
	}
	cursor, err := e.loadCursor(ctx, workspaceID)
	if err != nil {
		return nil, err
	}
	items, sessions, newCursor, err := e.poll(ctx, workspaceID, dir, cursor)
	if err != nil {
		return nil, err
	}
	res := &ResearchGraphExportResult{Cursor: newCursor}
	if len(items) == 0 {
		return res, e.saveState(ctx, workspaceID, newCursor, nil)
	}

	// Same-name edges of every touched session, endpoints filtered to what
	// is or will be present in the graph.
	edges, err := e.pollEdges(ctx, workspaceID, sessions, items)
	if err != nil {
		return nil, err
	}

	var nodes, edgesWritten int
	wsStr := util.UUIDToString(workspaceID)
	if err := e.coord.WithGraphLock(ctx, wsStr, "research", wsStr, func(ctx context.Context) error {
		nodes, edgesWritten, err = e.writeBatch(dir, items, edges)
		return err
	}); err != nil {
		return nil, fmt.Errorf("research export: write: %w", err)
	}
	res.NodesWritten, res.EdgesWritten = nodes, edgesWritten
	return res, e.saveState(ctx, workspaceID, newCursor, items)
}

// loadCursor reads the workspace watermark; a missing state row starts at
// the beginning of time.
func (e *ResearchGraphExporter) loadCursor(ctx context.Context, workspaceID pgtype.UUID) (time.Time, error) {
	var cursor time.Time
	err := e.pool.QueryRow(ctx,
		`SELECT node_cursor FROM research_graph_export_state WHERE workspace_id=$1`, workspaceID).Scan(&cursor)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return time.Time{}, nil
		}
		return time.Time{}, fmt.Errorf("research export: load cursor: %w", err)
	}
	return cursor, nil
}

// poll fetches changed V5 nodes and rescans the V6 knowledge tables within
// the batch budget, maps admitted rows, and skips unchanged content via the
// export keys.
func (e *ResearchGraphExporter) poll(ctx context.Context, workspaceID pgtype.UUID, dir string, cursor time.Time) ([]researchExportItem, map[string]bool, time.Time, error) {
	rows, err := e.pool.Query(ctx, `
		SELECT id, workspace_id, session_id, node_type, title, summary, status,
		       actor_agent_id, superseded_by, derived_from, created_at, updated_at
		FROM research_graph_node
		WHERE workspace_id=$1 AND updated_at >= $2
		ORDER BY updated_at ASC, id ASC
		LIMIT $3`, workspaceID, cursor, e.cfg.Batch)
	if err != nil {
		return nil, nil, cursor, fmt.Errorf("research export: poll nodes: %w", err)
	}
	defer rows.Close()

	items := make([]researchExportItem, 0, e.cfg.Batch)
	sessions := map[string]bool{}
	newCursor := cursor
	for rows.Next() {
		var row researchNodeRow
		if err := rows.Scan(&row.ID, &row.WorkspaceID, &row.SessionID, &row.NodeType, &row.Title,
			&row.Summary, &row.Status, &row.ActorAgentID, &row.SupersededBy, &row.DerivedFrom,
			&row.CreatedAt, &row.UpdatedAt); err != nil {
			return nil, nil, cursor, fmt.Errorf("research export: scan node: %w", err)
		}
		if row.UpdatedAt.After(newCursor) {
			newCursor = row.UpdatedAt.UTC()
		}
		sessions[util.UUIDToString(row.SessionID)] = true
		if !researchNodeTypeAdmitted(row.NodeType) {
			continue // execution bookkeeping never crosses over
		}
		// The export key must change when epistemic-relevant fields change
		// (a later supersede keeps the same body), so the hash covers the
		// lifecycle status and lineage columns, not just the body text.
		sourceHash := memorygraph.ComputeContentHash(fmt.Sprintf("%s\x00%s\x00%s\x00%s",
			row.Title+"\n\n"+row.Summary, row.Status,
			util.UUIDToString(row.SupersededBy), util.UUIDToString(row.DerivedFrom)))
		items = append(items, researchExportItem{
			kind:         ResearchSourceKindNode,
			sourceID:     row.ID,
			sourceHash:   sourceHash,
			node:         mapResearchNodeToMemory(row),
			supersededBy: row.SupersededBy,
			derivedFrom:  row.DerivedFrom,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, nil, cursor, fmt.Errorf("research export: poll nodes: %w", err)
	}

	insights, err := e.pollInsights(ctx, workspaceID)
	if err != nil {
		return nil, nil, cursor, err
	}
	items = append(items, insights...)
	results, err := e.pollResults(ctx, workspaceID)
	if err != nil {
		return nil, nil, cursor, err
	}
	items = append(items, results...)

	// Skip unchanged sources: same ledger hash already exported AND the
	// node still present in the graph. A matching export key whose node is
	// gone (e.g. the on-disk graph was wiped) re-exports — deterministic
	// ids make the rebuild byte-consistent, so the graph directory can be
	// deleted and replayed (plan line 18, unification spec §4.2).
	keys, err := e.loadKeys(ctx, workspaceID)
	if err != nil {
		return nil, nil, cursor, err
	}
	present := researchGraphNodeIDsInDir(dir)
	filtered := items[:0]
	for _, it := range items {
		hash := it.sourceHash
		if hash == "" {
			hash = memorygraph.ComputeContentHash(it.node.Body)
		}
		if k, ok := keys[it.kind+":"+util.UUIDToString(it.sourceID)]; ok && k == hash && present[it.node.NodeID] {
			continue
		}
		filtered = append(filtered, it)
	}
	// Import-time deterministic dedup (spec §4.2): a high-similarity match
	// against the existing graph folds the incoming node into its survivor.
	if e.similarity != nil {
		for i := range filtered {
			best, score, err := e.similarity(ctx, dir, filtered[i].node.Body, filtered[i].node.NodeID)
			if err == nil && score >= e.cfg.DedupThreshold && best != "" && best != filtered[i].node.NodeID {
				filtered[i].mergeInto = best
			}
		}
	}
	return filtered, sessions, newCursor, nil
}

// pollInsights rescans the V6 insight ledger within the batch budget; the
// export keys' hash comparison turns the rescan into change detection.
func (e *ResearchGraphExporter) pollInsights(ctx context.Context, workspaceID pgtype.UUID) ([]researchExportItem, error) {
	rows, err := e.pool.Query(ctx, `
		SELECT i.id, i.workspace_id, i.session_id, i.title, i.summary, i.status,
		       COALESCE(v.content, ''), COALESCE(v.conclusion, ''), COALESCE(v.content_hash, '')
		FROM research_insight i
		LEFT JOIN research_insight_version v ON v.id = i.current_version_id
		WHERE i.workspace_id = $1
		ORDER BY i.created_at ASC
		LIMIT $2`, workspaceID, e.cfg.Batch)
	if err != nil {
		return nil, fmt.Errorf("research export: poll insights: %w", err)
	}
	defer rows.Close()

	items := make([]researchExportItem, 0, 8)
	for rows.Next() {
		var (
			id, ws, session                  pgtype.UUID
			title, summary, status           string
			content, conclusion, contentHash string
		)
		if err := rows.Scan(&id, &ws, &session, &title, &summary, &status, &content, &conclusion, &contentHash); err != nil {
			return nil, fmt.Errorf("research export: scan insight: %w", err)
		}
		body := title
		for _, part := range []string{summary, conclusion, content} {
			if part != "" {
				body += "\n\n" + part
			}
		}
		items = append(items, researchExportItem{
			kind:       ResearchSourceKindInsight,
			sourceID:   id,
			sourceHash: contentHash,
			node: &memorygraph.Node{
				NodeID:          ResearchSourceKindInsight + ":" + util.UUIDToString(id),
				Body:            body,
				Level:           0,
				Epistemic:       researchInsightEpistemic(status),
				TemporalStatus:  memorygraph.TemporalCurrent,
				Tags:            []string{"research", "insight"},
				Visibility:      "research",
				SourceKind:      ResearchSourceKindInsight,
				SourceSessionID: util.UUIDToString(session),
				ObservedAt:      time.Now().UTC(),
				CreatedBy:       memorygraph.CreatorIngester,
			},
		})
	}
	return items, rows.Err()
}

// pollResults rescans the V6 result ledger within the batch budget.
func (e *ResearchGraphExporter) pollResults(ctx context.Context, workspaceID pgtype.UUID) ([]researchExportItem, error) {
	rows, err := e.pool.Query(ctx, `
		SELECT id, workspace_id, session_id, COALESCE(objective, ''), COALESCE(conclusion, ''),
		       COALESCE(conclusion_state, 'proposed'), COALESCE(content, ''), COALESCE(content_hash, '')
		FROM research_result_node
		WHERE workspace_id = $1
		ORDER BY created_at ASC
		LIMIT $2`, workspaceID, e.cfg.Batch)
	if err != nil {
		return nil, fmt.Errorf("research export: poll results: %w", err)
	}
	defer rows.Close()

	items := make([]researchExportItem, 0, 8)
	for rows.Next() {
		var (
			id, ws, session                             pgtype.UUID
			objective, conclusion, state, content, hash string
		)
		if err := rows.Scan(&id, &ws, &session, &objective, &conclusion, &state, &content, &hash); err != nil {
			return nil, fmt.Errorf("research export: scan result: %w", err)
		}
		body := objective
		for _, part := range []string{conclusion, content} {
			if part != "" {
				body += "\n\n" + part
			}
		}
		items = append(items, researchExportItem{
			kind:       ResearchSourceKindResult,
			sourceID:   id,
			sourceHash: hash,
			node: &memorygraph.Node{
				NodeID:          ResearchSourceKindResult + ":" + util.UUIDToString(id),
				Body:            body,
				Level:           0,
				Epistemic:       researchResultEpistemic(state),
				TemporalStatus:  memorygraph.TemporalCurrent,
				Tags:            []string{"research", "result"},
				Visibility:      "research",
				SourceKind:      ResearchSourceKindResult,
				SourceSessionID: util.UUIDToString(session),
				ObservedAt:      time.Now().UTC(),
				CreatedBy:       memorygraph.CreatorIngester,
			},
		})
	}
	return items, rows.Err()
}

// pollEdges collects the same-name relation edges of the touched sessions,
// keeping only edges whose endpoints are both knowledge nodes. Endpoint
// presence in the graph is decided at write time (batch ∪ graph). No
// hierarchy edges are ever produced.
func (e *ResearchGraphExporter) pollEdges(ctx context.Context, workspaceID pgtype.UUID, sessions map[string]bool, items []researchExportItem) ([]*memorygraph.Edge, error) {
	if len(sessions) == 0 {
		return nil, nil
	}
	ids := make([]string, 0, len(sessions))
	for s := range sessions {
		ids = append(ids, s)
	}
	rows, err := e.pool.Query(ctx, `
		SELECT e.id, e.edge_type, e.from_node_id, e.to_node_id, n1.node_type, n2.node_type
		FROM research_graph_edge e
		JOIN research_graph_node n1 ON n1.id = e.from_node_id
		JOIN research_graph_node n2 ON n2.id = e.to_node_id
		WHERE e.workspace_id = $1 AND e.session_id = ANY($2::uuid[])
		  AND e.edge_type = ANY($3::text[])`,
		workspaceID, ids, []string{"supports", "contradicts", "supersedes", "derived_from", "merged_from"})
	if err != nil {
		return nil, fmt.Errorf("research export: poll edges: %w", err)
	}
	defer rows.Close()

	edges := make([]*memorygraph.Edge, 0, 8)
	for rows.Next() {
		var (
			edgeID           pgtype.UUID
			edgeType         string
			from, to         pgtype.UUID
			fromType, toType string
		)
		if err := rows.Scan(&edgeID, &edgeType, &from, &to, &fromType, &toType); err != nil {
			return nil, fmt.Errorf("research export: scan edge: %w", err)
		}
		if !researchNodeTypeAdmitted(fromType) || !researchNodeTypeAdmitted(toType) {
			continue
		}
		fromID := ResearchSourceKindNode + ":" + util.UUIDToString(from)
		toID := ResearchSourceKindNode + ":" + util.UUIDToString(to)
		edges = append(edges, &memorygraph.Edge{
			EdgeID: "research_edge:" + util.UUIDToString(edgeID),
			Type:   edgeType,
			From:   fromID,
			To:     toID,
		})
	}
	return edges, rows.Err()
}

// researchGraphNodeIDsInDir returns the node ids of the graph's current
// version, or an empty set when the store is unreadable — the caller then
// re-exports, which is the fail-safe direction for replay.
func researchGraphNodeIDsInDir(dir string) map[string]bool {
	store := memorygraph.NewStore(dir)
	if err := store.Init(); err != nil {
		return map[string]bool{}
	}
	version, err := store.CurrentVersion()
	if err != nil {
		return map[string]bool{}
	}
	graph, err := memorygraph.LoadGraph(store, version)
	if err != nil {
		return map[string]bool{}
	}
	ids := make(map[string]bool, len(graph.Nodes()))
	for _, n := range graph.Nodes() {
		ids[n.NodeID] = true
	}
	return ids
}

// recoverWipedGraph resets the export watermark when the durable export
// keys prove prior exports but the on-disk graph holds no nodes: the
// directory was wiped, so V5 rows behind the watermark must re-poll for the
// rebuild. Fresh workspaces (no keys) keep the normal watermark flow.
func (e *ResearchGraphExporter) recoverWipedGraph(ctx context.Context, workspaceID pgtype.UUID, dir string) error {
	keys, err := e.loadKeys(ctx, workspaceID)
	if err != nil {
		return err
	}
	if len(keys) == 0 || len(researchGraphNodeIDsInDir(dir)) > 0 {
		return nil
	}
	if _, err := e.pool.Exec(ctx,
		`DELETE FROM research_graph_export_state WHERE workspace_id=$1`, workspaceID); err != nil {
		return fmt.Errorf("research export: reset wiped cursor: %w", err)
	}
	return nil
}

// loadKeys reads the workspace's exported content hashes keyed by
// "<source_kind>:<source_id>".
func (e *ResearchGraphExporter) loadKeys(ctx context.Context, workspaceID pgtype.UUID) (map[string]string, error) {
	rows, err := e.pool.Query(ctx,
		`SELECT source_kind, source_id, content_hash FROM research_graph_export_key WHERE workspace_id=$1`, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("research export: load keys: %w", err)
	}
	defer rows.Close()
	keys := map[string]string{}
	for rows.Next() {
		var kind, hash string
		var id pgtype.UUID
		if err := rows.Scan(&kind, &id, &hash); err != nil {
			return nil, fmt.Errorf("research export: scan key: %w", err)
		}
		keys[kind+":"+util.UUIDToString(id)] = hash
	}
	return keys, rows.Err()
}

// writeBatch materializes one export batch as a new graph version: new or
// changed nodes are saved (created metadata preserved on updates), an
// import-time dedup hit folds the node into its survivor with merged_from
// lineage, lineage columns become supersedes/derived_from edges, and the
// same-name edges are appended with deterministic ids. Must run under the
// graph mutation lock.
func (e *ResearchGraphExporter) writeBatch(dir string, items []researchExportItem, edges []*memorygraph.Edge) (int, int, error) {
	store := memorygraph.NewStore(dir)
	if err := store.Init(); err != nil {
		return 0, 0, fmt.Errorf("research export: init store: %w", err)
	}
	current, err := store.CurrentVersion()
	if err != nil {
		return 0, 0, err
	}
	g, err := memorygraph.LoadGraph(store, current)
	if err != nil {
		return 0, 0, fmt.Errorf("research export: load graph v%d: %w", current, err)
	}
	v, err := store.CreateVersionFrom(current, memorygraph.CreatorIngester)
	if err != nil {
		return 0, 0, err
	}

	// presentInGraph grows with each batch write so edge endpoints written
	// in this batch resolve even when the graph snapshot predates them.
	presentInGraph := map[string]bool{}
	for _, n := range g.Nodes() {
		presentInGraph[n.NodeID] = true
	}

	nodesWritten := 0
	for _, it := range items {
		n := it.node
		existing := g.Node(n.NodeID)
		if existing != nil && existing.ContentHash == memorygraph.ComputeContentHash(n.Body) &&
			existing.Epistemic == n.Epistemic && it.mergeInto == "" {
			presentInGraph[n.NodeID] = true
			continue // byte-identical, same-epistemic replay
		}
		if existing != nil {
			n.CreatedBy = existing.CreatedBy
			n.CreatedVersion = existing.CreatedVersion
			n.SourceAgentIDs = mergeIDSets(existing.SourceAgentIDs, n.SourceAgentIDs)
		} else {
			n.CreatedVersion = v
		}
		if it.mergeInto != "" && g.Node(it.mergeInto) != nil && it.mergeInto != n.NodeID {
			// Import-time deterministic merge (spec §4.2): the survivor
			// keeps its identity and absorbs provenance; the research node
			// lands superseded with a merged_from edge for traceability.
			n.Epistemic = memorygraph.StatusSuperseded
			n.UpdatedVersion = v
			if err := store.SaveNode(v, n); err != nil {
				return 0, 0, err
			}
			survivor := g.Node(it.mergeInto)
			survivor.SourceAgentIDs = mergeIDSets(survivor.SourceAgentIDs, n.SourceAgentIDs)
			survivor.SourceSessionID = firstNonEmpty(survivor.SourceSessionID, n.SourceSessionID)
			survivor.UpdatedVersion = v
			if err := store.SaveNode(v, survivor); err != nil {
				return 0, 0, err
			}
			edges = append(edges, &memorygraph.Edge{
				EdgeID: "research_merge:" + n.NodeID,
				Type:   memorygraph.EdgeTypeMergedFrom,
				From:   n.NodeID,
				To:     it.mergeInto,
			})
		} else {
			n.UpdatedVersion = v
			if err := store.SaveNode(v, n); err != nil {
				return 0, 0, err
			}
		}
		presentInGraph[n.NodeID] = true
		nodesWritten++

		// Lineage columns become edges when the target is reachable.
		if it.supersededBy.Valid {
			if target := ResearchSourceKindNode + ":" + util.UUIDToString(it.supersededBy); presentInGraph[target] {
				edges = append(edges, &memorygraph.Edge{
					EdgeID: "research_supersedes:" + n.NodeID,
					Type:   memorygraph.EdgeTypeSupersedes,
					From:   target,
					To:     n.NodeID,
				})
			}
		}
		if it.derivedFrom.Valid && presentInGraph[ResearchSourceKindNode+":"+util.UUIDToString(it.derivedFrom)] {
			edges = append(edges, &memorygraph.Edge{
				EdgeID: "research_derived:" + n.NodeID,
				Type:   memorygraph.EdgeTypeDerivedFrom,
				From:   n.NodeID,
				To:     ResearchSourceKindNode + ":" + util.UUIDToString(it.derivedFrom),
			})
		}
	}

	// Append edges, deduped by deterministic id against the existing set.
	existingByID := map[string]bool{}
	for _, e2 := range g.RelationEdges() {
		existingByID[e2.EdgeID] = true
	}
	_, rel, err := store.LoadEdges(v)
	if err != nil {
		return 0, 0, err
	}
	edgesWritten := 0
	for _, e2 := range edges {
		if existingByID[e2.EdgeID] {
			continue
		}
		if !presentInGraph[e2.From] || !presentInGraph[e2.To] {
			continue // endpoint is not part of the memory graph
		}
		existingByID[e2.EdgeID] = true
		e2.CreatedBy = memorygraph.CreatorIngester
		e2.CreatedVersion = v
		rel = append(rel, e2)
		edgesWritten++
	}
	if err := store.SaveEdges(v, nil, rel); err != nil {
		return 0, 0, err
	}
	if err := store.SwitchCurrent(v); err != nil {
		return 0, 0, err
	}
	return nodesWritten, edgesWritten, nil
}

// saveState advances the watermark and upserts export keys in one
// transaction, only after the graph write succeeded.
func (e *ResearchGraphExporter) saveState(ctx context.Context, workspaceID pgtype.UUID, cursor time.Time, items []researchExportItem) error {
	tx, err := e.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `
		INSERT INTO research_graph_export_state (workspace_id, node_cursor, updated_at)
		VALUES ($1, $2, now())
		ON CONFLICT (workspace_id) DO UPDATE
		SET node_cursor = GREATEST(research_graph_export_state.node_cursor, $2), updated_at = now()`,
		workspaceID, cursor); err != nil {
		return fmt.Errorf("research export: save state: %w", err)
	}
	for _, it := range items {
		hash := it.sourceHash
		if hash == "" {
			hash = memorygraph.ComputeContentHash(it.node.Body)
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO research_graph_export_key (workspace_id, source_kind, source_id, content_hash, memory_node_id, exported_at)
			VALUES ($1, $2, $3, $4, $5, now())
			ON CONFLICT (workspace_id, source_kind, source_id) DO UPDATE
			SET content_hash = EXCLUDED.content_hash, memory_node_id = EXCLUDED.memory_node_id, exported_at = now()`,
			workspaceID, it.kind, it.sourceID, hash, it.node.NodeID); err != nil {
			return fmt.Errorf("research export: save key: %w", err)
		}
	}
	return tx.Commit(ctx)
}

func mergeIDSets(a, b []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(a)+len(b))
	for _, s := range append(append([]string{}, a...), b...) {
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
