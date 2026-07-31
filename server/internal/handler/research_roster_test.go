package handler

import (
	"errors"
	"testing"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/internal/researchwake"
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
