package daemon

import (
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

func TestReminderCacheReplaysUnackedServerReceiptOnlyOnReconnect(t *testing.T) {
	now := time.Date(2026, 8, 13, 8, 0, 0, 0, time.UTC)
	clock := &fakeReminderClock{now: now}
	wakeAttempts := 0
	receiptAttempts := 0
	cache := newReminderCache(clock, slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
	cache.fireRetryDelay = time.Second
	cache.onFireDelivery = func(protocol.ReminderTimerJob) bool {
		wakeAttempts++
		return true
	}
	cache.onFireReceipt = func(reminderDueReceipt) bool {
		receiptAttempts++
		return true
	}
	job := raft1016Job(now)
	job.LocalInput = &protocol.ReminderLocalInputPayload{Title: "local due"}
	if !cache.upsert(job) {
		t.Fatal("upsert rejected")
	}

	clock.fire(0)
	if wakeAttempts != 1 || receiptAttempts != 1 {
		t.Fatalf("initial attempts wake=%d receipt=%d, want 1/1", wakeAttempts, receiptAttempts)
	}
	activeRetryTimers := 0
	for _, timer := range clock.timers[1:] {
		if !timer.stopped {
			activeRetryTimers++
		}
	}
	if activeRetryTimers != 0 {
		t.Fatalf("active retry timers after local wake = %d, want 0 before reconnect", activeRetryTimers)
	}

	cache.suspend()
	cache.resume()
	if wakeAttempts != 1 {
		t.Fatalf("local wake attempts after reconnect = %d, want exactly one", wakeAttempts)
	}
	if receiptAttempts != 2 {
		t.Fatalf("server receipt attempts after reconnect = %d, want one replay", receiptAttempts)
	}

	identity := reminderDueIdentity{OwnerAgentID: job.OwnerAgentID, ReminderID: job.ReminderID, Version: job.Version}
	if !cache.ackFireReceipt(identity) {
		t.Fatal("server ack rejected")
	}
	if got := cache.pendingFireReceipts(); len(got) != 0 {
		t.Fatalf("pending receipts after ack = %+v", got)
	}
	cache.suspend()
	cache.resume()
	if receiptAttempts != 2 {
		t.Fatalf("server receipt attempts after ack reconnect = %d, want no replay", receiptAttempts)
	}
}

func TestReminderFireResultAcknowledgesOnlyItsAttemptedOccurrence(t *testing.T) {
	now := time.Date(2026, 8, 13, 8, 0, 0, 0, time.UTC)
	clock := &fakeReminderClock{now: now}
	cache := newReminderCache(clock, slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
	cache.onFireDelivery = func(protocol.ReminderTimerJob) bool { return true }

	first := reminderJob("recurring", "owner-a", 1, now.Add(-2*time.Minute))
	first.LocalInput = &protocol.ReminderLocalInputPayload{Title: "first due"}
	if !cache.upsert(first) {
		t.Fatal("first occurrence upsert rejected")
	}
	clock.fire(len(clock.timers) - 1)

	second := reminderJob("recurring", "owner-a", 2, now.Add(-time.Minute))
	second.LocalInput = &protocol.ReminderLocalInputPayload{Title: "second due"}
	if !cache.upsert(second) {
		t.Fatal("second occurrence upsert rejected")
	}
	clock.fire(len(clock.timers) - 1)
	if got := cache.pendingFireReceipts(); len(got) != 2 {
		t.Fatalf("pending recurring receipts before ACK = %+v, want two", got)
	}

	writes := make(chan []byte, 1)
	d := &Daemon{
		reminderCache:  cache,
		reminderWrites: writes,
		reminderWSDone: make(chan struct{}),
		runtimeIndex:   map[string]Runtime{"test": {ID: "test"}},
	}
	third := reminderJob("recurring", "owner-a", 3, now.Add(time.Hour))
	if err := d.handleReminderFireResult(protocol.ReminderFireResultPayload{
		Ack: protocol.ReminderFireAckPayload{
			AgentID:    first.OwnerAgentID,
			ReminderID: first.ReminderID,
			Version:    first.Version,
		},
		Upsert: &protocol.ReminderUpsertPayload{RuntimeID: "test", AgentID: "owner-a", Reminder: third},
	}); err != nil {
		t.Fatal(err)
	}

	receipts := cache.pendingFireReceipts()
	if len(receipts) != 1 || receipts[0].Job.Version != second.Version || !receipts[0].WakeEnqueued || receipts[0].ServerAcked {
		t.Fatalf("pending recurring receipts after exact ACK = %+v, want only unacked version 2", receipts)
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

	if err := cache.replaceRuntime("runtime-a", []protocol.ReminderTimerJob{job}); err != nil {
		t.Fatal(err)
	}
	receipts := cache.pendingFireReceipts()
	if len(receipts) != 1 || receipts[0].Job.ReminderID != "r-due" || receipts[0].Job.Version != 3 {
		t.Fatalf("snapshot dropped in-flight receipt: receipts=%+v", receipts)
	}

	if err := cache.replaceRuntime("runtime-b", []protocol.ReminderTimerJob{
		reminderJob("other", "owner-b", 9, now.Add(time.Hour)),
	}); err != nil {
		t.Fatal(err)
	}
	if _, ok := cache.get("other"); !ok {
		t.Fatal("second Runtime snapshot did not install its job")
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

func TestReminderDueTimerAcceptsLocalInboxWithoutServerTransport(t *testing.T) {
	now := time.Date(2026, 8, 13, 8, 0, 0, 0, time.UTC)
	clock := &fakeReminderClock{now: now}
	runtime := &reminderOwnerInputFakeRuntime{}
	d := newReminderOwnerInputDaemon(t, runtime, true)
	d.reminderCache = newReminderCache(clock, d.logger, nil)
	d.reminderCache.onFireDelivery = d.localReminderInbox.AcceptDue
	d.reminderCache.onFireReceipt = d.queueReminderFireReceipt

	job := protocol.ReminderTimerJob{
		ReminderID:   "reminder-a",
		OwnerAgentID: "agent-a",
		Version:      4,
		FireAt:       now.Add(-time.Minute).Format(time.RFC3339Nano),
		LocalInput: &protocol.ReminderLocalInputPayload{
			Title: "Review the deployment",
			Anchor: protocol.ReminderOwnerInputAnchor{
				Available: true, ChannelID: "channel-a", MessageID: "message-a",
				Target: "channel:channel-a", ReplyTarget: "#general",
			},
			Occurrence: protocol.ReminderLocalInputOccurrence{
				OccurrenceID: "reminder-a:4",
				ScheduledFor: now.Add(-time.Minute).Format(time.RFC3339Nano),
				DueAt:        now.Add(-time.Minute).Format(time.RFC3339Nano),
			},
		},
	}
	if applied, err := d.reminderCache.upsertForRuntime("runtime-a", job); err != nil || !applied {
		t.Fatal("upsert rejected")
	}
	clock.fire(0)

	inputs := runtime.snapshot()
	if len(inputs) != 1 {
		t.Fatalf("local Reminder inputs = %d, want 1 without a server transport", len(inputs))
	}
	if got := inputs[0]; got.ReminderID != job.ReminderID || got.Version != job.Version || got.Title != job.LocalInput.Title || got.Anchor.ReplyTarget != "#general" {
		t.Fatalf("local Reminder input = %+v", got)
	}
	receipts := d.reminderCache.pendingFireReceipts()
	if len(receipts) != 1 || !receipts[0].WakeEnqueued || receipts[0].ServerAcked {
		t.Fatalf("local due receipt = %+v, want local wake with pending server receipt", receipts)
	}

	// Replaying the same durable receipt may resend the server receipt, but it
	// must not inject the already-accepted local Inbox item a second time.
	d.reminderCache.suspend()
	d.reminderCache.resume()
	if got := len(runtime.snapshot()); got != 1 {
		t.Fatalf("local Reminder inputs after receipt replay = %d, want 1", got)
	}
}

func TestReminderDueTimerRetainsLocalInboxItemUntilResidentIsIdle(t *testing.T) {
	now := time.Date(2026, 8, 13, 8, 0, 0, 0, time.UTC)
	clock := &fakeReminderClock{now: now}
	runtime := &reminderOwnerInputFakeRuntime{}
	d := newReminderOwnerInputDaemon(t, runtime, true)
	d.reminderCache = newReminderCache(clock, d.logger, nil)
	d.reminderCache.fireRetryDelay = time.Second
	d.reminderCache.onFireDelivery = d.localReminderInbox.AcceptDue
	d.reminderCache.onFireReceipt = d.queueReminderFireReceipt
	d.canonicalRuntimes.slots["agent-a\x00runtime-a"].running = true

	job := protocol.ReminderTimerJob{
		ReminderID: "reminder-b", OwnerAgentID: "agent-a", Version: 2,
		FireAt: now.Add(-time.Minute).Format(time.RFC3339Nano),
		LocalInput: &protocol.ReminderLocalInputPayload{
			Title: "Retry after compaction",
			Occurrence: protocol.ReminderLocalInputOccurrence{
				OccurrenceID: "reminder-b:2",
				ScheduledFor: now.Add(-time.Minute).Format(time.RFC3339Nano),
				DueAt:        now.Add(-time.Minute).Format(time.RFC3339Nano),
			},
		},
	}
	if applied, err := d.reminderCache.upsertForRuntime("runtime-a", job); err != nil || !applied {
		t.Fatal("upsert rejected")
	}
	clock.fire(0)
	if got := len(runtime.snapshot()); got != 0 {
		t.Fatalf("busy resident accepted %d local Reminder inputs", got)
	}
	if receipts := d.reminderCache.pendingFireReceipts(); len(receipts) != 1 || receipts[0].WakeEnqueued {
		t.Fatalf("busy local receipt = %+v, want retained unenqueued due", receipts)
	}

	d.canonicalRuntimes.slots["agent-a\x00runtime-a"].running = false
	clock.fire(len(clock.timers) - 1)
	if got := len(runtime.snapshot()); got != 1 {
		t.Fatalf("idle retry local Reminder inputs = %d, want 1", got)
	}
	if receipts := d.reminderCache.pendingFireReceipts(); len(receipts) != 1 || !receipts[0].WakeEnqueued {
		t.Fatalf("idle retry receipt = %+v, want wake enqueued", receipts)
	}
}
