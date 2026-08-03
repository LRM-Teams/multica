package handler

import (
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func TestPlanResearchNextStepsPriority(t *testing.T) {
	var sq, finding, conflict pgtype.UUID
	_ = sq.Scan("11111111-1111-1111-1111-111111111111")
	_ = finding.Scan("22222222-2222-2222-2222-222222222222")
	_ = conflict.Scan("33333333-3333-3333-3333-333333333333")

	nodes := []db.ResearchGraphNode{
		{ID: sq, NodeType: "subquestion", Title: "Q1", Status: "active"},
		{ID: finding, NodeType: "finding", Title: "F1", Status: "active", Payload: []byte(`{}`)},
		{ID: conflict, NodeType: "conflict", Title: "C1", Status: "active"},
	}
	got := planResearchNextSteps(nodes, nil, nil, 3)
	if len(got) != 3 {
		t.Fatalf("want 3 candidates, got %d", len(got))
	}
	if got[0].Kind != "expand_subquestion" {
		t.Fatalf("first kind = %q", got[0].Kind)
	}
	if got[1].Kind != "evidence_gap" {
		t.Fatalf("second kind = %q", got[1].Kind)
	}
	if got[2].Kind != "resolve_conflict" {
		t.Fatalf("third kind = %q", got[2].Kind)
	}
}

func TestPlanResearchNextStepsSkipsCoveredSubquestion(t *testing.T) {
	var sq, child pgtype.UUID
	_ = sq.Scan("11111111-1111-1111-1111-111111111111")
	_ = child.Scan("22222222-2222-2222-2222-222222222222")
	nodes := []db.ResearchGraphNode{
		{ID: sq, NodeType: "subquestion", Title: "Q1", Status: "active"},
		{ID: child, NodeType: "probe", Title: "P1", Status: "active"},
	}
	edges := []db.ResearchGraphEdge{{
		FromNodeID: sq,
		ToNodeID:   child,
		EdgeType:   "leads_to",
	}}
	got := planResearchNextSteps(nodes, edges, nil, 3)
	for _, c := range got {
		if c.Kind == "expand_subquestion" {
			t.Fatal("covered subquestion should not be planned")
		}
	}
}

func TestResearchCompletionBlockers(t *testing.T) {
	session := db.ResearchSession{CurrentStage: "s3_validation"}
	blockers := researchCompletionBlockers(session, nil, nil, nil, nil)
	if len(blockers) == 0 {
		t.Fatal("expected blockers")
	}
	joined := strings.Join(blockers, "|")
	if !strings.Contains(joined, "s4_delivery") {
		t.Fatalf("expected s4 blocker, got %s", joined)
	}

	var fid pgtype.UUID
	_ = fid.Scan("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa")
	session.CurrentStage = "s4_delivery"
	report := &db.ResearchReport{ContentMd: "# ok report"}
	nodes := []db.ResearchGraphNode{{
		ID: fid, NodeType: "finding", Status: "active", Title: "claim", Payload: []byte(`{"source_id":"bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"}`),
	}}
	blockers = researchCompletionBlockers(session, nodes, nil, []db.ResearchSource{{}}, report)
	if len(blockers) != 0 {
		t.Fatalf("expected pass, got %v", blockers)
	}
}

func TestResearchSessionIsUserQuiet(t *testing.T) {
	t.Setenv("RESEARCH_UNATTENDED_QUIET_AFTER", "1m")
	now := time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC)
	session := db.ResearchSession{UnattendedEnabled: true}
	var ts pgtype.Timestamptz
	_ = ts.Scan(now.Add(-30 * time.Second))
	session.LastUserActivityAt = ts
	if researchSessionIsUserQuiet(session, now) {
		t.Fatal("should not be quiet within 1m")
	}
	_ = ts.Scan(now.Add(-2 * time.Minute))
	session.LastUserActivityAt = ts
	if !researchSessionIsUserQuiet(session, now) {
		t.Fatal("should be quiet after 1m")
	}
	session.UnattendedEnabled = false
	if researchSessionIsUserQuiet(session, now) {
		t.Fatal("disabled unattended must not count as quiet")
	}
}

func TestResearchNodeCountsAsBranchExpand(t *testing.T) {
	if !researchNodeCountsAsBranchExpand("subquestion") || !researchNodeCountsAsBranchExpand("probe") {
		t.Fatal("expected expand types")
	}
	if researchNodeCountsAsBranchExpand("finding") || researchNodeCountsAsBranchExpand("agent_activity") {
		t.Fatal("finding/activity should not consume branch budget")
	}
}

func TestBuildResearchWakePromptStillMentionsTools(t *testing.T) {
	var sid pgtype.UUID
	_ = sid.Scan("11111111-1111-1111-1111-111111111111")
	prompt := buildResearchWakePrompt(db.ResearchSession{
		ID: sid, Title: "T", Goal: "G", Status: "running", CurrentStage: "s1_plan",
	}, "ResearchNextStep (unattended): kind=expand_subquestion", "system")
	if !strings.Contains(prompt, "graph-append") {
		t.Fatal("wake must keep research CLI tools")
	}
}
