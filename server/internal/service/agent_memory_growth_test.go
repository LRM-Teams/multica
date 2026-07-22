package service

import "testing"

func TestComputeAgentMemoryGrowthZeroWrites(t *testing.T) {
	if got := ComputeAgentMemoryGrowth(0, 3, 2); got != nil {
		t.Fatalf("zero writes: want nil, got %#v", got)
	}
}

func TestComputeAgentMemoryGrowthBronze(t *testing.T) {
	got := ComputeAgentMemoryGrowth(2, 3, 2)
	if got == nil {
		t.Fatal("expected growth payload")
	}
	if got.Tier != MemoryGrowthTierBronze {
		t.Fatalf("tier = %q, want bronze", got.Tier)
	}
	if got.NextTier == nil || *got.NextTier != MemoryGrowthTierSilver {
		t.Fatalf("next_tier = %#v, want silver", got.NextTier)
	}
	if got.ProgressCurrent != 2 || got.ProgressTarget != 3 {
		t.Fatalf("progress = %d/%d, want 2/3", got.ProgressCurrent, got.ProgressTarget)
	}
	if len(got.Segments) != 4 || got.Segments[0].State != MemoryGrowthSegmentCurrent {
		t.Fatalf("segments = %#v", got.Segments)
	}
}

func TestComputeAgentMemoryGrowthSilver(t *testing.T) {
	got := ComputeAgentMemoryGrowth(5, 3, 2)
	if got.Tier != MemoryGrowthTierSilver {
		t.Fatalf("tier = %q, want silver", got.Tier)
	}
	if got.ProgressTarget != 6 {
		t.Fatalf("progress target = %d, want 6", got.ProgressTarget)
	}
	if got.Segments[0].State != MemoryGrowthSegmentCompleted || got.Segments[1].State != MemoryGrowthSegmentCurrent {
		t.Fatalf("segments = %#v", got.Segments)
	}
}

func TestComputeAgentMemoryGrowthPlatinumProgress(t *testing.T) {
	got := ComputeAgentMemoryGrowth(12, 3, 2)
	if got.Tier != MemoryGrowthTierPlatinum {
		t.Fatalf("tier = %q, want platinum", got.Tier)
	}
	if got.NextTier != nil {
		t.Fatalf("next_tier = %#v, want nil at platinum", got.NextTier)
	}
	if got.ProgressCurrent != 12 || got.ProgressTarget != 24 {
		t.Fatalf("progress = %d/%d, want 12/24", got.ProgressCurrent, got.ProgressTarget)
	}
}

func TestComputeAgentMemoryGrowthPlatinumMax(t *testing.T) {
	got := ComputeAgentMemoryGrowth(30, 3, 2)
	if got.Tier != MemoryGrowthTierPlatinum {
		t.Fatalf("tier = %q, want platinum", got.Tier)
	}
	if got.ProgressTarget != 0 {
		t.Fatalf("progress target = %d, want 0 at cap", got.ProgressTarget)
	}
}

func TestComputeAgentMemoryGrowthDefaults(t *testing.T) {
	got := ComputeAgentMemoryGrowth(4, 0, 0)
	if got.ThresholdBase != DefaultMemoryGrowthBase || got.ThresholdRatio != DefaultMemoryGrowthRatio {
		t.Fatalf("thresholds = %d/%d, want defaults", got.ThresholdBase, got.ThresholdRatio)
	}
}
