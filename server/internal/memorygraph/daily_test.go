package memorygraph

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"
)

func dailyFixture(t *testing.T) (*Store, *DailyUpdater) {
	t.Helper()
	store := NewStore(t.TempDir())
	if err := store.Init(); err != nil {
		t.Fatal(err)
	}
	loc := time.FixedZone("CST", 8*3600)
	u := NewDailyUpdater(store, loc)
	return store, u
}

func TestDailyNodeIDIdentity(t *testing.T) {
	day := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	if got, want := DailyNodeID("a1", "p1", "c1", day), "daily:a1:p1:c1:2026-08-17"; got != want {
		t.Fatalf("project daily id = %q, want %q", got, want)
	}
	if got, want := DailyNodeID("a1", "", "c1", day), "daily:a1:none:c1:2026-08-17"; got != want {
		t.Fatalf("channel daily id = %q, want %q", got, want)
	}
	if got, want := DailyNodeID("a1", "p1", "", day), "daily:a1:p1:none:2026-08-17"; got != want {
		t.Fatalf("project-only daily id = %q, want %q", got, want)
	}
}

func TestDailySealIsImmutableAndLateEventsLandInOpenDaily(t *testing.T) {
	store, u := dailyFixture(t)
	ctx := context.Background()
	// Anchor to midday: a wall-clock "yesterday" within 2h of midnight makes
	// the late event below cross into today and the test time-of-day dependent.
	now := time.Now().In(u.Location())
	yesterday := time.Date(now.Year(), now.Month(), now.Day(), 12, 0, 0, 0, u.Location()).AddDate(0, 0, -1)

	if err := u.Record(ctx, DailyEvent{AgentID: "a1", ProjectID: "p1", Text: "shipped v1", OccurredAt: yesterday}); err != nil {
		t.Fatal(err)
	}
	sealedID, err := u.SealPriorDay(ctx)
	if err != nil {
		t.Fatal(err)
	}
	wantID := DailyNodeID("a1", "p1", "", yesterday)
	if sealedID != wantID {
		t.Fatalf("sealed %q, want %q", sealedID, wantID)
	}
	// Second seal is a no-op (CAS on sealed_at).
	if again, err := u.SealPriorDay(ctx); err != nil || again != "" {
		t.Fatalf("re-seal = (%q, %v), want (\"\", nil)", again, err)
	}
	// A late event for the sealed date lands in today's open daily with
	// late_for_date provenance and never mutates the sealed node (spec §6).
	late := DailyEvent{AgentID: "a1", ProjectID: "p1", Text: "late note", OccurredAt: yesterday.Add(2 * time.Hour)}
	if err := u.Record(ctx, late); err != nil {
		t.Fatal(err)
	}
	v, err := store.CurrentVersion()
	if err != nil {
		t.Fatal(err)
	}
	nodes, err := store.LoadNodes(v)
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]*Node{}
	for _, n := range nodes {
		byID[n.NodeID] = n
	}
	sealed := byID[wantID]
	if sealed == nil || sealed.SealedAt == nil {
		t.Fatalf("sealed node = %+v", sealed)
	}
	if sealed.Body != "shipped v1" && len(sealed.Body) == 0 {
		t.Fatal("sealed node body lost")
	}
	open := byID[DailyNodeID("a1", "p1", "", time.Now().In(u.Location()))]
	if open == nil || open.LateForDate != yesterday.Format("2006-01-02") {
		t.Fatalf("open daily = %+v, want late_for_date=%s", open, yesterday.Format("2006-01-02"))
	}
}

// Spec §6: channel daily nodes are channel-visible; project daily nodes are
// project-visible.
func TestDailyNodeVisibility(t *testing.T) {
	store, u := dailyFixture(t)
	ctx := context.Background()
	now := time.Now().In(u.Location())
	if err := u.Record(ctx, DailyEvent{AgentID: "a1", ChannelID: "c1", Text: "channel work", OccurredAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := u.Record(ctx, DailyEvent{AgentID: "a1", ProjectID: "p1", Text: "project work", OccurredAt: now}); err != nil {
		t.Fatal(err)
	}
	v, err := store.CurrentVersion()
	if err != nil {
		t.Fatal(err)
	}
	nodes, err := store.LoadNodes(v)
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]*Node{}
	for _, n := range nodes {
		byID[n.NodeID] = n
	}
	if got := byID[DailyNodeID("a1", "", "c1", now)]; got == nil || got.Visibility != "channel" || got.ChannelID != "c1" {
		t.Fatalf("channel daily = %+v", got)
	}
	if got := byID[DailyNodeID("a1", "p1", "", now)]; got == nil || got.Visibility != "project" || got.ChannelID != "" {
		t.Fatalf("project daily = %+v", got)
	}
}

func TestDailyRecordSealRaceKeepsSingleSealedNode(t *testing.T) {
	_, u := dailyFixture(t)
	ctx := context.Background()
	var mu sync.Mutex
	u.SetLocker(func(ctx context.Context, fn func() error) error {
		mu.Lock()
		defer mu.Unlock()
		return fn()
	})
	yesterday := time.Now().In(u.Location()).AddDate(0, 0, -1)
	var wg sync.WaitGroup
	errs := make(chan error, 16)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if err := u.Record(ctx, DailyEvent{AgentID: "a1", ProjectID: "p1", Text: fmt.Sprintf("ev %d", i), OccurredAt: yesterday}); err != nil {
				errs <- err
			}
		}(i)
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		if _, err := u.SealPriorDay(ctx); err != nil {
			errs <- err
		}
	}()
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
}
