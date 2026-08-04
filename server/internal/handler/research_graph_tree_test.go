package handler

import (
	"encoding/json"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func TestMapGraphNodesTreeAndAssessment(t *testing.T) {
	goalID := mustTestUUID("11111111-1111-1111-1111-111111111111")
	childID := mustTestUUID("22222222-2222-2222-2222-222222222222")
	grandID := mustTestUUID("33333333-3333-3333-3333-333333333333")
	sessionID := mustTestUUID("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa")

	nodes := []db.ResearchGraphNode{
		{
			ID: goalID, SessionID: sessionID, NodeType: "goal", Title: "G", Status: "active",
			Payload: []byte(`{}`),
		},
		{
			ID: childID, SessionID: sessionID, NodeType: "subquestion", Title: "Q", Status: "active",
			Payload: []byte(`{"assessment":"trusted","confidence":0.9,"reason":"multi-source","theme_key":"pricing"}`),
		},
		{
			ID: grandID, SessionID: sessionID, NodeType: "finding", Title: "F", Status: "active",
			Payload: []byte(`{"assessment":"弯路","evidence":"2 sources contradict"}`),
		},
	}
	edges := []db.ResearchGraphEdge{
		{FromNodeID: goalID, ToNodeID: childID, EdgeType: "leads_to"},
		{FromNodeID: childID, ToNodeID: grandID, EdgeType: "leads_to"},
		{FromNodeID: childID, ToNodeID: grandID, EdgeType: "supports"}, // semantic; ignore for tree
	}

	out := mapGraphNodes(nodes, edges)
	if len(out) != 3 {
		t.Fatalf("len=%d", len(out))
	}

	byID := map[string]ResearchGraphNodeResp{}
	for _, n := range out {
		byID[n.ID] = n
	}

	root := byID[uuidToString(goalID)]
	if root.ParentID != nil {
		t.Fatalf("root parent_id=%v", root.ParentID)
	}
	if root.ChildCount != 1 || root.DescendantCount != 2 {
		t.Fatalf("root counts child=%d desc=%d", root.ChildCount, root.DescendantCount)
	}
	if root.ThemeKey != "type:goal" {
		t.Fatalf("root theme=%q", root.ThemeKey)
	}
	if root.Assessment != researchAssessmentPendingReview {
		t.Fatalf("root assessment=%q", root.Assessment)
	}

	mid := byID[uuidToString(childID)]
	if mid.ParentID == nil || *mid.ParentID != uuidToString(goalID) {
		t.Fatalf("mid parent=%v", mid.ParentID)
	}
	if mid.ChildCount != 1 || mid.DescendantCount != 1 {
		t.Fatalf("mid counts child=%d desc=%d", mid.ChildCount, mid.DescendantCount)
	}
	if mid.ThemeKey != "pricing" {
		t.Fatalf("mid theme=%q", mid.ThemeKey)
	}
	if mid.Assessment != researchAssessmentTrusted {
		t.Fatalf("mid assessment=%q", mid.Assessment)
	}
	if mid.Reason == nil || *mid.Reason != "multi-source" {
		t.Fatalf("mid reason=%v", mid.Reason)
	}
	if mid.Confidence == nil || *mid.Confidence != 0.9 {
		t.Fatalf("mid confidence=%v", mid.Confidence)
	}

	leaf := byID[uuidToString(grandID)]
	if leaf.ParentID == nil || *leaf.ParentID != uuidToString(childID) {
		t.Fatalf("leaf parent=%v", leaf.ParentID)
	}
	if leaf.ChildCount != 0 || leaf.DescendantCount != 0 {
		t.Fatalf("leaf counts child=%d desc=%d", leaf.ChildCount, leaf.DescendantCount)
	}
	if leaf.Assessment != researchAssessmentDetour {
		t.Fatalf("leaf assessment=%q", leaf.Assessment)
	}
	if leaf.EvidenceSummary == nil || *leaf.EvidenceSummary != "2 sources contradict" {
		t.Fatalf("leaf evidence=%v", leaf.EvidenceSummary)
	}
}

func TestNormalizeResearchAssessmentIllegalDefaultsPending(t *testing.T) {
	if got := normalizeResearchAssessment("nope"); got != researchAssessmentPendingReview {
		t.Fatalf("got=%q", got)
	}
	if got := normalizeResearchAssessment(nil); got != researchAssessmentPendingReview {
		t.Fatalf("nil got=%q", got)
	}
}

func TestMapGraphNodeWithEdgeSetsParent(t *testing.T) {
	parent := mustTestUUID("44444444-4444-4444-4444-444444444444")
	child := mustTestUUID("55555555-5555-5555-5555-555555555555")
	sessionID := mustTestUUID("bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb")
	node := db.ResearchGraphNode{
		ID: child, SessionID: sessionID, NodeType: "probe", Title: "P", Status: "active",
		Payload: json.RawMessage(`{}`),
	}
	edge := &db.ResearchGraphEdge{FromNodeID: parent, ToNodeID: child, EdgeType: "leads_to"}
	resp := mapGraphNodeWithEdge(node, edge)
	if resp.ParentID == nil || *resp.ParentID != uuidToString(parent) {
		t.Fatalf("parent=%v", resp.ParentID)
	}
	if resp.ThemeKey != "type:probe" {
		t.Fatalf("theme=%q", resp.ThemeKey)
	}
}

func mustTestUUID(s string) pgtype.UUID {
	return parseUUID(s)
}
