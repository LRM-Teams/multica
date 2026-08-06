package memorygrowth

import "testing"

func TestComputeZeroWritesNil(t *testing.T) {
	if got := Compute(0, DefaultBase, DefaultRatio); got != nil {
		t.Fatalf("want nil for zero writes, got %#v", got)
	}
}

func TestComputeBronzeProgress(t *testing.T) {
	snap := Compute(2, DefaultBase, DefaultRatio)
	if snap == nil {
		t.Fatal("expected snapshot")
	}
	if snap.Tier != "bronze" || snap.TotalWrites != 2 {
		t.Fatalf("tier=%s writes=%d", snap.Tier, snap.TotalWrites)
	}
	if snap.Segments[0].Status != "current" {
		t.Fatalf("bronze segment=%s want current", snap.Segments[0].Status)
	}
	if snap.Next == nil || snap.Next.Tier != "silver" || snap.Next.Current != 2 || snap.Next.Required != 3 {
		t.Fatalf("next=%#v", snap.Next)
	}
}

func TestComputeSilverProgress(t *testing.T) {
	snap := Compute(5, DefaultBase, DefaultRatio)
	if snap.Tier != "silver" {
		t.Fatalf("tier=%s", snap.Tier)
	}
	if snap.Segments[0].Status != "complete" || snap.Segments[1].Status != "current" {
		t.Fatalf("segments=%v", snap.Segments)
	}
	if snap.Next == nil || snap.Next.Tier != "gold" || snap.Next.Current != 5 || snap.Next.Required != 6 {
		t.Fatalf("next=%#v", snap.Next)
	}
}

func TestComputePlatinumProgress(t *testing.T) {
	snap := Compute(12, DefaultBase, DefaultRatio)
	if snap.Tier != "platinum" {
		t.Fatalf("tier=%s", snap.Tier)
	}
	if snap.Segments[3].Status != "current" {
		t.Fatalf("platinum segment=%s", snap.Segments[3].Status)
	}
	if snap.Next == nil || snap.Next.Required != 24 || snap.Next.Current != 12 {
		t.Fatalf("next=%#v", snap.Next)
	}
}

func TestComputeMaxTierNoNext(t *testing.T) {
	snap := Compute(24, DefaultBase, DefaultRatio)
	if snap.Tier != "platinum" {
		t.Fatalf("tier=%s", snap.Tier)
	}
	for _, seg := range snap.Segments {
		if seg.Status != "complete" {
			t.Fatalf("segment %s status=%s want complete", seg.Tier, seg.Status)
		}
	}
	if snap.Next != nil {
		t.Fatalf("expected no next progress at max, got %#v", snap.Next)
	}
}

func TestTierThresholdsBaseRatio(t *testing.T) {
	got := tierThresholds(3, 2)
	want := []int{3, 6, 12, 24}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("threshold[%d]=%d want %d", i, got[i], want[i])
		}
	}
}
