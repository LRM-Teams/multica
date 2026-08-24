package researchrun

import (
	"context"
	"encoding/json"
)

type V6ProjectionEntityRef struct {
	Kind        string `json:"kind"`
	ID          string `json:"id"`
	Revision    int    `json:"revision,omitempty"`
	VersionID   string `json:"version_id,omitempty"`
	ContentHash string `json:"content_hash,omitempty"`
}

type V6ProjectionTermination struct {
	ReasonCode   string `json:"reason_code"`
	ReasonDetail string `json:"reason_detail"`
}

type V6ProjectionState struct {
	Execution   string                   `json:"execution"`
	Conclusion  string                   `json:"conclusion"`
	Integration string                   `json:"integration"`
	Termination *V6ProjectionTermination `json:"termination,omitempty"`
}

type V6ProjectionNode struct {
	ID               string                `json:"id"`
	Kind             string                `json:"kind"`
	Tier             string                `json:"tier"`
	CanonicalRef     V6ProjectionEntityRef `json:"canonical_ref"`
	BranchIDs        []string              `json:"branch_ids"`
	State            V6ProjectionState     `json:"state"`
	Title            string                `json:"title,omitempty"`
	CatalogSummary   string                `json:"catalog_summary"`
	Absorbed         bool                  `json:"absorbed"`
	Terminal         bool                  `json:"terminal"`
	Expandable       bool                  `json:"expandable"`
	HiddenChildCount int                   `json:"hidden_child_count"`
	UpdatedAt        string                `json:"updated_at"`
}

type V6ProjectionEdge struct {
	ID          string `json:"id"`
	Kind        string `json:"kind"`
	FromNodeID  string `json:"from_node_id"`
	ToNodeID    string `json:"to_node_id"`
	Canonical   bool   `json:"canonical"`
	HiddenCount int    `json:"hidden_count"`
	Expandable  bool   `json:"expandable"`
}

type V6DensityBounds struct {
	X, Y, Width, Height float64
}

func (b V6DensityBounds) MarshalJSON() ([]byte, error) {
	return json.Marshal(map[string]float64{"x": b.X, "y": b.Y, "width": b.Width, "height": b.Height})
}

type V6ProjectionDensityBin struct {
	ID              string          `json:"id"`
	BranchID        string          `json:"branch_id"`
	Bounds          V6DensityBounds `json:"bounds"`
	Total           int             `json:"total"`
	ReasonCounts    map[string]int  `json:"reason_counts"`
	ExecutionCounts map[string]int  `json:"execution_counts"`
}

type V6ProjectionSnapshot struct {
	ContractKind         string                   `json:"contract_kind"`
	SchemaVersion        int                      `json:"schema_version"`
	SnapshotID           string                   `json:"snapshot_id"`
	WorkspaceID          string                   `json:"workspace_id"`
	RunID                string                   `json:"run_id"`
	ThroughEventSequence int64                    `json:"through_event_sequence"`
	ProjectionHash       string                   `json:"projection_hash"`
	SliceKey             string                   `json:"slice_key"`
	Nodes                []V6ProjectionNode       `json:"nodes"`
	Edges                []V6ProjectionEdge       `json:"edges"`
	DensityBins          []V6ProjectionDensityBin `json:"density_bins"`
	HasMore              bool                     `json:"has_more"`
	NextCursor           string                   `json:"next_cursor,omitempty"`
}

type V6ProjectionDelta struct {
	ContractKind           string             `json:"contract_kind"`
	SchemaVersion          int                `json:"schema_version"`
	WorkspaceID            string             `json:"workspace_id"`
	RunID                  string             `json:"run_id"`
	SnapshotID             string             `json:"snapshot_id"`
	EventSequence          int64              `json:"event_sequence"`
	PreviousProjectionHash string             `json:"previous_projection_hash"`
	ProjectionHash         string             `json:"projection_hash"`
	UpsertNodes            []V6ProjectionNode `json:"upsert_nodes"`
	RemoveNodeIDs          []string           `json:"remove_node_ids"`
	UpsertEdges            []V6ProjectionEdge `json:"upsert_edges"`
	RemoveEdgeIDs          []string           `json:"remove_edge_ids"`
	InvalidateSliceKeys    []string           `json:"invalidate_slice_keys"`
}

type V6ProjectionPageRequest struct {
	WorkspaceID, RunID, Cursor string
	Limit                      int
}

type V6ProjectionSliceRequest struct {
	WorkspaceID, RunID, SnapshotID, RootNodeID, Cursor string
	Depth, Limit                                       int
}

type V6ProjectionDeltaRequest struct {
	WorkspaceID, RunID, SnapshotID, ProjectionHash string
	After                                          int64
}

type V6ProjectionDeltaPage struct {
	RunID          string              `json:"run_id"`
	Deltas         []V6ProjectionDelta `json:"deltas"`
	NextCursor     *string             `json:"next_cursor"`
	ResyncRequired bool                `json:"resync_required"`
}

type V6ProjectionNodeDetail struct {
	SnapshotID           string                  `json:"snapshot_id"`
	ThroughEventSequence int64                   `json:"through_event_sequence"`
	ProjectionHash       string                  `json:"projection_hash"`
	View                 string                  `json:"view"`
	Node                 V6ProjectionNode        `json:"node"`
	ContentLayers        *V6ContentLayers        `json:"content_layers,omitempty"`
	Incoming             []V6ProjectionEdge      `json:"incoming"`
	Outgoing             []V6ProjectionEdge      `json:"outgoing"`
	HistoryRefs          []V6ProjectionEntityRef `json:"history_refs"`
	AgentRefs            []V6ProjectionEntityRef `json:"agent_refs"`
	WorkItemRefs         []V6ProjectionEntityRef `json:"work_item_refs"`
	AttemptRefs          []V6ProjectionEntityRef `json:"attempt_refs"`
	EvidenceRefs         []V6ProjectionEntityRef `json:"evidence_refs"`
	DiscussionRefs       []V6ProjectionEntityRef `json:"discussion_refs"`
	ReportRefs           []V6ProjectionEntityRef `json:"report_refs"`
}

type V6ProjectionReader interface {
	ProjectionV6Snapshot(context.Context, V6ProjectionPageRequest) (V6ProjectionSnapshot, error)
	ProjectionV6Slice(context.Context, V6ProjectionSliceRequest) (V6ProjectionSnapshot, error)
	ProjectionV6Deltas(context.Context, V6ProjectionDeltaRequest) (V6ProjectionDeltaPage, error)
	ProjectionV6NodeDetail(context.Context, string, string, string, string, string) (V6ProjectionNodeDetail, error)
}

// IsValidV6ProjectionNodeID reports whether value matches the frozen V6 key
// contract used by projection node identifiers.
func IsValidV6ProjectionNodeID(value string) bool {
	return validV6Key(value)
}
