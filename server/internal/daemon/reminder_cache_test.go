package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/internal/agentworkspace"
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

func reminderProjection(seq int64, runtimeID, eventType string, job protocol.ReminderTimerJob, terminal bool) protocol.ReminderProjectionEvent {
	event := protocol.ReminderProjectionEvent{
		Seq: seq, PrevSeq: seq - 1, RuntimeID: runtimeID, AgentID: job.OwnerAgentID, EventType: eventType,
		PlacementGeneration: 1, ReminderID: job.ReminderID, Version: job.Version, Terminal: terminal,
	}
	if !terminal {
		event.FireAt = job.FireAt
		event.Reminder = job
	}
	return event
}

func TestReminderCacheVersionFencingAndTimerReplacement(t *testing.T) {
	now := time.Date(2026, 7, 22, 8, 0, 0, 0, time.UTC)
	clock := &fakeReminderClock{now: now}
	var fired []protocol.ReminderTimerJob
	cache := newReminderCache(clock, slog.New(slog.NewTextHandler(io.Discard, nil)), func(job protocol.ReminderTimerJob) { fired = append(fired, job) })

	if !cache.upsert(reminderJob("r1", "a1", 2, now.Add(time.Hour))) {
		t.Fatal("initial upsert rejected")
	}
	if cache.upsert(reminderJob("r1", "a1", 2, now.Add(2*time.Hour))) || cache.upsert(reminderJob("r1", "a1", 1, now.Add(2*time.Hour))) {
		t.Fatal("stale/equal upsert replaced current timer")
	}
	if cache.cancel("r1", 1) {
		t.Fatal("stale cancel removed current timer")
	}
	if !cache.upsert(reminderJob("r1", "a1", 3, now.Add(3*time.Hour))) {
		t.Fatal("newer upsert rejected")
	}
	clock.fire(0)
	if len(fired) != 0 {
		t.Fatal("replaced timer fired")
	}
	clock.fire(1)
	if len(fired) != 1 || fired[0].Version != 3 {
		t.Fatalf("new timer fire = %+v", fired)
	}
}

func TestReminderCacheOwnerSnapshotAndPastDue(t *testing.T) {
	now := time.Date(2026, 7, 22, 8, 0, 0, 0, time.UTC)
	clock := &fakeReminderClock{now: now}
	var fired []protocol.ReminderTimerJob
	cache := newReminderCache(clock, nil, func(job protocol.ReminderTimerJob) { fired = append(fired, job) })
	cache.upsert(reminderJob("a-old", "a", 1, now.Add(time.Hour)))
	cache.upsert(reminderJob("b-keep", "b", 1, now.Add(time.Hour)))

	installed, err := cache.snapshot("runtime-a", "a", 2, []protocol.ReminderTimerJob{
		reminderJob("a-new", "a", 2, now.Add(-time.Minute)),
		reminderJob("cross-owner", "b", 9, now.Add(time.Hour)),
	})
	if err != nil {
		t.Fatal(err)
	}
	if installed != 1 {
		t.Fatalf("installed = %d, want 1", installed)
	}
	if _, ok := cache.get("a-old"); ok {
		t.Fatal("owner snapshot retained stale owner entry")
	}
	if _, ok := cache.get("b-keep"); !ok {
		t.Fatal("owner snapshot removed other owner entry")
	}
	if _, ok := cache.get("cross-owner"); ok {
		t.Fatal("owner snapshot accepted cross-owner entry")
	}
	last := len(clock.timers) - 1
	if clock.delays[last] != 0 {
		t.Fatalf("past due delay = %s, want 0", clock.delays[last])
	}
	clock.fire(last)
	if len(fired) != 1 || fired[0].ReminderID != "a-new" {
		t.Fatalf("past due fire = %+v", fired)
	}
}

func TestReminderOwnerSnapshotDoesNotAdvanceRuntimeProjectionCursor(t *testing.T) {
	now := time.Date(2026, 7, 22, 8, 0, 0, 0, time.UTC)
	cache := newReminderCache(&fakeReminderClock{now: now}, nil, nil)
	if installed, err := cache.snapshot("runtime-a", "agent-a", 9, []protocol.ReminderTimerJob{
		reminderJob("a-current", "agent-a", 1, now.Add(time.Hour)),
	}); err != nil || installed != 1 {
		t.Fatalf("snapshot installed=%d err=%v", installed, err)
	}
	if got := cache.projectionCursors()["runtime-a"]; got != 0 {
		t.Fatalf("owner snapshot advanced runtime cursor to %d", got)
	}
	if _, ok := cache.get("a-current"); !ok {
		t.Fatal("owner snapshot did not merge owner timer")
	}
}

func TestReminderCacheTombstoneAndNewerEventFenceSnapshots(t *testing.T) {
	now := time.Date(2026, 7, 22, 8, 0, 0, 0, time.UTC)
	cache := newReminderCache(&fakeReminderClock{now: now}, nil, nil)
	jobV2 := reminderJob("r1", "agent-a", 2, now.Add(time.Hour))
	if applied, err := cache.applyProjection(reminderProjection(1, "runtime-a", "upsert", jobV2, false)); err != nil || !applied {
		t.Fatalf("apply v2 = %v, %v", applied, err)
	}
	jobV3 := reminderJob("r1", "agent-a", 3, now.Add(2*time.Hour))
	if applied, err := cache.applyProjection(reminderProjection(2, "runtime-a", "cancel", jobV3, true)); err != nil || !applied {
		t.Fatalf("apply cancel v3 = %v, %v", applied, err)
	}
	if installed, err := cache.snapshot("runtime-a", "agent-a", 2, []protocol.ReminderTimerJob{jobV3}); err != nil || installed != 0 {
		t.Fatalf("equal-version tombstone snapshot = %d, %v", installed, err)
	}
	if _, ok := cache.get("r1"); ok {
		t.Fatal("equal-version snapshot resurrected cancelled reminder")
	}
	if applied, err := cache.applyProjection(reminderProjection(3, "runtime-a", "upsert", jobV3, false)); err != nil || applied {
		t.Fatalf("equal-version upsert after tombstone = %v, %v", applied, err)
	}
	if got := cache.projectionCursors()["runtime-a"]; got != 3 {
		t.Fatalf("stale event cursor = %d, want 3", got)
	}

	jobV4 := reminderJob("r1", "agent-a", 4, now.Add(3*time.Hour))
	if applied, err := cache.applyProjection(reminderProjection(4, "runtime-a", "upsert", jobV4, false)); err != nil || !applied {
		t.Fatalf("apply v4 = %v, %v", applied, err)
	}
	if installed, err := cache.snapshot("runtime-a", "agent-a", 3, []protocol.ReminderTimerJob{reminderJob("r1", "agent-a", 5, now.Add(4*time.Hour))}); err != nil || installed != 0 {
		t.Fatalf("older-watermark snapshot = %d, %v", installed, err)
	}
	got, ok := cache.get("r1")
	if !ok || got.Version != 4 {
		t.Fatalf("older snapshot replaced newer event: %+v, %v", got, ok)
	}
}

func TestReminderCacheOnlyFireResultRearmsSameVersionPendingFire(t *testing.T) {
	now := time.Date(2026, 7, 22, 8, 0, 0, 0, time.UTC)
	clock := &fakeReminderClock{now: now}
	cache := newReminderCache(clock, nil, nil)
	job := reminderJob("r1", "agent-a", 1, now.Add(-time.Minute))
	if applied, err := cache.applyProjection(reminderProjection(1, "runtime-a", "upsert", job, false)); err != nil || !applied {
		t.Fatalf("apply due job = %v, %v", applied, err)
	}
	clock.fire(0)
	// Task #68: firing keeps the job as an in-flight local record instead of
	// deleting it — a fire_attempt that never gets confirmed must still have
	// something locally to retry from. Only a fire_result confirms and clears it.
	if _, ok := cache.get("r1"); !ok {
		t.Fatal("fired job dropped from cache before confirmation arrived")
	}
	if installed, err := cache.snapshot("runtime-a", "agent-a", 1, []protocol.ReminderTimerJob{job}); err != nil || installed != 0 {
		t.Fatalf("same-version snapshot rearm = %d, %v", installed, err)
	}
	result := reminderProjection(2, "runtime-a", "fire_result", job, false)
	if applied, err := cache.applyProjection(result); err != nil || !applied {
		t.Fatalf("same-version fire result = %v, %v", applied, err)
	}
	if got, ok := cache.get("r1"); !ok || got.Version != 1 {
		t.Fatalf("fire result did not rearm canonical job: %+v, %v", got, ok)
	}
}

// TestReminderCacheFireRetriesLocallyUntilConfirmed pins task #68's main fix:
// a fire_attempt that never gets a fire_result confirmation (dropped send,
// transient server failure, WS hiccup — anything short of a full reconnect)
// must not be lost. The daemon's own retry timer, not a server round trip,
// is what resends it.
func TestReminderCacheFireRetriesLocallyUntilConfirmed(t *testing.T) {
	now := time.Date(2026, 7, 22, 8, 0, 0, 0, time.UTC)
	clock := &fakeReminderClock{now: now}
	var fired []protocol.ReminderTimerJob
	cache := newReminderCache(clock, slog.New(slog.NewTextHandler(io.Discard, nil)), func(job protocol.ReminderTimerJob) {
		fired = append(fired, job)
	})
	job := reminderJob("r1", "agent-a", 1, now.Add(-time.Minute))
	if applied, err := cache.applyProjection(reminderProjection(1, "runtime-a", "upsert", job, false)); err != nil || !applied {
		t.Fatalf("apply due job = %v, %v", applied, err)
	}

	clock.fire(0) // initial due-time fire
	if len(fired) != 1 {
		t.Fatalf("fired after initial timer = %d, want 1", len(fired))
	}
	if len(clock.timers) != 2 {
		t.Fatalf("timers scheduled after initial fire = %d, want 2 (due + retry)", len(clock.timers))
	}

	// Confirmation never arrives. Firing the retry timer must resend.
	clock.fire(1)
	if len(fired) != 2 || fired[1].ReminderID != "r1" || fired[1].Version != 1 {
		t.Fatalf("fired after first retry = %+v, want a second r1/v1 fire", fired)
	}
	if len(clock.timers) != 3 {
		t.Fatalf("timers scheduled after first retry = %d, want 3 (due + 2 retries)", len(clock.timers))
	}
	if got, ok := cache.get("r1"); !ok || got.Version != 1 {
		t.Fatalf("in-flight job lost across retries: %+v, %v", got, ok)
	}

	// Fire result arrives: retry must stop, not fire again.
	result := reminderProjection(2, "runtime-a", "fire_result", job, true)
	if applied, err := cache.applyProjection(result); err != nil || !applied {
		t.Fatalf("terminal fire result = %v, %v", applied, err)
	}
	if !clock.timers[2].stopped {
		t.Fatal("fire_result confirmation did not stop the pending local retry timer")
	}
	clock.timers[2].fn() // simulate a race: retry fires anyway right after confirmation
	if len(fired) != 2 {
		t.Fatalf("retry re-fired after confirmation: fired = %+v", fired)
	}
	if _, ok := cache.get("r1"); ok {
		t.Fatal("terminal fire_result should have removed the confirmed job")
	}
}

// TestReminderCacheSnapshotPreservesInFlightRetry pins task #68's snapshot()
// fix: a job the cache is still locally retrying (pendingFires set, no
// fire_result yet) must survive a snapshot call instead of being silently
// dropped along with everything else not in the server's accepted list —
// otherwise every snapshot (including the one right after reconnect) would
// silently kill the very retry loop this fix adds.
func TestReminderCacheSnapshotPreservesInFlightRetry(t *testing.T) {
	now := time.Date(2026, 7, 22, 8, 0, 0, 0, time.UTC)
	clock := &fakeReminderClock{now: now}
	cache := newReminderCache(clock, slog.New(slog.NewTextHandler(io.Discard, nil)), func(protocol.ReminderTimerJob) {})
	job := reminderJob("r1", "agent-a", 1, now.Add(-time.Minute))
	if applied, err := cache.applyProjection(reminderProjection(1, "runtime-a", "upsert", job, false)); err != nil || !applied {
		t.Fatalf("apply due job = %v, %v", applied, err)
	}
	clock.fire(0)
	if _, ok := cache.get("r1"); !ok {
		t.Fatal("job should be in-flight before snapshot")
	}

	// Snapshot omits r1 entirely (as it would if the server hasn't heard the
	// fire_attempt back yet either) — the in-flight record must not vanish.
	if installed, err := cache.snapshot("runtime-a", "agent-a", 1, nil); err != nil || installed != 0 {
		t.Fatalf("snapshot with no jobs = %d, %v", installed, err)
	}
	if _, ok := cache.get("r1"); !ok {
		t.Fatal("snapshot silently dropped an in-flight retry")
	}
}

// TestOnReminderTimerOwnerMissingLogsAndSelfHealsOnNextRetry pins task #69:
// a due reminder timer firing while its owner is (transiently) absent from
// the local Attachment map used to be a pure silent drop — armLocked already
// deleted nothing (task #68 keeps the in-flight entry), but onReminderTimer
// itself returned with zero trace: no fire_attempt queued, no error, no
// reconnect forced. A perfectly healthy WS connection would show no symptom
// at all while this specific reminder just never fired — this is the
// leading candidate for why three machines with live heartbeats and
// advancing projection cursors still had overdue reminders during the
// 2026-08-01 incident. Fix: log it, and rely on task #68's existing local
// retry loop (fireAndScheduleRetryLocked re-invokes onFire on a schedule)
// to actually resend once the owner registers — this test proves both the
// log and that composition, not just "didn't crash".
func TestOnReminderTimerOwnerMissingLogsAndSelfHealsOnNextRetry(t *testing.T) {
	now := time.Date(2026, 7, 22, 8, 0, 0, 0, time.UTC)
	clock := &fakeReminderClock{now: now}
	var logBuf strings.Builder
	logger := slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	mgr := newLocalAgentAttachmentRegistry("", logger)
	cache := newReminderCache(clock, logger, nil)
	writes := make(chan []byte, 4)
	var d *Daemon
	cache.onFire = func(job protocol.ReminderTimerJob) { d.onReminderTimer(job) }
	d = &Daemon{
		logger: logger, agentAttachments: mgr, reminderCache: cache,
		runtimeIndex: map[string]Runtime{"runtime-x": {ID: "runtime-x", WorkspaceID: "workspace-x"}},
	}
	d.setReminderWS(writes, make(chan struct{}), func() error { return nil })
	cache.resume() // setReminderWS's beginConnection() suspends arming; mimic post-replay resume.

	job := reminderJob("r1", "agent-x", 1, now.Add(-time.Minute))
	if applied, err := cache.applyProjection(reminderProjection(1, "runtime-x", "upsert", job, false)); err != nil || !applied {
		t.Fatalf("apply due job = %v, %v", applied, err)
	}

	// Timer fires while agent-x has no residency entry yet.
	clock.fire(0)
	select {
	case frame := <-writes:
		t.Fatalf("fire_attempt sent despite missing owner: %s", frame)
	default:
	}
	logOutput := logBuf.String()
	if !strings.Contains(logOutput, "owner missing from current Agent Attachment set") ||
		!strings.Contains(logOutput, "reminder_id=r1") ||
		!strings.Contains(logOutput, "agent_id=agent-x") ||
		!strings.Contains(logOutput, "version=1") {
		t.Fatalf("expected a Warn log naming reminder_id/agent_id/version, got: %s", logOutput)
	}

	// Owner registers before the next local retry — task #68's retry loop
	// (not this fix) is what re-invokes onReminderTimer; the fix's job is
	// only to not have poisoned that path and to have made the gap visible.
	if _, err := mgr.Apply("workspace-x", AgentAttachmentEvent{
		Kind: AgentAttachmentEventAttach, AgentID: "agent-x", RuntimeID: "runtime-x",
		AttachmentGeneration: 1, LifecycleSeq: 1,
	}); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(clock.timers) != 2 {
		t.Fatalf("timers scheduled after initial fire = %d, want 2 (due + retry)", len(clock.timers))
	}
	clock.fire(1)

	select {
	case frame := <-writes:
		var msg protocol.Message
		if err := json.Unmarshal(frame, &msg); err != nil || msg.Type != protocol.EventReminderFireAttempt {
			t.Fatalf("retry frame = %s, want a fire_attempt", frame)
		}
		var attempt protocol.ReminderFireAttemptPayload
		if err := json.Unmarshal(msg.Payload, &attempt); err != nil || attempt.ReminderID != "r1" || attempt.AgentID != "agent-x" || attempt.Version != 1 {
			t.Fatalf("retry fire_attempt payload = %s", msg.Payload)
		}
	default:
		t.Fatal("local retry never resent the fire_attempt once the owner registered")
	}
}

func TestReminderCachePersistenceFailureRollsBackCursorAndFence(t *testing.T) {
	cache := newReminderCache(&fakeReminderClock{now: time.Now().UTC()}, nil, nil)
	cache.path = filepath.Join(t.TempDir(), reminderCacheStateFile)
	cache.writeState = func(string, []byte) error { return errors.New("injected reminder cache write failure") }
	job := reminderJob("r1", "agent-a", 1, time.Now().UTC().Add(time.Hour))
	if applied, err := cache.applyProjection(reminderProjection(1, "runtime-a", "upsert", job, false)); err == nil || applied {
		t.Fatalf("persist failure = applied %v err %v", applied, err)
	}
	if _, ok := cache.get("r1"); ok {
		t.Fatal("failed persist leaked timer")
	}
	if got := cache.projectionCursors()["runtime-a"]; got != 0 {
		t.Fatalf("failed persist leaked cursor %d", got)
	}
	if fence := cache.highWatermark("r1"); fence.Version != 0 {
		t.Fatalf("failed persist leaked fence %+v", fence)
	}
}

func TestReminderCacheCorruptDurableStateRecoversThroughCanonicalRuntimeReset(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, ".daemon", reminderCacheStateFile)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{not-json"), 0o600); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 22, 8, 0, 0, 0, time.UTC)
	clock := &fakeReminderClock{now: now}
	cache := newReminderCache(clock, nil, nil)
	cache.setPersistence(root)
	if err := cache.stateError(); err != nil {
		t.Fatalf("corrupt read model remained unrecoverable: %v", err)
	}
	quarantined, err := filepath.Glob(path + ".corrupt-*")
	if err != nil || len(quarantined) != 1 {
		t.Fatalf("corrupt state quarantine=%v err=%v", quarantined, err)
	}
	if raw, err := os.ReadFile(quarantined[0]); err != nil || string(raw) != "{not-json" {
		t.Fatalf("quarantined diagnostic bytes=%q err=%v", raw, err)
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	mgr := newLocalAgentAttachmentRegistry(root, logger)
	if changed, accepted, err := mgr.applyLegacyStart("agent-a", "runtime-a", "workspace-a", 3); err != nil || !changed || !accepted {
		t.Fatalf("seed local Attachment changed=%v accepted=%v err=%v", changed, accepted, err)
	}
	writes := make(chan []byte, 8)
	d := &Daemon{
		logger: logger,
		workspaces: map[string]*workspaceState{
			"workspace-a": newWorkspaceState("workspace-a", []string{"runtime-a"}, protocol.DaemonCapabilityReminderVersionedCache),
		},
		runtimeIndex:     map[string]Runtime{"runtime-a": {ID: "runtime-a", WorkspaceID: "workspace-a"}},
		agentAttachments: mgr,
		reminderCache:    cache,
	}
	prepareHeadlessWorkspaceRunnerTestDaemon(d, root)
	d.setReminderWS(writes, make(chan struct{}), func() error { return nil })
	if len(clock.timers) != 0 {
		t.Fatal("corrupt startup armed a timer before canonical reset")
	}
	if err := d.handleDaemonAgentLifecycleReplayEnd(protocol.DaemonAgentLifecycleReplayEndPayload{RuntimeCursors: map[string]int64{"runtime-a": 9}}); err != nil {
		t.Fatal(err)
	}
	var lifecycleAck, request protocol.Message
	if err := json.Unmarshal(<-writes, &lifecycleAck); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(<-writes, &request); err != nil {
		t.Fatal(err)
	}
	if lifecycleAck.Type != protocol.EventDaemonAgentLifecycleAck || request.Type != protocol.EventReminderProjectionReq {
		t.Fatalf("startup recovery frames=%q then %q", lifecycleAck.Type, request.Type)
	}
	var replayRequest protocol.ReminderProjectionRequestPayload
	if err := json.Unmarshal(request.Payload, &replayRequest); err != nil {
		t.Fatal(err)
	}
	if replayRequest.RuntimeCursors["runtime-a"] != 0 || !replayRequest.RuntimeResetRequired["runtime-a"] || len(replayRequest.RuntimeResidencies["runtime-a"]) != 1 {
		t.Fatalf("corrupt startup replay request=%+v", replayRequest)
	}
	if len(clock.timers) != 0 {
		t.Fatal("recovery request armed a timer before canonical reset persisted")
	}

	job := reminderJob("canonical", "agent-a", 4, now.Add(time.Hour))
	if err := d.handleReminderProjectionReplayEnd(protocol.ReminderProjectionReplayEndPayload{
		RuntimeCursors: map[string]int64{"runtime-a": 12},
		RuntimeResets: map[string]protocol.ReminderRuntimeReset{"runtime-a": {
			ProjectionWatermark: 12,
			Owners:              []protocol.ReminderRuntimeResetOwner{{AgentID: "agent-a", PlacementGeneration: 3, Reminders: []protocol.ReminderTimerJob{job}}},
		}},
	}); err != nil {
		t.Fatal(err)
	}
	var projectionAck protocol.Message
	if err := json.Unmarshal(<-writes, &projectionAck); err != nil {
		t.Fatal(err)
	}
	if projectionAck.Type != protocol.EventReminderProjectionAck {
		t.Fatalf("first post-reset frame=%q", projectionAck.Type)
	}
	if got, ok := cache.get("canonical"); !ok || got.Version != 4 || len(clock.timers) != 1 {
		t.Fatalf("canonical reset timer=%+v present=%v armed=%d", got, ok, len(clock.timers))
	}
	reloaded := newReminderCache(clock, logger, nil)
	reloaded.setPersistence(root)
	if err := reloaded.stateError(); err != nil || reloaded.projectionCursors()["runtime-a"] != 12 || reloaded.requiredRuntimeResets()["runtime-a"] {
		t.Fatalf("durable replacement err=%v cursors=%v resets=%v", err, reloaded.projectionCursors(), reloaded.requiredRuntimeResets())
	}
}

func TestReminderCacheCorruptDurableStateResetPersistFailureKeepsTimersAndProjectionAckClosed(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, ".daemon", reminderCacheStateFile)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{not-json"), 0o600); err != nil {
		t.Fatal(err)
	}
	clock := &fakeReminderClock{now: time.Now().UTC()}
	cache := newReminderCache(clock, nil, nil)
	cache.setPersistence(root)
	if err := cache.stateError(); err != nil {
		t.Fatal(err)
	}
	writesBeforeFailure := 0
	cache.writeState = func(path string, raw []byte) error {
		writesBeforeFailure++
		if writesBeforeFailure == 2 {
			return errors.New("injected canonical reset replacement failure")
		}
		return writeDaemonStateAtomically(path, raw)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	mgr := newLocalAgentAttachmentRegistry(root, logger)
	if _, _, err := mgr.applyLegacyStart("agent-a", "runtime-a", "workspace-a", 3); err != nil {
		t.Fatal(err)
	}
	writes := make(chan []byte, 8)
	d := &Daemon{
		logger: logger,
		workspaces: map[string]*workspaceState{
			"workspace-a": newWorkspaceState("workspace-a", []string{"runtime-a"}, protocol.DaemonCapabilityReminderVersionedCache),
		},
		runtimeIndex:     map[string]Runtime{"runtime-a": {ID: "runtime-a", WorkspaceID: "workspace-a"}},
		agentAttachments: mgr,
		reminderCache:    cache,
	}
	prepareHeadlessWorkspaceRunnerTestDaemon(d, root)
	d.setReminderWS(writes, make(chan struct{}), func() error { return nil })
	if err := d.handleDaemonAgentLifecycleReplayEnd(protocol.DaemonAgentLifecycleReplayEndPayload{RuntimeCursors: map[string]int64{"runtime-a": 9}}); err != nil {
		t.Fatal(err)
	}
	if writesBeforeFailure != 1 {
		t.Fatalf("recovery request durable writes=%d want 1", writesBeforeFailure)
	}
	err := d.handleReminderProjectionReplayEnd(protocol.ReminderProjectionReplayEndPayload{
		RuntimeCursors: map[string]int64{"runtime-a": 12},
		RuntimeResets: map[string]protocol.ReminderRuntimeReset{"runtime-a": {
			ProjectionWatermark: 12,
			Owners: []protocol.ReminderRuntimeResetOwner{{AgentID: "agent-a", PlacementGeneration: 3, Reminders: []protocol.ReminderTimerJob{
				reminderJob("canonical", "agent-a", 4, clock.now.Add(time.Hour)),
			}}},
		}},
	})
	if err == nil {
		t.Fatal("corrupt-state canonical reset persist unexpectedly succeeded")
	}
	if len(clock.timers) != 0 {
		t.Fatal("failed canonical replacement armed a timer")
	}
	if d.reminderReplayComplete {
		t.Fatal("failed canonical replacement opened reminder gate")
	}
	if got := cache.projectionCursors()["runtime-a"]; got != 0 {
		t.Fatalf("failed canonical replacement cursor=%d", got)
	}
	for len(writes) > 0 {
		var frame protocol.Message
		if err := json.Unmarshal(<-writes, &frame); err != nil {
			t.Fatal(err)
		}
		if frame.Type == protocol.EventReminderProjectionAck {
			t.Fatal("failed canonical replacement queued projection ACK")
		}
	}
	reloaded := newReminderCache(clock, logger, nil)
	reloaded.setPersistence(root)
	if !reloaded.requiredRuntimeResets()["runtime-a"] || reloaded.projectionCursors()["runtime-a"] != 0 {
		t.Fatalf("failed replacement lost durable reset marker: cursors=%v resets=%v", reloaded.projectionCursors(), reloaded.requiredRuntimeResets())
	}
}

func TestReminderCacheCorruptQuarantineCrashWindowRetriesRecoveryMarker(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, ".daemon", reminderCacheStateFile)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{not-json"), 0o600); err != nil {
		t.Fatal(err)
	}

	first := newReminderCache(&fakeReminderClock{now: time.Now().UTC()}, nil, nil)
	first.writeState = func(string, []byte) error { return errors.New("injected recovery marker write failure") }
	first.setPersistence(root)
	if err := first.stateError(); err == nil {
		t.Fatal("quarantine without recovery marker unexpectedly remained startable")
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("corrupt primary survived quarantine: %v", err)
	}
	quarantined, err := filepath.Glob(path + ".corrupt-*")
	if err != nil || len(quarantined) != 1 {
		t.Fatalf("quarantine files=%v err=%v", quarantined, err)
	}

	second := newReminderCache(&fakeReminderClock{now: time.Now().UTC()}, nil, nil)
	second.setPersistence(root)
	if err := second.stateError(); err != nil {
		t.Fatalf("restart did not recover quarantine crash window: %v", err)
	}
	if !second.recoveryRequired {
		t.Fatal("restart lost durable canonical reset requirement")
	}
	if _, err := os.Stat(quarantined[0]); err != nil {
		t.Fatalf("restart lost quarantined diagnostic state: %v", err)
	}
	reloaded := newReminderCache(&fakeReminderClock{now: time.Now().UTC()}, nil, nil)
	reloaded.setPersistence(root)
	if err := reloaded.stateError(); err != nil || !reloaded.recoveryRequired {
		t.Fatalf("recovery marker was not durable err=%v required=%v", err, reloaded.recoveryRequired)
	}
}

func TestReminderCacheOwnerRemovalPersistFailureRestoresPendingFireFence(t *testing.T) {
	now := time.Now().UTC()
	clock := &fakeReminderClock{now: now}
	cache := newReminderCache(clock, nil, nil)
	cache.path = filepath.Join(t.TempDir(), reminderCacheStateFile)
	job := reminderJob("r1", "agent-a", 3, now.Add(-time.Minute))
	if applied, err := cache.applyProjection(reminderProjection(1, "runtime-a", "upsert", job, false)); err != nil || !applied {
		t.Fatalf("seed projection = %v, %v", applied, err)
	}
	clock.fire(0)
	cache.writeState = func(string, []byte) error { return errors.New("injected owner removal failure") }
	if err := cache.removeOwner("agent-a"); err == nil {
		t.Fatal("owner removal persist unexpectedly succeeded")
	}
	if fence := cache.highWatermark("r1"); fence.Version != 3 || fence.Terminal {
		t.Fatalf("owner removal failure lost fence: %+v", fence)
	}
	cache.writeState = writeDaemonStateAtomically
	result := reminderProjection(2, "runtime-a", "fire_result", job, false)
	if applied, err := cache.applyProjection(result); err != nil || !applied {
		t.Fatalf("restored pending fire did not accept canonical result = %v, %v", applied, err)
	}
}

func TestReminderProjectionStalePlacementOnlyAdvancesDurableCursor(t *testing.T) {
	root := t.TempDir()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	cache := newReminderCache(&fakeReminderClock{now: time.Now().UTC()}, logger, nil)
	cache.setPersistence(root)
	mgr := newLocalAgentAttachmentRegistry(t.TempDir(), logger)
	if changed, accepted, err := mgr.applyLegacyStart("agent-a", "runtime-a", "workspace-a", 2); err != nil || !changed || !accepted {
		t.Fatal("seed placement")
	}
	writes := make(chan []byte, 2)
	d := &Daemon{
		logger:           logger,
		runtimeIndex:     map[string]Runtime{"runtime-a": {ID: "runtime-a", WorkspaceID: "workspace-a"}},
		agentAttachments: mgr,
		reminderCache:    cache,
	}
	d.setReminderWS(writes, make(chan struct{}), func() error { return nil })
	job := reminderJob("r1", "agent-a", 1, time.Now().UTC().Add(time.Hour))
	stale := reminderProjection(1, "runtime-a", "upsert", job, false)
	stale.PlacementGeneration = 1
	if err := d.handleReminderProjection(stale); err != nil {
		t.Fatal(err)
	}
	if _, ok := cache.get("r1"); ok {
		t.Fatal("stale placement installed timer")
	}
	current := reminderProjection(2, "runtime-a", "upsert", job, false)
	current.PlacementGeneration = 2
	if err := d.handleReminderProjection(current); err != nil {
		t.Fatal(err)
	}
	if _, ok := cache.get("r1"); !ok {
		t.Fatal("current placement did not install timer")
	}
	if got := newReminderCache(&fakeReminderClock{now: time.Now().UTC()}, logger, nil); got != nil {
		got.setPersistence(root)
		if seq := got.projectionCursors()["runtime-a"]; seq != 2 {
			t.Fatalf("durable cursor = %d, want 2", seq)
		}
	}
}

func TestReminderProjectionForDepartedOwnerPersistsCursorBeforeAck(t *testing.T) {
	root := t.TempDir()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	cache := newReminderCache(&fakeReminderClock{now: time.Now().UTC()}, logger, nil)
	cache.setPersistence(root)
	writes := make(chan []byte, 1)
	done := make(chan struct{})
	d := &Daemon{
		logger:           logger,
		runtimeIndex:     map[string]Runtime{"runtime-a": {ID: "runtime-a", WorkspaceID: "workspace-a"}},
		agentAttachments: newLocalAgentAttachmentRegistry(t.TempDir(), logger),
		reminderCache:    cache,
	}
	d.setReminderWS(writes, done, func() error { return nil })
	job := reminderJob("departed", "agent-gone", 7, time.Now().UTC().Add(time.Hour))
	event := reminderProjection(11, "runtime-a", "upsert", job, false)
	event.PrevSeq = 0
	if err := d.handleReminderProjection(event); err != nil {
		t.Fatal(err)
	}
	if _, ok := cache.get(job.ReminderID); ok {
		t.Fatal("departed owner projection installed a timer")
	}
	if got := cache.projectionCursors()["runtime-a"]; got != 11 {
		t.Fatalf("departed owner cursor = %d, want 11", got)
	}
	reloaded := newReminderCache(&fakeReminderClock{now: time.Now().UTC()}, logger, nil)
	reloaded.setPersistence(root)
	if got := reloaded.projectionCursors()["runtime-a"]; got != 11 {
		t.Fatalf("durable departed owner cursor = %d, want 11", got)
	}
	var frame protocol.Message
	if err := json.Unmarshal(<-writes, &frame); err != nil {
		t.Fatal(err)
	}
	if frame.Type != protocol.EventReminderProjectionAck {
		t.Fatalf("departed owner frame = %q", frame.Type)
	}
}

func TestReminderProjectionLiveGapReplaysBeforeAckingHigherSequence(t *testing.T) {
	now := time.Now().UTC()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	cache := newReminderCache(&fakeReminderClock{now: now}, logger, nil)
	mgr := newLocalAgentAttachmentRegistry(t.TempDir(), logger)
	if changed, accepted, err := mgr.applyLegacyStart("agent-a", "runtime-a", "workspace-a", 1); err != nil || !changed || !accepted {
		t.Fatal("seed placement")
	}
	writes := make(chan []byte, 8)
	d := &Daemon{
		logger:                 logger,
		runtimeIndex:           map[string]Runtime{"runtime-a": {ID: "runtime-a", WorkspaceID: "workspace-a"}},
		agentAttachments:       mgr,
		reminderCache:          cache,
		reminderReplayComplete: true,
	}
	d.setReminderWS(writes, make(chan struct{}), func() error { return nil })
	d.reminderGateMu.Lock()
	d.reminderReplayComplete = true
	d.reminderGateMu.Unlock()

	first := reminderProjection(10, "runtime-a", "upsert", reminderJob("r1", "agent-a", 1, now.Add(time.Hour)), false)
	first.PrevSeq = 0
	second := reminderProjection(20, "runtime-a", "upsert", reminderJob("r2", "agent-a", 1, now.Add(2*time.Hour)), false)
	second.PrevSeq = 10

	if err := d.handleReminderProjection(second); err != nil {
		t.Fatal(err)
	}
	if got := cache.projectionCursors()["runtime-a"]; got != 0 {
		t.Fatalf("gap advanced cursor to %d", got)
	}
	if _, ok := cache.get("r2"); ok {
		t.Fatal("gap installed higher-sequence timer")
	}
	var replayReq protocol.Message
	if err := json.Unmarshal(<-writes, &replayReq); err != nil || replayReq.Type != protocol.EventReminderProjectionReq {
		t.Fatalf("gap frame = %+v err=%v, want replay request", replayReq, err)
	}

	if err := d.handleReminderProjection(first); err != nil {
		t.Fatal(err)
	}
	if got := cache.projectionCursors()["runtime-a"]; got != 10 {
		t.Fatalf("first cursor = %d, want 10", got)
	}
	if err := d.handleReminderProjection(second); err != nil {
		t.Fatal(err)
	}
	if got := cache.projectionCursors()["runtime-a"]; got != 20 {
		t.Fatalf("replayed second cursor = %d, want 20", got)
	}
	if _, ok := cache.get("r1"); !ok {
		t.Fatal("first timer missing after replay")
	}
	if _, ok := cache.get("r2"); !ok {
		t.Fatal("second timer missing after replay")
	}
}

func TestReminderProjectionGapDuringReplayLatchesImmediateSuccessorBeforeOpeningGate(t *testing.T) {
	now := time.Date(2026, 7, 22, 8, 0, 0, 0, time.UTC)
	clock := &fakeReminderClock{now: now}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	cache := newReminderCache(clock, logger, nil)
	mgr := newLocalAgentAttachmentRegistry(t.TempDir(), logger)
	if _, _, err := mgr.applyLegacyStart("agent-a", "runtime-a", "workspace-a", 1); err != nil {
		t.Fatal(err)
	}
	writes := make(chan []byte, 8)
	d := &Daemon{
		logger:           logger,
		runtimeIndex:     map[string]Runtime{"runtime-a": {ID: "runtime-a", WorkspaceID: "workspace-a"}},
		agentAttachments: mgr,
		reminderCache:    cache,
	}
	d.setReminderWS(writes, make(chan struct{}), func() error { return nil })
	d.reminderGateMu.Lock()
	d.reminderReplayComplete = true
	d.reminderProjectionReplayInFlight = true
	d.reminderGateMu.Unlock()
	cache.resume()

	n1 := reminderProjection(10, "runtime-a", "upsert", reminderJob("r1", "agent-a", 1, now.Add(time.Hour)), false)
	n1.PrevSeq = 0
	n2 := reminderProjection(20, "runtime-a", "upsert", reminderJob("r2", "agent-a", 1, now.Add(2*time.Hour)), false)
	n2.PrevSeq = 10
	if err := d.handleReminderProjection(n2); err != nil {
		t.Fatal(err)
	}
	if err := d.handleReminderProjection(n1); err != nil {
		t.Fatal(err)
	}
	if len(writes) != 0 || len(clock.timers) != 0 {
		t.Fatalf("first replay leaked frames=%d timers=%d", len(writes), len(clock.timers))
	}
	if err := d.handleReminderProjectionReplayEnd(protocol.ReminderProjectionReplayEndPayload{RuntimeCursors: map[string]int64{"runtime-a": 10}}); err != nil {
		t.Fatal(err)
	}
	var successor protocol.Message
	if err := json.Unmarshal(<-writes, &successor); err != nil {
		t.Fatal(err)
	}
	if successor.Type != protocol.EventReminderProjectionReq || len(writes) != 0 {
		t.Fatalf("pending gap first end frames=%q remaining=%d", successor.Type, len(writes))
	}
	d.reminderGateMu.Lock()
	firstOpen := d.reminderReplayComplete
	firstInFlight := d.reminderProjectionReplayInFlight
	d.reminderGateMu.Unlock()
	if firstOpen || !firstInFlight || len(clock.timers) != 0 {
		t.Fatalf("first end gate open=%v inflight=%v timers=%d", firstOpen, firstInFlight, len(clock.timers))
	}

	if err := d.handleReminderProjection(n2); err != nil {
		t.Fatal(err)
	}
	if err := d.handleReminderProjectionReplayEnd(protocol.ReminderProjectionReplayEndPayload{RuntimeCursors: map[string]int64{"runtime-a": 20}}); err != nil {
		t.Fatal(err)
	}
	var ack protocol.Message
	if err := json.Unmarshal(<-writes, &ack); err != nil {
		t.Fatal(err)
	}
	if ack.Type != protocol.EventReminderProjectionAck {
		t.Fatalf("final replay frame=%q want ACK", ack.Type)
	}
	d.reminderGateMu.Lock()
	finalOpen := d.reminderReplayComplete
	finalInFlight := d.reminderProjectionReplayInFlight
	d.reminderGateMu.Unlock()
	if !finalOpen || finalInFlight || len(clock.timers) != 2 {
		t.Fatalf("final gate open=%v inflight=%v timers=%d", finalOpen, finalInFlight, len(clock.timers))
	}
}

func TestAgentAttachmentRegistryPersistsProvisionalTaskObservation(t *testing.T) {
	root := t.TempDir()
	mgr := newLocalAgentAttachmentRegistry(root, nil)
	if created, observed := mgr.observeTaskStarted("a1", "r1", "w1"); !created || !observed {
		t.Fatal("first local Attachment was not reported new")
	}
	if created, observed := mgr.observeTaskStarted("a1", "r1", "w1"); created || !observed {
		t.Fatal("existing local Attachment reported new")
	}
	mgr.observeTaskFinished("a1")
	mgr.observeTaskFinished("a1")

	reloaded := newLocalAgentAttachmentRegistry(root, nil)
	entry, ok := reloaded.localRecord("a1")
	if !ok || entry.ActiveTasks != 0 || entry.RuntimeID != "r1" || entry.WorkspaceID != "w1" {
		t.Fatalf("reloaded Attachment record = %+v, %v", entry, ok)
	}
	if removed, accepted, err := reloaded.applyLegacyStop("a1", "r1", 1); err != nil || !removed || !accepted || len(reloaded.localAgentIDs()) != 0 {
		t.Fatal("terminal removal did not clear local Attachment")
	}
}

func TestAgentAttachmentRegistryBootstrapsRecoverableConfigAndKeepsRemovalTombstone(t *testing.T) {
	root := t.TempDir()
	config := cachedAgentCredential{
		AgentID:     uuid.NewString(),
		RuntimeID:   "runtime-bootstrap",
		WorkspaceID: uuid.NewString(),
	}
	raw, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(agentworkspace.Root(root, config.WorkspaceID, config.AgentID), "runtime", "credentials", "current.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}

	mgr := newLocalAgentAttachmentRegistry(root, nil)
	entry, ok := mgr.localRecord(config.AgentID)
	if !ok || entry.RuntimeID != config.RuntimeID || entry.WorkspaceID != config.WorkspaceID || entry.ActiveTasks != 0 {
		t.Fatalf("bootstrapped Attachment record = %+v, %v", entry, ok)
	}
	if removed, accepted, err := mgr.applyLegacyStop(config.AgentID, config.RuntimeID, 1); err != nil || !removed || !accepted {
		t.Fatal("remove bootstrapped owner = false")
	}
	if _, ok := newLocalAgentAttachmentRegistry(root, nil).localRecord(config.AgentID); ok {
		t.Fatal("removed owner was resurrected from stale local config")
	}

	reloaded := newLocalAgentAttachmentRegistry(root, nil)
	if changed, accepted, err := reloaded.applyLegacyStart(config.AgentID, "runtime-new", config.WorkspaceID, 2); err != nil || !changed || !accepted {
		t.Fatal("local Attachment recreation was not reported as new")
	}
	entry, ok = newLocalAgentAttachmentRegistry(root, nil).localRecord(config.AgentID)
	if !ok || entry.RuntimeID != "runtime-new" {
		t.Fatalf("recreated Attachment record = %+v, %v", entry, ok)
	}
}

func TestDaemonAgentStopClearsOwnerResidencyAndReminderCache(t *testing.T) {
	clock := &fakeReminderClock{now: time.Now().UTC()}
	d := &Daemon{
		agentAttachments: newLocalAgentAttachmentRegistry(t.TempDir(), nil),
		reminderCache:    newReminderCache(clock, slog.New(slog.NewTextHandler(io.Discard, nil)), func(protocol.ReminderTimerJob) {}),
	}
	d.localAttachmentRegistry().observeTaskStarted("agent-a", "runtime-a", "workspace-a")
	d.localAttachmentRegistry().observeTaskFinished("agent-a")
	d.localAttachmentRegistry().observeTaskStarted("agent-b", "runtime-a", "workspace-a")
	d.localAttachmentRegistry().observeTaskFinished("agent-b")
	d.reminderCache.upsert(reminderJob("reminder-a", "agent-a", 1, clock.now.Add(time.Hour)))
	d.reminderCache.upsert(reminderJob("reminder-b", "agent-b", 1, clock.now.Add(time.Hour)))

	d.handleDaemonAgentStop(protocol.DaemonAgentStopPayload{AgentID: "agent-a", RuntimeID: "runtime-a", PlacementGeneration: 1})

	if _, ok := d.localAttachmentRegistry().localRecord("agent-a"); ok {
		t.Fatal("stopped owner remains resident")
	}
	if _, ok := d.reminderCache.get("reminder-a"); ok {
		t.Fatal("stopped owner reminder remains cached")
	}
	if _, ok := d.localAttachmentRegistry().localRecord("agent-b"); !ok {
		t.Fatal("unrelated Agent Attachment was removed")
	}
	if _, ok := d.reminderCache.get("reminder-b"); !ok {
		t.Fatal("unrelated owner reminder was removed")
	}
}

func TestAgentAttachmentGenerationFencesAtoBtoAOutOfOrder(t *testing.T) {
	mgr := newLocalAgentAttachmentRegistry(t.TempDir(), nil)
	if changed, accepted, err := mgr.applyLegacyStart("agent-a", "runtime-a", "workspace-a", 1); err != nil || !changed || !accepted {
		t.Fatal("initial A start rejected")
	}
	if changed, accepted, err := mgr.applyLegacyStart("agent-a", "runtime-b", "workspace-a", 2); err != nil || !changed || !accepted {
		t.Fatal("A to B start rejected")
	}
	if changed, accepted, err := mgr.applyLegacyStart("agent-a", "runtime-b", "workspace-a", 2); err != nil || changed || !accepted {
		t.Fatal("duplicate B start was not idempotent")
	}
	if changed, accepted, err := mgr.applyLegacyStart("agent-a", "runtime-c", "workspace-a", 2); err != nil || changed || accepted {
		t.Fatal("same-generation conflicting runtime start was accepted")
	}
	if removed, accepted, err := mgr.applyLegacyStop("agent-a", "runtime-a", 2); err != nil || removed || !accepted {
		t.Fatal("same-generation old-runtime stop removed B")
	}
	if changed, accepted, err := mgr.applyLegacyStart("agent-a", "runtime-a", "workspace-a", 3); err != nil || !changed || !accepted {
		t.Fatal("B to A start rejected")
	}
	if removed, accepted, err := mgr.applyLegacyStop("agent-a", "runtime-b", 2); err != nil || removed || accepted {
		t.Fatal("stale B stop was accepted")
	}
	if changed, accepted, err := mgr.applyLegacyStart("agent-a", "runtime-b", "workspace-a", 2); err != nil || changed || accepted {
		t.Fatal("stale B start was accepted")
	}
	entry, ok := mgr.localRecord("agent-a")
	if !ok || entry.RuntimeID != "runtime-a" || entry.PlacementGeneration != 3 {
		t.Fatalf("final placement = %+v, %v", entry, ok)
	}

	reloaded := newLocalAgentAttachmentRegistry(mgr.root, nil)
	entry, ok = reloaded.localRecord("agent-a")
	if !ok || entry.RuntimeID != "runtime-a" || entry.PlacementGeneration != 3 {
		t.Fatalf("reloaded placement = %+v, %v", entry, ok)
	}
}

func TestReminderReconnectRequestsSnapshotForAttachedAgents(t *testing.T) {
	root := t.TempDir()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	mgr := newLocalAgentAttachmentRegistry(root, logger)
	if _, err := mgr.Apply("workspace-a", AgentAttachmentEvent{
		Kind: AgentAttachmentEventAttach, AgentID: "agent-running", RuntimeID: "runtime-a",
		AttachmentGeneration: 1, LifecycleSeq: 1,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := mgr.Apply("workspace-a", AgentAttachmentEvent{
		Kind: AgentAttachmentEventAttach, AgentID: "agent-idle", RuntimeID: "runtime-a",
		AttachmentGeneration: 1, LifecycleSeq: 2,
	}); err != nil {
		t.Fatal(err)
	}
	writes := make(chan []byte, 2)
	done := make(chan struct{})
	d := &Daemon{
		logger:           logger,
		workspaces:       map[string]*workspaceState{"workspace-a": newWorkspaceState("workspace-a", []string{"runtime-a"}, protocol.DaemonCapabilityReminderVersionedCache)},
		runtimeIndex:     map[string]Runtime{"runtime-a": {ID: "runtime-a", WorkspaceID: "workspace-a"}},
		agentAttachments: mgr,
	}
	d.setReminderWS(writes, done, func() error { return nil })
	d.reminderGateMu.Lock()
	d.reminderReplayComplete = true
	d.reminderGateMu.Unlock()
	d.requestReminderSnapshots()

	got := map[string]bool{}
	for range 2 {
		var msg protocol.Message
		if err := json.Unmarshal(<-writes, &msg); err != nil {
			t.Fatal(err)
		}
		if msg.Type != protocol.EventReminderSnapshotRequest {
			t.Fatalf("snapshot request type = %q", msg.Type)
		}
		var payload protocol.ReminderSnapshotRequestPayload
		if err := json.Unmarshal(msg.Payload, &payload); err != nil {
			t.Fatal(err)
		}
		got[payload.AgentID] = true
	}
	if !got["agent-running"] || !got["agent-idle"] || len(got) != 2 {
		t.Fatalf("snapshot owners = %#v", got)
	}
}

func TestReminderLifecycleReplayEndsBeforeSnapshotAndPersistsAckCursor(t *testing.T) {
	root := t.TempDir()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	mgr := newLocalAgentAttachmentRegistry(root, logger)
	if changed, accepted, err := mgr.applyLegacyStart("agent-a", "runtime-a", "workspace-a", 4); err != nil || !changed || !accepted {
		t.Fatal("seed placement failed")
	}
	writes := make(chan []byte, 4)
	done := make(chan struct{})
	d := &Daemon{
		logger: logger,
		workspaces: map[string]*workspaceState{
			"workspace-a": newWorkspaceState("workspace-a", []string{"runtime-a"}, protocol.DaemonCapabilityReminderVersionedCache),
		},
		runtimeIndex:     map[string]Runtime{"runtime-a": {ID: "runtime-a", WorkspaceID: "workspace-a"}},
		agentAttachments: mgr,
		reminderCache:    newReminderCache(&fakeReminderClock{now: time.Now().UTC()}, logger, nil),
	}
	d.setReminderWS(writes, done, func() error { return nil })
	d.handleDaemonAgentLifecycleReplayEnd(protocol.DaemonAgentLifecycleReplayEndPayload{RuntimeCursors: map[string]int64{"runtime-a": 9}})

	var first, second protocol.Message
	if err := json.Unmarshal(<-writes, &first); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(<-writes, &second); err != nil {
		t.Fatal(err)
	}
	if first.Type != protocol.EventDaemonAgentLifecycleAck || second.Type != protocol.EventReminderProjectionReq {
		t.Fatalf("frame order = %q then %q", first.Type, second.Type)
	}
	if err := d.handleReminderProjectionReplayEnd(protocol.ReminderProjectionReplayEndPayload{RuntimeCursors: map[string]int64{"runtime-a": 0}}); err != nil {
		t.Fatal(err)
	}
	var third, fourth protocol.Message
	if err := json.Unmarshal(<-writes, &third); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(<-writes, &fourth); err != nil {
		t.Fatal(err)
	}
	if third.Type != protocol.EventReminderProjectionAck || fourth.Type != protocol.EventReminderSnapshotRequest {
		t.Fatalf("post-replay frame order = %q then %q", third.Type, fourth.Type)
	}
	var snapshot protocol.ReminderSnapshotRequestPayload
	if err := json.Unmarshal(fourth.Payload, &snapshot); err != nil {
		t.Fatal(err)
	}
	if snapshot.AgentID != "agent-a" || snapshot.RuntimeID != "runtime-a" || snapshot.PlacementGeneration != 4 {
		t.Fatalf("snapshot placement = %+v", snapshot)
	}
	if got := newLocalAgentAttachmentRegistry(root, nil).legacyRecoveryCursors()["runtime-a"]; got != 9 {
		t.Fatalf("persisted ack cursor = %d, want 9", got)
	}
}

func TestReminderProjectionReplaySnapshotBurstWaitsForWriterCapacity(t *testing.T) {
	root := t.TempDir()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	mgr := newLocalAgentAttachmentRegistry(root, logger)
	for _, agentID := range []string{"agent-a", "agent-b", "agent-c"} {
		if changed, accepted, err := mgr.applyLegacyStart(agentID, "runtime-a", "workspace-a", 1); err != nil || !changed || !accepted {
			t.Fatalf("seed %s: changed=%v accepted=%v err=%v", agentID, changed, accepted, err)
		}
	}
	writes := make(chan []byte, 2)
	done := make(chan struct{})
	closed := make(chan struct{}, 1)
	d := &Daemon{
		logger:           logger,
		runtimeIndex:     map[string]Runtime{"runtime-a": {ID: "runtime-a", WorkspaceID: "workspace-a"}},
		agentAttachments: mgr,
		reminderCache:    newReminderCache(&fakeReminderClock{now: time.Now().UTC()}, logger, nil),
	}
	d.setReminderWS(writes, done, func() error {
		closed <- struct{}{}
		return nil
	})

	result := make(chan error, 1)
	go func() {
		result <- d.handleReminderProjectionReplayEnd(protocol.ReminderProjectionReplayEndPayload{RuntimeCursors: map[string]int64{"runtime-a": 0}})
	}()

	select {
	case err := <-result:
		t.Fatalf("snapshot burst returned before the writer made room: %v", err)
	case <-time.After(50 * time.Millisecond):
	}

	seenSnapshots := map[string]bool{}
	for range 4 { // projection ACK plus one snapshot for each resident agent
		var message protocol.Message
		if err := json.Unmarshal(<-writes, &message); err != nil {
			t.Fatal(err)
		}
		if message.Type != protocol.EventReminderSnapshotRequest {
			continue
		}
		var snapshot protocol.ReminderSnapshotRequestPayload
		if err := json.Unmarshal(message.Payload, &snapshot); err != nil {
			t.Fatal(err)
		}
		seenSnapshots[snapshot.AgentID] = true
	}
	if err := <-result; err != nil {
		t.Fatalf("snapshot burst replay: %v", err)
	}
	if len(seenSnapshots) != 3 || !seenSnapshots["agent-a"] || !seenSnapshots["agent-b"] || !seenSnapshots["agent-c"] {
		t.Fatalf("snapshot owners = %#v", seenSnapshots)
	}
	select {
	case <-closed:
		t.Fatal("snapshot burst closed a healthy websocket")
	default:
	}
}

func TestReminderLifecycleHeartbeatCatchupRecoversLostMoveWithoutReconnect(t *testing.T) {
	now := time.Date(2026, 7, 22, 8, 0, 0, 0, time.UTC)
	clock := &fakeReminderClock{now: now}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	root := t.TempDir()
	mgr := newLocalAgentAttachmentRegistry(root, logger)
	if _, _, err := mgr.applyLegacyStart("agent-a", "runtime-a", "workspace-a", 1); err != nil {
		t.Fatal(err)
	}
	cache := newReminderCache(clock, logger, nil)
	writes := make(chan []byte, 16)
	closed := 0
	d := &Daemon{
		logger: logger,
		workspaces: map[string]*workspaceState{
			"workspace-a": newWorkspaceState("workspace-a", []string{"runtime-a", "runtime-b"}, protocol.DaemonCapabilityReminderVersionedCache),
		},
		runtimeIndex: map[string]Runtime{
			"runtime-a": {ID: "runtime-a", WorkspaceID: "workspace-a"},
			"runtime-b": {ID: "runtime-b", WorkspaceID: "workspace-a"},
		},
		agentAttachments: mgr,
		reminderCache:    cache,
	}
	prepareHeadlessWorkspaceRunnerTestDaemon(d, root)
	d.setReminderWS(writes, make(chan struct{}), func() error { closed++; return nil })
	d.reminderGateMu.Lock()
	d.reminderReplayComplete = true
	d.reminderGateMu.Unlock()
	cache.resume()
	if !cache.upsert(reminderJob("old-a", "agent-a", 1, now.Add(time.Hour))) {
		t.Fatal("seed old-runtime timer")
	}

	d.sendWSHeartbeats(context.Background(), []string{"runtime-a", "runtime-b"}, writes)
	d.sendWSHeartbeats(context.Background(), []string{"runtime-a", "runtime-b"}, writes)
	lifecycleRequests := 0
	for len(writes) > 0 {
		var frame protocol.Message
		if err := json.Unmarshal(<-writes, &frame); err != nil {
			t.Fatal(err)
		}
		if frame.Type == protocol.EventDaemonAgentLifecycleReq {
			lifecycleRequests++
			var request protocol.DaemonAgentLifecycleRequestPayload
			if err := json.Unmarshal(frame.Payload, &request); err != nil {
				t.Fatal(err)
			}
			if _, ok := request.RuntimeCursors["runtime-a"]; !ok {
				t.Fatalf("lifecycle request omitted runtime-a: %+v", request)
			}
			if _, ok := request.RuntimeCursors["runtime-b"]; !ok {
				t.Fatalf("lifecycle request omitted runtime-b: %+v", request)
			}
		}
	}
	if lifecycleRequests != 1 {
		t.Fatalf("heartbeat lifecycle requests=%d want one in-flight request", lifecycleRequests)
	}

	if err := d.handleDaemonAgentStop(protocol.DaemonAgentStopPayload{AgentID: "agent-a", RuntimeID: "runtime-a", PlacementGeneration: 2, LifecycleSeq: 2, Replay: true}); err != nil {
		t.Fatal(err)
	}
	if err := d.handleDaemonAgentStart(protocol.DaemonAgentStartPayload{AgentID: "agent-a", RuntimeID: "runtime-b", WorkspaceID: "workspace-a", PlacementGeneration: 2, LifecycleSeq: 1, Replay: true}); err != nil {
		t.Fatal(err)
	}
	if _, ok := cache.get("old-a"); ok {
		t.Fatal("replayed old-runtime stop retained timer")
	}
	if err := d.handleDaemonAgentLifecycleReplayEnd(protocol.DaemonAgentLifecycleReplayEndPayload{RuntimeCursors: map[string]int64{"runtime-a": 2, "runtime-b": 1}}); err != nil {
		t.Fatal(err)
	}
	var lifecycleAck, projectionRequest protocol.Message
	if err := json.Unmarshal(<-writes, &lifecycleAck); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(<-writes, &projectionRequest); err != nil {
		t.Fatal(err)
	}
	if lifecycleAck.Type != protocol.EventDaemonAgentLifecycleAck || projectionRequest.Type != protocol.EventReminderProjectionReq || len(writes) != 0 {
		t.Fatalf("catch-up order=%q then %q remaining=%d", lifecycleAck.Type, projectionRequest.Type, len(writes))
	}
	if err := d.handleReminderProjectionReplayEnd(protocol.ReminderProjectionReplayEndPayload{RuntimeCursors: map[string]int64{"runtime-a": 0, "runtime-b": 0}}); err != nil {
		t.Fatal(err)
	}
	var projectionAck, snapshotRequest protocol.Message
	if err := json.Unmarshal(<-writes, &projectionAck); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(<-writes, &snapshotRequest); err != nil {
		t.Fatal(err)
	}
	if projectionAck.Type != protocol.EventReminderProjectionAck || snapshotRequest.Type != protocol.EventReminderSnapshotRequest {
		t.Fatalf("post-catch-up order=%q then %q", projectionAck.Type, snapshotRequest.Type)
	}
	var snapshot protocol.ReminderSnapshotRequestPayload
	if err := json.Unmarshal(snapshotRequest.Payload, &snapshot); err != nil {
		t.Fatal(err)
	}
	if snapshot.AgentID != "agent-a" || snapshot.RuntimeID != "runtime-b" || snapshot.PlacementGeneration != 2 {
		t.Fatalf("target snapshot=%+v", snapshot)
	}
	canonical := reminderJob("canonical-b", "agent-a", 3, now.Add(2*time.Hour))
	if err := d.handleReminderSnapshot(protocol.ReminderSnapshotPayload{
		AgentID: "agent-a", RuntimeID: "runtime-b", PlacementGeneration: 2, ProjectionWatermark: 0,
		Reminders: []protocol.ReminderTimerJob{canonical},
	}); err != nil {
		t.Fatal(err)
	}
	owner, ok := mgr.localRecord("agent-a")
	if !ok || owner.RuntimeID != "runtime-b" || owner.PlacementGeneration != 2 {
		t.Fatalf("recovered owner=%+v present=%v", owner, ok)
	}
	if got, ok := cache.get("canonical-b"); !ok || got.Version != 3 {
		t.Fatalf("recovered timer=%+v present=%v", got, ok)
	}
	activeTimers := 0
	for _, timer := range clock.timers {
		if !timer.stopped {
			activeTimers++
		}
	}
	if activeTimers != 1 || closed != 0 {
		t.Fatalf("recovery active timers=%d reconnects=%d", activeTimers, closed)
	}
}

func TestReminderRuntimeSetReconcileRetiresOldStateBeforeNewLifecycleRecovery(t *testing.T) {
	now := time.Date(2026, 7, 22, 8, 0, 0, 0, time.UTC)
	clock := &fakeReminderClock{now: now}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	root := t.TempDir()
	mgr := newLocalAgentAttachmentRegistry(root, logger)
	if _, _, err := mgr.applyLegacyStart("agent-a", "runtime-old", "workspace-a", 1); err != nil {
		t.Fatal(err)
	}
	if err := mgr.advanceLegacyRecovery(map[string]int64{"runtime-old": 7}); err != nil {
		t.Fatal(err)
	}
	var fired []protocol.ReminderTimerJob
	cache := newReminderCache(clock, logger, func(job protocol.ReminderTimerJob) { fired = append(fired, job) })
	cache.setPersistence(root)
	oldJob := reminderJob("old-runtime-timer", "agent-a", 1, now.Add(time.Hour))
	oldProjection := reminderProjection(5, "runtime-old", "upsert", oldJob, false)
	oldProjection.PrevSeq = 0
	if applied, err := cache.applyProjection(oldProjection); err != nil || !applied {
		t.Fatalf("seed old projection applied=%v err=%v", applied, err)
	}
	writes := make(chan []byte, 12)
	closed := 0
	d := &Daemon{
		logger: logger,
		workspaces: map[string]*workspaceState{
			"workspace-a": newWorkspaceState("workspace-a", []string{"runtime-new"}, protocol.DaemonCapabilityReminderVersionedCache),
		},
		runtimeIndex:     map[string]Runtime{"runtime-new": {ID: "runtime-new", WorkspaceID: "workspace-a"}},
		agentAttachments: mgr,
		reminderCache:    cache,
	}
	prepareHeadlessWorkspaceRunnerTestDaemon(d, root)
	if err := d.reconcileReminderRuntimeSet([]string{"runtime-new"}); err != nil {
		t.Fatal(err)
	}
	if _, ok := mgr.localRecord("agent-a"); ok {
		t.Fatal("retired runtime owner survived local reconciliation")
	}
	if _, ok := mgr.legacyRecoveryCursors()["runtime-old"]; ok {
		t.Fatalf("retired lifecycle cursor survived: %v", mgr.legacyRecoveryCursors())
	}
	if _, ok := cache.get("old-runtime-timer"); ok {
		t.Fatal("retired runtime timer survived local reconciliation")
	}
	if _, ok := cache.projectionCursors()["runtime-old"]; ok {
		t.Fatalf("retired projection cursor survived: %v", cache.projectionCursors())
	}
	clock.fire(0)
	if len(fired) != 0 {
		t.Fatalf("retired runtime timer fired: %+v", fired)
	}

	d.setReminderWS(writes, make(chan struct{}), func() error { closed++; return nil })
	if !d.requestAgentLifecycleReplay() {
		t.Fatal("queue new-runtime lifecycle replay")
	}
	var lifecycleRequest protocol.Message
	if err := json.Unmarshal(<-writes, &lifecycleRequest); err != nil {
		t.Fatal(err)
	}
	var lifecyclePayload protocol.DaemonAgentLifecycleRequestPayload
	if lifecycleRequest.Type != protocol.EventDaemonAgentLifecycleReq || json.Unmarshal(lifecycleRequest.Payload, &lifecyclePayload) != nil {
		t.Fatalf("new-runtime lifecycle request=%+v", lifecycleRequest)
	}
	if len(lifecyclePayload.RuntimeCursors) != 1 || lifecyclePayload.RuntimeCursors["runtime-new"] != 0 {
		t.Fatalf("authorized lifecycle cursors=%v", lifecyclePayload.RuntimeCursors)
	}
	if err := d.handleDaemonAgentStart(protocol.DaemonAgentStartPayload{
		AgentID: "agent-a", RuntimeID: "runtime-new", WorkspaceID: "workspace-a", PlacementGeneration: 2, LifecycleSeq: 1, Replay: true,
	}); err != nil {
		t.Fatal(err)
	}
	if err := d.handleDaemonAgentLifecycleReplayEnd(protocol.DaemonAgentLifecycleReplayEndPayload{RuntimeCursors: map[string]int64{"runtime-new": 1}}); err != nil {
		t.Fatal(err)
	}
	var lifecycleAck, projectionRequest protocol.Message
	if err := json.Unmarshal(<-writes, &lifecycleAck); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(<-writes, &projectionRequest); err != nil {
		t.Fatal(err)
	}
	var projectionPayload protocol.ReminderProjectionRequestPayload
	if projectionRequest.Type != protocol.EventReminderProjectionReq || json.Unmarshal(projectionRequest.Payload, &projectionPayload) != nil {
		t.Fatalf("new-runtime projection request=%+v", projectionRequest)
	}
	if len(projectionPayload.RuntimeCursors) != 1 || projectionPayload.RuntimeCursors["runtime-new"] != 0 || len(projectionPayload.RuntimeResidencies["runtime-old"]) != 0 || len(projectionPayload.RuntimeResidencies["runtime-new"]) != 1 {
		t.Fatalf("authorized projection payload=%+v", projectionPayload)
	}
	if err := d.handleReminderProjectionReplayEnd(protocol.ReminderProjectionReplayEndPayload{RuntimeCursors: map[string]int64{"runtime-new": 0}}); err != nil {
		t.Fatal(err)
	}
	var projectionAck, snapshotRequest protocol.Message
	if err := json.Unmarshal(<-writes, &projectionAck); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(<-writes, &snapshotRequest); err != nil {
		t.Fatal(err)
	}
	var snapshot protocol.ReminderSnapshotRequestPayload
	if snapshotRequest.Type != protocol.EventReminderSnapshotRequest || json.Unmarshal(snapshotRequest.Payload, &snapshot) != nil {
		t.Fatalf("new-runtime snapshot request=%+v", snapshotRequest)
	}
	if snapshot.RuntimeID != "runtime-new" || snapshot.AgentID != "agent-a" || snapshot.PlacementGeneration != 2 {
		t.Fatalf("authorized snapshot=%+v", snapshot)
	}
	canonical := reminderJob("new-runtime-canonical", "agent-a", 2, now.Add(2*time.Hour))
	if err := d.handleReminderSnapshot(protocol.ReminderSnapshotPayload{
		AgentID: "agent-a", RuntimeID: "runtime-new", PlacementGeneration: 2, ProjectionWatermark: 0,
		Reminders: []protocol.ReminderTimerJob{canonical},
	}); err != nil {
		t.Fatal(err)
	}
	if got, ok := cache.get(canonical.ReminderID); !ok || got.Version != 2 {
		t.Fatalf("new-runtime canonical timer=%+v present=%v", got, ok)
	}
	if closed != 0 || len(writes) != 0 {
		t.Fatalf("runtime reconciliation reconnects=%d extra_frames=%d", closed, len(writes))
	}
}

func TestReminderRuntimeSetReconcilePersistFailureStartsNoWSAndKeepsTimersSuspended(t *testing.T) {
	now := time.Date(2026, 7, 22, 8, 0, 0, 0, time.UTC)
	clock := &fakeReminderClock{now: now}
	root := t.TempDir()
	mgr := newLocalAgentAttachmentRegistry(root, nil)
	if _, _, err := mgr.applyLegacyStart("agent-a", "runtime-old", "workspace-a", 1); err != nil {
		t.Fatal(err)
	}
	if err := mgr.advanceLegacyRecovery(map[string]int64{"runtime-old": 7}); err != nil {
		t.Fatal(err)
	}
	var fired []protocol.ReminderTimerJob
	cache := newReminderCache(clock, nil, func(job protocol.ReminderTimerJob) { fired = append(fired, job) })
	cache.setPersistence(root)
	old := reminderProjection(5, "runtime-old", "upsert", reminderJob("old", "agent-a", 1, now.Add(time.Hour)), false)
	old.PrevSeq = 0
	if applied, err := cache.applyProjection(old); err != nil || !applied {
		t.Fatalf("seed old timer applied=%v err=%v", applied, err)
	}
	cache.writeState = func(string, []byte) error { return errors.New("injected retired cache persist failure") }
	d := &Daemon{
		logger:           slog.New(slog.NewTextHandler(io.Discard, nil)),
		runtimeIndex:     map[string]Runtime{"runtime-new": {ID: "runtime-new", WorkspaceID: "workspace-a"}},
		agentAttachments: mgr,
		reminderCache:    cache,
	}
	if err := d.reconcileReminderRuntimeSet([]string{"runtime-new"}); err == nil {
		t.Fatal("retired runtime cache persist unexpectedly succeeded")
	}
	if d.reminderWrites != nil {
		t.Fatal("failed runtime reconciliation started websocket")
	}
	clock.fire(0)
	if len(fired) != 0 {
		t.Fatalf("failed runtime reconciliation fired old timer: %+v", fired)
	}
	if _, ok := cache.get("old"); !ok {
		t.Fatal("failed cache persist did not roll back in-memory entry")
	}
	if _, ok := newLocalAgentAttachmentRegistry(root, nil).localRecord("agent-a"); ok {
		t.Fatal("persisted retired owner resurrected after partial reconciliation")
	}
	cache.writeState = writeDaemonStateAtomically
	if err := d.reconcileReminderRuntimeSet([]string{"runtime-new"}); err != nil {
		t.Fatalf("retry retired runtime reconciliation: %v", err)
	}
	if _, ok := cache.get("old"); ok {
		t.Fatal("retry did not remove retired timer")
	}
	if _, ok := cache.projectionCursors()["runtime-old"]; ok {
		t.Fatalf("retry retained old projection cursor: %v", cache.projectionCursors())
	}
}

func TestReminderProjectionCursorLossAtomicResetUsesOnlyLocalResidencies(t *testing.T) {
	now := time.Date(2026, 7, 22, 8, 0, 0, 0, time.UTC)
	clock := &fakeReminderClock{now: now}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	mgr := newLocalAgentAttachmentRegistry(t.TempDir(), logger)
	mgr.applyLegacyStart("agent-a", "runtime-a", "workspace-a", 3)
	mgr.applyLegacyStart("agent-stale", "runtime-a", "workspace-a", 4)
	var fired []protocol.ReminderTimerJob
	cache := newReminderCache(clock, logger, func(job protocol.ReminderTimerJob) { fired = append(fired, job) })
	cache.upsert(reminderJob("stale-due", "agent-stale", 1, now.Add(-time.Minute)))
	writes := make(chan []byte, 8)
	done := make(chan struct{})
	d := &Daemon{
		logger: logger,
		workspaces: map[string]*workspaceState{
			"workspace-a": newWorkspaceState("workspace-a", []string{"runtime-a"}, protocol.DaemonCapabilityReminderVersionedCache),
		},
		runtimeIndex:     map[string]Runtime{"runtime-a": {ID: "runtime-a", WorkspaceID: "workspace-a"}},
		agentAttachments: mgr,
		reminderCache:    cache,
	}
	d.setReminderWS(writes, done, func() error { return nil })
	clock.fire(0)
	if len(fired) != 0 {
		t.Fatal("stale due timer fired while reset gate was closed")
	}
	reset := protocol.ReminderRuntimeReset{
		ProjectionWatermark: 7,
		Owners: []protocol.ReminderRuntimeResetOwner{
			{AgentID: "agent-a", PlacementGeneration: 3, Reminders: []protocol.ReminderTimerJob{reminderJob("canonical", "agent-a", 2, now.Add(time.Hour))}},
			{AgentID: "agent-stale", PlacementGeneration: 5, Terminal: true},
		},
	}
	if err := d.handleReminderProjectionReplayEnd(protocol.ReminderProjectionReplayEndPayload{
		RuntimeCursors: map[string]int64{"runtime-a": 7}, RuntimeResets: map[string]protocol.ReminderRuntimeReset{"runtime-a": reset},
	}); err != nil {
		t.Fatal(err)
	}
	if _, ok := mgr.localRecord("agent-stale"); !ok {
		t.Fatal("Reminder runtime reset mutated native Attachment authority")
	}
	if _, ok := mgr.localRecord("agent-a"); !ok {
		t.Fatal("canonical local Attachment was removed")
	}
	if job, ok := cache.get("canonical"); !ok || job.Version != 2 {
		t.Fatalf("canonical reset timer=%+v present=%v", job, ok)
	}
	if _, ok := cache.get("server-extra-owner"); ok {
		t.Fatal("server inventory injected an unsubmitted owner")
	}
	if got := cache.projectionCursors()["runtime-a"]; got != 7 {
		t.Fatalf("atomic reset cursor=%d want 7", got)
	}
	var first protocol.Message
	if err := json.Unmarshal(<-writes, &first); err != nil {
		t.Fatal(err)
	}
	if first.Type != protocol.EventReminderProjectionAck {
		t.Fatalf("first post-reset frame=%q want ACK", first.Type)
	}
}

func TestReminderProjectionCursorLossResetPersistFailureKeepsGateClosedAndSendsNoAck(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	mgr := newLocalAgentAttachmentRegistry(t.TempDir(), logger)
	mgr.applyLegacyStart("agent-a", "runtime-a", "workspace-a", 3)
	cache := newReminderCache(&fakeReminderClock{now: time.Now().UTC()}, logger, nil)
	cache.path = filepath.Join(t.TempDir(), "reminder-cache.json")
	cache.writeState = func(string, []byte) error { return errors.New("injected reset persist failure") }
	writes := make(chan []byte, 4)
	done := make(chan struct{})
	d := &Daemon{
		logger: logger,
		workspaces: map[string]*workspaceState{
			"workspace-a": newWorkspaceState("workspace-a", []string{"runtime-a"}, protocol.DaemonCapabilityReminderVersionedCache),
		},
		runtimeIndex:     map[string]Runtime{"runtime-a": {ID: "runtime-a", WorkspaceID: "workspace-a"}},
		agentAttachments: mgr,
		reminderCache:    cache,
	}
	d.setReminderWS(writes, done, func() error { return nil })
	err := d.handleReminderProjectionReplayEnd(protocol.ReminderProjectionReplayEndPayload{
		RuntimeCursors: map[string]int64{"runtime-a": 4},
		RuntimeResets: map[string]protocol.ReminderRuntimeReset{"runtime-a": {
			ProjectionWatermark: 4,
			Owners:              []protocol.ReminderRuntimeResetOwner{{AgentID: "agent-a", PlacementGeneration: 3}},
		}},
	})
	if err == nil {
		t.Fatal("runtime reset persist unexpectedly succeeded")
	}
	if len(writes) != 0 {
		t.Fatalf("failed runtime reset queued %d frames", len(writes))
	}
	if d.reminderReplayComplete {
		t.Fatal("failed runtime reset opened replay gate")
	}
	if got := cache.projectionCursors()["runtime-a"]; got != 0 {
		t.Fatalf("failed runtime reset advanced cursor=%d", got)
	}
}

func TestReminderLifecyclePersistFailureRollsBackAndSendsNoAckOrSnapshot(t *testing.T) {
	root := t.TempDir()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	mgr := newLocalAgentAttachmentRegistry(root, logger)
	if _, _, err := mgr.applyLegacyStart("agent-a", "runtime-a", "workspace-a", 4); err != nil {
		t.Fatal(err)
	}
	mgr.writeState = func(string, []byte) error { return errors.New("injected state write failure") }
	if changed, accepted, err := mgr.applyLegacyStart("agent-b", "runtime-a", "workspace-a", 5); err == nil || changed || accepted {
		t.Fatalf("failed placement persist = changed %v accepted %v err %v", changed, accepted, err)
	}
	if _, ok := mgr.localRecord("agent-b"); ok {
		t.Fatal("failed placement persist leaked in-memory owner")
	}

	writes := make(chan []byte, 2)
	done := make(chan struct{})
	d := &Daemon{
		logger: logger,
		workspaces: map[string]*workspaceState{
			"workspace-a": newWorkspaceState("workspace-a", []string{"runtime-a"}, protocol.DaemonCapabilityReminderVersionedCache),
		},
		runtimeIndex:     map[string]Runtime{"runtime-a": {ID: "runtime-a", WorkspaceID: "workspace-a"}},
		agentAttachments: mgr,
	}
	d.setReminderWS(writes, done, func() error { return nil })
	err := d.handleDaemonAgentLifecycleReplayEnd(protocol.DaemonAgentLifecycleReplayEndPayload{RuntimeCursors: map[string]int64{"runtime-a": 9}})
	if err == nil {
		t.Fatal("lifecycle cursor persist unexpectedly succeeded")
	}
	if len(writes) != 0 {
		t.Fatalf("persist failure queued %d ACK/snapshot frames", len(writes))
	}
	if got := mgr.legacyRecoveryCursors()["runtime-a"]; got != 0 {
		t.Fatalf("failed cursor persist leaked in-memory cursor %d", got)
	}
	if got := newLocalAgentAttachmentRegistry(root, nil).legacyRecoveryCursors()["runtime-a"]; got != 0 {
		t.Fatalf("failed cursor persist leaked durable cursor %d", got)
	}
}

func TestServerOriginatedOwnerStartBypassesStaleCapabilityCacheAndSchedulesTimer(t *testing.T) {
	clock := &fakeReminderClock{now: time.Date(2026, 7, 22, 8, 0, 0, 0, time.UTC)}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	writes := make(chan []byte, 1)
	done := make(chan struct{})
	d := &Daemon{
		logger: logger,
		workspaces: map[string]*workspaceState{
			"workspace-a": newWorkspaceState("workspace-a", []string{"runtime-a"}),
		},
		runtimeIndex:     map[string]Runtime{"runtime-a": {ID: "runtime-a", WorkspaceID: "workspace-a"}},
		agentAttachments: newLocalAgentAttachmentRegistry(t.TempDir(), logger),
		reminderCache:    newReminderCache(clock, logger, func(protocol.ReminderTimerJob) {}),
	}
	prepareHeadlessWorkspaceRunnerTestDaemon(d, t.TempDir())
	d.setReminderWS(writes, done, func() error { return nil })
	d.reminderGateMu.Lock()
	d.reminderReplayComplete = true
	d.reminderGateMu.Unlock()
	d.reminderCache.resume()
	if err := d.handleDaemonAgentStart(protocol.DaemonAgentStartPayload{
		AgentID: "agent-a", RuntimeID: "runtime-a", WorkspaceID: "workspace-a", PlacementGeneration: 7,
	}); err != nil {
		t.Fatal(err)
	}
	var request protocol.Message
	if err := json.Unmarshal(<-writes, &request); err != nil {
		t.Fatal(err)
	}
	if request.Type != protocol.EventReminderSnapshotRequest {
		t.Fatalf("restore start frame = %q", request.Type)
	}
	d.handleReminderSnapshot(protocol.ReminderSnapshotPayload{
		AgentID: "agent-a", RuntimeID: "runtime-a", PlacementGeneration: 7,
		Reminders: []protocol.ReminderTimerJob{reminderJob("restored-reminder", "agent-a", 1, clock.now.Add(time.Hour))},
	})
	_, present := d.reminderCache.get("restored-reminder")
	if !present || len(clock.timers) != 1 {
		t.Fatalf("restored owner snapshot did not install timer: present=%v timers=%d", present, len(clock.timers))
	}
}

func TestReminderGenZeroOwnerRecoversAckedProjectionsThroughLifecycleCheckpointSnapshotAndFire(t *testing.T) {
	now := time.Date(2026, 7, 23, 10, 0, 0, 0, time.UTC)
	clock := &fakeReminderClock{now: now}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	root := t.TempDir()
	config := cachedAgentCredential{
		AgentID:     uuid.NewString(),
		RuntimeID:   "runtime-a",
		WorkspaceID: uuid.NewString(),
	}
	raw, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(agentworkspace.Root(root, config.WorkspaceID, config.AgentID), "runtime", "credentials", "current.json")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, raw, 0o600); err != nil {
		t.Fatal(err)
	}

	mgr := newLocalAgentAttachmentRegistry(root, logger)
	owner, ok := mgr.localRecord(config.AgentID)
	if !ok || owner.PlacementGeneration != 0 {
		t.Fatalf("bootstrapped owner=%+v present=%v, want generation zero", owner, ok)
	}
	cache := newReminderCache(clock, logger, nil)
	cache.setPersistence(root)
	writes := make(chan []byte, 32)
	var d *Daemon
	cache.onFire = func(job protocol.ReminderTimerJob) { d.onReminderTimer(job) }
	d = &Daemon{
		logger: logger,
		workspaces: map[string]*workspaceState{
			config.WorkspaceID: newWorkspaceState(config.WorkspaceID, []string{config.RuntimeID}),
		},
		runtimeIndex:     map[string]Runtime{config.RuntimeID: {ID: config.RuntimeID, WorkspaceID: config.WorkspaceID}},
		agentAttachments: mgr,
		reminderCache:    cache,
	}
	prepareHeadlessWorkspaceRunnerTestDaemon(d, root)
	d.setReminderWS(writes, make(chan struct{}), func() error { return nil })

	jobs := make([]protocol.ReminderTimerJob, 0, 6)
	for seq := int64(1); seq <= 6; seq++ {
		job := reminderJob(fmt.Sprintf("reminder-%d", seq), config.AgentID, 1, now.Add(-time.Duration(seq)*time.Minute))
		jobs = append(jobs, job)
		event := reminderProjection(seq, config.RuntimeID, "upsert", job, false)
		if err := d.handleReminderProjection(event); err != nil {
			t.Fatal(err)
		}
		var ack protocol.Message
		if err := json.Unmarshal(<-writes, &ack); err != nil {
			t.Fatal(err)
		}
		if ack.Type != protocol.EventReminderProjectionAck {
			t.Fatalf("discarded projection %d frame=%q", seq, ack.Type)
		}
	}
	if got := cache.projectionCursors()[config.RuntimeID]; got != 6 {
		t.Fatalf("discarded projection cursor=%d, want 6", got)
	}
	if len(cache.fences) != 0 || len(clock.timers) != 0 {
		t.Fatalf("generation-zero discard leaked fences/timers=%d/%d", len(cache.fences), len(clock.timers))
	}

	// Model reconnect/heartbeat recovery after the server has already garbage
	// collected the ACKed projection rows. The authoritative current-owner
	// checkpoint must repair generation zero before projection replay, and the
	// pending snapshot must restore the definitions without another upsert.
	d.clearReminderWS(writes)
	writes = make(chan []byte, 32)
	d.setReminderWS(writes, make(chan struct{}), func() error { return nil })
	if !d.requestAgentLifecycleReplay() {
		t.Fatal("queue lifecycle protocol probe")
	}
	var lifecycleRequest protocol.Message
	if err := json.Unmarshal(<-writes, &lifecycleRequest); err != nil {
		t.Fatal(err)
	}
	var lifecycleReq protocol.DaemonAgentLifecycleRequestPayload
	if lifecycleRequest.Type != protocol.EventDaemonAgentLifecycleReq ||
		json.Unmarshal(lifecycleRequest.Payload, &lifecycleReq) != nil ||
		lifecycleReq.RuntimeCursors[config.RuntimeID] != 0 {
		t.Fatalf("lifecycle protocol probe=%+v payload=%s", lifecycleRequest, lifecycleRequest.Payload)
	}
	if err := d.handleDaemonAgentStart(protocol.DaemonAgentStartPayload{
		AgentID: config.AgentID, RuntimeID: config.RuntimeID, WorkspaceID: config.WorkspaceID,
		PlacementGeneration: 1, LifecycleSeq: 6, Replay: true,
	}); err != nil {
		t.Fatal(err)
	}
	owner, ok = mgr.localRecord(config.AgentID)
	if !ok || owner.PlacementGeneration != 1 {
		t.Fatalf("authoritative checkpoint owner=%+v present=%v", owner, ok)
	}
	if err := d.handleDaemonAgentLifecycleReplayEnd(protocol.DaemonAgentLifecycleReplayEndPayload{
		RuntimeCursors: map[string]int64{config.RuntimeID: 6},
	}); err != nil {
		t.Fatal(err)
	}
	var lifecycleAck, projectionRequest protocol.Message
	if err := json.Unmarshal(<-writes, &lifecycleAck); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(<-writes, &projectionRequest); err != nil {
		t.Fatal(err)
	}
	var replay protocol.ReminderProjectionRequestPayload
	if lifecycleAck.Type != protocol.EventDaemonAgentLifecycleAck ||
		projectionRequest.Type != protocol.EventReminderProjectionReq ||
		json.Unmarshal(projectionRequest.Payload, &replay) != nil {
		t.Fatalf("recovery frames=%q/%q", lifecycleAck.Type, projectionRequest.Type)
	}
	residencies := replay.RuntimeResidencies[config.RuntimeID]
	if replay.RuntimeCursors[config.RuntimeID] != 6 || len(residencies) != 1 ||
		residencies[0].AgentID != config.AgentID || residencies[0].PlacementGeneration != 1 {
		t.Fatalf("recovery projection request=%+v", replay)
	}
	if err := d.handleReminderProjectionReplayEnd(protocol.ReminderProjectionReplayEndPayload{
		RuntimeCursors: map[string]int64{config.RuntimeID: 6},
	}); err != nil {
		t.Fatal(err)
	}
	var projectionAck, snapshotRequest protocol.Message
	if err := json.Unmarshal(<-writes, &projectionAck); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(<-writes, &snapshotRequest); err != nil {
		t.Fatal(err)
	}
	var snapshotReq protocol.ReminderSnapshotRequestPayload
	if projectionAck.Type != protocol.EventReminderProjectionAck ||
		snapshotRequest.Type != protocol.EventReminderSnapshotRequest ||
		json.Unmarshal(snapshotRequest.Payload, &snapshotReq) != nil {
		t.Fatalf("post-replay recovery frames=%q/%q", projectionAck.Type, snapshotRequest.Type)
	}
	if snapshotReq.AgentID != config.AgentID || snapshotReq.RuntimeID != config.RuntimeID || snapshotReq.PlacementGeneration != 1 {
		t.Fatalf("recovery snapshot request=%+v", snapshotReq)
	}
	if err := d.handleReminderSnapshot(protocol.ReminderSnapshotPayload{
		AgentID: config.AgentID, RuntimeID: config.RuntimeID, PlacementGeneration: 1,
		ProjectionWatermark: 6, Reminders: jobs,
	}); err != nil {
		t.Fatal(err)
	}
	if len(cache.fences) != 6 || len(clock.timers) != 6 {
		t.Fatalf("recovered fences/timers=%d/%d, want 6/6", len(cache.fences), len(clock.timers))
	}
	for i, delay := range clock.delays {
		if delay != 0 {
			t.Fatalf("recovered overdue timer %d delay=%s, want 0", i, delay)
		}
	}

	clock.fire(0)
	var fireAttempt protocol.Message
	if err := json.Unmarshal(<-writes, &fireAttempt); err != nil {
		t.Fatal(err)
	}
	var attempt protocol.ReminderFireAttemptPayload
	if fireAttempt.Type != protocol.EventReminderFireAttempt || json.Unmarshal(fireAttempt.Payload, &attempt) != nil {
		t.Fatalf("recovered overdue fire frame=%q payload=%s", fireAttempt.Type, fireAttempt.Payload)
	}
	recoveredJob := false
	for _, job := range jobs {
		if attempt.ReminderID == job.ReminderID && attempt.Version == job.Version {
			recoveredJob = true
			break
		}
	}
	if attempt.AgentID != config.AgentID || attempt.RuntimeID != config.RuntimeID ||
		attempt.PlacementGeneration != 1 || !recoveredJob {
		t.Fatalf("recovered overdue fire attempt=%+v", attempt)
	}

	// Exact duplicates stay idempotent after recovery.
	if err := d.handleDaemonAgentStart(protocol.DaemonAgentStartPayload{
		AgentID: config.AgentID, RuntimeID: config.RuntimeID, WorkspaceID: config.WorkspaceID,
		PlacementGeneration: 1,
	}); err != nil {
		t.Fatal(err)
	}
	duplicate := reminderProjection(6, config.RuntimeID, "upsert", jobs[5], false)
	if err := d.handleReminderProjection(duplicate); err != nil {
		t.Fatal(err)
	}
	var duplicateAck protocol.Message
	if err := json.Unmarshal(<-writes, &duplicateAck); err != nil || duplicateAck.Type != protocol.EventReminderProjectionAck {
		t.Fatalf("duplicate projection ack=%q err=%v", duplicateAck.Type, err)
	}
	// 7, not 6: reminder-1 already fired above and (task #68) its entry stays
	// alive with a local retry timer instead of being deleted, so the fired
	// one now accounts for two scheduled timers (its due-time timer already
	// consumed by clock.fire(0), plus the retry timer fireLocked scheduled).
	if len(clock.timers) != 7 || len(writes) != 0 {
		t.Fatalf("duplicate recovery timers/extra_frames=%d/%d, want 7/0", len(clock.timers), len(writes))
	}
}

func TestReminderLifecycleProbeAgainstOldServerStaysFailClosedWithoutReconnect(t *testing.T) {
	now := time.Date(2026, 7, 23, 10, 0, 0, 0, time.UTC)
	clock := &fakeReminderClock{now: now}
	var fired []protocol.ReminderTimerJob
	cache := newReminderCache(clock, nil, func(job protocol.ReminderTimerJob) { fired = append(fired, job) })
	if !cache.upsert(reminderJob("pre-connect", "agent-a", 1, now.Add(time.Minute))) {
		t.Fatal("seed pre-connect timer")
	}
	writes := make(chan []byte, 4)
	closed := 0
	d := &Daemon{
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		workspaces: map[string]*workspaceState{
			"workspace-a": newWorkspaceState("workspace-a", []string{"runtime-a"}),
		},
		runtimeIndex:     map[string]Runtime{"runtime-a": {ID: "runtime-a", WorkspaceID: "workspace-a"}},
		agentAttachments: newLocalAgentAttachmentRegistry(t.TempDir(), nil),
		reminderCache:    cache,
	}
	d.setReminderWS(writes, make(chan struct{}), func() error { closed++; return nil })
	if !d.requestAgentLifecycleReplay() {
		t.Fatal("queue lifecycle protocol probe")
	}
	var probe protocol.Message
	if err := json.Unmarshal(<-writes, &probe); err != nil {
		t.Fatal(err)
	}
	if probe.Type != protocol.EventDaemonAgentLifecycleReq {
		t.Fatalf("old-server protocol probe=%q", probe.Type)
	}

	// An old server ignores the unknown application frame. A later heartbeat
	// must not queue duplicate probes while this connection remains attached,
	// and the cache stays suspended with no timer or fire side effect.
	d.requestAgentLifecycleReplay()
	clock.fire(0)
	if len(writes) != 0 || closed != 0 || len(fired) != 0 {
		t.Fatalf("old-server probe frames/reconnects/fires=%d/%d/%d", len(writes), closed, len(fired))
	}
	cache.mu.Lock()
	suspended := cache.suspended
	entries := len(cache.entries)
	cache.mu.Unlock()
	if !suspended || entries != 0 {
		t.Fatalf("old-server cache suspended=%v entries=%d", suspended, entries)
	}
	d.reminderGateMu.Lock()
	inFlight := false
	replayComplete := d.reminderReplayComplete
	d.reminderGateMu.Unlock()
	if !inFlight || replayComplete {
		t.Fatalf("old-server lifecycle in_flight=%v replay_complete=%v", inFlight, replayComplete)
	}
}

func TestReminderOldStopAndSnapshotCannotDeleteOrRestoreNewPlacement(t *testing.T) {
	clock := &fakeReminderClock{now: time.Now().UTC()}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	mgr := newLocalAgentAttachmentRegistry(t.TempDir(), logger)
	mgr.applyLegacyStart("agent-a", "runtime-a", "workspace-a", 3)
	cache := newReminderCache(clock, logger, func(protocol.ReminderTimerJob) {})
	cache.upsert(reminderJob("current", "agent-a", 5, clock.now.Add(time.Hour)))
	d := &Daemon{agentAttachments: mgr, reminderCache: cache}

	d.handleDaemonAgentStop(protocol.DaemonAgentStopPayload{AgentID: "agent-a", RuntimeID: "runtime-b", PlacementGeneration: 2})
	if _, ok := mgr.localRecord("agent-a"); !ok {
		t.Fatal("old stop removed new placement")
	}
	if _, ok := cache.get("current"); !ok {
		t.Fatal("old stop removed new placement timer")
	}
	owner, _ := mgr.localRecord("agent-a")
	if owner.RuntimeID == "runtime-b" || owner.PlacementGeneration != 3 {
		t.Fatalf("owner changed by old frame: %+v", owner)
	}
	d.handleReminderSnapshot(protocol.ReminderSnapshotPayload{
		AgentID: "agent-a", RuntimeID: "runtime-b", PlacementGeneration: 2,
		Reminders: []protocol.ReminderTimerJob{reminderJob("late-old", "agent-a", 9, clock.now.Add(2*time.Hour))},
	})
	if _, ok := cache.get("late-old"); ok {
		t.Fatal("late old-placement snapshot restored a stale timer")
	}
	if _, ok := cache.get("current"); !ok {
		t.Fatal("late old-placement snapshot replaced the current timer set")
	}
}
