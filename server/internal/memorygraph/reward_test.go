// SPDX-License-Identifier: Apache-2.0

package memorygraph

import "testing"

// Spec §7, acceptance A7: overall is the minimum of the three dimensions (no
// compensation); reward = overall − w_round × server-counted explore rounds,
// unclamped, with no threshold, pass/fail label, or fixed miss penalty.
// Negative rewards are possible for low-quality/high-cost normal runs.

func TestOverallScoreIsMinDimension(t *testing.T) {
	cases := []struct {
		name  string
		score DiveTrajectoryScore
		want  float64
	}{
		{"relevance binds", DiveTrajectoryScore{Relevance: 0.1, Groundedness: 0.9, Completeness: 0.9}, 0.1},
		{"groundedness binds", DiveTrajectoryScore{Relevance: 0.9, Groundedness: 0.2, Completeness: 0.9}, 0.2},
		{"completeness binds", DiveTrajectoryScore{Relevance: 0.9, Groundedness: 0.9, Completeness: 0.3}, 0.3},
		{"all equal", DiveTrajectoryScore{Relevance: 0.5, Groundedness: 0.5, Completeness: 0.5}, 0.5},
		{"perfect", DiveTrajectoryScore{Relevance: 1, Groundedness: 1, Completeness: 1}, 1},
		{"zero floor", DiveTrajectoryScore{Relevance: 0.8, Groundedness: 0, Completeness: 0.8}, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.score.Overall(); got != c.want {
				t.Fatalf("Overall() = %v, want %v (min dimension, no compensation)", got, c.want)
			}
		})
	}
}

func TestExploreRewardFormula(t *testing.T) {
	cases := []struct {
		name    string
		overall float64
		wRound  float64
		rounds  int
		want    float64
	}{
		{"basic", 0.9, 0.1, 3, 0.6},
		{"zero rounds", 0.7, 0.1, 0, 0.7},
		{"zero weight", 0.7, 0, 9, 0.7},
		// A low-quality/high-cost normal run goes negative; the reward is
		// never clamped (A7).
		{"negative allowed", 0.2, 0.1, 5, -0.3},
		{"deeply negative", 0.0, 0.25, 8, -2.0},
		// A miss run is graded by the same formula: no fixed miss penalty.
		{"miss uses same formula", 0.4, 0.1, 2, 0.2},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := ExploreReward(c.overall, c.wRound, c.rounds)
			if got < c.want-1e-9 || got > c.want+1e-9 {
				t.Fatalf("ExploreReward(%v, %v, %d) = %v, want %v", c.overall, c.wRound, c.rounds, got, c.want)
			}
		})
	}
}
