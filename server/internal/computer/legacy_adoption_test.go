package computer

import (
	"testing"

	"github.com/google/uuid"
)

func verifiedLegacy(userID, workspaceID, computerID string) LegacyEvidence {
	return LegacyEvidence{
		Source: "profile-a", OriginHost: CanonicalCloudOrigin, SignedInUser: userID,
		WorkspaceID: workspaceID, ComputerIDCandidates: []string{computerID},
		UserVerified: true, WorkspaceVerified: true, ComputerVerified: true,
	}
}

func TestPlanLegacyAdoptionRequiresEveryProof(t *testing.T) {
	userID, workspaceID, computerID := uuid.NewString(), uuid.NewString(), uuid.NewString()
	base := verifiedLegacy(userID, workspaceID, computerID)
	tests := []struct {
		name string
		edit func(*LegacyEvidence)
	}{
		{"custom origin", func(e *LegacyEvidence) { e.OriginHost = "http://localhost:8080" }},
		{"wrong user", func(e *LegacyEvidence) { e.SignedInUser = uuid.NewString() }},
		{"unverified user", func(e *LegacyEvidence) { e.UserVerified = false }},
		{"unverified workspace", func(e *LegacyEvidence) { e.WorkspaceVerified = false }},
		{"mutable workspace", func(e *LegacyEvidence) { e.WorkspaceID = "pretty-slug" }},
		{"unverified computer", func(e *LegacyEvidence) { e.ComputerVerified = false }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := base
			tt.edit(&e)
			plan := PlanLegacyAdoption(userID, []LegacyEvidence{e})
			if plan.ComputerID != "" || !plan.NeedsChoice || len(plan.Exclusions) == 0 {
				t.Fatalf("unsafe evidence produced plan %+v", plan)
			}
		})
	}
}

func TestPlanLegacyAdoptionBuildsCompleteConnectionSet(t *testing.T) {
	userID, computerID := uuid.NewString(), uuid.NewString()
	a := verifiedLegacy(userID, uuid.NewString(), computerID)
	b := verifiedLegacy(userID, uuid.NewString(), computerID)
	b.Source = "profile-b"
	plan := PlanLegacyAdoption(userID, []LegacyEvidence{a, b, a})
	if plan.NeedsChoice || plan.ComputerID != computerID || len(plan.Connections) != 2 {
		t.Fatalf("plan = %+v, want one Computer and two deduplicated connections", plan)
	}
}

func TestPlanLegacyAdoptionNormalizesHistoricalProductionOrigins(t *testing.T) {
	for _, origin := range []string{"https://leagent.me", "https://www.leagent.me", "https://api.leagent.me"} {
		t.Run(origin, func(t *testing.T) {
			userID, computerID := uuid.NewString(), uuid.NewString()
			evidence := verifiedLegacy(userID, uuid.NewString(), computerID)
			evidence.OriginHost = origin
			plan := PlanLegacyAdoption(userID, []LegacyEvidence{evidence})
			if plan.NeedsChoice || len(plan.Connections) != 1 || plan.Connections[0].Origin != CanonicalCloudOrigin {
				t.Fatalf("plan = %+v", plan)
			}
		})
	}
}

func TestPlanLegacyAdoptionRejectsConflictingComputers(t *testing.T) {
	userID := uuid.NewString()
	a := verifiedLegacy(userID, uuid.NewString(), uuid.NewString())
	b := verifiedLegacy(userID, uuid.NewString(), uuid.NewString())
	plan := PlanLegacyAdoption(userID, []LegacyEvidence{a, b})
	if !plan.NeedsChoice || plan.ComputerID != "" {
		t.Fatalf("conflicting identities must require a choice: %+v", plan)
	}
}
