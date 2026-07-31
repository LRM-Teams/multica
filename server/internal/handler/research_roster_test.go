package handler

import (
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/researchwake"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func TestResearchRosterCap(t *testing.T) {
	if researchFleetMaxActiveMembers != 12 {
		t.Fatalf("cap = %d, want 12 (depth-budget aligned)", researchFleetMaxActiveMembers)
	}
	members := make([]db.ResearchFleetMember, 0, 12)
	for i := 0; i < 11; i++ {
		members = append(members, db.ResearchFleetMember{Status: "active"})
	}
	members = append(members, db.ResearchFleetMember{Status: "archived"})
	if got := countNonArchivedFleetMembers(members); got != 11 {
		t.Fatalf("count = %d want 11", got)
	}
	if researchRosterAtCap(11) {
		t.Fatal("11 should be under cap 12")
	}
	if !researchRosterAtCap(12) {
		t.Fatal("12 should hit cap")
	}
}

func TestResolveResearchHireModel(t *testing.T) {
	def := resolveResearchHireModel("", "cursor")
	if !def.Valid || def.String == "" {
		t.Fatal("expected runtime default model")
	}
	custom := resolveResearchHireModel("claude-opus-4-7-high", "cursor")
	if custom.String != "claude-opus-4-7-high" {
		t.Fatalf("custom model = %q", custom.String)
	}
}

func TestResearchAgentMayNotMutateSessionGoal(t *testing.T) {
	if researchAgentMayMutateSessionGoal() {
		t.Fatal("fleet agents must not mutate session.goal (LRM-898/904)")
	}
}

func TestArchivedMemberCannotWake(t *testing.T) {
	err := researchwake.RequireActiveMember("archived", nil)
	if err == nil {
		t.Fatal("archived member must fail wake gate")
	}
	var wakeErr *researchwake.Error
	if !errors.As(err, &wakeErr) || wakeErr.Reason != researchwake.ReasonArchived {
		t.Fatalf("want archived reason, got %v", err)
	}
	err = researchwake.RequireActiveMember("pending_prompt_review", nil)
	if err == nil {
		t.Fatal("pending_prompt_review must fail wake gate")
	}
}

func TestRosterChangePayloadShape(t *testing.T) {
	payload := map[string]any{
		"action":    "archive",
		"member_id": "m1",
		"agent_id":  "a1",
		"role":      "patent_scout",
		"reason":    "idle after S2",
		"status":    "archived",
	}
	raw := marshalJSONRaw(payload)
	if len(raw) < 10 {
		t.Fatal("expected serialized roster payload")
	}
}

func TestValidateResearchHireGap(t *testing.T) {
	members := []db.ResearchFleetMember{
		{Role: "scout", Status: "active"},
		{Role: "reader", Status: "archived"},
	}
	if err := validateResearchHireGap("ok", "patent_scout", "missing patent/IP coverage for filings", members, false); err != nil {
		t.Fatalf("valid gap: %v", err)
	}
	if err := validateResearchHireGap("ok", "scout", "duplicate specialty should fail", members, false); err == nil {
		t.Fatal("expected duplicate role rejection")
	}
	if err := validateResearchHireGap("ok", "patent_scout", "", members, false); err == nil {
		t.Fatal("expected missing reason rejection")
	}
	if err := validateResearchHireGap("ok", "patent_scout", "too short", members, false); err == nil {
		t.Fatal("expected vague reason rejection")
	}
	if err := validateResearchHireGap("lrm904-cap-pad-1", "cap_pad_904_1", "capacity", members, false); err == nil {
		t.Fatal("expected shell pad rejection on user path")
	}
	if err := validateResearchHireGap("lrm904-cap-pad-1", "cap_pad_904_1", "capacity fixture", members, true); err != nil {
		t.Fatalf("fixture pad should pass: %v", err)
	}
}

func TestValidateResearchArchiveAntiChurn(t *testing.T) {
	now := time.Date(2026, 7, 31, 10, 0, 0, 0, time.UTC)
	member := db.ResearchFleetMember{Status: "active", IsLead: false}
	hired := now.Add(-5 * time.Minute)
	if err := validateResearchArchiveAntiChurn(member, hired, false, false, now); err == nil {
		t.Fatal("expected shell archive rejection")
	}
	if err := validateResearchArchiveAntiChurn(member, hired, true, false, now); err != nil {
		t.Fatalf("work should allow archive: %v", err)
	}
	if err := validateResearchArchiveAntiChurn(member, hired, false, true, now); err != nil {
		t.Fatalf("fixture should allow archive: %v", err)
	}
	old := now.Add(-2 * time.Hour)
	if err := validateResearchArchiveAntiChurn(member, old, false, false, now); err != nil {
		t.Fatalf("old idle should allow archive: %v", err)
	}
}

func TestResearchRosterGraphStatus(t *testing.T) {
	if got := researchRosterGraphStatus("archive"); got != "archived" {
		t.Fatalf("archive status = %q", got)
	}
	if got := researchRosterGraphStatus("hire"); got != "pending" {
		t.Fatalf("hire status = %q", got)
	}
}

func TestResearchMemberHasObservableWork(t *testing.T) {
	var agent pgtype.UUID
	_ = agent.Scan("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa")
	nodes := []db.ResearchGraphNode{
		{NodeType: "roster_change", ActorAgentID: agent},
		{NodeType: "probe", ActorAgentID: agent},
	}
	if !researchMemberHasObservableWork(nodes, agent) {
		t.Fatal("probe should count as work")
	}
	nodes = []db.ResearchGraphNode{{NodeType: "roster_change", ActorAgentID: agent}}
	if researchMemberHasObservableWork(nodes, agent) {
		t.Fatal("roster_change alone is not observable work")
	}
}

func TestResearchRosterFixtureRequested(t *testing.T) {
	if !researchRosterFixtureRequested("1", false) || !researchRosterFixtureRequested("", true) {
		t.Fatal("expected fixture true")
	}
	if researchRosterFixtureRequested("", false) {
		t.Fatal("expected fixture false")
	}
}
