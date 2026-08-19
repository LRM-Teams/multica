package handler

import (
	"testing"
	"time"
)

func TestResolveNotePeriodBriefCustomWindowInclusiveDays(t *testing.T) {
	t.Parallel()
	got, err := resolveNotePeriodBriefWindow(
		noteRetrospectiveWindowCustom, "", "2026-08-10", "2026-08-12", "Asia/Shanghai", time.Time{},
	)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got.Kind != noteRetrospectiveWindowCustom {
		t.Fatalf("kind = %q", got.Kind)
	}
	if got.Label != "2026-08-10→2026-08-12" {
		t.Fatalf("label = %q", got.Label)
	}
	// Shanghai UTC+8: Aug 10 00:00 CST = Aug 9 16:00 UTC; exclusive end Aug 13 00:00 CST.
	wantStart := time.Date(2026, 8, 9, 16, 0, 0, 0, time.UTC)
	wantEnd := time.Date(2026, 8, 12, 16, 0, 0, 0, time.UTC)
	if !got.Start.Equal(wantStart) || !got.End.Equal(wantEnd) {
		t.Fatalf("range = %s → %s, want %s → %s", got.Start, got.End, wantStart, wantEnd)
	}
}

func TestResolveNotePeriodBriefCustomWindowRejectsInvertedAndOverlong(t *testing.T) {
	t.Parallel()
	if _, err := resolveNotePeriodBriefWindow(
		noteRetrospectiveWindowCustom, "", "2026-08-12", "2026-08-10", "UTC", time.Time{},
	); err == nil {
		t.Fatal("expected inverted range error")
	}
	if _, err := resolveNotePeriodBriefWindow(
		noteRetrospectiveWindowCustom, "", "2026-01-01", "2026-05-01", "UTC", time.Time{},
	); err == nil {
		t.Fatal("expected overlong range error")
	}
}

func TestResolveNotePeriodBriefWindowDelegatesWeek(t *testing.T) {
	t.Parallel()
	got, err := resolveNotePeriodBriefWindow(
		noteRetrospectiveWindowWeek, "2026-08-13", "", "", "Asia/Shanghai", time.Time{},
	)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got.Kind != noteRetrospectiveWindowWeek || !stringsHasPrefix(got.Label, "2026-W") {
		t.Fatalf("got %+v", got)
	}
}

func stringsHasPrefix(s, prefix string) bool {
	return len(s) >= len(prefix) && s[:len(prefix)] == prefix
}
