package problemevolution

import (
	"fmt"
	"math"
)

// ScoreDimension is one dimension's contribution to a candidate score.
type ScoreDimension struct {
	DimensionID string  `json:"dimension_id"`
	Score       float64 `json:"score"`
	Weight      float64 `json:"weight"`
	Hard        bool    `json:"hard,omitempty"`
}

// Score is the candidate-level scorecard persisted as JSONB. An unscored
// candidate stores SQL NULL rather than a zero total, so "not evaluated" and
// "scored zero" stay distinguishable.
type Score struct {
	SchemaVersion  int              `json:"schema_version"`
	Total          float64          `json:"total"`
	Scale          string           `json:"scale"`
	HardGatePassed bool             `json:"hard_gate_passed"`
	Dimensions     []ScoreDimension `json:"dimensions"`
}

// Behavior profile kinds. Mode A compares scored dimensions; mode B compares
// verifier rewards, degenerating to a single entry for a binary reward.
const (
	BehaviorKindDimensionVector = "dimension_vector"
	BehaviorKindRewardVector    = "reward_vector"
)

// BehaviorEntry is one axis of a behavior profile.
type BehaviorEntry struct {
	Key   string  `json:"key"`
	Value float64 `json:"value"`
}

// BehaviorProfile drives complementarity selection: two candidates with the
// same total but different strengths are more useful than two similar ones.
type BehaviorProfile struct {
	SchemaVersion int             `json:"schema_version"`
	Kind          string          `json:"kind"`
	Entries       []BehaviorEntry `json:"entries"`
}

// Validate rejects scores the canvas and selection logic cannot interpret.
func (s Score) Validate() error {
	if s.SchemaVersion != SchemaVersion {
		return fmt.Errorf("unsupported score schema_version %d", s.SchemaVersion)
	}
	if s.Scale != ScaleUnitInterval {
		return fmt.Errorf("unsupported score scale %q", s.Scale)
	}
	if err := validateUnitInterval("total", s.Total); err != nil {
		return err
	}
	if len(s.Dimensions) == 0 {
		return fmt.Errorf("score needs at least one dimension")
	}
	seen := make(map[string]struct{}, len(s.Dimensions))
	for _, dimension := range s.Dimensions {
		if dimension.DimensionID == "" {
			return fmt.Errorf("score dimension_id is required")
		}
		if _, duplicate := seen[dimension.DimensionID]; duplicate {
			return fmt.Errorf("duplicate score dimension_id %q", dimension.DimensionID)
		}
		seen[dimension.DimensionID] = struct{}{}
		if err := validateUnitInterval("dimension "+dimension.DimensionID, dimension.Score); err != nil {
			return err
		}
	}
	return nil
}

// Validate rejects behavior profiles with out-of-range or unnamed axes.
func (p BehaviorProfile) Validate() error {
	if p.SchemaVersion != SchemaVersion {
		return fmt.Errorf("unsupported behavior_profile schema_version %d", p.SchemaVersion)
	}
	switch p.Kind {
	case BehaviorKindDimensionVector, BehaviorKindRewardVector:
	default:
		return fmt.Errorf("unsupported behavior_profile kind %q", p.Kind)
	}
	if len(p.Entries) == 0 {
		return fmt.Errorf("behavior_profile needs at least one entry")
	}
	for _, entry := range p.Entries {
		if entry.Key == "" {
			return fmt.Errorf("behavior_profile entry key is required")
		}
		if err := validateUnitInterval("behavior "+entry.Key, entry.Value); err != nil {
			return err
		}
	}
	return nil
}

func validateUnitInterval(label string, value float64) error {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return fmt.Errorf("%s must be a finite number", label)
	}
	if value < 0 || value > 1 {
		return fmt.Errorf("%s must be within 0..1", label)
	}
	return nil
}
