package daemon

import (
	"encoding/json"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

// Raft 1.0.16 due identity: owner + reminder id + version. A due occurrence
// must persist a local receipt, enqueue the owner wake, and only then treat
// server ack as convergence. These tests drive the shipped reminderCache.

func raft1016Job(now time.Time) protocol.ReminderTimerJob {
	return reminderJob("r-due", "owner-a", 3, now.Add(-time.Minute))
}

func TestReminderCacheDuePersistsReceiptThenWakesBeforeServerAck(t *testing.T) {
	now := time.Date(2026, 8, 13, 8, 0, 0, 0, time.UTC)
	clock := &fakeReminderClock{now: now}
	root := t.TempDir()
	var fired []protocol.ReminderTimerJob
	cache := newReminderCache(clock, slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
	cache.onFireDelivery = func(job protocol.ReminderTimerJob) bool {
		fired = append(fired, job)
		return true
	}
	cache.setPersistence(root)

	if !cache.upsert(raft1016Job(now)) {
		t.Fatal("upsert rejected")
	}
	clock.fire(len(clock.timers) - 1)

	if len(fired) != 1 || fired[0].ReminderID != "r-due" || fired[0].OwnerAgentID != "owner-a" || fired[0].Version != 3 {
		t.Fatalf("owner wake = %+v", fired)
	}
	if _, armed := cache.get("r-due"); armed {
		t.Fatal("due job stayed armed after local receipt")
	}
	receipts := cache.pendingFireReceipts()
	if len(receipts) != 1 {
		t.Fatalf("pending receipts = %+v, want one unacked due fact", receipts)
	}
	got := receipts[0]
	if got.Job.ReminderID != "r-due" || got.Job.OwnerAgentID != "owner-a" || got.Job.Version != 3 {
		t.Fatalf("receipt identity = %+v", got)
	}
	if !got.WakeEnqueued || got.ServerAcked {
		t.Fatalf("receipt flags = %+v, want wake enqueued and server still unacked", got)
	}

	reloaded := newReminderCache(clock, slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
	reloaded.setPersistence(root)
	reloadedReceipts := reloaded.pendingFireReceipts()
	if len(reloadedReceipts) != 1 || reloadedReceipts[0].Job.ReminderID != "r-due" || !reloadedReceipts[0].WakeEnqueued || reloadedReceipts[0].ServerAcked {
		t.Fatalf("reloaded receipts = %+v, want the same unacked due fact", reloadedReceipts)
	}
}

func TestReminderCacheRetriesUntilWakeEnqueued(t *testing.T) {
	now := time.Date(2026, 8, 13, 8, 0, 0, 0, time.UTC)
	clock := &fakeReminderClock{now: now}
	attempts := 0
	cache := newReminderCache(clock, slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
	cache.fireRetryDelay = time.Second
	cache.onFireDelivery = func(protocol.ReminderTimerJob) bool {
		attempts++
		return attempts >= 3
	}
	if !cache.upsert(raft1016Job(now)) {
		t.Fatal("upsert rejected")
	}
	clock.fire(0)
	if attempts != 1 {
		t.Fatalf("attempts after due = %d, want 1", attempts)
	}
	receipts := cache.pendingFireReceipts()
	if len(receipts) != 1 || receipts[0].WakeEnqueued {
		t.Fatalf("receipt after failed wake = %+v", receipts)
	}
	if len(clock.timers) < 2 {
		t.Fatalf("retry timers = %d, want a fire retry after failed wake", len(clock.timers))
	}
	clock.fire(len(clock.timers) - 1)
	if attempts != 2 {
		t.Fatalf("attempts after first retry = %d, want 2", attempts)
	}
	clock.fire(len(clock.timers) - 1)
	if attempts != 3 {
		t.Fatalf("attempts after second retry = %d, want 3", attempts)
	}
	receipts = cache.pendingFireReceipts()
	if len(receipts) != 1 || !receipts[0].WakeEnqueued || receipts[0].ServerAcked {
		t.Fatalf("receipt after wake = %+v, want wake enqueued and still unacked", receipts)
	}
	retryTimers := 0
	for i := 1; i < len(clock.timers); i++ {
		if !clock.timers[i].stopped {
			retryTimers++
		}
	}
	if retryTimers != 0 {
		t.Fatalf("active retry timers after wake = %d, want 0", retryTimers)
	}
}

func TestReminderCacheSnapshotKeepsInFlightReceipt(t *testing.T) {
	now := time.Date(2026, 8, 13, 8, 0, 0, 0, time.UTC)
	clock := &fakeReminderClock{now: now}
	cache := newReminderCache(clock, slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
	cache.onFireDelivery = func(protocol.ReminderTimerJob) bool { return true }
	job := raft1016Job(now)
	if !cache.upsert(job) {
		t.Fatal("upsert rejected")
	}
	clock.fire(len(clock.timers) - 1)
	if len(cache.pendingFireReceipts()) != 1 {
		t.Fatal("expected a persisted due receipt before snapshot")
	}

	installed, err := cache.snapshot("runtime-a", "owner-a", 3, []protocol.ReminderTimerJob{job})
	if err != nil {
		t.Fatal(err)
	}
	_ = installed
	receipts := cache.pendingFireReceipts()
	if len(receipts) != 1 || receipts[0].Job.ReminderID != "r-due" || receipts[0].Job.Version != 3 {
		t.Fatalf("snapshot dropped in-flight receipt: installed=%d receipts=%+v", installed, receipts)
	}

	otherInstalled, err := cache.snapshot("runtime-b", "owner-b", 1, []protocol.ReminderTimerJob{
		reminderJob("cross", "owner-a", 9, now.Add(time.Hour)),
	})
	if err != nil {
		t.Fatal(err)
	}
	if otherInstalled != 0 {
		t.Fatalf("cross-owner snapshot installed = %d, want 0", otherInstalled)
	}
	if _, ok := cache.get("cross"); ok {
		t.Fatal("cross-owner snapshot armed a foreign job")
	}
	if got := cache.pendingFireReceipts(); len(got) != 1 || got[0].Job.OwnerAgentID != "owner-a" {
		t.Fatalf("cross-owner snapshot disturbed owner-a receipt: %+v", got)
	}
}

func TestReminderCacheStaleUpsertAndWrongOwnerCancelDoNotDropReceipt(t *testing.T) {
	now := time.Date(2026, 8, 13, 8, 0, 0, 0, time.UTC)
	clock := &fakeReminderClock{now: now}
	cache := newReminderCache(clock, slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
	cache.onFireDelivery = func(protocol.ReminderTimerJob) bool { return true }
	if !cache.upsert(reminderJob("r1", "owner-a", 2, now.Add(time.Hour))) {
		t.Fatal("seed upsert rejected")
	}
	if cache.upsert(reminderJob("r1", "owner-a", 2, now.Add(2*time.Hour))) {
		t.Fatal("equal upsert replaced the current timer")
	}
	if cache.upsert(reminderJob("r1", "owner-a", 1, now.Add(2*time.Hour))) {
		t.Fatal("stale upsert replaced the current timer")
	}
	if cache.cancelOwned("r1", 2, "owner-b") {
		t.Fatal("wrong-owner cancel applied")
	}
	if cache.cancelOwned("r1", 1, "owner-a") {
		t.Fatal("stale cancel applied")
	}
	if _, ok := cache.get("r1"); !ok {
		t.Fatal("fencing removed the current timer")
	}

	past := reminderJob("r1", "owner-a", 3, now.Add(-time.Minute))
	if !cache.upsert(past) {
		t.Fatal("newer due upsert rejected")
	}
	clock.fire(len(clock.timers) - 1)
	if cache.cancelOwned("r1", 3, "owner-b") {
		t.Fatal("wrong-owner cancel dropped a due receipt")
	}
	if got := cache.pendingFireReceipts(); len(got) != 1 || got[0].Job.Version != 3 {
		t.Fatalf("receipt after wrong-owner cancel = %+v", got)
	}
}

func TestReminderCacheAckConvergesOnlyAfterWakeAndServerAck(t *testing.T) {
	now := time.Date(2026, 8, 13, 8, 0, 0, 0, time.UTC)
	clock := &fakeReminderClock{now: now}
	wake := false
	cache := newReminderCache(clock, slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
	cache.fireRetryDelay = time.Second
	cache.onFireDelivery = func(protocol.ReminderTimerJob) bool { return wake }
	if !cache.upsert(raft1016Job(now)) {
		t.Fatal("upsert rejected")
	}
	clock.fire(0)
	identity := reminderDueIdentity{OwnerAgentID: "owner-a", ReminderID: "r-due", Version: 3}
	if !cache.ackFireReceipt(identity) {
		t.Fatal("server ack of unconfirmed due fact rejected")
	}
	receipts := cache.pendingFireReceipts()
	if len(receipts) != 1 || !receipts[0].ServerAcked || receipts[0].WakeEnqueued {
		t.Fatalf("acked-without-wake receipts = %+v, want receipt retained until wake", receipts)
	}

	wake = true
	clock.fire(len(clock.timers) - 1)
	if got := cache.pendingFireReceipts(); len(got) != 0 {
		t.Fatalf("pending after wake+ack = %+v, want empty", got)
	}
}

func TestOnReminderTimerDoesNotSendFireAttemptUntilOwnerWakeEnqueued(t *testing.T) {
	now := time.Date(2026, 8, 13, 8, 0, 0, 0, time.UTC)
	clock := &fakeReminderClock{now: now}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	mgr := newLocalAgentAttachmentRegistry("", logger)
	cache := newReminderCache(clock, logger, nil)
	writes := make(chan []byte, 4)
	var d *Daemon
	cache.fireRetryDelay = time.Second
	cache.onFireDelivery = func(job protocol.ReminderTimerJob) bool { return d.onReminderTimer(job) }
	d = &Daemon{
		logger: logger, agentAttachments: mgr, reminderCache: cache,
		runtimeIndex: map[string]Runtime{"runtime-x": {ID: "runtime-x", WorkspaceID: "workspace-x"}},
	}
	d.setReminderWS(writes, make(chan struct{}), func() error { return nil })
	cache.resume()

	job := reminderJob("r1", "agent-x", 1, now.Add(-time.Minute))
	if applied, err := cache.applyProjection(reminderProjection(1, "runtime-x", "upsert", job, false)); err != nil || !applied {
		t.Fatalf("apply due job = %v, %v", applied, err)
	}
	clock.fire(0)
	select {
	case frame := <-writes:
		t.Fatalf("fire_attempt sent before owner wake: %s", frame)
	default:
	}
	if got := cache.pendingFireReceipts(); len(got) != 1 || got[0].WakeEnqueued {
		t.Fatalf("missing-owner receipts = %+v, want one unenqueued due fact", got)
	}
	if len(clock.timers) < 2 {
		t.Fatal("missing owner did not schedule a wake retry")
	}

	if _, err := mgr.Apply("workspace-x", AgentAttachmentEvent{
		Kind: AgentAttachmentEventAttach, AgentID: "agent-x", RuntimeID: "runtime-x",
		AttachmentGeneration: 1, LifecycleSeq: 1,
	}); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	clock.fire(len(clock.timers) - 1)
	select {
	case frame := <-writes:
		var msg protocol.Message
		if err := json.Unmarshal([]byte(frame), &msg); err != nil || msg.Type != protocol.EventReminderFireAttempt {
			t.Fatalf("post-wake frame = %s, want fire_attempt", frame)
		}
	default:
		t.Fatal("owner wake did not send fire_attempt")
	}
	if got := cache.pendingFireReceipts(); len(got) != 1 || !got[0].WakeEnqueued || got[0].ServerAcked {
		t.Fatalf("post-wake receipts = %+v, want wake enqueued and server unacked", got)
	}
}

func TestOnReminderTimerDoesNotMarkWakeWhenWebsocketUnavailable(t *testing.T) {
	now := time.Date(2026, 8, 13, 8, 0, 0, 0, time.UTC)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	assertUnenqueuedRetry := func(t *testing.T, label string, setup func(*fakeReminderClock, *reminderCache, *Daemon)) {
		t.Helper()
		clock := &fakeReminderClock{now: now}
		mgr := newLocalAgentAttachmentRegistry("", logger)
		cache := newReminderCache(clock, logger, nil)
		cache.fireRetryDelay = time.Second
		var d *Daemon
		cache.onFireDelivery = func(job protocol.ReminderTimerJob) bool { return d.onReminderTimer(job) }
		d = &Daemon{
			logger: logger, agentAttachments: mgr, reminderCache: cache,
			runtimeIndex: map[string]Runtime{"runtime-x": {ID: "runtime-x", WorkspaceID: "workspace-x"}},
		}
		if _, err := mgr.Apply("workspace-x", AgentAttachmentEvent{
			Kind: AgentAttachmentEventAttach, AgentID: "agent-x", RuntimeID: "runtime-x",
			AttachmentGeneration: 1, LifecycleSeq: 1,
		}); err != nil {
			t.Fatalf("%s Apply: %v", label, err)
		}
		setup(clock, cache, d)
		job := reminderJob("r-ws", "agent-x", 1, now.Add(-time.Minute))
		if applied, err := cache.applyProjection(reminderProjection(1, "runtime-x", "upsert", job, false)); err != nil || !applied {
			t.Fatalf("%s apply due job = %v, %v", label, applied, err)
		}
		clock.fire(len(clock.timers) - 1)
		if got := cache.pendingFireReceipts(); len(got) != 1 || got[0].WakeEnqueued || got[0].Job.ReminderID != "r-ws" {
			t.Fatalf("%s receipts = %+v, want one unenqueued due fact", label, got)
		}
		retryTimers := 0
		for _, timer := range clock.timers {
			if !timer.stopped {
				retryTimers++
			}
		}
		if retryTimers == 0 {
			t.Fatalf("%s dropped the wake retry after a failed enqueue", label)
		}
	}

	t.Run("nil writes", func(t *testing.T) {
		assertUnenqueuedRetry(t, "nil writes", func(*fakeReminderClock, *reminderCache, *Daemon) {})
	})
	t.Run("closed writes", func(t *testing.T) {
		assertUnenqueuedRetry(t, "closed writes", func(clock *fakeReminderClock, cache *reminderCache, d *Daemon) {
			done := make(chan struct{})
			close(done)
			d.setReminderWS(make(chan []byte), done, func() error { return nil })
			cache.resume()
		})
	})
}
