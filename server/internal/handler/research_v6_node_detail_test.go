package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/multica-ai/multica/server/internal/researchrun"
)

type researchV6NodeDetailRunStub struct {
	researchrun.ResearchRun
	researchrun.V6ProjectionReader
	receivedNodeID string
}

func (s *researchV6NodeDetailRunStub) ProjectionV6NodeDetail(_ context.Context, _, _, nodeID, view string) (researchrun.V6ProjectionNodeDetail, error) {
	s.receivedNodeID = nodeID
	return researchrun.V6ProjectionNodeDetail{
		SnapshotID:           "3d8ce6bb-208a-4d3e-88e6-a8e7d149e036",
		ThroughEventSequence: 1,
		ProjectionHash:       "sha256:test",
		View:                 view,
		Node: researchrun.V6ProjectionNode{
			ID:             nodeID,
			Kind:           "goal",
			Tier:           "GOAL",
			CanonicalRef:   researchrun.V6ProjectionEntityRef{Kind: "goal", ID: "60f8f7f3-82e6-48f0-a3f7-b5c0d8a012a2", Revision: 1},
			BranchIDs:      []string{},
			State:          researchrun.V6ProjectionState{Execution: "running", Conclusion: "accepted", Integration: "unmatched"},
			CatalogSummary: "goal",
			UpdatedAt:      "2026-08-21T00:00:00Z",
		},
		Incoming:       []researchrun.V6ProjectionEdge{},
		Outgoing:       []researchrun.V6ProjectionEdge{},
		HistoryRefs:    []researchrun.V6ProjectionEntityRef{},
		AgentRefs:      []researchrun.V6ProjectionEntityRef{},
		WorkItemRefs:   []researchrun.V6ProjectionEntityRef{},
		AttemptRefs:    []researchrun.V6ProjectionEntityRef{},
		EvidenceRefs:   []researchrun.V6ProjectionEntityRef{},
		DiscussionRefs: []researchrun.V6ProjectionEntityRef{},
		ReportRefs:     []researchrun.V6ProjectionEntityRef{},
	}, nil
}

func TestGetResearchV6ProjectionNodeDetailAcceptsStableNodeID(t *testing.T) {
	const (
		runID  = "ecfab91c-7fe7-4e65-b636-f4d7ea65088b"
		nodeID = "pv6:goal:60f8f7f3-82e6-48f0-a3f7-b5c0d8a012a2:1"
	)
	service := &researchV6NodeDetailRunStub{}
	h := &Handler{ResearchRun: service}
	req := withURLParams(newRequest(http.MethodGet, "/api/research/v6/runs/"+runID+"/projection/nodes/"+nodeID+"?view=brief", nil), "runId", runID, "nodeId", nodeID)
	w := httptest.NewRecorder()

	h.GetResearchV6ProjectionNodeDetail(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", w.Code, http.StatusOK, w.Body.String())
	}
	if service.receivedNodeID != nodeID {
		t.Fatalf("nodeID = %q, want %q", service.receivedNodeID, nodeID)
	}
}

func TestGetResearchV6ProjectionNodeDetailRejectsInvalidStableNodeID(t *testing.T) {
	const runID = "ecfab91c-7fe7-4e65-b636-f4d7ea65088b"
	service := &researchV6NodeDetailRunStub{}
	h := &Handler{ResearchRun: service}
	req := withURLParams(newRequest(http.MethodGet, "/api/research/v6/runs/"+runID+"/projection/nodes/invalid", nil), "runId", runID, "nodeId", "invalid node")
	w := httptest.NewRecorder()

	h.GetResearchV6ProjectionNodeDetail(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body=%s", w.Code, http.StatusBadRequest, w.Body.String())
	}
	if service.receivedNodeID != "" {
		t.Fatalf("invalid nodeID reached service: %q", service.receivedNodeID)
	}
}

func TestBuildResearchV6NodeDetailReturnsStableNeighborsAndCompleteness(t *testing.T) {
	complete := map[string]any{}
	for _, field := range researchV6RequiredDetailFields {
		complete[field] = "value"
	}
	snapshot := researchV6Snapshot{SnapshotID: "snap", ThroughEventSequence: 7, Nodes: []researchV6ProjectionNode{{ID: "root", NodeKind: "question", Title: "Q", Status: "active", Detail: complete}, {ID: "a", NodeKind: "task", Title: "A"}, {ID: "b", NodeKind: "claim", Title: "B"}}, Edges: []researchV6ProjectionEdge{{ID: "e2", FromNodeID: "root", ToNodeID: "b", EdgeType: "supports"}, {ID: "e1", FromNodeID: "a", ToNodeID: "root", EdgeType: "depends_on"}}}
	detail, found := buildResearchV6NodeDetail(snapshot, "root")
	if !found || !detail.DetailComplete || len(detail.Incoming) != 1 || len(detail.Outgoing) != 1 || detail.Incoming[0].NodeID != "a" || detail.Outgoing[0].NodeID != "b" {
		t.Fatalf("detail=%+v found=%v", detail, found)
	}
}

func TestBuildResearchV6NodeDetailReportsMissingFieldsWithoutFabrication(t *testing.T) {
	snapshot := researchV6Snapshot{Nodes: []researchV6ProjectionNode{{ID: "node", Detail: map[string]any{"purpose": "known"}}}}
	detail, found := buildResearchV6NodeDetail(snapshot, "node")
	if !found || detail.DetailComplete || len(detail.MissingDetailFields) != len(researchV6RequiredDetailFields)-1 {
		t.Fatalf("detail=%+v", detail)
	}
	if _, found := buildResearchV6NodeDetail(snapshot, "missing"); found {
		t.Fatal("expected missing node")
	}
}
