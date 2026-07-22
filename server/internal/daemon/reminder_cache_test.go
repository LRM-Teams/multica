package daemon

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
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
	if _, ok := cache.get("r1"); ok {
		t.Fatal("due timer remained installed")
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

func TestReminderCacheCorruptDurableStateFailsClosedWithoutAckableMutation(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, ".daemon", reminderCacheStateFile)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{not-json"), 0o600); err != nil {
		t.Fatal(err)
	}
	cache := newReminderCache(&fakeReminderClock{now: time.Now().UTC()}, nil, nil)
	cache.setPersistence(root)
	if cache.stateError() == nil {
		t.Fatal("corrupt state did not surface startup error")
	}
	job := reminderJob("r1", "agent-a", 1, time.Now().UTC().Add(time.Hour))
	if applied, err := cache.applyProjection(reminderProjection(1, "runtime-a", "upsert", job, false)); err == nil || applied {
		t.Fatalf("corrupt state mutation = applied %v err %v", applied, err)
	}
	if got := cache.projectionCursors()["runtime-a"]; got != 0 {
		t.Fatalf("corrupt state advanced cursor to %d", got)
	}
	if _, ok := cache.get("r1"); ok {
		t.Fatal("corrupt state installed timer")
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
	cache.writeState = writeReminderAgentState
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
	mgr := newReminderAgentManager(t.TempDir(), logger)
	if changed, accepted, err := mgr.applyStart("agent-a", "runtime-a", "workspace-a", 2); err != nil || !changed || !accepted {
		t.Fatal("seed placement")
	}
	writes := make(chan []byte, 2)
	d := &Daemon{
		logger:         logger,
		runtimeIndex:   map[string]Runtime{"runtime-a": {ID: "runtime-a", WorkspaceID: "workspace-a"}},
		reminderAgents: mgr,
		reminderCache:  cache,
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
		logger:         logger,
		runtimeIndex:   map[string]Runtime{"runtime-a": {ID: "runtime-a", WorkspaceID: "workspace-a"}},
		reminderAgents: newReminderAgentManager(t.TempDir(), logger),
		reminderCache:  cache,
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
	mgr := newReminderAgentManager(t.TempDir(), logger)
	if changed, accepted, err := mgr.applyStart("agent-a", "runtime-a", "workspace-a", 1); err != nil || !changed || !accepted {
		t.Fatal("seed placement")
	}
	writes := make(chan []byte, 8)
	d := &Daemon{
		logger:                 logger,
		runtimeIndex:           map[string]Runtime{"runtime-a": {ID: "runtime-a", WorkspaceID: "workspace-a"}},
		reminderAgents:         mgr,
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

func TestReminderAgentManagerPersistsIdleResidency(t *testing.T) {
	root := t.TempDir()
	mgr := newReminderAgentManager(root, nil)
	if !mgr.markRunning("a1", "r1", "w1") {
		t.Fatal("first local owner was not reported new")
	}
	if mgr.markRunning("a1", "r1", "w1") {
		t.Fatal("existing local owner reported new")
	}
	mgr.markIdle("a1")
	mgr.markIdle("a1")

	reloaded := newReminderAgentManager(root, nil)
	entry, ok := reloaded.get("a1")
	if !ok || entry.Running != 0 || entry.RuntimeID != "r1" || entry.WorkspaceID != "w1" {
		t.Fatalf("reloaded residency = %+v, %v", entry, ok)
	}
	if removed, accepted, err := reloaded.applyStop("a1", "r1", 1); err != nil || !removed || !accepted || len(reloaded.residentAgentIDs()) != 0 {
		t.Fatal("terminal removal did not clear local owner")
	}
}

func TestReminderAgentManagerBootstrapsRecoverableConfigAndKeepsRemovalTombstone(t *testing.T) {
	root := t.TempDir()
	config := cachedAgentCredential{
		AgentID:     "agent-bootstrap",
		RuntimeID:   "runtime-bootstrap",
		WorkspaceID: "workspace-bootstrap",
	}
	raw, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, config.WorkspaceID, ".multica", "agents", config.AgentID, "runtime", "credentials", "current.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}

	mgr := newReminderAgentManager(root, nil)
	entry, ok := mgr.get(config.AgentID)
	if !ok || entry.RuntimeID != config.RuntimeID || entry.WorkspaceID != config.WorkspaceID || entry.Running != 0 {
		t.Fatalf("bootstrapped residency = %+v, %v", entry, ok)
	}
	if removed, accepted, err := mgr.applyStop(config.AgentID, config.RuntimeID, 1); err != nil || !removed || !accepted {
		t.Fatal("remove bootstrapped owner = false")
	}
	if _, ok := newReminderAgentManager(root, nil).get(config.AgentID); ok {
		t.Fatal("removed owner was resurrected from stale local config")
	}

	reloaded := newReminderAgentManager(root, nil)
	if changed, accepted, err := reloaded.applyStart(config.AgentID, "runtime-new", config.WorkspaceID, 2); err != nil || !changed || !accepted {
		t.Fatal("local owner recreation was not reported as new")
	}
	entry, ok = newReminderAgentManager(root, nil).get(config.AgentID)
	if !ok || entry.RuntimeID != "runtime-new" {
		t.Fatalf("recreated residency = %+v, %v", entry, ok)
	}
}

func TestDaemonAgentStopClearsOwnerResidencyAndReminderCache(t *testing.T) {
	clock := &fakeReminderClock{now: time.Now().UTC()}
	d := &Daemon{
		reminderAgents: newReminderAgentManager(t.TempDir(), nil),
		reminderCache:  newReminderCache(clock, slog.New(slog.NewTextHandler(io.Discard, nil)), func(protocol.ReminderTimerJob) {}),
	}
	d.reminderAgents.markRunning("agent-a", "runtime-a", "workspace-a")
	d.reminderAgents.markIdle("agent-a")
	d.reminderAgents.markRunning("agent-b", "runtime-a", "workspace-a")
	d.reminderAgents.markIdle("agent-b")
	d.reminderCache.upsert(reminderJob("reminder-a", "agent-a", 1, clock.now.Add(time.Hour)))
	d.reminderCache.upsert(reminderJob("reminder-b", "agent-b", 1, clock.now.Add(time.Hour)))

	d.handleDaemonAgentStop(protocol.DaemonAgentStopPayload{AgentID: "agent-a", RuntimeID: "runtime-a", PlacementGeneration: 1})

	if _, ok := d.reminderAgents.get("agent-a"); ok {
		t.Fatal("stopped owner remains resident")
	}
	if _, ok := d.reminderCache.get("reminder-a"); ok {
		t.Fatal("stopped owner reminder remains cached")
	}
	if _, ok := d.reminderAgents.get("agent-b"); !ok {
		t.Fatal("unrelated owner residency was removed")
	}
	if _, ok := d.reminderCache.get("reminder-b"); !ok {
		t.Fatal("unrelated owner reminder was removed")
	}
}

func TestReminderAgentPlacementGenerationFencesAtoBtoAOutOfOrder(t *testing.T) {
	mgr := newReminderAgentManager(t.TempDir(), nil)
	if changed, accepted, err := mgr.applyStart("agent-a", "runtime-a", "workspace-a", 1); err != nil || !changed || !accepted {
		t.Fatal("initial A start rejected")
	}
	if changed, accepted, err := mgr.applyStart("agent-a", "runtime-b", "workspace-a", 2); err != nil || !changed || !accepted {
		t.Fatal("A to B start rejected")
	}
	if changed, accepted, err := mgr.applyStart("agent-a", "runtime-b", "workspace-a", 2); err != nil || changed || !accepted {
		t.Fatal("duplicate B start was not idempotent")
	}
	if changed, accepted, err := mgr.applyStart("agent-a", "runtime-c", "workspace-a", 2); err != nil || changed || accepted {
		t.Fatal("same-generation conflicting runtime start was accepted")
	}
	if removed, accepted, err := mgr.applyStop("agent-a", "runtime-a", 2); err != nil || removed || !accepted {
		t.Fatal("same-generation old-runtime stop removed B")
	}
	if changed, accepted, err := mgr.applyStart("agent-a", "runtime-a", "workspace-a", 3); err != nil || !changed || !accepted {
		t.Fatal("B to A start rejected")
	}
	if removed, accepted, err := mgr.applyStop("agent-a", "runtime-b", 2); err != nil || removed || accepted {
		t.Fatal("stale B stop was accepted")
	}
	if changed, accepted, err := mgr.applyStart("agent-a", "runtime-b", "workspace-a", 2); err != nil || changed || accepted {
		t.Fatal("stale B start was accepted")
	}
	entry, ok := mgr.get("agent-a")
	if !ok || entry.RuntimeID != "runtime-a" || entry.PlacementGeneration != 3 {
		t.Fatalf("final placement = %+v, %v", entry, ok)
	}

	reloaded := newReminderAgentManager(mgr.root, nil)
	entry, ok = reloaded.get("agent-a")
	if !ok || entry.RuntimeID != "runtime-a" || entry.PlacementGeneration != 3 {
		t.Fatalf("reloaded placement = %+v, %v", entry, ok)
	}
}

func TestReminderReconnectRequestsSnapshotForRunningAndIdleOwners(t *testing.T) {
	root := t.TempDir()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	mgr := newReminderAgentManager(root, logger)
	mgr.markRunning("agent-running", "runtime-a", "workspace-a")
	mgr.markRunning("agent-idle", "runtime-a", "workspace-a")
	mgr.markIdle("agent-idle")
	writes := make(chan []byte, 2)
	done := make(chan struct{})
	d := &Daemon{
		logger:         logger,
		workspaces:     map[string]*workspaceState{"workspace-a": newWorkspaceState("workspace-a", []string{"runtime-a"}, "", nil, nil, protocol.DaemonCapabilityReminderVersionedCache)},
		reminderAgents: mgr,
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
	mgr := newReminderAgentManager(root, logger)
	if changed, accepted, err := mgr.applyStart("agent-a", "runtime-a", "workspace-a", 4); err != nil || !changed || !accepted {
		t.Fatal("seed placement failed")
	}
	writes := make(chan []byte, 4)
	done := make(chan struct{})
	d := &Daemon{
		logger: logger,
		workspaces: map[string]*workspaceState{
			"workspace-a": newWorkspaceState("workspace-a", []string{"runtime-a"}, "", nil, nil, protocol.DaemonCapabilityReminderVersionedCache),
		},
		runtimeIndex:   map[string]Runtime{"runtime-a": {ID: "runtime-a", WorkspaceID: "workspace-a"}},
		reminderAgents: mgr,
		reminderCache:  newReminderCache(&fakeReminderClock{now: time.Now().UTC()}, logger, nil),
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
	if got := newReminderAgentManager(root, nil).lifecycleCursors()["runtime-a"]; got != 9 {
		t.Fatalf("persisted ack cursor = %d, want 9", got)
	}
}

func TestReminderProjectionCursorLossAtomicResetUsesOnlyLocalResidencies(t *testing.T) {
	now := time.Date(2026, 7, 22, 8, 0, 0, 0, time.UTC)
	clock := &fakeReminderClock{now: now}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	mgr := newReminderAgentManager(t.TempDir(), logger)
	mgr.applyStart("agent-a", "runtime-a", "workspace-a", 3)
	mgr.applyStart("agent-stale", "runtime-a", "workspace-a", 4)
	var fired []protocol.ReminderTimerJob
	cache := newReminderCache(clock, logger, func(job protocol.ReminderTimerJob) { fired = append(fired, job) })
	cache.upsert(reminderJob("stale-due", "agent-stale", 1, now.Add(-time.Minute)))
	writes := make(chan []byte, 8)
	done := make(chan struct{})
	d := &Daemon{
		logger: logger,
		workspaces: map[string]*workspaceState{
			"workspace-a": newWorkspaceState("workspace-a", []string{"runtime-a"}, "", nil, nil, protocol.DaemonCapabilityReminderVersionedCache),
		},
		runtimeIndex:   map[string]Runtime{"runtime-a": {ID: "runtime-a", WorkspaceID: "workspace-a"}},
		reminderAgents: mgr,
		reminderCache:  cache,
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
	if _, ok := mgr.get("agent-stale"); ok {
		t.Fatal("terminal local owner survived runtime reset")
	}
	if _, ok := mgr.get("agent-a"); !ok {
		t.Fatal("canonical local owner was removed")
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
	mgr := newReminderAgentManager(t.TempDir(), logger)
	mgr.applyStart("agent-a", "runtime-a", "workspace-a", 3)
	cache := newReminderCache(&fakeReminderClock{now: time.Now().UTC()}, logger, nil)
	cache.path = filepath.Join(t.TempDir(), "reminder-cache.json")
	cache.writeState = func(string, []byte) error { return errors.New("injected reset persist failure") }
	writes := make(chan []byte, 4)
	done := make(chan struct{})
	d := &Daemon{
		logger: logger,
		workspaces: map[string]*workspaceState{
			"workspace-a": newWorkspaceState("workspace-a", []string{"runtime-a"}, "", nil, nil, protocol.DaemonCapabilityReminderVersionedCache),
		},
		runtimeIndex:   map[string]Runtime{"runtime-a": {ID: "runtime-a", WorkspaceID: "workspace-a"}},
		reminderAgents: mgr,
		reminderCache:  cache,
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
	mgr := newReminderAgentManager(root, logger)
	if _, _, err := mgr.applyStart("agent-a", "runtime-a", "workspace-a", 4); err != nil {
		t.Fatal(err)
	}
	mgr.writeState = func(string, []byte) error { return errors.New("injected state write failure") }
	if changed, accepted, err := mgr.applyStart("agent-b", "runtime-a", "workspace-a", 5); err == nil || changed || accepted {
		t.Fatalf("failed placement persist = changed %v accepted %v err %v", changed, accepted, err)
	}
	if _, ok := mgr.get("agent-b"); ok {
		t.Fatal("failed placement persist leaked in-memory owner")
	}

	writes := make(chan []byte, 2)
	done := make(chan struct{})
	d := &Daemon{
		logger: logger,
		workspaces: map[string]*workspaceState{
			"workspace-a": newWorkspaceState("workspace-a", []string{"runtime-a"}, "", nil, nil, protocol.DaemonCapabilityReminderVersionedCache),
		},
		runtimeIndex:   map[string]Runtime{"runtime-a": {ID: "runtime-a", WorkspaceID: "workspace-a"}},
		reminderAgents: mgr,
	}
	d.setReminderWS(writes, done, func() error { return nil })
	err := d.handleDaemonAgentLifecycleReplayEnd(protocol.DaemonAgentLifecycleReplayEndPayload{RuntimeCursors: map[string]int64{"runtime-a": 9}})
	if err == nil {
		t.Fatal("lifecycle cursor persist unexpectedly succeeded")
	}
	if len(writes) != 0 {
		t.Fatalf("persist failure queued %d ACK/snapshot frames", len(writes))
	}
	if got := mgr.lifecycleCursors()["runtime-a"]; got != 0 {
		t.Fatalf("failed cursor persist leaked in-memory cursor %d", got)
	}
	if got := newReminderAgentManager(root, nil).lifecycleCursors()["runtime-a"]; got != 0 {
		t.Fatalf("failed cursor persist leaked durable cursor %d", got)
	}
}

func TestRestoredOwnerStartFromZeroResidentsRequestsSnapshotAndSchedulesTimer(t *testing.T) {
	clock := &fakeReminderClock{now: time.Date(2026, 7, 22, 8, 0, 0, 0, time.UTC)}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	writes := make(chan []byte, 1)
	done := make(chan struct{})
	d := &Daemon{
		logger: logger,
		workspaces: map[string]*workspaceState{
			"workspace-a": newWorkspaceState("workspace-a", []string{"runtime-a"}, "", nil, nil, protocol.DaemonCapabilityReminderVersionedCache),
		},
		runtimeIndex:   map[string]Runtime{"runtime-a": {ID: "runtime-a", WorkspaceID: "workspace-a"}},
		reminderAgents: newReminderAgentManager(t.TempDir(), logger),
		reminderCache:  newReminderCache(clock, logger, func(protocol.ReminderTimerJob) {}),
	}
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

func TestReminderOldStopAndSnapshotCannotDeleteOrRestoreNewPlacement(t *testing.T) {
	clock := &fakeReminderClock{now: time.Now().UTC()}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	mgr := newReminderAgentManager(t.TempDir(), logger)
	mgr.applyStart("agent-a", "runtime-a", "workspace-a", 3)
	cache := newReminderCache(clock, logger, func(protocol.ReminderTimerJob) {})
	cache.upsert(reminderJob("current", "agent-a", 5, clock.now.Add(time.Hour)))
	d := &Daemon{reminderAgents: mgr, reminderCache: cache}

	d.handleDaemonAgentStop(protocol.DaemonAgentStopPayload{AgentID: "agent-a", RuntimeID: "runtime-b", PlacementGeneration: 2})
	if _, ok := mgr.get("agent-a"); !ok {
		t.Fatal("old stop removed new placement")
	}
	if _, ok := cache.get("current"); !ok {
		t.Fatal("old stop removed new placement timer")
	}
	owner, _ := mgr.get("agent-a")
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
