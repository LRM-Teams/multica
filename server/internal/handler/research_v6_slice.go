package handler

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
)

const researchV6SliceMaximumLimit = 500

type researchV6SliceRequest struct {
	RootNodeID      string   `json:"root_node_id"`
	Direction       string   `json:"direction"`
	RelationTypes   []string `json:"relation_types"`
	MaxDepth        int      `json:"max_depth"`
	Statuses        []string `json:"statuses"`
	ImportanceFloor float64  `json:"importance_floor"`
	Cursor          *string  `json:"cursor"`
	Limit           int      `json:"limit"`
}

type researchV6SliceNode struct {
	Node                  researchV6ProjectionNode `json:"node"`
	UnloadedNeighborCount int                      `json:"unloaded_neighbor_count"`
	DescendantCount       int                      `json:"descendant_count"`
	CanExpand             bool                     `json:"can_expand"`
}

type researchV6ProjectionSlice struct {
	SnapshotID string                        `json:"snapshot_id"`
	Request    researchV6SliceRequest        `json:"request"`
	Nodes      []researchV6SliceNode         `json:"nodes"`
	Edges      []researchV6ProjectionEdge    `json:"edges"`
	Clusters   []researchV6ProjectionCluster `json:"clusters"`
	NextCursor *string                       `json:"next_cursor"`
}

type researchV6SliceCursor struct {
	SnapshotID  string `json:"snapshot_id"`
	RequestHash string `json:"request_hash"`
	Offset      int    `json:"offset"`
}

func (h *Handler) GetResearchV6ProjectionSlice(w http.ResponseWriter, r *http.Request) {
	request, err := parseResearchV6SliceRequest(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	snapshot, err := h.loadResearchV6Snapshot(r)
	if err != nil {
		writeResearchV6Error(w, err)
		return
	}
	page, err := buildResearchV6ProjectionSlice(snapshot, request)
	if err != nil {
		if strings.Contains(err.Error(), "resync") {
			writeError(w, http.StatusConflict, err.Error())
		} else {
			writeError(w, http.StatusBadRequest, err.Error())
		}
		return
	}
	writeJSON(w, http.StatusOK, page)
}

func parseResearchV6SliceRequest(r *http.Request) (researchV6SliceRequest, error) {
	query := r.URL.Query()
	request := researchV6SliceRequest{RootNodeID: strings.TrimSpace(query.Get("root_node_id")), Direction: query.Get("direction")}
	if request.Direction == "" {
		request.Direction = "both"
	}
	request.RelationTypes = sortedNonemptyCSV(query.Get("relation_types"))
	request.Statuses = sortedNonemptyCSV(query.Get("statuses"))
	var err error
	if request.MaxDepth, err = strconv.Atoi(query.Get("max_depth")); err != nil || request.MaxDepth < 0 || request.MaxDepth > 32 {
		return request, fmt.Errorf("max_depth must be in [0,32]")
	}
	if request.ImportanceFloor, err = strconv.ParseFloat(query.Get("importance_floor"), 64); err != nil || request.ImportanceFloor < 0 || request.ImportanceFloor > 1 {
		return request, fmt.Errorf("importance_floor must be in [0,1]")
	}
	if request.Limit, err = strconv.Atoi(query.Get("limit")); err != nil || request.Limit <= 0 || request.Limit > researchV6SliceMaximumLimit {
		return request, fmt.Errorf("limit must be in [1,%d]", researchV6SliceMaximumLimit)
	}
	if request.RootNodeID == "" {
		return request, fmt.Errorf("root_node_id is required")
	}
	if request.Direction != "out" && request.Direction != "in" && request.Direction != "both" {
		return request, fmt.Errorf("direction must be out, in, or both")
	}
	if cursor := strings.TrimSpace(query.Get("cursor")); cursor != "" {
		request.Cursor = &cursor
	}
	return request, nil
}

func buildResearchV6ProjectionSlice(snapshot researchV6Snapshot, request researchV6SliceRequest) (researchV6ProjectionSlice, error) {
	nodes := make(map[string]researchV6ProjectionNode, len(snapshot.Nodes))
	for _, node := range snapshot.Nodes {
		nodes[node.ID] = node
	}
	if _, exists := nodes[request.RootNodeID]; !exists {
		return researchV6ProjectionSlice{}, fmt.Errorf("root_node_id not found")
	}
	requestHash := hashResearchV6SliceRequest(request)
	offset := 0
	if request.Cursor != nil {
		cursor, err := decodeResearchV6SliceCursor(*request.Cursor)
		if err != nil {
			return researchV6ProjectionSlice{}, fmt.Errorf("invalid slice cursor")
		}
		if cursor.SnapshotID != snapshot.SnapshotID || cursor.RequestHash != requestHash {
			return researchV6ProjectionSlice{}, fmt.Errorf("slice cursor baseline changed; resync required")
		}
		offset = cursor.Offset
	}
	relations := stringSet(request.RelationTypes)
	statuses := stringSet(request.Statuses)
	adjacency := map[string][]string{}
	for _, edge := range snapshot.Edges {
		if len(relations) > 0 {
			if _, ok := relations[edge.EdgeType]; !ok {
				continue
			}
		}
		if request.Direction == "out" || request.Direction == "both" {
			adjacency[edge.FromNodeID] = append(adjacency[edge.FromNodeID], edge.ToNodeID)
		}
		if request.Direction == "in" || request.Direction == "both" {
			adjacency[edge.ToNodeID] = append(adjacency[edge.ToNodeID], edge.FromNodeID)
		}
	}
	for id := range adjacency {
		sort.Strings(adjacency[id])
	}
	type queued struct {
		id    string
		depth int
	}
	queue := []queued{{request.RootNodeID, 0}}
	seen := map[string]struct{}{request.RootNodeID: {}}
	ordered := make([]string, 0)
	for head := 0; head < len(queue); head++ {
		item := queue[head]
		node := nodes[item.id]
		include := item.id == request.RootNodeID || ((len(statuses) == 0 || hasStringKey(statuses, node.Status)) && node.Importance >= request.ImportanceFloor)
		if include {
			ordered = append(ordered, item.id)
		}
		if item.depth >= request.MaxDepth {
			continue
		}
		for _, neighbor := range adjacency[item.id] {
			if _, ok := seen[neighbor]; ok {
				continue
			}
			seen[neighbor] = struct{}{}
			queue = append(queue, queued{neighbor, item.depth + 1})
		}
	}
	if offset < 0 || offset > len(ordered) {
		return researchV6ProjectionSlice{}, fmt.Errorf("slice cursor offset is invalid")
	}
	end := offset + request.Limit
	if end > len(ordered) {
		end = len(ordered)
	}
	pageIDs := ordered[offset:end]
	pageSet := stringSet(pageIDs)
	page := researchV6ProjectionSlice{SnapshotID: snapshot.SnapshotID, Request: request, Nodes: []researchV6SliceNode{}, Edges: []researchV6ProjectionEdge{}, Clusters: []researchV6ProjectionCluster{}}
	page.Request.Cursor = request.Cursor
	for _, id := range pageIDs {
		unloaded := 0
		for _, neighbor := range adjacency[id] {
			if _, ok := pageSet[neighbor]; !ok {
				unloaded++
			}
		}
		page.Nodes = append(page.Nodes, researchV6SliceNode{Node: nodes[id], UnloadedNeighborCount: unloaded, DescendantCount: countResearchV6Descendants(id, adjacency), CanExpand: unloaded > 0})
	}
	for _, edge := range snapshot.Edges {
		if (len(relations) == 0 || hasStringKey(relations, edge.EdgeType)) && hasStringKey(pageSet, edge.FromNodeID) && hasStringKey(pageSet, edge.ToNodeID) {
			page.Edges = append(page.Edges, edge)
		}
	}
	for _, cluster := range snapshot.Clusters {
		for _, memberID := range cluster.MemberNodeIDs {
			if hasStringKey(pageSet, memberID) {
				page.Clusters = append(page.Clusters, cluster)
				break
			}
		}
	}
	if end < len(ordered) {
		encoded := encodeResearchV6SliceCursor(researchV6SliceCursor{SnapshotID: snapshot.SnapshotID, RequestHash: requestHash, Offset: end})
		page.NextCursor = &encoded
	}
	return page, nil
}

func hashResearchV6SliceRequest(request researchV6SliceRequest) string {
	request.Cursor = nil
	payload, _ := json.Marshal(request)
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}
func encodeResearchV6SliceCursor(cursor researchV6SliceCursor) string {
	payload, _ := json.Marshal(cursor)
	return base64.RawURLEncoding.EncodeToString(payload)
}
func decodeResearchV6SliceCursor(value string) (researchV6SliceCursor, error) {
	var cursor researchV6SliceCursor
	payload, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return cursor, err
	}
	err = json.Unmarshal(payload, &cursor)
	return cursor, err
}
func sortedNonemptyCSV(value string) []string {
	if strings.TrimSpace(value) == "" {
		return []string{}
	}
	values := strings.Split(value, ",")
	result := []string{}
	seen := map[string]struct{}{}
	for _, item := range values {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if _, ok := seen[item]; ok {
			continue
		}
		seen[item] = struct{}{}
		result = append(result, item)
	}
	sort.Strings(result)
	return result
}
func stringSet(values []string) map[string]struct{} {
	result := make(map[string]struct{}, len(values))
	for _, value := range values {
		result[value] = struct{}{}
	}
	return result
}
func hasStringKey(values map[string]struct{}, value string) bool { _, ok := values[value]; return ok }
func countResearchV6Descendants(root string, adjacency map[string][]string) int {
	seen := map[string]struct{}{root: {}}
	queue := []string{root}
	for head := 0; head < len(queue) && len(seen) <= 10000; head++ {
		for _, next := range adjacency[queue[head]] {
			if _, ok := seen[next]; ok {
				continue
			}
			seen[next] = struct{}{}
			queue = append(queue, next)
		}
	}
	return len(seen) - 1
}
