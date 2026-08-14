package daemon

import (
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

type fakeReminderTimer struct {
	fn      func()
	stopped bool
}

func (t *fakeReminderTimer) Stop() bool {
	wasActive := !t.stopped
	t.stopped = true
	return wasActive
}

type fakeReminderClock struct {
	now    time.Time
	timers []*fakeReminderTimer
	delays []time.Duration
}

func (c *fakeReminderClock) Now() time.Time { return c.now }
func (c *fakeReminderClock) AfterFunc(delay time.Duration, fn func()) reminderTimer {
	timer := &fakeReminderTimer{fn: fn}
	c.timers = append(c.timers, timer)
	c.delays = append(c.delays, delay)
	return timer
}
func (c *fakeReminderClock) fire(index int) {
	timer := c.timers[index]
	if !timer.stopped {
		timer.stopped = true
		timer.fn()
	}
}

func reminderJob(id, owner string, version int64, fireAt time.Time) protocol.ReminderTimerJob {
	return protocol.ReminderTimerJob{ReminderID: id, OwnerAgentID: owner, Version: version, FireAt: fireAt.Format(time.RFC3339Nano)}
}

func TestReminderCacheDirectUpsertCancelVersionFence(t *testing.T) {
	now := time.Date(2026, 7, 22, 8, 0, 0, 0, time.UTC)
	clock := &fakeReminderClock{now: now}
	cache := newReminderCache(clock, slog.New(slog.NewTextHandler(io.Discard, nil)), nil)

	if applied, err := cache.upsertForRuntime("runtime-a", reminderJob("r1", "a1", 2, now.Add(time.Hour))); err != nil || !applied {
		t.Fatalf("initial upsert applied=%v err=%v", applied, err)
	}
	if applied, err := cache.upsertForRuntime("runtime-a", reminderJob("r1", "a1", 1, now.Add(2*time.Hour))); err != nil || applied {
		t.Fatalf("stale upsert applied=%v err=%v", applied, err)
	}
	if applied, err := cache.cancelForRuntime("runtime-a", "a1", "r1", 1); err != nil || applied {
		t.Fatalf("stale cancel applied=%v err=%v", applied, err)
	}
	if applied, err := cache.cancelForRuntime("runtime-a", "a1", "r1", 3); err != nil || !applied {
		t.Fatalf("new cancel applied=%v err=%v", applied, err)
	}
	if _, ok := cache.get("r1"); ok {
		t.Fatal("cancelled reminder remained armed")
	}
}

func TestReminderCacheRuntimeSnapshotReplacesOnlyThatRuntime(t *testing.T) {
	now := time.Date(2026, 7, 22, 8, 0, 0, 0, time.UTC)
	cache := newReminderCache(&fakeReminderClock{now: now}, nil, nil)
	_, _ = cache.upsertForRuntime("runtime-a", reminderJob("a-old", "a", 1, now.Add(time.Hour)))
	_, _ = cache.upsertForRuntime("runtime-b", reminderJob("b-keep", "b", 1, now.Add(time.Hour)))
	if err := cache.replaceRuntime("runtime-a", []protocol.ReminderTimerJob{reminderJob("a-new", "a", 2, now.Add(2*time.Hour))}); err != nil {
		t.Fatal(err)
	}
	if _, ok := cache.get("a-old"); ok {
		t.Fatal("runtime snapshot retained stale timer")
	}
	if _, ok := cache.get("a-new"); !ok {
		t.Fatal("runtime snapshot did not install current timer")
	}
	if _, ok := cache.get("b-keep"); !ok {
		t.Fatal("runtime snapshot removed another Runtime's timer")
	}
}

func TestReminderCacheRuntimeSnapshotDoesNotResurrectDueReceipt(t *testing.T) {
	now := time.Date(2026, 7, 22, 8, 0, 0, 0, time.UTC)
	clock := &fakeReminderClock{now: now}
	cache := newReminderCache(clock, nil, nil)
	cache.onFireDelivery = func(protocol.ReminderTimerJob) bool { return true }
	job := reminderJob("r1", "a1", 1, now.Add(-time.Minute))
	_, _ = cache.upsertForRuntime("runtime-a", job)
	clock.fire(0)
	if err := cache.replaceRuntime("runtime-a", []protocol.ReminderTimerJob{job}); err != nil {
		t.Fatal(err)
	}
	if _, ok := cache.get("r1"); ok {
		t.Fatal("snapshot rearmed an occurrence with a durable due receipt")
	}
}

func TestReminderCacheRuntimeSetDropsDepartedRuntimeOnly(t *testing.T) {
	now := time.Date(2026, 7, 22, 8, 0, 0, 0, time.UTC)
	cache := newReminderCache(&fakeReminderClock{now: now}, nil, nil)
	_, _ = cache.upsertForRuntime("runtime-a", reminderJob("a", "owner-a", 1, now.Add(time.Hour)))
	_, _ = cache.upsertForRuntime("runtime-b", reminderJob("b", "owner-b", 1, now.Add(time.Hour)))
	changed, err := cache.reconcileRuntimeSet(map[string]bool{"runtime-b": true})
	if err != nil || !changed {
		t.Fatalf("reconcile changed=%v err=%v", changed, err)
	}
	if _, ok := cache.get("a"); ok {
		t.Fatal("departed Runtime timer remained")
	}
	if _, ok := cache.get("b"); !ok {
		t.Fatal("owned Runtime timer was removed")
	}
}
