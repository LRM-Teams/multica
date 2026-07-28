package handler

import (
	"testing"
	"time"
)

func TestComputeUpdateInventoryDiagnostic_StuckCounts(t *testing.T) {
	now := time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)
	runStart := now.Add(-45 * time.Minute)
	freshReady := now.Add(-5 * time.Minute)
	oldReady := now.Add(-40 * time.Minute)

	latest := map[string]*UpdateRequest{
		"rt-run-stuck": {
			ID:           "u1",
			RuntimeID:    "rt-run-stuck",
			Status:       UpdateRunning,
			RunStartedAt: &runStart,
			UpdatedAt:    runStart,
		},
		"rt-ready-stuck": {
			ID:        "u2",
			RuntimeID: "rt-ready-stuck",
			Status:    UpdateReady,
			UpdatedAt: oldReady,
		},
		"rt-ready-fresh": {
			ID:        "u3",
			RuntimeID: "rt-ready-fresh",
			Status:    UpdateReady,
			UpdatedAt: freshReady,
		},
		"rt-done": {
			ID:        "u4",
			RuntimeID: "rt-done",
			Status:    UpdateCompleted,
			UpdatedAt: now.Add(-2 * time.Hour),
		},
	}
	versions := []string{"0.3.77", "0.3.77", "0.3.64", "", "v0.3.78"}

	got := ComputeUpdateInventoryDiagnostic(now, 30*time.Minute, latest, versions)
	if got.Kind != "inventory_diagnostic" {
		t.Fatalf("kind = %q", got.Kind)
	}
	if got.StuckOverMinutes != 30 {
		t.Fatalf("stuck_over_minutes = %d", got.StuckOverMinutes)
	}
	if got.StuckUpdateCounts[string(UpdateRunning)] != 1 {
		t.Fatalf("running stuck = %d, want 1", got.StuckUpdateCounts[string(UpdateRunning)])
	}
	if got.StuckUpdateCounts[string(UpdateReady)] != 1 {
		t.Fatalf("ready stuck = %d, want 1 (only old ready)", got.StuckUpdateCounts[string(UpdateReady)])
	}
	if got.EligibleRuntimeCount != 5 {
		t.Fatalf("eligible = %d", got.EligibleRuntimeCount)
	}
	if got.CLIVersionDistribution["0.3.77"] != 2 {
		t.Fatalf("dist 0.3.77 = %d", got.CLIVersionDistribution["0.3.77"])
	}
	if got.CLIVersionDistribution["unknown"] != 1 {
		t.Fatalf("dist unknown = %d", got.CLIVersionDistribution["unknown"])
	}
	if got.CLIVersionDistribution["0.3.64"] != 1 || got.CLIVersionDistribution["v0.3.78"] != 1 {
		t.Fatalf("dist = %#v", got.CLIVersionDistribution)
	}
	if got.Notes == "" || got.Notes == "safety" {
		t.Fatalf("notes must mark inventory/diagnostic, got %q", got.Notes)
	}
}

func TestComputeUpdateInventoryDiagnostic_Empty(t *testing.T) {
	got := ComputeUpdateInventoryDiagnostic(time.Now(), 30*time.Minute, nil, nil)
	if got.StuckUpdateCounts[string(UpdateRunning)] != 0 || got.StuckUpdateCounts[string(UpdateReady)] != 0 {
		t.Fatalf("stuck = %#v", got.StuckUpdateCounts)
	}
	if got.EligibleRuntimeCount != 0 {
		t.Fatalf("eligible = %d", got.EligibleRuntimeCount)
	}
}
