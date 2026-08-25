package daemon

import (
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

// A due occurrence must persist an exact fire request, wait for server
// authorization, materialize an App Inbox item, enqueue its notice, and remain
// until explicit consumption.

func dueReminderJob(now time.Time) protocol.ReminderTimerJob {
	return reminderJob("r-due", "owner-a", 3, now.Add(-time.Minute))
}

func registerReminderNoticeRuntime(t *testing.T, d *Daemon, busy bool) *idleMessageFakeRuntime {
	t.Helper()
	d.mu.Lock()
	d.runtimeIndex["runtime-a"] = Runtime{ID: "runtime-a", WorkspaceID: "workspace-a"}
	d.mu.Unlock()
	runner := registerTestInbox(t, d, InboxKey{WorkspaceID: "workspace-a", AgentID: testInboxAgentID}, "runtime-a", &MessageCoordinator{
		key: InboxKey{WorkspaceID: "workspace-a", AgentID: testInboxAgentID}, pending: make(map[string]map[int64]protocol.AgentMessageProjection),
	})
	if _, err := runner.processes.Start(agentProcessStartRequest{AgentID: testInboxAgentID, RuntimeID: "runtime-a"}); err != nil {
		t.Fatal(err)
	}
	markTestLaunchRunning(t, runner, testInboxAgentID)
	runtime := &idleMessageFakeRuntime{}
	d.canonicalRuntimes.mu.Lock()
	d.canonicalRuntimes.slots[testInboxAgentID+"\x00runtime-a"] = &agentRuntimeSlot{backend: runtime, running: busy}
	d.canonicalRuntimes.mu.Unlock()
	return runtime
}

func TestReminderCacheDueWaitsForServerAuthorizationBeforeWake(t *testing.T) {
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

	if !cache.upsert(dueReminderJob(now)) {
		t.Fatal("upsert rejected")
	}
	clock.fire(len(clock.timers) - 1)

	if len(fired) != 0 {
		t.Fatalf("owner wake before server authorization = %+v", fired)
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
	if got.WakeEnqueued || got.ServerAcked || got.RequestID == "" {
		t.Fatalf("receipt flags = %+v, want pending exact fire request", got)
	}
	identity := receiptIdentity(got)
	if !cache.acceptFireRequest(identity, got.RequestID, true, got.Catchup) {
		t.Fatal("accepted fire request result rejected")
	}
	if len(fired) != 1 || fired[0].ReminderID != "r-due" || fired[0].OwnerAgentID != "owner-a" || fired[0].Version != 3 {
		t.Fatalf("authorized owner wake = %+v", fired)
	}

	reloaded := newReminderCache(clock, slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
	reloaded.setPersistence(root)
	reloadedReceipts := reloaded.pendingFireReceipts()
	if len(reloadedReceipts) != 1 || reloadedReceipts[0].Job.ReminderID != "r-due" || !reloadedReceipts[0].WakeEnqueued || !reloadedReceipts[0].ServerAcked || !reloadedReceipts[0].ServerFired {
		t.Fatalf("reloaded receipts = %+v, want authorized materialized due fact", reloadedReceipts)
	}
}

func TestReminderCacheDoesNotDispatchUntilDueReceiptIsDurable(t *testing.T) {
	now := time.Date(2026, 8, 13, 8, 0, 0, 0, time.UTC)
	clock := &fakeReminderClock{now: now}
	cache := newReminderCache(clock, slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
	cache.setPersistence(t.TempDir())
	if !cache.upsert(dueReminderJob(now)) {
		t.Fatal("upsert rejected")
	}
	writes := cache.writeState
	cache.writeState = func(string, []byte) error { return errors.New("disk unavailable") }
	fireRequests := 0
	cache.onFireReceipt = func(reminderDueReceipt) bool {
		fireRequests++
		return true
	}

	clock.fire(0)
	if fireRequests != 0 {
		t.Fatalf("fire requests=%d before durable receipt, want 0", fireRequests)
	}
	if receipts := cache.pendingFireReceipts(); len(receipts) != 0 {
		t.Fatalf("non-durable receipt remained in memory: %+v", receipts)
	}
	retry := lastActiveReminderTimer(clock)
	if retry < 0 {
		t.Fatal("missing persistence retry timer")
	}

	cache.writeState = writes
	clock.fire(retry)
	if fireRequests != 1 {
		t.Fatalf("fire requests=%d after persistence recovered, want 1", fireRequests)
	}
	if receipts := cache.pendingFireReceipts(); len(receipts) != 1 {
		t.Fatalf("durable receipt after recovery=%+v, want one", receipts)
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
	if !cache.upsert(dueReminderJob(now)) {
		t.Fatal("upsert rejected")
	}
	clock.fire(0)
	receipts := cache.pendingFireReceipts()
	if len(receipts) != 1 || !cache.acceptFireRequest(receiptIdentity(receipts[0]), receipts[0].RequestID, true, true) {
		t.Fatalf("authorize receipt = %+v", receipts)
	}
	if attempts != 1 {
		t.Fatalf("attempts after due = %d, want 1", attempts)
	}
	receipts = cache.pendingFireReceipts()
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
	if len(receipts) != 1 || !receipts[0].WakeEnqueued || !receipts[0].ServerAcked {
		t.Fatalf("receipt after wake = %+v, want authorized wake", receipts)
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

func TestReminderCacheStopsFireRequestReplayAfterAcceptedResult(t *testing.T) {
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
	job := dueReminderJob(now)
	job.Title = "local due"
	if !cache.upsert(job) {
		t.Fatal("upsert rejected")
	}

	clock.fire(0)
	if wakeAttempts != 0 || receiptAttempts != 1 {
		t.Fatalf("initial attempts wake=%d request=%d, want 0/1", wakeAttempts, receiptAttempts)
	}
	receipts := cache.pendingFireReceipts()
	if len(receipts) != 1 || !cache.acceptFireRequest(receiptIdentity(receipts[0]), receipts[0].RequestID, true, true) {
		t.Fatalf("authorize receipt = %+v", receipts)
	}
	if wakeAttempts != 1 {
		t.Fatalf("wake attempts after authorization = %d, want 1", wakeAttempts)
	}

	cache.suspend()
	cache.resume()
	if wakeAttempts != 1 {
		t.Fatalf("local wake attempts after reconnect = %d, want exactly one", wakeAttempts)
	}
	if receiptAttempts != 1 {
		t.Fatalf("fire requests after accepted reconnect = %d, want no replay", receiptAttempts)
	}

	identity := reminderDueIdentity{OwnerAgentID: job.OwnerAgentID, ReminderID: job.ReminderID, Version: job.Version}
	if got := cache.pendingFireReceipts(); len(got) != 1 || !got[0].ServerAcked || got[0].ItemConsumed {
		t.Fatalf("receipt after server ACK = %+v, want retained until item consumption", got)
	}
	if !cache.consumeFireReceipt(identity) {
		t.Fatal("item consumption rejected")
	}
	if got := cache.pendingFireReceipts(); len(got) != 0 {
		t.Fatalf("pending receipts after three-way convergence = %+v", got)
	}
	cache.suspend()
	cache.resume()
	if receiptAttempts != 1 {
		t.Fatalf("server receipt attempts after ack reconnect = %d, want no replay", receiptAttempts)
	}
}

func TestReminderFireResultAcknowledgesOnlyItsAttemptedOccurrence(t *testing.T) {
	now := time.Date(2026, 8, 13, 8, 0, 0, 0, time.UTC)
	clock := &fakeReminderClock{now: now}
	cache := newReminderCache(clock, slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
	cache.onFireDelivery = func(protocol.ReminderTimerJob) bool { return true }

	first := reminderJob("recurring", "owner-a", 1, now.Add(-2*time.Minute))
	first.Title = "first due"
	if !cache.upsert(first) {
		t.Fatal("first occurrence upsert rejected")
	}
	clock.fire(len(clock.timers) - 1)

	second := reminderJob("recurring", "owner-a", 2, now.Add(-time.Minute))
	second.Title = "second due"
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
	receipts := cache.pendingFireReceipts()
	firstRequestID := receipts[0].RequestID
	if err := d.handleReminderFireRequestResult(protocol.ReminderFireRequestResultPayload{
		AgentID: first.OwnerAgentID, ReminderID: first.ReminderID, Version: first.Version,
		RequestID: firstRequestID, Outcome: "accepted", Fired: true, Catchup: true,
	}); err != nil {
		t.Fatal(err)
	}
	if !cache.consumeFireReceipt(reminderDueIdentity{OwnerAgentID: first.OwnerAgentID, ReminderID: first.ReminderID, Version: first.Version}) {
		t.Fatal("consume first occurrence")
	}

	receipts = cache.pendingFireReceipts()
	if len(receipts) != 1 || receipts[0].Job.Version != second.Version || receipts[0].WakeEnqueued || receipts[0].ServerAcked {
		t.Fatalf("pending recurring receipts after exact ACK = %+v, want only unacked version 2", receipts)
	}
}

func TestReminderCacheSnapshotKeepsInFlightReceipt(t *testing.T) {
	now := time.Date(2026, 8, 13, 8, 0, 0, 0, time.UTC)
	clock := &fakeReminderClock{now: now}
	cache := newReminderCache(clock, slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
	cache.onFireDelivery = func(protocol.ReminderTimerJob) bool { return true }
	job := dueReminderJob(now)
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

func TestReminderCacheReceiptConvergesOnlyAfterWakeServerAckAndItemConsumption(t *testing.T) {
	now := time.Date(2026, 8, 13, 8, 0, 0, 0, time.UTC)
	clock := &fakeReminderClock{now: now}
	wake := false
	cache := newReminderCache(clock, slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
	cache.fireRetryDelay = time.Second
	cache.onFireDelivery = func(protocol.ReminderTimerJob) bool { return wake }
	if !cache.upsert(dueReminderJob(now)) {
		t.Fatal("upsert rejected")
	}
	clock.fire(0)
	identity := reminderDueIdentity{OwnerAgentID: "owner-a", ReminderID: "r-due", Version: 3}
	receipts := cache.pendingFireReceipts()
	if len(receipts) != 1 || !cache.acceptFireRequest(identity, receipts[0].RequestID, true, true) {
		t.Fatalf("server authorization rejected: %+v", receipts)
	}
	receipts = cache.pendingFireReceipts()
	if len(receipts) != 1 || !receipts[0].ServerAcked || receipts[0].WakeEnqueued {
		t.Fatalf("acked-without-wake receipts = %+v, want receipt retained until wake", receipts)
	}

	wake = true
	clock.fire(len(clock.timers) - 1)
	if got := cache.pendingFireReceipts(); len(got) != 1 || !got[0].WakeEnqueued || !got[0].ServerAcked || got[0].ItemConsumed {
		t.Fatalf("pending after wake+server ACK = %+v, want retained until item consumption", got)
	}
	if !cache.consumeFireReceipt(identity) {
		t.Fatal("item consumption rejected")
	}
	if got := cache.pendingFireReceipts(); len(got) != 0 {
		t.Fatalf("pending after three-way convergence = %+v, want empty", got)
	}
}

func TestReminderDueTimerMaterializesAppInboxWithoutServerTransport(t *testing.T) {
	now := time.Date(2026, 8, 13, 8, 0, 0, 0, time.UTC)
	clock := &fakeReminderClock{now: now}
	root := t.TempDir()
	d := New(Config{WorkspacesRoot: root, BindingsRoot: root, MachineID: "machine-1", WorkspaceID: "workspace-a"}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	d.reminderCache = newReminderCache(clock, d.logger, nil)
	d.reminderCache.onFireDelivery = d.materializeReminderFire
	d.reminderCache.onFireReceipt = d.queueReminderFireReceipt
	runtime := registerReminderNoticeRuntime(t, d, true)

	job := protocol.ReminderTimerJob{
		ReminderID:   testInboxReminderID,
		OwnerAgentID: testInboxAgentID,
		Version:      4,
		FireAt:       now.Add(-time.Minute).Format(time.RFC3339Nano),
		Title:        "Review the deployment",
	}
	if applied, err := d.reminderCache.upsertForRuntime("runtime-a", job); err != nil || !applied {
		t.Fatal("upsert rejected")
	}
	clock.fire(0)
	receipts := d.reminderCache.pendingFireReceipts()
	if len(receipts) != 1 || !d.reminderCache.acceptFireRequest(receiptIdentity(receipts[0]), receipts[0].RequestID, true, true) {
		t.Fatalf("authorize Reminder fire = %+v", receipts)
	}

	store, err := d.agentAppInboxes.Store(testInboxAgentID)
	if err != nil {
		t.Fatal(err)
	}
	items := store.List()
	if len(items) != 1 || items[0].ItemID != "reminder:"+testInboxReminderID+":4" || items[0].Title != job.Title {
		t.Fatalf("materialized app Inbox items = %+v", items)
	}
	receipts = d.reminderCache.pendingFireReceipts()
	if len(receipts) != 1 || !receipts[0].WakeEnqueued || !receipts[0].ServerAcked || !receipts[0].ServerFired {
		t.Fatalf("local due receipt = %+v, want authorized materialized wake", receipts)
	}
	if notices := runtime.noticeSnapshot(); len(notices) != 1 || notices[0].PendingAppItems != 1 || notices[0].TotalPending != 0 {
		t.Fatalf("content-free App Inbox notices = %+v", notices)
	}

	// Replaying the same durable receipt may resend the server receipt, but the
	// stable app identity must remain a single item.
	d.reminderCache.suspend()
	d.reminderCache.resume()
	if got := len(store.List()); got != 1 {
		t.Fatalf("app Inbox items after receipt replay = %d, want 1", got)
	}
}

func TestReminderDueTimerMaterializationIsIndependentOfAgentRuntimeActivity(t *testing.T) {
	now := time.Date(2026, 8, 13, 8, 0, 0, 0, time.UTC)
	clock := &fakeReminderClock{now: now}
	root := t.TempDir()
	d := New(Config{WorkspacesRoot: root, BindingsRoot: root, MachineID: "machine-1", WorkspaceID: "workspace-a"}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	d.reminderCache = newReminderCache(clock, d.logger, nil)
	d.reminderCache.fireRetryDelay = time.Second
	d.reminderCache.onFireDelivery = d.materializeReminderFire
	d.reminderCache.onFireReceipt = d.queueReminderFireReceipt
	runtime := registerReminderNoticeRuntime(t, d, true)

	job := protocol.ReminderTimerJob{
		ReminderID: testInboxReminderID, OwnerAgentID: testInboxAgentID, Version: 2,
		FireAt: now.Add(-time.Minute).Format(time.RFC3339Nano),
		Title:  "Retry after compaction",
	}
	if applied, err := d.reminderCache.upsertForRuntime("runtime-a", job); err != nil || !applied {
		t.Fatal("upsert rejected")
	}
	clock.fire(0)
	receipts := d.reminderCache.pendingFireReceipts()
	if len(receipts) != 1 || !d.reminderCache.acceptFireRequest(receiptIdentity(receipts[0]), receipts[0].RequestID, true, true) {
		t.Fatalf("authorize busy Reminder fire = %+v", receipts)
	}
	store, err := d.agentAppInboxes.Store(testInboxAgentID)
	if err != nil {
		t.Fatal(err)
	}
	if items := store.List(); len(items) != 1 || items[0].SourceRef.Revision != "2" {
		t.Fatalf("busy resident app Inbox items = %+v", items)
	}
	if receipts := d.reminderCache.pendingFireReceipts(); len(receipts) != 1 || !receipts[0].WakeEnqueued {
		t.Fatalf("materialized app Inbox receipt = %+v, want wake enqueued", receipts)
	}
	if notices := runtime.noticeSnapshot(); len(notices) != 1 || notices[0].PendingAppItems != 1 {
		t.Fatalf("busy runtime App Inbox notices = %+v", notices)
	}
}

func lastActiveReminderTimer(clock *fakeReminderClock) int {
	for i := len(clock.timers) - 1; i >= 0; i-- {
		if !clock.timers[i].stopped {
			return i
		}
	}
	return -1
}

func TestReminderCacheExhaustsWakeRetryAfterMaxAttempts(t *testing.T) {
	now := time.Date(2026, 8, 13, 8, 0, 0, 0, time.UTC)
	clock := &fakeReminderClock{now: now}
	attempts := 0
	var exhausted *reminderRetryExhaustion
	cache := newReminderCache(clock, slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
	cache.onRetryExhausted = func(next reminderRetryExhaustion) { exhausted = &next }
	cache.onFireReceipt = func(reminderDueReceipt) bool {
		attempts++
		return false
	}
	if !cache.upsert(dueReminderJob(now)) {
		t.Fatal("upsert rejected")
	}
	clock.fire(0)
	for attempts < defaultReminderFireRetryMaxAttempts {
		index := lastActiveReminderTimer(clock)
		if index < 0 {
			t.Fatalf("retry stopped after %d attempts, want %d deliveries", attempts, defaultReminderFireRetryMaxAttempts)
		}
		clock.fire(index)
	}
	if attempts != defaultReminderFireRetryMaxAttempts {
		t.Fatalf("attempts = %d, want %d", attempts, defaultReminderFireRetryMaxAttempts)
	}
	if lastActiveReminderTimer(clock) >= 0 {
		t.Fatal("retry timer still armed after max attempts")
	}
	receipts := cache.pendingFireReceipts()
	if len(receipts) != 1 || receipts[0].WakeEnqueued || receipts[0].RetryTerminal == nil {
		t.Fatalf("receipt after exhaustion = %+v, want terminal unenqueued due", receipts)
	}
	got := receipts[0].RetryTerminal
	if got.Code != reminderDeliveryRetryExhaustedCode || got.Stage != "fire_request" || got.Attempts != defaultReminderFireRetryMaxAttempts || got.ReminderID != "r-due" || got.OwnerAgentID != "owner-a" || got.Version != 3 {
		t.Fatalf("retry terminal = %+v", got)
	}
	if exhausted == nil || *exhausted != *got {
		t.Fatalf("onRetryExhausted = %+v, want %+v", exhausted, got)
	}
	index := lastActiveReminderTimer(clock)
	if index >= 0 {
		clock.fire(index)
	}
	if attempts != defaultReminderFireRetryMaxAttempts {
		t.Fatalf("attempts after exhaustion = %d, want no further delivery", attempts)
	}
}

func TestReminderCacheExhaustsWakeRetryAfterDeadline(t *testing.T) {
	now := time.Date(2026, 8, 13, 8, 0, 0, 0, time.UTC)
	clock := &fakeReminderClock{now: now}
	attempts := 0
	cache := newReminderCache(clock, slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
	cache.onFireReceipt = func(reminderDueReceipt) bool {
		attempts++
		return false
	}
	if !cache.upsert(dueReminderJob(now)) {
		t.Fatal("upsert rejected")
	}
	clock.fire(0)
	if attempts != 1 {
		t.Fatalf("attempts after due = %d, want 1", attempts)
	}
	index := lastActiveReminderTimer(clock)
	if index < 0 {
		t.Fatal("missing retry timer before deadline")
	}
	clock.now = now.Add(defaultReminderFireRetryDeadline)
	clock.fire(index)
	if attempts != 1 {
		t.Fatalf("attempts after deadline = %d, want no extra delivery", attempts)
	}
	receipts := cache.pendingFireReceipts()
	if len(receipts) != 1 || receipts[0].RetryTerminal == nil || receipts[0].RetryTerminal.Code != reminderDeliveryRetryExhaustedCode {
		t.Fatalf("receipt after deadline = %+v, want terminal due", receipts)
	}
	if lastActiveReminderTimer(clock) >= 0 {
		t.Fatal("retry timer still armed after deadline")
	}
}

func TestReminderCacheWakeRetryBackoffDoublesUntilCap(t *testing.T) {
	now := time.Date(2026, 8, 13, 8, 0, 0, 0, time.UTC)
	clock := &fakeReminderClock{now: now}
	cache := newReminderCache(clock, slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
	cache.onFireReceipt = func(reminderDueReceipt) bool { return false }
	if !cache.upsert(dueReminderJob(now)) {
		t.Fatal("upsert rejected")
	}
	clock.fire(0)
	want := []time.Duration{
		time.Second,
		2 * time.Second,
		4 * time.Second,
		8 * time.Second,
		16 * time.Second,
		32 * time.Second,
		defaultReminderFireRetryMaxDelay,
	}
	got := make([]time.Duration, 0, len(want))
	for range want {
		index := lastActiveReminderTimer(clock)
		if index < 0 {
			t.Fatalf("retry delays = %v, want %v", got, want)
		}
		got = append(got, clock.delays[index])
		clock.fire(index)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("retry delays = %v, want %v", got, want)
		}
	}
	if lastActiveReminderTimer(clock) >= 0 {
		t.Fatal("retry timer still armed after backoff cap and max attempts")
	}
}

func TestReminderCachePersistsCamelCaseRetryState(t *testing.T) {
	now := time.Date(2026, 8, 13, 8, 0, 0, 0, time.UTC)
	clock := &fakeReminderClock{now: now}
	root := t.TempDir()
	cache := newReminderCache(clock, slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
	cache.onFireDelivery = func(protocol.ReminderTimerJob) bool { return false }
	cache.setPersistence(root)
	if !cache.upsert(dueReminderJob(now)) {
		t.Fatal("upsert rejected")
	}
	clock.fire(0)
	raw, err := os.ReadFile(filepath.Join(root, "owner-a", reminderCacheStateFile))
	if err != nil {
		t.Fatal(err)
	}
	body := string(raw)
	for _, legacy := range []string{"wake_enqueued", "retry_deadline_at", "fired_at_client", "server_acked", "retry_attempt"} {
		if strings.Contains(body, legacy) {
			t.Fatalf("persisted reminder cache still uses snake_case %q: %s", legacy, body)
		}
	}
	for _, want := range []string{`"ownerAgentId"`, `"wakeEnqueued"`, `"retryDeadlineAt"`, `"firedAtClient"`, `"retryAttempt"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("persisted reminder cache missing %s: %s", want, body)
		}
	}
}
