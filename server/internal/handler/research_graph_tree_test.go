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

// First inbound leads_to wins parent_id AND must be the only parent that lists the child.
func TestMapGraphNodesLosingLeadsToDoesNotPolluteCounts(t *testing.T) {
	winner := mustTestUUID("66666666-6666-6666-6666-666666666666")
	loser := mustTestUUID("77777777-7777-7777-7777-777777777777")
	child := mustTestUUID("88888888-8888-8888-8888-888888888888")
	sessionID := mustTestUUID("cccccccc-cccc-cccc-cccc-cccccccccccc")

	nodes := []db.ResearchGraphNode{
		{ID: winner, SessionID: sessionID, NodeType: "goal", Title: "W", Status: "active", Payload: []byte(`{}`)},
		{ID: loser, SessionID: sessionID, NodeType: "goal", Title: "L", Status: "active", Payload: []byte(`{}`)},
		{ID: child, SessionID: sessionID, NodeType: "finding", Title: "C", Status: "active", Payload: []byte(`{}`)},
	}
	edges := []db.ResearchGraphEdge{
		{FromNodeID: winner, ToNodeID: child, EdgeType: "leads_to"}, // earliest → wins
		{FromNodeID: loser, ToNodeID: child, EdgeType: "leads_to"},  // losing second parent
	}

	out := mapGraphNodes(nodes, edges)
	byID := map[string]ResearchGraphNodeResp{}
	for _, n := range out {
		byID[n.ID] = n
	}

	c := byID[uuidToString(child)]
	if c.ParentID == nil || *c.ParentID != uuidToString(winner) {
		t.Fatalf("child parent=%v want winner", c.ParentID)
	}

	w := byID[uuidToString(winner)]
	if w.ChildCount != 1 || w.DescendantCount != 1 || len(w.ChildIDs) != 1 || w.ChildIDs[0] != uuidToString(child) {
		t.Fatalf("winner children=%v count=%d desc=%d", w.ChildIDs, w.ChildCount, w.DescendantCount)
	}

	l := byID[uuidToString(loser)]
	if l.ChildCount != 0 || l.DescendantCount != 0 || len(l.ChildIDs) != 0 {
		t.Fatalf("loser must not list child: children=%v count=%d desc=%d", l.ChildIDs, l.ChildCount, l.DescendantCount)
	}
}

func TestMapGraphNodesContentFacesAndAbandonReason(t *testing.T) {
	activeID := mustTestUUID("99999999-9999-9999-9999-999999999991")
	abandonedID := mustTestUUID("99999999-9999-9999-9999-999999999992")
	emptyID := mustTestUUID("99999999-9999-9999-9999-999999999993")
	sessionID := mustTestUUID("dddddddd-dddd-dddd-dddd-dddddddddddd")

	nodes := []db.ResearchGraphNode{
		{
			ID: activeID, SessionID: sessionID, NodeType: "subquestion", Title: "定价", Status: "active",
			Payload: []byte(`{
				"content": {
					"goal": "摸清价带",
					"operation_approach": "官网交叉",
					"research_approach": "先横向",
					"result": "粗分完成"
				},
				"assessment": "trusted"
			}`),
		},
		{
			ID: abandonedID, SessionID: sessionID, NodeType: "finding", Title: "旧支", Status: "abandoned",
			Payload: []byte(`{
				"goal_text": "flat goal",
				"operation_approach": "flat ops",
				"research_approach": "flat research",
				"result_summary": "flat result",
				"deprecate_reason": "用户改监管合规 — 不符",
				"assessment": "detour"
			}`),
		},
		{
			ID: emptyID, SessionID: sessionID, NodeType: "probe", Title: "空", Status: "active",
			Payload: []byte(`{}`),
		},
	}

	out := mapGraphNodes(nodes, nil)
	byID := map[string]ResearchGraphNodeResp{}
	for _, n := range out {
		byID[n.ID] = n
	}

	active := byID[uuidToString(activeID)]
	if active.Content.Goal != "摸清价带" ||
		active.Content.OperationApproach != "官网交叉" ||
		active.Content.ResearchApproach != "先横向" ||
		active.Content.Result != "粗分完成" {
		t.Fatalf("active content=%+v", active.Content)
	}
	if active.AbandonReason != nil {
		t.Fatalf("active must omit abandon_reason, got %v", active.AbandonReason)
	}

	abandoned := byID[uuidToString(abandonedID)]
	if abandoned.Content.Goal != "flat goal" ||
		abandoned.Content.OperationApproach != "flat ops" ||
		abandoned.Content.ResearchApproach != "flat research" ||
		abandoned.Content.Result != "flat result" {
		t.Fatalf("abandoned content=%+v", abandoned.Content)
	}
	if abandoned.AbandonReason == nil || *abandoned.AbandonReason != "用户改监管合规 — 不符" {
		t.Fatalf("abandon_reason=%v", abandoned.AbandonReason)
	}
	if abandoned.Assessment != researchAssessmentDetour || abandoned.Status != "abandoned" {
		t.Fatalf("must keep assessment≠status: assessment=%q status=%q", abandoned.Assessment, abandoned.Status)
	}

	empty := byID[uuidToString(emptyID)]
	if empty.Content != (ResearchNodeContentFaces{}) {
		t.Fatalf("empty content must be neutral zero, got %+v", empty.Content)
	}
	if empty.AbandonReason != nil {
		t.Fatalf("empty abandon_reason=%v", empty.AbandonReason)
	}
}

func mustTestUUID(s string) pgtype.UUID {
	return parseUUID(s)
}
