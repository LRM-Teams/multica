package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/multica-ai/multica/server/internal/researchrun"
)

type researchV6NodeDetailRunStub struct {
	researchrun.ResearchRun
	researchrun.V6ProjectionReader
	receivedSnapshotID string
	receivedNodeID     string
}

func (s *researchV6NodeDetailRunStub) ProjectionV6NodeDetail(_ context.Context, _, _, snapshotID, nodeID, view string) (researchrun.V6ProjectionNodeDetail, error) {
	s.receivedSnapshotID = snapshotID
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

func TestGetResearchV6ProjectionNodeDetailRouteDecodesStableNodeID(t *testing.T) {
	const (
		runID         = "ecfab91c-7fe7-4e65-b636-f4d7ea65088b"
		snapshotID    = "3d8ce6bb-208a-4d3e-88e6-a8e7d149e036"
		nodeID        = "pv6:goal:60f8f7f3-82e6-48f0-a3f7-b5c0d8a012a2:1"
		encodedNodeID = "pv6%3Agoal%3A60f8f7f3-82e6-48f0-a3f7-b5c0d8a012a2%3A1"
	)
	service := &researchV6NodeDetailRunStub{}
	h := &Handler{ResearchRun: service}
	router := chi.NewRouter()
	router.Get("/api/research/v6/runs/{runId}/projection/nodes/{nodeId}", h.GetResearchV6ProjectionNodeDetail)
	req := newRequest(http.MethodGet, "/api/research/v6/runs/"+runID+"/projection/nodes/"+encodedNodeID+"?snapshot_id="+snapshotID+"&view=brief", nil)
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", w.Code, http.StatusOK, w.Body.String())
	}
	if service.receivedNodeID != nodeID {
		t.Fatalf("nodeID = %q, want %q", service.receivedNodeID, nodeID)
	}
	if service.receivedSnapshotID != snapshotID {
		t.Fatalf("snapshotID = %q, want %q", service.receivedSnapshotID, snapshotID)
	}
}

func TestGetResearchV6ProjectionNodeDetailRequiresSnapshotID(t *testing.T) {
	const (
		runID  = "ecfab91c-7fe7-4e65-b636-f4d7ea65088b"
		nodeID = "pv6:goal:60f8f7f3-82e6-48f0-a3f7-b5c0d8a012a2:1"
	)
	service := &researchV6NodeDetailRunStub{}
	h := &Handler{ResearchRun: service}
	req := withURLParams(newRequest(http.MethodGet, "/api/research/v6/runs/"+runID+"/projection/nodes/"+nodeID, nil), "runId", runID, "nodeId", nodeID)
	w := httptest.NewRecorder()

	h.GetResearchV6ProjectionNodeDetail(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body=%s", w.Code, http.StatusBadRequest, w.Body.String())
	}
	if service.receivedNodeID != "" {
		t.Fatalf("request without snapshot reached service: %q", service.receivedNodeID)
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
