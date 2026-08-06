package service

import (
	"math"
	"testing"
)

func TestDeliveryPillar(t *testing.T) {
	t.Parallel()
	score := deliveryPillar(10, 0)
	if score < 80 {
		t.Fatalf("expected strong delivery score, got %v", score)
	}
	if deliveryPillar(0, 0) != 0 {
		t.Fatalf("expected zero for no tasks")
	}
}

func TestEvolutionPillar(t *testing.T) {
	t.Parallel()
	score := evolutionPillar(8, 10, 2)
	if score < 50 {
		t.Fatalf("expected meaningful evolution score, got %v", score)
	}
}

func TestClassID(t *testing.T) {
	t.Parallel()
	cases := []struct {
		score float64
		want  string
	}{
		{90, "dreadnought"},
		{75, "battleship"},
		{60, "cruiser"},
		{45, "frigate"},
		{30, "corvette"},
		{10, "reserve"},
	}
	for _, tc := range cases {
		got := fleetClassFromScore(tc.score, true)
		if got != tc.want {
			t.Fatalf("score %v => %q, want %q", tc.score, got, tc.want)
		}
	}
	if fleetClassFromScore(90, false) != "reserve" {
		t.Fatal("insufficient sample should force reserve")
	}
}

func TestFleetScoreWeightsSum(t *testing.T) {
	t.Parallel()
	sum := fleetWeightDelivery + fleetWeightEvolution + fleetWeightGrowth + fleetWeightEfficiency
	if math.Abs(sum-1.0) > 0.0001 {
		t.Fatalf("pillar weights must sum to 1, got %v", sum)
	}
}
