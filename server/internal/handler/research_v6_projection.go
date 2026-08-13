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
	FromSequenceExclusive int64                      `json:"from_sequence_exclusive"`
	ThroughSequence       int64                      `json:"through_sequence"`
	NodeUpserts           []researchV6ProjectionNode `json:"node_upserts"`
	EdgeUpserts           []researchV6ProjectionEdge `json:"edge_upserts"`
	NodeTombstones        []string                   `json:"node_tombstones"`
	EdgeTombstones        []string                   `json:"edge_tombstones"`
	AffectedRootNodeIDs   []string                   `json:"affected_root_node_ids"`
	TransitionKind        *string                    `json:"transition_kind"`
}
type researchV6Snapshot struct {
	SnapshotID           string                     `json:"snapshot_id"`
	RunID                string                     `json:"run_id"`
	ThroughEventSequence int64                      `json:"through_event_sequence"`
	GraphContentHash     map[string]string          `json:"graph_content_hash"`
	Nodes                []researchV6ProjectionNode `json:"nodes"`
	Edges                []researchV6ProjectionEdge `json:"edges"`
	NextCursor           *string                    `json:"next_cursor"`
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
	nodeIDs := make(map[string]string, len(legacyNodes))
	nodes := make([]researchV6ProjectionNode, 0, len(legacyNodes))
	for _, n := range legacyNodes {
		mapped := mapResearchV6Node(runID, n)
		nodeIDs[n.ID] = mapped.ID
		nodes = append(nodes, mapped)
	}
	edges := make([]researchV6ProjectionEdge, 0, len(legacyEdges))
	for _, e := range legacyEdges {
		from, fromOK := nodeIDs[e.FromNodeID]
		to, toOK := nodeIDs[e.ToNodeID]
		if !fromOK || !toOK {
			continue
		}
		edges = append(edges, researchV6ProjectionEdge{ID: runID + ":edge:" + e.ID, RunID: runID, FromNodeID: from, ToNodeID: to, EdgeType: e.EdgeType})
	}
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].ID < nodes[j].ID })
	sort.Slice(edges, func(i, j int) bool { return edges[i].ID < edges[j].ID })
	nodeBytes, _ := json.Marshal(nodes)
	edgeBytes, _ := json.Marshal(edges)
	nh := sha256.Sum256(nodeBytes)
	eh := sha256.Sum256(edgeBytes)
	snapshotSeed := fmt.Sprintf("%s:%d:%x:%x", runID, sequence, nh, eh)
	sh := sha256.Sum256([]byte(snapshotSeed))
	return researchV6Snapshot{SnapshotID: "sha256:" + hex.EncodeToString(sh[:]), RunID: runID, ThroughEventSequence: sequence, GraphContentHash: map[string]string{"nodes": "sha256:" + hex.EncodeToString(nh[:]), "edges": "sha256:" + hex.EncodeToString(eh[:])}, Nodes: nodes, Edges: edges, NextCursor: nil}, nil
}

func mapResearchV6Node(runID string, node ResearchGraphNodeResp) researchV6ProjectionNode {
	kind := "generic"
	entityID := node.ID
	var detail map[string]any
	_ = json.Unmarshal(node.Payload, &detail)
	if raw, ok := detail["kind"].(string); ok && raw != "" {
		kind = normalizeResearchV6EntityKind(raw)
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
	return researchV6ProjectionNode{ID: id, RunID: runID, EntityKind: kind, EntityID: entityID, NodeKind: kind, NodeSubtype: node.NodeType, SchemaVersion: 1, Title: node.Title, Summary: node.Summary, Status: node.Status, Importance: 0.5, ActorAgentID: node.ActorAgentID, CreatedAt: &created, UpdatedAt: &updated, Detail: detail}
}

func normalizeResearchV6EntityKind(raw string) string {
	switch strings.TrimSpace(raw) {
	case "root", "generic", "gate",
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
		LastConfirmedSequence int64 `json:"last_confirmed_sequence"`
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
	if req.LastConfirmedSequence > snap.ThroughEventSequence {
		writeJSON(w, 200, map[string]any{"ok": false, "resync_required": true})
		return
	}
	delta := researchV6Delta{FromSequenceExclusive: req.LastConfirmedSequence, ThroughSequence: snap.ThroughEventSequence, NodeUpserts: []researchV6ProjectionNode{}, EdgeUpserts: []researchV6ProjectionEdge{}, NodeTombstones: []string{}, EdgeTombstones: []string{}, AffectedRootNodeIDs: []string{}}
	if req.LastConfirmedSequence < snap.ThroughEventSequence {
		delta.NodeUpserts = snap.Nodes
		delta.EdgeUpserts = snap.Edges
		delta.AffectedRootNodeIDs = researchV6RootIDs(snap.Nodes)
	}
	writeJSON(w, 200, map[string]any{"ok": true, "delta": delta})
}
func researchV6RootIDs(nodes []researchV6ProjectionNode) []string {
	out := []string{}
	for _, n := range nodes {
		if n.EntityKind == "root" || n.NodeSubtype == "goal" {
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
