package handler

import (
	"strings"
	"testing"
	"time"
)

func TestComputeUpdateInventoryDiagnostic_StatusAgeCounts(t *testing.T) {
	now := time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)
	runStart := now.Add(-45 * time.Minute)
	freshReady := now.Add(-5 * time.Minute)
	oldReady := now.Add(-40 * time.Minute)

	latest := map[string]*UpdateRequest{
		"rt-run-old": {
			ID:           "u1",
			RuntimeID:    "rt-run-old",
			Status:       UpdateRunning,
			RunStartedAt: &runStart,
			UpdatedAt:    runStart,
		},
		"rt-ready-old": {
			ID:        "u2",
			RuntimeID: "rt-ready-old",
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
	if got.StatusAgeOverMinutes != 30 {
		t.Fatalf("status_age_over_minutes = %d", got.StatusAgeOverMinutes)
	}
	if got.RunningOverThreshold != 1 {
		t.Fatalf("running_over_threshold = %d, want 1", got.RunningOverThreshold)
	}
	if got.ReadyToApplyOverThreshold != 1 {
		t.Fatalf("ready_to_apply_over_threshold = %d, want 1", got.ReadyToApplyOverThreshold)
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
	if strings.Contains(strings.ToLower(got.Notes), "stuck") {
		t.Fatalf("notes must not use judgment word stuck: %q", got.Notes)
	}
}

func TestComputeUpdateInventoryDiagnostic_Empty(t *testing.T) {
	got := ComputeUpdateInventoryDiagnostic(time.Now(), 30*time.Minute, nil, nil)
	if got.RunningOverThreshold != 0 || got.ReadyToApplyOverThreshold != 0 {
		t.Fatalf("counts = %d/%d", got.RunningOverThreshold, got.ReadyToApplyOverThreshold)
	}
	if got.EligibleRuntimeCount != 0 {
		t.Fatalf("eligible = %d", got.EligibleRuntimeCount)
	}
}
