package researchrun

import "testing"

func TestResolveV6ProjectionTerritoriesUsesTopLevelBranch(t *testing.T) {
	branches := map[string]v6ProjectionBranchRecord{
		"root":       {id: "root", objective: "Goal"},
		"market":     {id: "market", parentID: "root", objective: "Market adoption"},
		"pricing":    {id: "pricing", parentID: "market", objective: "Pricing"},
		"enterprise": {id: "enterprise", parentID: "pricing", objective: "Enterprise pricing"},
		"orphan":     {id: "orphan", objective: "Legacy orphan"},
	}

	territories := resolveV6ProjectionTerritories(branches, "root")
	for _, branchID := range []string{"market", "pricing", "enterprise"} {
		territory, exists := territories[branchID]
		if !exists || territory.BranchID != "market" || territory.Label != "Market adoption" {
			t.Fatalf("territory[%q]=%+v, want top-level market", branchID, territory)
		}
	}
	if _, exists := territories["root"]; exists {
		t.Fatal("root Branch must not become a visual territory")
	}
	if _, exists := territories["orphan"]; exists {
		t.Fatal("orphaned legacy Branch must not invent a visual territory")
	}
}

func TestProjectionTerritoryForBranchesDropsCrossTerritorySynthesis(t *testing.T) {
	build := &v6ProjectionBuild{territoryByBranch: map[string]V6ProjectionTerritory{
		"market-leaf": {BranchID: "market", Label: "Market"},
		"tech-leaf":   {BranchID: "tech", Label: "Technology"},
	}}

	if got := projectionTerritoryForBranches(build, []string{"market-leaf"}); got == nil || got.BranchID != "market" {
		t.Fatalf("single-territory node=%+v, want market", got)
	}
	if got := projectionTerritoryForBranches(build, []string{"market-leaf", "tech-leaf"}); got != nil {
		t.Fatalf("cross-territory synthesis=%+v, want no single territory", got)
	}
}
