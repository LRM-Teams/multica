package handler

import (
	"net/http"
	"sort"
	"strings"

	"github.com/go-chi/chi/v5"
)

var researchV6RequiredDetailFields = []string{"purpose", "objective", "entry_condition", "method", "input_artifacts", "actions_taken", "actor", "result", "evidence", "decision", "failure", "recovery", "upstream", "downstream"}

type researchV6NodeNeighbor struct {
	NodeID   string `json:"node_id"`
	NodeKind string `json:"node_kind"`
	Title    string `json:"title"`
	Status   string `json:"status"`
	EdgeID   string `json:"edge_id"`
	EdgeType string `json:"edge_type"`
}

type researchV6NodeDetailResponse struct {
	SnapshotID           string                   `json:"snapshot_id"`
	ThroughEventSequence int64                    `json:"through_event_sequence"`
	Node                 researchV6ProjectionNode `json:"node"`
	Incoming             []researchV6NodeNeighbor `json:"incoming"`
	Outgoing             []researchV6NodeNeighbor `json:"outgoing"`
	DetailComplete       bool                     `json:"detail_complete"`
	MissingDetailFields  []string                 `json:"missing_detail_fields"`
}

func (h *Handler) GetResearchV6ProjectionNodeDetail(w http.ResponseWriter, r *http.Request) {
	snapshot, err := h.loadResearchV6Snapshot(r)
	if err != nil {
		writeResearchV6Error(w, err)
		return
	}
	nodeID := strings.TrimSpace(chi.URLParam(r, "nodeId"))
	detail, found := buildResearchV6NodeDetail(snapshot, nodeID)
	if !found {
		writeError(w, http.StatusNotFound, "research V6 projection node not found")
		return
	}
	writeJSON(w, http.StatusOK, detail)
}

func buildResearchV6NodeDetail(snapshot researchV6Snapshot, nodeID string) (researchV6NodeDetailResponse, bool) {
	nodes := make(map[string]researchV6ProjectionNode, len(snapshot.Nodes))
	for _, node := range snapshot.Nodes {
		nodes[node.ID] = node
	}
	node, found := nodes[nodeID]
	if !found || nodeID == "" {
		return researchV6NodeDetailResponse{}, false
	}
	response := researchV6NodeDetailResponse{SnapshotID: snapshot.SnapshotID, ThroughEventSequence: snapshot.ThroughEventSequence, Node: node, Incoming: []researchV6NodeNeighbor{}, Outgoing: []researchV6NodeNeighbor{}}
	for _, edge := range snapshot.Edges {
		if edge.FromNodeID == nodeID {
			if neighbor, ok := nodes[edge.ToNodeID]; ok {
				response.Outgoing = append(response.Outgoing, researchV6Neighbor(edge, neighbor))
			}
		}
		if edge.ToNodeID == nodeID {
			if neighbor, ok := nodes[edge.FromNodeID]; ok {
				response.Incoming = append(response.Incoming, researchV6Neighbor(edge, neighbor))
			}
		}
	}
	sort.Slice(response.Incoming, func(i, j int) bool {
		if response.Incoming[i].EdgeType != response.Incoming[j].EdgeType {
			return response.Incoming[i].EdgeType < response.Incoming[j].EdgeType
		}
		return response.Incoming[i].NodeID < response.Incoming[j].NodeID
	})
	sort.Slice(response.Outgoing, func(i, j int) bool {
		if response.Outgoing[i].EdgeType != response.Outgoing[j].EdgeType {
			return response.Outgoing[i].EdgeType < response.Outgoing[j].EdgeType
		}
		return response.Outgoing[i].NodeID < response.Outgoing[j].NodeID
	})
	detail, _ := node.Detail.(map[string]any)
	for _, field := range researchV6RequiredDetailFields {
		value, ok := detail[field]
		if !ok || value == nil || value == "" {
			response.MissingDetailFields = append(response.MissingDetailFields, field)
		}
	}
	response.DetailComplete = len(response.MissingDetailFields) == 0
	return response, true
}

func researchV6Neighbor(edge researchV6ProjectionEdge, node researchV6ProjectionNode) researchV6NodeNeighbor {
	return researchV6NodeNeighbor{NodeID: node.ID, NodeKind: node.NodeKind, Title: node.Title, Status: node.Status, EdgeID: edge.ID, EdgeType: edge.EdgeType}
}
