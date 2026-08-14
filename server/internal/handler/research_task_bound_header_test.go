package handler

import (
	"testing"

	"github.com/multica-ai/multica/server/internal/researchrun"
)

func TestTaskBoundResearchHeaderUsesOnlyFrozenRunAndPrincipalFields(t *testing.T) {
	run := researchrun.Run{
		SessionID: "session-frozen", WorkspaceID: "workspace-frozen",
		FleetID: "fleet-frozen", CreatedBy: "creator-frozen",
		Title: "title-frozen", Goal: "goal-frozen", Status: researchrun.RunStatusRunning,
		CurrentStage: "s2_sources", DepthTier: "deep",
	}
	session := taskBoundResearchSessionResponse(run)
	if session.ID != run.SessionID || session.Goal != run.Goal || session.Status != string(run.Status) {
		t.Fatalf("session=%+v", session)
	}
	if session.ProjectID != nil || session.ChannelID != nil || session.HandoffSummary != nil ||
		session.CreatedAt != "" || session.UpdatedAt != "" || session.LastUserActivityAt != nil {
		t.Fatalf("task-bound session leaked non-frozen live fields: %+v", session)
	}

	fleet := taskBoundResearchFleetResponse(run, []researchrun.FleetMember{
		{AgentID: "lead-frozen", Role: "lead", Status: "active", IsLead: true},
		{AgentID: "archived-hidden", Role: "reader", Status: "archived"},
		{AgentID: "duplicate-hidden", Role: "lead", Status: "active"},
	})
	if fleet.ID != run.FleetID || fleet.WorkspaceID != run.WorkspaceID || fleet.LeadAgentID == nil || *fleet.LeadAgentID != "lead-frozen" {
		t.Fatalf("fleet=%+v", fleet)
	}
	if len(fleet.Members) != 1 || fleet.Members[0].AgentID != "lead-frozen" {
		t.Fatalf("members=%+v", fleet.Members)
	}
	member := fleet.Members[0]
	if member.ID != "" || member.Name != "" || member.DisplayName != "" || member.AvatarURL != nil {
		t.Fatalf("task-bound Fleet leaked live member/profile fields: %+v", member)
	}
	if fleet.CreatedAt != "" || fleet.UpdatedAt != "" {
		t.Fatalf("task-bound Fleet leaked live timestamps: %+v", fleet)
	}
}
