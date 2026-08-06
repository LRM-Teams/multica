package memorygrowth

// Phase② Memory growth tiers for LRM-274 / LRM-303.
// XP source: cumulative valid agent_memory_write_event rows (Phase①).

const (
	DefaultBase  = 3
	DefaultRatio = 2
)

var tierOrder = []string{"bronze", "silver", "gold", "platinum"}

var tierLabels = map[string]string{
	"bronze":   "Bronze",
	"silver":   "Silver",
	"gold":     "Gold",
	"platinum": "Platinum",
}

// Segment is one bar slot (Bronze → Platinum).
type Segment struct {
	Tier      string `json:"tier"`
	TierLabel string `json:"tier_label"`
	Status    string `json:"status"` // complete | current | upcoming
}

// NextProgress is the fine-grained "Next · n/m writes" row.
type NextProgress struct {
	Tier      string `json:"tier"`
	TierLabel string `json:"tier_label"`
	Current   int    `json:"current"`
	Required  int    `json:"required"`
}

// Snapshot is the profile/card payload. Nil when totalWrites == 0.
type Snapshot struct {
	TotalWrites int           `json:"total_writes"`
	Tier        string        `json:"tier"`
	TierLabel   string        `json:"tier_label"`
	Segments    []Segment     `json:"segments"`
	Next        *NextProgress `json:"next,omitempty"`
}

// Compute derives tier + progress from a cumulative valid-write count.
// base/ratio follow base × ratio^(n−1) thresholds (default 3/6/12/24).
func Compute(totalWrites int, base, ratio int) *Snapshot {
	if totalWrites <= 0 {
		return nil
	}
	if base <= 0 {
		base = DefaultBase
	}
	if ratio <= 0 {
		ratio = DefaultRatio
	}

	thresholds := tierThresholds(base, ratio)
	tierIdx := currentTierIndex(totalWrites, thresholds)

	segments := make([]Segment, len(tierOrder))
	for i, tier := range tierOrder {
		status := "upcoming"
		if i < len(thresholds) && totalWrites >= thresholds[i] {
			status = "complete"
		} else if i == tierIdx {
			status = "current"
		}
		segments[i] = Segment{
			Tier:      tier,
			TierLabel: tierLabels[tier],
			Status:    status,
		}
	}

	tier := tierOrder[tierIdx]
	return &Snapshot{
		TotalWrites: totalWrites,
		Tier:        tier,
		TierLabel:   tierLabels[tier],
		Segments:    segments,
		Next:        nextProgress(totalWrites, thresholds),
	}
}

func tierThresholds(base, ratio int) []int {
	out := make([]int, len(tierOrder))
	for i := range tierOrder {
		t := base
		for j := 0; j < i; j++ {
			t *= ratio
		}
		out[i] = t
	}
	return out
}

func currentTierIndex(total int, thresholds []int) int {
	idx := 0
	for i := 0; i < len(tierOrder)-1; i++ {
		if total >= thresholds[i] {
			idx = i + 1
		}
	}
	if idx >= len(tierOrder) {
		return len(tierOrder) - 1
	}
	return idx
}

func nextProgress(total int, thresholds []int) *NextProgress {
	for i, required := range thresholds {
		if total < required {
			tierIdx := i + 1
			if tierIdx >= len(tierOrder) {
				tierIdx = len(tierOrder) - 1
			}
			tier := tierOrder[tierIdx]
			return &NextProgress{
				Tier:      tier,
				TierLabel: tierLabels[tier],
				Current:   total,
				Required:  required,
			}
		}
	}
	return nil
}
