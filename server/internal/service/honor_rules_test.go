package service

import (
	"testing"
	"time"
)

func TestLevelFromTotalXP_IncreasesWithXP(t *testing.T) {
	if LevelFromTotalXP(0) != 1 {
		t.Fatalf("expected level 1 at 0 xp")
	}
	if LevelFromTotalXP(100) < 2 {
		t.Fatalf("expected level >= 2 at 100 xp")
	}
}

func TestIsFoundingMember_BeforeCutoff(t *testing.T) {
	created := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	if !IsFoundingMember(created) {
		t.Fatal("expected founding member before cutoff")
	}
	after := time.Date(2026, 8, 2, 0, 0, 0, 0, time.UTC)
	if IsFoundingMember(after) {
		t.Fatal("expected non-founding after cutoff")
	}
}

func TestPillarTierFromCounter_SteepCurve(t *testing.T) {
	if tier := PillarTierFromCounter(HonorPillarUsage, 5); tier != 0 {
		t.Fatalf("expected tier 0, got %d", tier)
	}
	if tier := PillarTierFromCounter(HonorPillarUsage, 50); tier < 2 {
		t.Fatalf("expected tier >= 2 at 50 usage actions, got %d", tier)
	}
}
