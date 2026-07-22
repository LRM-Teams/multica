package service

// Memory growth tiers for LRM-274 Phase② (Slack profile field).
// Thresholds follow base × r^(n−1) with defaults base=3, r=2 → 3 / 6 / 12 / 24.

const (
	DefaultMemoryGrowthBase  = 3
	DefaultMemoryGrowthRatio = 2
)

type MemoryGrowthTier string

const (
	MemoryGrowthTierBronze   MemoryGrowthTier = "bronze"
	MemoryGrowthTierSilver   MemoryGrowthTier = "silver"
	MemoryGrowthTierGold     MemoryGrowthTier = "gold"
	MemoryGrowthTierPlatinum MemoryGrowthTier = "platinum"
)

var memoryGrowthTierOrder = []MemoryGrowthTier{
	MemoryGrowthTierBronze,
	MemoryGrowthTierSilver,
	MemoryGrowthTierGold,
	MemoryGrowthTierPlatinum,
}

var memoryGrowthTierLabels = map[MemoryGrowthTier]string{
	MemoryGrowthTierBronze:   "Bronze",
	MemoryGrowthTierSilver:   "Silver",
	MemoryGrowthTierGold:     "Gold",
	MemoryGrowthTierPlatinum: "Platinum",
}

var memoryGrowthDotColors = map[MemoryGrowthTier]string{
	MemoryGrowthTierBronze:   "t1",
	MemoryGrowthTierSilver:   "t2",
	MemoryGrowthTierGold:     "t3",
	MemoryGrowthTierPlatinum: "t4",
}

type MemoryGrowthSegmentState string

const (
	MemoryGrowthSegmentCompleted MemoryGrowthSegmentState = "completed"
	MemoryGrowthSegmentCurrent   MemoryGrowthSegmentState = "current"
	MemoryGrowthSegmentPending   MemoryGrowthSegmentState = "pending"
)

type MemoryGrowthSegment struct {
	Tier  MemoryGrowthTier         `json:"tier"`
	State MemoryGrowthSegmentState `json:"state"`
}

type MemoryGrowth struct {
	TotalWrites     int                   `json:"total_writes"`
	Tier            MemoryGrowthTier      `json:"tier"`
	TierLabel       string                `json:"tier_label"`
	DotColor        string                `json:"dot_color"`
	Segments        []MemoryGrowthSegment `json:"segments"`
	NextTier        *MemoryGrowthTier     `json:"next_tier"`
	NextTierLabel   *string               `json:"next_tier_label"`
	ProgressCurrent int                   `json:"progress_current"`
	ProgressTarget  int                   `json:"progress_target"`
	ThresholdBase   int                   `json:"threshold_base"`
	ThresholdRatio  int                   `json:"threshold_ratio"`
}

// memoryGrowthAdvanceThreshold returns cumulative effective writes required to
// advance from tier at tierIndex to the next tier (tierIndex 0 → Silver at 3).
func memoryGrowthAdvanceThreshold(tierIndex, base, ratio int) int {
	threshold := base
	for i := 0; i < tierIndex; i++ {
		threshold *= ratio
	}
	return threshold
}

// ComputeAgentMemoryGrowth derives tier, four-segment bar, and fine progress
// from Phase① cumulative effective write count. Returns nil when totalWrites <= 0.
func ComputeAgentMemoryGrowth(totalWrites, base, ratio int) *MemoryGrowth {
	if totalWrites <= 0 {
		return nil
	}
	if base <= 0 {
		base = DefaultMemoryGrowthBase
	}
	if ratio <= 1 {
		ratio = DefaultMemoryGrowthRatio
	}

	tierIndex := 0
	for i := len(memoryGrowthTierOrder) - 2; i >= 0; i-- {
		if totalWrites >= memoryGrowthAdvanceThreshold(i, base, ratio) {
			tierIndex = i + 1
			break
		}
	}
	if tierIndex >= len(memoryGrowthTierOrder) {
		tierIndex = len(memoryGrowthTierOrder) - 1
	}

	currentTier := memoryGrowthTierOrder[tierIndex]
	segments := make([]MemoryGrowthSegment, len(memoryGrowthTierOrder))
	for i, tier := range memoryGrowthTierOrder {
		switch {
		case i < tierIndex:
			segments[i] = MemoryGrowthSegment{Tier: tier, State: MemoryGrowthSegmentCompleted}
		case i == tierIndex:
			segments[i] = MemoryGrowthSegment{Tier: tier, State: MemoryGrowthSegmentCurrent}
		default:
			segments[i] = MemoryGrowthSegment{Tier: tier, State: MemoryGrowthSegmentPending}
		}
	}

	var nextTier *MemoryGrowthTier
	var nextTierLabel *string
	progressCurrent := totalWrites
	progressTarget := 0

	if tierIndex < len(memoryGrowthTierOrder)-1 {
		target := memoryGrowthAdvanceThreshold(tierIndex, base, ratio)
		nt := memoryGrowthTierOrder[tierIndex+1]
		label := memoryGrowthTierLabels[nt]
		nextTier = &nt
		nextTierLabel = &label
		progressTarget = target
	} else if cap := memoryGrowthAdvanceThreshold(tierIndex, base, ratio); totalWrites < cap {
		progressTarget = cap
	}

	return &MemoryGrowth{
		TotalWrites:     totalWrites,
		Tier:            currentTier,
		TierLabel:       memoryGrowthTierLabels[currentTier],
		DotColor:        memoryGrowthDotColors[currentTier],
		Segments:        segments,
		NextTier:        nextTier,
		NextTierLabel:   nextTierLabel,
		ProgressCurrent: progressCurrent,
		ProgressTarget:  progressTarget,
		ThresholdBase:   base,
		ThresholdRatio:  ratio,
	}
}
