package handler

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/multica-ai/multica/server/internal/researchrun"
)

type researchV6ProjectionNode struct {
	ID               string   `json:"id"`
	RunID            string   `json:"run_id"`
	EntityKind       string   `json:"entity_kind"`
	EntityID         string   `json:"entity_id"`
	NodeKind         string   `json:"node_kind"`
	NodeSubtype      string   `json:"node_subtype"`
	SchemaVersion    int      `json:"schema_version"`
	Title            string   `json:"title"`
	Summary          string   `json:"summary"`
	Status           string   `json:"status"`
	Importance       float64  `json:"importance"`
	Level            string   `json:"level"`
	ClusterID        *string  `json:"cluster_id"`
	ParentID         *string  `json:"parent_id"`
	Round            int32    `json:"round"`
	Confidence       *float64 `json:"confidence"`
	DocumentCount    *int32   `json:"document_count"`
	ConclusionCount  *int32   `json:"conclusion_count"`
	DerivedFrom      *string  `json:"derived_from"`
	MergedFrom       []string `json:"merged_from"`
	SupersededBy     *string  `json:"superseded_by"`
	RestartOf        *string  `json:"restart_of"`
	InvalidatedBy    *string  `json:"invalidated_by"`
	Freshness        *string  `json:"freshness"`
	ContractVersion  *string  `json:"contract_version"`
	PlanVersion      *string  `json:"plan_version"`
	StrategyVersion  *string  `json:"strategy_version"`
	ActorAgentID     *string  `json:"actor_agent_id"`
	TaskID           *string  `json:"task_id"`
	AttemptID        *string  `json:"attempt_id"`
	CreatedAt        *string  `json:"created_at"`
	UpdatedAt        *string  `json:"updated_at"`
	Cost             *float64 `json:"cost,omitempty"`
	Detail           any      `json:"detail"`
	CreatedSequence  *int64   `json:"created_sequence"`
	UpdatedSequence  *int64   `json:"updated_sequence"`
	TerminalSequence *int64   `json:"terminal_sequence"`
}
type researchV6ProjectionCluster struct {
	ID              string   `json:"id"`
	Label           string   `json:"label"`
	ClusterType     string   `json:"cluster_type"`
	MemberNodeIDs   []string `json:"member_node_ids"`
	Confidence      *float64 `json:"confidence"`
	DocumentCount   *int32   `json:"document_count"`
	ConclusionCount *int32   `json:"conclusion_count"`
}
type researchV6ProjectionEdge struct {
	ID                   string `json:"id"`
	RunID                string `json:"run_id"`
	FromNodeID           string `json:"from_node_id"`
	ToNodeID             string `json:"to_node_id"`
	EdgeType             string `json:"edge_type"`
	CreatedSequence      *int64 `json:"created_sequence"`
	TombstonedAtSequence *int64 `json:"tombstoned_at_sequence"`
}
type researchV6Delta struct {
	FromSequenceExclusive int64                         `json:"from_sequence_exclusive"`
	ThroughSequence       int64                         `json:"through_sequence"`
	NodeUpserts           []researchV6ProjectionNode    `json:"node_upserts"`
	EdgeUpserts           []researchV6ProjectionEdge    `json:"edge_upserts"`
	NodeTombstones        []string                      `json:"node_tombstones"`
	EdgeTombstones        []string                      `json:"edge_tombstones"`
	ClusterUpserts        []researchV6ProjectionCluster `json:"cluster_upserts"`
	ClusterTombstones     []string                      `json:"cluster_tombstones"`
	AffectedRootNodeIDs   []string                      `json:"affected_root_node_ids"`
	TransitionKind        *string                       `json:"transition_kind"`
}
type researchV6DeltaEnvelope struct {
	RunID string          `json:"run_id"`
	Delta researchV6Delta `json:"delta"`
}
type researchV6Snapshot struct {
	SnapshotID           string                        `json:"snapshot_id"`
	RunID                string                        `json:"run_id"`
	ThroughEventSequence int64                         `json:"through_event_sequence"`
	GraphContentHash     map[string]string             `json:"graph_content_hash"`
	Nodes                []researchV6ProjectionNode    `json:"nodes"`
	Edges                []researchV6ProjectionEdge    `json:"edges"`
	Clusters             []researchV6ProjectionCluster `json:"clusters"`
	NextCursor           *string                       `json:"next_cursor"`
}

type researchV6ResumeCursor struct {
	SnapshotID            string
	LastConfirmedSequence int64
}

func researchV6ResumeRequiresResync(cursor researchV6ResumeCursor, current researchV6Snapshot, firstRetainedSequence int64) bool {
	if cursor.LastConfirmedSequence < 0 || cursor.LastConfirmedSequence > current.ThroughEventSequence {
		return true
	}
	if cursor.SnapshotID != "" && cursor.SnapshotID != current.SnapshotID {
		return true
	}
	// The first needed event is cursor+1 and must still exist in the retained
	// event window. A zero first sequence means the Run has no events.
	return firstRetainedSequence > 0 && firstRetainedSequence > cursor.LastConfirmedSequence+1
}

func (h *Handler) loadResearchV6Snapshot(r *http.Request) (researchV6Snapshot, error) {
	runID := strings.TrimSpace(chi.URLParam(r, "runId"))
	workspaceID := h.resolveWorkspaceID(r)
	if runID == "" || workspaceID == "" {
		return researchV6Snapshot{}, researchrun.ErrRunNotFound
	}
	snap, err := h.ResearchRun.Snapshot(r.Context(), runID, workspaceID)
	if err != nil {
		return researchV6Snapshot{}, err
	}
	legacyNodes, legacyEdges := projectRunV2Graph(snap)
	var sequence int64
	if err = h.DB.QueryRow(r.Context(), `SELECT COALESCE(max(sequence),0) FROM research_run_event WHERE workspace_id=$1::uuid AND session_id=$2::uuid`, workspaceID, runID).Scan(&sequence); err != nil {
		return researchV6Snapshot{}, err
	}
	typedGraph := projectRunV2TypedGraph(snap, 0, 0, false)
	typedByID := make(map[string]ResearchGraphTypedNodeResp, len(typedGraph.Nodes))
	for _, node := range typedGraph.Nodes {
		typedByID[node.ID] = node
	}
	nodeIDs := make(map[string]string, len(legacyNodes))
	nodes := make([]researchV6ProjectionNode, 0, len(legacyNodes))
	for _, n := range legacyNodes {
		typed := typedByID[n.ID]
		mapped := mapResearchV6NodeWithSemantics(runID, n, &typed)
		nodeIDs[n.ID] = mapped.ID
		nodes = append(nodes, mapped)
	}
	for index := range nodes {
		remapResearchV6NodeReferences(&nodes[index], nodeIDs)
	}
	edges := make([]researchV6ProjectionEdge, 0, len(legacyEdges))
	for _, e := range legacyEdges {
		from, fromOK := nodeIDs[e.FromNodeID]
		to, toOK := nodeIDs[e.ToNodeID]
		if !fromOK || !toOK {
			continue
		}
		edges = append(edges, researchV6ProjectionEdge{ID: runID + ":edge:" + e.ID, RunID: runID, FromNodeID: from, ToNodeID: to, EdgeType: normalizeResearchV6EdgeType(e.EdgeType)})
	}
	clusters := projectResearchV6Clusters(typedGraph.Clusters, typedGraph.Nodes, nodeIDs)
	canonicalNodes, canonicalEdges, err := h.loadResearchV6InquiryProjection(r.Context(), workspaceID, runID)
	if err != nil {
		return researchV6Snapshot{}, err
	}
	nodeByID := make(map[string]researchV6ProjectionNode, len(nodes)+len(canonicalNodes))
	for _, node := range nodes {
		nodeByID[node.ID] = node
	}
	for _, node := range canonicalNodes {
		nodeByID[node.ID] = node
	}
	nodes = nodes[:0]
	for _, node := range nodeByID {
		nodes = append(nodes, node)
	}
	edgeByID := make(map[string]researchV6ProjectionEdge, len(edges)+len(canonicalEdges))
	for _, edge := range edges {
		edgeByID[edge.ID] = edge
	}
	for _, edge := range canonicalEdges {
		if _, fromOK := nodeByID[edge.FromNodeID]; !fromOK {
			continue
		}
		if _, toOK := nodeByID[edge.ToNodeID]; !toOK {
			continue
		}
		edgeByID[edge.ID] = edge
	}
	edges = edges[:0]
	for _, edge := range edgeByID {
		edges = append(edges, edge)
	}
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].ID < nodes[j].ID })
	sort.Slice(edges, func(i, j int) bool { return edges[i].ID < edges[j].ID })
	nodeBytes, _ := json.Marshal(nodes)
	edgeBytes, _ := json.Marshal(edges)
	clusterBytes, _ := json.Marshal(clusters)
	nh := sha256.Sum256(nodeBytes)
	eh := sha256.Sum256(edgeBytes)
	ch := sha256.Sum256(clusterBytes)
	snapshotSeed := fmt.Sprintf("%s:%d:%x:%x:%x", runID, sequence, nh, eh, ch)
	sh := sha256.Sum256([]byte(snapshotSeed))
	return researchV6Snapshot{SnapshotID: "sha256:" + hex.EncodeToString(sh[:]), RunID: runID, ThroughEventSequence: sequence, GraphContentHash: map[string]string{"nodes": "sha256:" + hex.EncodeToString(nh[:]), "edges": "sha256:" + hex.EncodeToString(eh[:]), "clusters": "sha256:" + hex.EncodeToString(ch[:])}, Nodes: nodes, Edges: edges, Clusters: clusters, NextCursor: nil}, nil
}

func remapResearchV6NodeReferences(node *researchV6ProjectionNode, nodeIDs map[string]string) {
	if node == nil {
		return
	}
	remap := func(value **string) {
		if *value == nil {
			return
		}
		if mapped := nodeIDs[**value]; mapped != "" {
			*value = &mapped
		} else {
			*value = nil
		}
	}
	remap(&node.ParentID)
	remap(&node.DerivedFrom)
	remap(&node.SupersededBy)
	remap(&node.RestartOf)
	remap(&node.InvalidatedBy)
	merged := make([]string, 0, len(node.MergedFrom))
	for _, id := range node.MergedFrom {
		if mapped := nodeIDs[id]; mapped != "" {
			merged = append(merged, mapped)
		}
	}
	node.MergedFrom = merged
}

// mapResearchV6Graph is the single V5/run-v2 to V6 identity boundary used by
// both HTTP snapshots and realtime deltas. Keeping it shared prevents a live
// frame from naming a node or edge differently from the subsequent resync.
func mapResearchV6Graph(runID string, legacyNodes []ResearchGraphNodeResp, legacyEdges []ResearchGraphEdgeResp) ([]researchV6ProjectionNode, []researchV6ProjectionEdge) {
	nodeIDs := make(map[string]string, len(legacyNodes))
	nodes := make([]researchV6ProjectionNode, 0, len(legacyNodes))
	for _, node := range legacyNodes {
		mapped := mapResearchV6Node(runID, node)
		nodeIDs[node.ID] = mapped.ID
		nodes = append(nodes, mapped)
	}
	edges := make([]researchV6ProjectionEdge, 0, len(legacyEdges))
	for _, edge := range legacyEdges {
		from, fromOK := nodeIDs[edge.FromNodeID]
		to, toOK := nodeIDs[edge.ToNodeID]
		if !fromOK || !toOK {
			continue
		}
		edges = append(edges, researchV6ProjectionEdge{
			ID: runID + ":edge:" + edge.ID, RunID: runID,
			FromNodeID: from, ToNodeID: to, EdgeType: edge.EdgeType,
		})
	}
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].ID < nodes[j].ID })
	sort.Slice(edges, func(i, j int) bool { return edges[i].ID < edges[j].ID })
	return nodes, edges
}

// mapResearchV6GraphStrict is the integrity boundary between the compatibility
// projection and V6. A Snapshot must never conceal ambiguous identity or a
// broken topology: clients use its hashes as proof of one rebuildable graph.
func mapResearchV6GraphStrict(runID string, legacyNodes []ResearchGraphNodeResp, legacyEdges []ResearchGraphEdgeResp) ([]researchV6ProjectionNode, []researchV6ProjectionEdge, error) {
	if strings.TrimSpace(runID) == "" {
		return nil, nil, fmt.Errorf("research V6 projection run identity is empty")
	}
	nodeIDs := make(map[string]string, len(legacyNodes))
	canonicalNodeIDs := make(map[string]string, len(legacyNodes))
	nodes := make([]researchV6ProjectionNode, 0, len(legacyNodes))
	for _, node := range legacyNodes {
		if strings.TrimSpace(node.ID) == "" {
			return nil, nil, fmt.Errorf("research V6 projection contains an empty source node identity")
		}
		if _, duplicate := nodeIDs[node.ID]; duplicate {
			return nil, nil, fmt.Errorf("research V6 projection contains duplicate source node %q", node.ID)
		}
		mapped := mapResearchV6Node(runID, node)
		if prior, duplicate := canonicalNodeIDs[mapped.ID]; duplicate {
			return nil, nil, fmt.Errorf("research V6 projection nodes %q and %q collapse to canonical identity %q", prior, node.ID, mapped.ID)
		}
		nodeIDs[node.ID] = mapped.ID
		canonicalNodeIDs[mapped.ID] = node.ID
		nodes = append(nodes, mapped)
	}
	edgeIDs := make(map[string]string, len(legacyEdges))
	edges := make([]researchV6ProjectionEdge, 0, len(legacyEdges))
	for _, edge := range legacyEdges {
		if strings.TrimSpace(edge.ID) == "" {
			return nil, nil, fmt.Errorf("research V6 projection contains an empty source edge identity")
		}
		canonicalID := runID + ":edge:" + edge.ID
		if prior, duplicate := edgeIDs[canonicalID]; duplicate {
			return nil, nil, fmt.Errorf("research V6 projection edges %q and %q collapse to canonical identity %q", prior, edge.ID, canonicalID)
		}
		from, fromOK := nodeIDs[edge.FromNodeID]
		to, toOK := nodeIDs[edge.ToNodeID]
		if !fromOK || !toOK {
			return nil, nil, fmt.Errorf("research V6 projection edge %q has a dangling endpoint", edge.ID)
		}
		if strings.TrimSpace(edge.EdgeType) == "" {
			return nil, nil, fmt.Errorf("research V6 projection edge %q has an empty type", edge.ID)
		}
		edgeIDs[canonicalID] = edge.ID
		edges = append(edges, researchV6ProjectionEdge{ID: canonicalID, RunID: runID, FromNodeID: from, ToNodeID: to, EdgeType: edge.EdgeType})
	}
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].ID < nodes[j].ID })
	sort.Slice(edges, func(i, j int) bool { return edges[i].ID < edges[j].ID })
	return nodes, edges, nil
}

func mapResearchV6Node(runID string, node ResearchGraphNodeResp) researchV6ProjectionNode {
	return mapResearchV6NodeWithSemantics(runID, node, nil)
}

func mapResearchV6NodeWithSemantics(runID string, node ResearchGraphNodeResp, typed *ResearchGraphTypedNodeResp) researchV6ProjectionNode {
	kind := "generic"
	entityID := node.ID
	var detail map[string]any
	_ = json.Unmarshal(node.Payload, &detail)
	if raw, ok := detail["kind"].(string); ok && raw != "" {
		kind = normalizeResearchV6EntityKind(raw)
	}
	if kind == "root" || node.NodeType == "goal" {
		kind = "goal"
	}
	if kind != "generic" {
		for _, key := range []string{"task_id", "attempt_id", "question_id", "claim_id"} {
			if raw, ok := detail[key].(string); ok && raw != "" {
				entityID = raw
				break
			}
		}
	}
	id := runID + ":" + kind + ":" + entityID
	created := node.CreatedAt
	updated := node.UpdatedAt
	projected := researchV6ProjectionNode{ID: id, RunID: runID, EntityKind: kind, EntityID: entityID, NodeKind: kind, NodeSubtype: node.NodeType, SchemaVersion: 1, Title: node.Title, Summary: node.Summary, Status: node.Status, Importance: normalizeResearchV6Importance(node.Confidence), Level: "m", Round: 1, MergedFrom: []string{}, ActorAgentID: node.ActorAgentID, CreatedAt: &created, UpdatedAt: &updated, Detail: detail}
	if kind == "goal" {
		projected.NodeSubtype = "goal"
		if node.Status == "abandoned" {
			projected.Status = "abandoned"
		} else {
			projected.Status = "active"
		}
	}
	if typed != nil && typed.ID != "" {
		projected.Level = strings.ToLower(typed.Level)
		projected.Round = typed.Round
		projected.ClusterID = typed.ClusterID
		projected.ParentID = typed.ParentID
		projected.Confidence = typed.Confidence
		projected.DerivedFrom = typed.DerivedFrom
		projected.MergedFrom = append([]string(nil), typed.MergedFrom...)
		projected.SupersededBy = typed.SupersededBy
		projected.RestartOf = typed.RestartOf
		projected.InvalidatedBy = typed.InvalidatedBy
		if typed.DocumentCount > 0 {
			value := typed.DocumentCount
			projected.DocumentCount = &value
		}
		if typed.ConclusionCount > 0 {
			value := typed.ConclusionCount
			projected.ConclusionCount = &value
		}
	}
	if kind == "goal" {
		projected.Level = "m"
	}
	return projected
}

func normalizeResearchV6Importance(confidence *float64) float64 {
	if confidence == nil || *confidence < 0 || *confidence > 1 {
		return 0.5
	}
	return *confidence
}

func normalizeResearchV6EdgeType(edgeType string) string {
	switch edgeType {
	case "leads_to", "deepens":
		return "decomposes"
	case "merged_from":
		return "integrates"
	case "invalidated_by":
		return "invalidates"
	case "superseded_by":
		return "supersedes"
	default:
		return edgeType
	}
}

func normalizeResearchV6EntityKind(raw string) string {
	switch strings.TrimSpace(raw) {
	case "goal", "root", "generic", "gate",
		"task", "attempt", "result_artifact", "search_plan", "query_execution",
		"source_candidate", "screening_decision", "source_snapshot", "observation",
		"claim", "question", "hypothesis", "branch", "insight", "insight_derivation",
		"integration_round", "integration_contribution", "dispute", "dispute_position",
		"deliberation", "deliberation_turn", "decision", "team_formation", "team_membership",
		"divergence_pass", "capability_observation", "report_revision", "evaluation_defect",
		"monitoring_cycle", "episode":
		return strings.TrimSpace(raw)
	default:
		return "generic"
	}
}

func projectResearchV6Clusters(clusters []ResearchGraphClusterResp, typedNodes []ResearchGraphTypedNodeResp, nodeIDs map[string]string) []researchV6ProjectionCluster {
	members := make(map[string][]ResearchGraphTypedNodeResp, len(clusters))
	for _, node := range typedNodes {
		if node.ClusterID != nil {
			members[*node.ClusterID] = append(members[*node.ClusterID], node)
		}
	}
	out := make([]researchV6ProjectionCluster, 0, len(clusters))
	for _, cluster := range clusters {
		canonicalType := cluster.ClusterType == "stable_result" || cluster.ClusterType == "exploration" || cluster.ClusterType == "new_frontier"
		clusterType := cluster.ClusterType
		if !canonicalType {
			clusterType = "exploration"
		}
		projected := researchV6ProjectionCluster{ID: cluster.ID, Label: cluster.Label, ClusterType: clusterType, MemberNodeIDs: []string{}}
		var confidenceTotal float64
		confidenceCount := 0
		var documents, conclusions int32
		stable := len(members[cluster.ID]) > 0
		frontier := false
		for _, node := range members[cluster.ID] {
			if id := nodeIDs[node.ID]; id != "" {
				projected.MemberNodeIDs = append(projected.MemberNodeIDs, id)
			}
			if node.Confidence != nil {
				confidenceTotal += *node.Confidence
				confidenceCount++
			}
			documents += node.DocumentCount
			conclusions += node.ConclusionCount
			stable = stable && (node.Status == "done" || node.Status == "succeeded" || node.Status == "accepted" || node.Status == "stable")
			frontier = frontier || node.RestartOf != nil
		}
		if !canonicalType && frontier {
			projected.ClusterType = "new_frontier"
		} else if !canonicalType && stable {
			projected.ClusterType = "stable_result"
		}
		if confidenceCount > 0 {
			value := confidenceTotal / float64(confidenceCount)
			projected.Confidence = &value
		}
		if documents > 0 {
			projected.DocumentCount = &documents
		}
		if conclusions > 0 {
			projected.ConclusionCount = &conclusions
		}
		sort.Strings(projected.MemberNodeIDs)
		if len(projected.MemberNodeIDs) > 0 {
			out = append(out, projected)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func (h *Handler) GetResearchV6ProjectionSnapshot(w http.ResponseWriter, r *http.Request) {
	if h.ResearchRun == nil {
		writeError(w, 503, "research run engine is unavailable")
		return
	}
	snap, err := h.loadResearchV6Snapshot(r)
	if err != nil {
		writeResearchV6Error(w, err)
		return
	}
	if rawLimit := strings.TrimSpace(r.URL.Query().Get("limit")); rawLimit != "" {
		limit, parseErr := strconv.Atoi(rawLimit)
		if parseErr != nil {
			writeError(w, http.StatusBadRequest, "snapshot limit must be an integer")
			return
		}
		snap, err = paginateResearchV6Snapshot(snap, limit, strings.TrimSpace(r.URL.Query().Get("cursor")))
		if err != nil {
			if strings.Contains(err.Error(), "resync") {
				writeError(w, http.StatusConflict, err.Error())
			} else {
				writeError(w, http.StatusBadRequest, err.Error())
			}
			return
		}
	} else if strings.TrimSpace(r.URL.Query().Get("cursor")) != "" {
		writeError(w, http.StatusBadRequest, "snapshot cursor requires limit")
		return
	}
	writeJSON(w, 200, snap)
}
func (h *Handler) GetResearchV6ProjectionDeltas(w http.ResponseWriter, r *http.Request) {
	snap, err := h.loadResearchV6Snapshot(r)
	if err != nil {
		writeResearchV6Error(w, err)
		return
	}
	from, err := strconv.ParseInt(r.URL.Query().Get("from_sequence_exclusive"), 10, 64)
	if err != nil || from < 0 {
		writeError(w, 400, "from_sequence_exclusive must be a non-negative integer")
		return
	}
	if from > snap.ThroughEventSequence {
		writeError(w, 409, "projection cursor is ahead of run")
		return
	}
	if from == snap.ThroughEventSequence {
		writeJSON(w, 200, nil)
		return
	}
	rows, err := h.DB.Query(r.Context(), `
		SELECT sequence, event_type, payload
		FROM research_run_event
		WHERE workspace_id=$1::uuid AND session_id=$2::uuid AND sequence > $3
		ORDER BY sequence
		LIMIT $4
	`, h.resolveWorkspaceID(r), strings.TrimSpace(chi.URLParam(r, "runId")), from, researchV6MaximumDeltaEvents+1)
	if err != nil {
		writeResearchV6Error(w, err)
		return
	}
	defer rows.Close()
	events := []researchV6ProjectionEvent{}
	for rows.Next() {
		var event researchV6ProjectionEvent
		if err = rows.Scan(&event.Sequence, &event.Type, &event.Payload); err != nil {
			writeResearchV6Error(w, err)
			return
		}
		events = append(events, event)
	}
	if err = rows.Err(); err != nil {
		writeResearchV6Error(w, err)
		return
	}
	delta, safe := buildResearchV6EventDelta(snap, from, events)
	if !safe {
		writeError(w, http.StatusConflict, "projection delta cannot be reconstructed safely; snapshot resync required")
		return
	}
	writeJSON(w, http.StatusOK, delta)
}
func (h *Handler) PostResearchV6ProjectionResume(w http.ResponseWriter, r *http.Request) {
	var req struct {
		SnapshotID            string `json:"snapshot_id,omitempty"`
		LastConfirmedSequence int64  `json:"last_confirmed_sequence"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.LastConfirmedSequence < 0 {
		writeError(w, 400, "last_confirmed_sequence must be a non-negative integer")
		return
	}
	snap, err := h.loadResearchV6Snapshot(r)
	if err != nil {
		writeResearchV6Error(w, err)
		return
	}
	var firstRetainedSequence int64
	if err = h.DB.QueryRow(r.Context(), `
		SELECT COALESCE(min(sequence), 0)
		FROM research_run_event
		WHERE workspace_id=$1::uuid AND session_id=$2::uuid
	`, h.resolveWorkspaceID(r), strings.TrimSpace(chi.URLParam(r, "runId"))).Scan(&firstRetainedSequence); err != nil {
		writeResearchV6Error(w, err)
		return
	}
	if researchV6ResumeRequiresResync(researchV6ResumeCursor{SnapshotID: req.SnapshotID, LastConfirmedSequence: req.LastConfirmedSequence}, snap, firstRetainedSequence) {
		writeJSON(w, 200, map[string]any{"ok": false, "resync_required": true})
		return
	}
	delta := researchV6Delta{FromSequenceExclusive: req.LastConfirmedSequence, ThroughSequence: snap.ThroughEventSequence, NodeUpserts: []researchV6ProjectionNode{}, EdgeUpserts: []researchV6ProjectionEdge{}, NodeTombstones: []string{}, EdgeTombstones: []string{}, ClusterUpserts: []researchV6ProjectionCluster{}, ClusterTombstones: []string{}, AffectedRootNodeIDs: []string{}}
	if req.LastConfirmedSequence < snap.ThroughEventSequence {
		delta.NodeUpserts = snap.Nodes
		delta.EdgeUpserts = snap.Edges
		delta.ClusterUpserts = snap.Clusters
		delta.AffectedRootNodeIDs = researchV6RootIDs(snap.Nodes)
	}
	writeJSON(w, 200, map[string]any{"ok": true, "delta": delta})
}
func researchV6RootIDs(nodes []researchV6ProjectionNode) []string {
	out := []string{}
	for _, n := range nodes {
		if n.EntityKind == "goal" || n.EntityKind == "root" || n.NodeSubtype == "goal" {
			out = append(out, n.ID)
		}
	}
	return out
}
func writeResearchV6Error(w http.ResponseWriter, err error) {
	if errors.Is(err, researchrun.ErrRunNotFound) || errors.Is(err, pgx.ErrNoRows) {
		writeError(w, 404, "research V6 run not found")
		return
	}
	writeError(w, 500, "failed to load research V6 projection")
}
