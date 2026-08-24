// SPDX-License-Identifier: Apache-2.0

package memorygraph

// Scoring and reward (spec §7): continuous per-dimension scores in [0,1],
// the min-dimension overall, and the unclamped round-cost reward. The system
// defines no score thresholds, pass/fail labels, or fixed miss penalties.

// Overall returns the minimum of the three grading dimensions: no dimension
// compensates for another.
func (s DiveTrajectoryScore) Overall() float64 {
	m := s.Relevance
	if s.Groundedness < m {
		m = s.Groundedness
	}
	if s.Completeness < m {
		m = s.Completeness
	}
	return m
}

// ExploreReward computes reward = overall − w_round × server-counted explore
// rounds. The reward is never clamped; a low-quality/high-cost normal
// trajectory may receive a negative reward (A7).
func ExploreReward(overall, wRound float64, rounds int) float64 {
	return overall - wRound*float64(rounds)
}
