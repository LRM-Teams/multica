package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// LRM-1505 typed graph model: nodes carry stable literal fields (level/round/
// cluster/confidence/counts) and lineage fields (derived_from/merged_from/
// superseded_by/restart_of/invalidated_by) in the database, not only in the
// loose JSON payload. This file provides the typed Graph GET (one render pass
// for the star-map canvas) and the server-side atomic merge command.

// mergeEdgeType is the semantic edge written from each input node to the
// conclusion that unified them (LRM-1505).
const mergeEdgeType = "merged_from"

// ---------------------------------------------------------------------------
// Response shapes (typed contract for the frontend)
// ---------------------------------------------------------------------------

type ResearchGraphTypedNodeResp struct {
	ID              string          `json:"id"`
	SessionID       string          `json:"session_id"`
	NodeType        string          `json:"node_type"`
	Title           string          `json:"title"`
	Summary         string          `json:"summary"`
	Status          string          `json:"status"`
	ActorAgentID    *string         `json:"actor_agent_id"`
	Payload         json.RawMessage `json:"payload"`
	Level           string          `json:"level"`
	Round           int32           `json:"round"`
	ClusterID       *string         `json:"cluster_id"`
	Confidence      *float64        `json:"confidence,omitempty"`
	DocumentCount   int32           `json:"document_count"`
	ConclusionCount int32           `json:"conclusion_count"`
	GoalVersionID   *string         `json:"goal_version_id"`
	// Lineage (single-parent semantics via FKs; merged_from is the multi-input set).
	DerivedFrom   *string  `json:"derived_from"`
	MergedFrom    []string `json:"merged_from"`
	SupersededBy  *string  `json:"superseded_by"`
	RestartOf     *string  `json:"restart_of"`
	InvalidatedBy *string  `json:"invalidated_by"`
	SupersededAt  *string  `json:"superseded_at,omitempty"`
	InvalidatedAt *string  `json:"invalidated_at,omitempty"`
	// Tree/display projection (leads_to single-parent tree + typed lineage index).
	ParentID   *string  `json:"parent_id"`
	ChildIDs   []string `json:"child_ids"`
	ChildrenOf []string `json:"children_of"` // nodes for which this node is an input (parents that converge into this conclusion)
	CreatedAt  string   `json:"created_at"`
	UpdatedAt  string   `json:"updated_at"`
}

type ResearchGraphClusterResp struct {
	ID            string          `json:"id"`
	SessionID     string          `json:"session_id"`
	Name          string          `json:"name"`
	Label         string          `json:"label"`
	Level         string          `json:"level"`
	ClusterType   string          `json:"cluster_type"`
	GoalVersionID *string         `json:"goal_version_id"`
	Payload       json.RawMessage `json:"payload"`
	CreatedAt     string          `json:"created_at"`
	UpdatedAt     string          `json:"updated_at"`
}

// ResearchGraphLineageResp indexes lineage relationships so the canvas can
// render derived/merged/superseded links without re-deriving them from edges.
type ResearchGraphLineageResp struct {
	// Derived maps child -> its single derived_from parent.
	Derived map[string]string `json:"derived"`
	// Merged maps conclusion -> its input node ids (multi-to-one).
	Merged map[string][]string `json:"merged"`
	// Superseded maps old node -> the node that superseded it.
	Superseded map[string]string `json:"superseded"`
	// Restarted maps new node -> the node it restarts.
	Restarted map[string]string `json:"restarted"`
	// Invalidated maps node -> the node that invalidated it.
	Invalidated map[string]string `json:"invalidated"`
	// OldSupersededByNode maps node id -> nodes it superseded (reverse index).
	Supersedes map[string][]string `json:"supersedes"`
}

type ResearchGraphTypedResp struct {
	SessionID    string                       `json:"session_id"`
	GraphVersion int64                        `json:"graph_version"`
	Nodes        []ResearchGraphTypedNodeResp `json:"nodes"`
	Edges        []ResearchGraphEdgeResp      `json:"edges"`
	Clusters     []ResearchGraphClusterResp   `json:"clusters"`
	Lineage      ResearchGraphLineageResp     `json:"lineage"`
}

// ---------------------------------------------------------------------------
// Graph GET — typed nodes/edges/clusters/lineage for one render pass.
// Legacy sessions safely degrade: typed columns have sensible defaults and the
// response is always renderable even if it predates the typed migration.
// ---------------------------------------------------------------------------

func (h *Handler) GetResearchGraphTyped(w http.ResponseWriter, r *http.Request) {
	workspaceID := h.resolveWorkspaceID(r)
	wsUUID, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace_id")
	if !ok {
		return
	}
	sessionID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "id")
	if !ok {
		return
	}
	if _, err := h.Queries.GetResearchSession(r.Context(), db.GetResearchSessionParams{
		ID:          sessionID,
		WorkspaceID: wsUUID,
	}); err != nil {
		writeError(w, http.StatusNotFound, "research session not found")
		return
	}

	nodes, err := h.Queries.ListResearchGraphNodesTyped(r.Context(), db.ListResearchGraphNodesTypedParams{
		SessionID:   sessionID,
		WorkspaceID: wsUUID,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load research graph nodes")
		return
	}
	edges, err := h.Queries.ListResearchGraphEdges(r.Context(), db.ListResearchGraphEdgesParams{
		SessionID:   sessionID,
		WorkspaceID: wsUUID,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load research graph edges")
		return
	}
	clusters, err := h.Queries.ListResearchGraphClusters(r.Context(), db.ListResearchGraphClustersParams{
		SessionID:   sessionID,
		WorkspaceID: wsUUID,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load research graph clusters")
		return
	}

	graphVersion, _ := h.Queries.GetResearchSessionGraphVersion(r.Context(), db.GetResearchSessionGraphVersionParams{
		ID:          sessionID,
		WorkspaceID: wsUUID,
	})

	resp := buildResearchGraphTypedResp(uuidToString(sessionID), graphVersion, nodes, edges, clusters)
	writeJSON(w, http.StatusOK, resp)
}

func mapTypedGraphNode(
	n db.ResearchGraphNode,
	sessionID string,
	parentOf map[string]string,
	childrenOf map[string][]string,
	mergedInputsOf map[string][]string,
) ResearchGraphTypedNodeResp {
	id := uuidToString(n.ID)
	merged := make([]string, 0, len(n.MergedFrom))
	for _, m := range n.MergedFrom {
		if s := uuidToString(m); s != "" {
			merged = append(merged, s)
		}
	}
	var parentID *string
	if p, ok := parentOf[id]; ok && p != "" {
		parentID = &p
	}
	childIDs := childrenOf[id]
	if childIDs == nil {
		childIDs = []string{}
	}
	children := mergedInputsOf[id]
	if children == nil {
		children = []string{}
	}
	return ResearchGraphTypedNodeResp{
		ID:              id,
		SessionID:       sessionID,
		NodeType:        n.NodeType,
		Title:           n.Title,
		Summary:         n.Summary,
		Status:          n.Status,
		ActorAgentID:    uuidToPtr(n.ActorAgentID),
		Payload:         json.RawMessage(n.Payload),
		Level:           n.Level,
		Round:           n.Round,
		ClusterID:       uuidToPtr(n.ClusterID),
		Confidence:      float8ToPtr(n.Confidence),
		DocumentCount:   n.DocumentCount,
		ConclusionCount: n.ConclusionCount,
		GoalVersionID:   uuidToPtr(n.GoalVersionID),
		DerivedFrom:     uuidToPtr(n.DerivedFrom),
		MergedFrom:      merged,
		SupersededBy:    uuidToPtr(n.SupersededBy),
		RestartOf:       uuidToPtr(n.RestartOf),
		InvalidatedBy:   uuidToPtr(n.InvalidatedBy),
		SupersededAt:    timestampToPtr(n.SupersededAt),
		InvalidatedAt:   timestampToPtr(n.InvalidatedAt),
		ParentID:        parentID,
		ChildIDs:        childIDs,
		ChildrenOf:      children,
		CreatedAt:       timestampToString(n.CreatedAt),
		UpdatedAt:       timestampToString(n.UpdatedAt),
	}
}

func buildResearchGraphTypedResp(
	sessionID string,
	graphVersion int64,
	nodes []db.ResearchGraphNode,
	edges []db.ResearchGraphEdge,
	clusters []db.ResearchGraphCluster,
) ResearchGraphTypedResp {
	parentOf, childrenOf := buildResearchTreeIndex(edges)

	// childrenOf maps "conclusion" -> input nodes converging into it, keyed by
	// the merged_from relationship (edge from input -> conclusion).
	mergedInputsOf := map[string][]string{}
	for _, n := range nodes {
		for _, id := range n.MergedFrom {
			kid := uuidToString(id)
			if kid == "" {
				continue
			}
			mergedInputsOf[uuidToString(n.ID)] = append(mergedInputsOf[uuidToString(n.ID)], kid)
		}
	}

	nodeResp := make([]ResearchGraphTypedNodeResp, 0, len(nodes))
	for _, n := range nodes {
		nodeResp = append(nodeResp, mapTypedGraphNode(n, sessionID, parentOf, childrenOf, mergedInputsOf))
	}

	clusterResp := make([]ResearchGraphClusterResp, 0, len(clusters))
	for _, c := range clusters {
		clusterResp = append(clusterResp, ResearchGraphClusterResp{
			ID:            uuidToString(c.ID),
			SessionID:     uuidToString(c.SessionID),
			Name:          c.Name,
			Label:         c.Label,
			Level:         c.Level,
			ClusterType:   c.ClusterType,
			GoalVersionID: uuidToPtr(c.GoalVersionID),
			Payload:       json.RawMessage(c.Payload),
			CreatedAt:     timestampToString(c.CreatedAt),
			UpdatedAt:     timestampToString(c.UpdatedAt),
		})
	}

	lineage := ResearchGraphLineageResp{
		Derived:     map[string]string{},
		Merged:      map[string][]string{},
		Superseded:  map[string]string{},
		Restarted:   map[string]string{},
		Invalidated: map[string]string{},
		Supersedes:  map[string][]string{},
	}
	for _, n := range nodes {
		id := uuidToString(n.ID)
		for _, d := range []struct {
			from *string
			to   map[string]string
		}{
			{uuidToPtr(n.DerivedFrom), lineage.Derived},
			{uuidToPtr(n.RestartOf), lineage.Restarted},
			{uuidToPtr(n.InvalidatedBy), lineage.Invalidated},
		} {
			if d.from != nil && *d.from != "" {
				d.to[id] = *d.from
			}
		}
		if s := uuidToPtr(n.SupersededBy); s != nil && *s != "" {
			lineage.Superseded[id] = *s
			lineage.Supersedes[*s] = append(lineage.Supersedes[*s], id)
		}
		if len(n.MergedFrom) > 0 {
			ins := make([]string, 0, len(n.MergedFrom))
			for _, m := range n.MergedFrom {
				if s := uuidToString(m); s != "" {
					ins = append(ins, s)
				}
			}
			lineage.Merged[id] = ins
		}
	}

	return ResearchGraphTypedResp{
		SessionID:    sessionID,
		GraphVersion: graphVersion,
		Nodes:        nodeResp,
		Edges:        mapEdges(edges),
		Clusters:     clusterResp,
		Lineage:      lineage,
	}
}

// ---------------------------------------------------------------------------
// Merge command — atomic, same-session, idempotent, audited.
//
//  1. Validates every input node belongs to <workspace, session>
//  2. Runs the whole mutation in one transaction so the conclusion node, the
//     merged_from edges, the superseded flags, the audit command row and the
//     graph_version bump either all commit or all roll back.
//  3. Idempotency: a merge with the same (workspace, session, idempotency_key)
//     returns the already-created conclusion instead of minting a second one.
//  4. Cross-session / cross-workspace references are rejected outright.
// ---------------------------------------------------------------------------

type mergeGraphRequest struct {
	InputNodeIDs   []string `json:"input_node_ids"`
	Title          string   `json:"title"`
	Summary        string   `json:"summary"`
	IDempotencyKey string   `json:"idempotency_key"`
	Reason         string   `json:"reason"`
	Level          string   `json:"level"`
	Confidence     *float64 `json:"confidence"`
}

func (h *Handler) PostResearchGraphMerge(w http.ResponseWriter, r *http.Request) {
	workspaceID := h.resolveWorkspaceID(r)
	wsUUID, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace_id")
	if !ok {
		return
	}
	member, ok := h.requireActiveFleetMember(w, r, wsUUID)
	if !ok {
		return
	}
	if h.TxStarter == nil {
		writeError(w, http.StatusServiceUnavailable, "database transaction service unavailable")
		return
	}
	sessionID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "id")
	if !ok {
		return
	}
	session, err := h.Queries.GetResearchSession(r.Context(), db.GetResearchSessionParams{
		ID:          sessionID,
		WorkspaceID: wsUUID,
	})
	if err != nil {
		writeError(w, http.StatusNotFound, "research session not found")
		return
	}
	if h.rejectLegacyResearchMutation(w, r, wsUUID, sessionID) {
		return
	}
	if session.Status == "paused" {
		writeError(w, http.StatusConflict, "research session is paused")
		return
	}

	var req mergeGraphRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if len(req.InputNodeIDs) < 2 {
		writeError(w, http.StatusBadRequest, "merge requires at least two input nodes")
		return
	}
	req.IDempotencyKey = strings.TrimSpace(req.IDempotencyKey)
	if req.IDempotencyKey == "" {
		writeError(w, http.StatusBadRequest, "idempotency_key is required for a merge command")
		return
	}
	level := strings.TrimSpace(req.Level)
	if level == "" {
		level = "M"
	}
	if !validResearchNodeLevel(level) {
		writeError(w, http.StatusBadRequest, "invalid level; must be one of XXL/XL/L/M/S")
		return
	}

	ctx := r.Context()
	actorType, actorID := h.mergeCommandActor(member)
	conclusion, _, graphVersion, exists, err := h.mergeNodesAtomic(ctx, wsUUID, sessionID, member, req, level, actorType, actorID)
	if err != nil {
		writeError(w, errStatus(err), err.Error())
		return
	}

	// Load the full typed render for the updated graph to publish.
	edgeRows, _ := h.Queries.ListResearchGraphEdges(ctx, db.ListResearchGraphEdgesParams{
		SessionID:   sessionID,
		WorkspaceID: wsUUID,
	})
	edgesForEvent := make([]ResearchGraphEdgeResp, 0, len(edgeRows))
	for _, e := range edgeRows {
		edgesForEvent = append(edgesForEvent, mapEdges([]db.ResearchGraphEdge{e})[0])
	}
	typedNode := mapTypedGraphNode(conclusion, uuidToString(sessionID), map[string]string{}, map[string][]string{}, map[string][]string{})

	h.publish(protocol.EventResearchSessionGraphUpdated, workspaceID, actorType, actorID, map[string]any{
		"session_id":    uuidToString(sessionID),
		"op":            "merge",
		"graph_version": graphVersion,
		"idempotent":    exists,
		"node":          typedNode,
		"edges":         edgesForEvent,
	})

	h.emitResearchProcessCard(ctx, workspaceID, wsUUID, sessionID, actorType, actorID, researchProcessEvent{
		Op:      "graph_merge",
		Title:   firstNonEmpty(req.Title, "融合结论"),
		Body:    "融合 · " + firstNonEmpty(req.Title, "多源结论已经融合") + "：" + truncateRunes(req.Summary, 120),
		ActorID: member.AgentID,
		Meta: map[string]any{
			"node_id":          uuidToString(conclusion.ID),
			"input_node_ids":   req.InputNodeIDs,
			"idempotency_key":  req.IDempotencyKey,
			"graph_version":    graphVersion,
			"replayed":         exists,
			"conclusion_count": conclusion.ConclusionCount,
		},
	})
	h.maybeRecordResearchUnattendedMutation(ctx, session, "graph_merge")

	if exists {
		writeJSON(w, http.StatusOK, map[string]any{
			"node":          typedNode,
			"graph_version": graphVersion,
			"replayed":      true,
			"duplicate":     true,
		})
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"node":          typedNode,
		"graph_version": graphVersion,
		"replayed":      false,
	})
}

// mergeNodesAtomic performs the whole merge in one DB transaction.
// Returns the conclusion node, the created edge ids, the new graph_version and
// whether this idempotency key was already handled.
func (h *Handler) mergeNodesAtomic(
	ctx context.Context,
	wsUUID, sessionID pgtype.UUID,
	member db.ResearchFleetMember,
	req mergeGraphRequest,
	level, actorType, actorID string,
) (db.ResearchGraphNode, []string, int64, bool, error) {
	tx, err := h.TxStarter.Begin(ctx)
	if err != nil {
		return db.ResearchGraphNode{}, nil, 0, false, pgErr("failed to begin merge transaction", http.StatusInternalServerError)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	qtx := h.Queries.WithTx(tx)

	// Idempotency: same (workspace, session, key) was already handled → return
	// the previously created conclusion without minting a new node.
	exists, err := qtx.ResearchGraphCommandExists(ctx, db.ResearchGraphCommandExistsParams{
		WorkspaceID:    wsUUID,
		SessionID:      sessionID,
		IdempotencyKey: req.IDempotencyKey,
	})
	if err != nil {
		return db.ResearchGraphNode{}, nil, 0, false, pgErr("failed to check merge idempotency", http.StatusInternalServerError)
	}
	if exists {
		cmd, err := qtx.GetResearchGraphCommandByKey(ctx, db.GetResearchGraphCommandByKeyParams{
			WorkspaceID:    wsUUID,
			SessionID:      sessionID,
			IdempotencyKey: req.IDempotencyKey,
		})
		if err != nil {
			return db.ResearchGraphNode{}, nil, 0, false, pgErr("failed to load prior merge command", http.StatusInternalServerError)
		}
		conclusion, err := qtx.GetResearchGraphNode(ctx, db.GetResearchGraphNodeParams{
			ID:          cmd.ResultNodeID,
			WorkspaceID: wsUUID,
		})
		if err != nil || !conclusion.ID.Valid {
			return db.ResearchGraphNode{}, nil, 0, false, pgErr("prior merge conclusion no longer exists", http.StatusConflict)
		}
		ver, _ := qtx.GetResearchSessionGraphVersion(ctx, db.GetResearchSessionGraphVersionParams{
			ID:          sessionID,
			WorkspaceID: wsUUID,
		})
		return conclusion, nil, ver, true, nil
	}

	// Same-session validation: every input node must belong to this exact
	// workspace+session. Cross-workspace/session references are rejected.
	inputIDs := make([]pgtype.UUID, 0, len(req.InputNodeIDs))
	seen := map[string]struct{}{}
	for _, raw := range req.InputNodeIDs {
		id := parseUUID(raw)
		if !id.Valid {
			return db.ResearchGraphNode{}, nil, 0, false, pgErr("invalid node id: "+raw, http.StatusBadRequest)
		}
		node, err := qtx.GetResearchGraphNode(ctx, db.GetResearchGraphNodeParams{
			ID:          id,
			WorkspaceID: wsUUID,
		})
		if err != nil {
			return db.ResearchGraphNode{}, nil, 0, false, pgErr("input node not found in this workspace: "+raw, http.StatusBadRequest)
		}
		if node.SessionID != sessionID {
			return db.ResearchGraphNode{}, nil, 0, false, pgErr("input node belongs to a different session: "+raw, http.StatusConflict)
		}
		if _, dup := seen[uuidToString(id)]; dup {
			continue
		}
		seen[uuidToString(id)] = struct{}{}
		inputIDs = append(inputIDs, id)
	}
	if len(inputIDs) < 2 {
		return db.ResearchGraphNode{}, nil, 0, false, pgErr("merge requires at least two distinct input nodes", http.StatusBadRequest)
	}

	// Create the conclusion node (node_type=conclusion) with merged_from = inputs.
	confidence := pgtype.Float8{}
	if req.Confidence != nil {
		confidence = pgtype.Float8{Float64: *req.Confidence, Valid: true}
	}
	round := int32(1)
	conclusion, err := qtx.CreateResearchGraphNodeTyped(ctx, db.CreateResearchGraphNodeTypedParams{
		WorkspaceID:     wsUUID,
		SessionID:       sessionID,
		NodeType:        "conclusion",
		Title:           strings.TrimSpace(req.Title),
		Summary:         req.Summary,
		Status:          "active",
		ActorAgentID:    member.AgentID,
		Level:           level,
		Round:           round,
		ClusterID:       pgtype.UUID{},
		Confidence:      confidence,
		DocumentCount:   0,
		ConclusionCount: int32(len(inputIDs)),
		GoalVersionID:   pgtype.UUID{},
		DerivedFrom:     pgtype.UUID{},
		MergedFrom:      inputIDs,
		SupersededBy:    pgtype.UUID{},
		RestartOf:       pgtype.UUID{},
		InvalidatedBy:   pgtype.UUID{},
		Payload:         marshalJSONRaw(map[string]any{"merge": true}),
	})
	if err != nil {
		return db.ResearchGraphNode{}, nil, 0, false, pgErr("failed to create conclusion node", http.StatusInternalServerError)
	}
	if err := ensureGraphNodePassportTx(ctx, tx, wsUUID, sessionID, conclusion.ID); err != nil {
		return db.ResearchGraphNode{}, nil, 0, false, pgErr("failed to register conclusion passport", http.StatusInternalServerError)
	}

	// Write merged_from edges (input -> conclusion) and mark each input superseded.
	edgeIDs := make([]string, 0, len(inputIDs))
	for _, inID := range inputIDs {
		e, eerr := qtx.CreateResearchGraphEdge(ctx, db.CreateResearchGraphEdgeParams{
			WorkspaceID: wsUUID,
			SessionID:   sessionID,
			FromNodeID:  inID,
			ToNodeID:    conclusion.ID,
			EdgeType:    mergeEdgeType,
		})
		if eerr != nil {
			return db.ResearchGraphNode{}, nil, 0, false, pgErr("failed to write merged_from edge", http.StatusInternalServerError)
		}
		if err := ensureGraphEdgePassportTx(ctx, tx, wsUUID, sessionID, e.ID); err != nil {
			return db.ResearchGraphNode{}, nil, 0, false, pgErr("failed to register merge edge passport", http.StatusInternalServerError)
		}
		edgeIDs = append(edgeIDs, uuidToString(e.ID))

		_, uerr := qtx.UpdateResearchGraphNodeTyped(ctx, db.UpdateResearchGraphNodeTypedParams{
			ID:            inID,
			WorkspaceID:   wsUUID,
			Status:        pgtype.Text{String: "superseded", Valid: true},
			Level:         pgtype.Text{},
			ClusterID:     pgtype.UUID{},
			Confidence:    pgtype.Float8{},
			Title:         pgtype.Text{},
			Summary:       pgtype.Text{},
			SupersededBy:  conclusion.ID,
			SupersededAt:  nowUTC(),
			MergedFrom:    []pgtype.UUID{},
			RestartOf:     pgtype.UUID{},
			InvalidatedBy: pgtype.UUID{},
		})
		if uerr != nil {
			return db.ResearchGraphNode{}, nil, 0, false, pgErr("failed to mark input node superseded", http.StatusInternalServerError)
		}
	}

	// Audit: record the command with its idempotency key + inputs + executor.
	if _, cerr := qtx.CreateResearchGraphCommand(ctx, db.CreateResearchGraphCommandParams{
		WorkspaceID:    wsUUID,
		SessionID:      sessionID,
		Op:             "merge",
		IdempotencyKey: req.IDempotencyKey,
		ResultNodeID:   conclusion.ID,
		InputNodeIds:   inputIDs,
		Reason:         req.Reason,
		ActorType:      actorType,
		ActorID:        memberActorID(member),
		Meta:           marshalJSONRaw(map[string]any{"title": strings.TrimSpace(req.Title), "level": level}),
	}); cerr != nil {
		// Unique violation on (workspace, session, key) means a concurrent
		// duplicate won the race — treat as replay, not failure.
		if isUniqueViolation(cerr) {
			cmd, _ := qtx.GetResearchGraphCommandByKey(ctx, db.GetResearchGraphCommandByKeyParams{
				WorkspaceID:    wsUUID,
				SessionID:      sessionID,
				IdempotencyKey: req.IDempotencyKey,
			})
			winner, gerr := qtx.GetResearchGraphNode(ctx, db.GetResearchGraphNodeParams{
				ID:          cmd.ResultNodeID,
				WorkspaceID: wsUUID,
			})
			if gerr == nil && winner.ID.Valid {
				ver, _ := qtx.GetResearchSessionGraphVersion(ctx, db.GetResearchSessionGraphVersionParams{ID: sessionID, WorkspaceID: wsUUID})
				return winner, edgeIDs, ver, true, nil
			}
		}
		return db.ResearchGraphNode{}, nil, 0, false, pgErr("failed to record merge command", http.StatusInternalServerError)
	}

	// Bump graph_version within the same transaction.
	ver, err := qtx.BumpResearchGraphVersion(ctx, db.BumpResearchGraphVersionParams{
		ID:          sessionID,
		WorkspaceID: wsUUID,
	})
	if err != nil {
		return db.ResearchGraphNode{}, nil, 0, false, pgErr("failed to bump graph version", http.StatusInternalServerError)
	}

	if err := tx.Commit(ctx); err != nil {
		return db.ResearchGraphNode{}, nil, 0, false, pgErr("failed to commit merge transaction", http.StatusInternalServerError)
	}
	return conclusion, edgeIDs, ver, false, nil
}

func (h *Handler) mergeCommandActor(member db.ResearchFleetMember) (string, string) {
	return "agent", uuidToString(member.AgentID)
}

func memberActorID(member db.ResearchFleetMember) pgtype.UUID {
	return member.AgentID
}

func validResearchNodeLevel(level string) bool {
	switch level {
	case "XXL", "XL", "L", "M", "S":
		return true
	}
	return false
}

func float8ToPtr(f pgtype.Float8) *float64 {
	if !f.Valid {
		return nil
	}
	v := f.Float64
	return &v
}

func errStatus(err error) int {
	var g *graphTypedError
	if errors.As(err, &g) {
		return g.status
	}
	return http.StatusInternalServerError
}

type graphTypedError struct {
	msg    string
	status int
}

func (e *graphTypedError) Error() string { return e.msg }

func pgErr(msg string, status int) error {
	return &graphTypedError{msg: msg, status: status}
}

func nowUTC() pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: time.Now().UTC(), Valid: true}
}
