package daemon

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

const reminderCacheStateFile = "reminder_cache.json"

type reminderTimer interface {
	Stop() bool
}

type reminderClock interface {
	Now() time.Time
	AfterFunc(time.Duration, func()) reminderTimer
}

type systemReminderClock struct{}

func (systemReminderClock) Now() time.Time { return time.Now() }
func (systemReminderClock) AfterFunc(delay time.Duration, fn func()) reminderTimer {
	return time.AfterFunc(delay, fn)
}

type reminderCacheEntry struct {
	job   protocol.ReminderTimerJob
	timer reminderTimer
}

type reminderVersionFence struct {
	OwnerAgentID string `json:"owner_agent_id"`
	Version      int64  `json:"version"`
	LastSeq      int64  `json:"last_seq"`
	Terminal     bool   `json:"terminal"`
}

type reminderCacheState struct {
	Fences         map[string]reminderVersionFence `json:"fences"`
	RuntimeCursors map[string]int64                `json:"runtime_cursors"`
}

// reminderCache is the owner-daemon timer projection. Entries are ephemeral,
// while the per-ID version fences and per-runtime projection cursors are
// persisted together before an event is ACKed. That makes deleted timers
// non-resurrectable without retaining unbounded terminal definitions server-side.
type reminderCache struct {
	mu             sync.Mutex
	entries        map[string]reminderCacheEntry
	fences         map[string]reminderVersionFence
	runtimeCursors map[string]int64
	pendingFires   map[string]int64
	clock          reminderClock
	onFire         func(protocol.ReminderTimerJob)
	logger         *slog.Logger
	path           string
	loadErr        error
	writeState     func(string, []byte) error
}

func newReminderCache(clock reminderClock, logger *slog.Logger, onFire func(protocol.ReminderTimerJob)) *reminderCache {
	if clock == nil {
		clock = systemReminderClock{}
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &reminderCache{
		entries:        make(map[string]reminderCacheEntry),
		fences:         make(map[string]reminderVersionFence),
		runtimeCursors: make(map[string]int64),
		pendingFires:   make(map[string]int64),
		clock:          clock,
		onFire:         onFire,
		logger:         logger,
		writeState:     writeReminderAgentState,
	}
}

func (c *reminderCache) setPersistence(root string) {
	if c == nil || root == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.path = filepath.Join(root, ".daemon", reminderCacheStateFile)
	raw, err := osReadFile(c.path)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			c.loadErr = fmt.Errorf("load reminder cache state: %w", err)
		}
		return
	}
	var state reminderCacheState
	if err := json.Unmarshal(raw, &state); err != nil {
		c.loadErr = fmt.Errorf("decode reminder cache state: %w", err)
		return
	}
	for id, fence := range state.Fences {
		if id != "" && fence.Version > 0 {
			c.fences[id] = fence
		}
	}
	for runtimeID, seq := range state.RuntimeCursors {
		if runtimeID != "" && seq >= 0 {
			c.runtimeCursors[runtimeID] = seq
		}
	}
}

// osReadFile is a seam for the state loader without exposing filesystem
// details to tests that keep the cache in memory.
var osReadFile = func(path string) ([]byte, error) {
	return os.ReadFile(path)
}

func (c *reminderCache) persistLocked() error {
	if c.loadErr != nil {
		return c.loadErr
	}
	if c.path == "" {
		return nil
	}
	state := reminderCacheState{Fences: c.fences, RuntimeCursors: c.runtimeCursors}
	raw, err := json.Marshal(state)
	if err != nil {
		return err
	}
	if c.writeState == nil {
		return errors.New("reminder cache state writer is not configured")
	}
	return c.writeState(c.path, raw)
}

func (c *reminderCache) stateError() error {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.loadErr
}

func cloneReminderFences(in map[string]reminderVersionFence) map[string]reminderVersionFence {
	out := make(map[string]reminderVersionFence, len(in))
	for id, fence := range in {
		out[id] = fence
	}
	return out
}

func cloneReminderCursors(in map[string]int64) map[string]int64 {
	out := make(map[string]int64, len(in))
	for id, seq := range in {
		out[id] = seq
	}
	return out
}

func clonePendingFires(in map[string]int64) map[string]int64 {
	out := make(map[string]int64, len(in))
	for id, version := range in {
		out[id] = version
	}
	return out
}

func (c *reminderCache) applyProjection(event protocol.ReminderProjectionEvent) (bool, error) {
	if c == nil || event.Seq < 1 || event.RuntimeID == "" || event.AgentID == "" || event.ReminderID == "" || event.Version < 1 {
		return false, nil
	}
	if !event.Terminal {
		if event.Reminder.ReminderID == "" {
			event.Reminder = protocol.ReminderTimerJob{ReminderID: event.ReminderID, OwnerAgentID: event.AgentID, Version: event.Version, FireAt: event.FireAt}
		}
		if event.Reminder.ReminderID != event.ReminderID || event.Reminder.OwnerAgentID != event.AgentID || event.Reminder.Version != event.Version {
			return false, nil
		}
		if _, err := time.Parse(time.RFC3339, event.Reminder.FireAt); err != nil {
			return false, nil
		}
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if event.Seq <= c.runtimeCursors[event.RuntimeID] {
		return false, nil
	}
	previousFences := cloneReminderFences(c.fences)
	previousCursors := cloneReminderCursors(c.runtimeCursors)
	previousPending, hadPending := c.pendingFires[event.ReminderID]

	fence, exists := c.fences[event.ReminderID]
	apply := false
	if event.Terminal {
		apply = !exists || event.Version >= fence.Version
	} else if !exists || event.Version > fence.Version {
		apply = true
	} else if event.EventType == "fire_result" && event.Version == fence.Version && hadPending && previousPending == event.Version {
		apply = true
	}
	if apply {
		c.fences[event.ReminderID] = reminderVersionFence{
			OwnerAgentID: event.AgentID, Version: event.Version, LastSeq: event.Seq, Terminal: event.Terminal,
		}
		delete(c.pendingFires, event.ReminderID)
	}
	c.runtimeCursors[event.RuntimeID] = event.Seq
	if err := c.persistLocked(); err != nil {
		c.fences = previousFences
		c.runtimeCursors = previousCursors
		if hadPending {
			c.pendingFires[event.ReminderID] = previousPending
		} else {
			delete(c.pendingFires, event.ReminderID)
		}
		return false, err
	}
	if !apply {
		return false, nil
	}
	if current, ok := c.entries[event.ReminderID]; ok && current.timer != nil {
		current.timer.Stop()
	}
	delete(c.entries, event.ReminderID)
	if !event.Terminal {
		fireAt, _ := time.Parse(time.RFC3339, event.Reminder.FireAt)
		c.entries[event.ReminderID] = reminderCacheEntry{job: event.Reminder}
		c.armLocked(event.Reminder, fireAt)
	}
	return true, nil
}

// upsert/cancel remain small unit-test helpers. Production frames always use
// applyProjection so sequence and durable ACK fencing cannot be bypassed.
func (c *reminderCache) upsert(job protocol.ReminderTimerJob) bool {
	event := protocol.ReminderProjectionEvent{Seq: c.nextTestSeq(), RuntimeID: "test", AgentID: job.OwnerAgentID, EventType: "upsert", ReminderID: job.ReminderID, Version: job.Version, FireAt: job.FireAt, Reminder: job}
	ok, _ := c.applyProjection(event)
	return ok
}

func (c *reminderCache) cancel(reminderID string, version int64) bool {
	fence := c.highWatermark(reminderID)
	event := protocol.ReminderProjectionEvent{Seq: c.nextTestSeq(), RuntimeID: "test", AgentID: fence.OwnerAgentID, EventType: "cancel", ReminderID: reminderID, Version: version, Terminal: true}
	if event.AgentID == "" {
		event.AgentID = "test"
	}
	ok, _ := c.applyProjection(event)
	return ok
}

func (c *reminderCache) nextTestSeq() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.runtimeCursors["test"] + 1
}

func (c *reminderCache) highWatermark(reminderID string) reminderVersionFence {
	if c == nil {
		return reminderVersionFence{}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.fences[reminderID]
}

func (c *reminderCache) projectionCursors() map[string]int64 {
	if c == nil {
		return map[string]int64{}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return cloneReminderCursors(c.runtimeCursors)
}

func (c *reminderCache) advanceProjectionCursor(runtimeID string, seq int64) error {
	if c == nil || runtimeID == "" || seq < 1 {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if seq <= c.runtimeCursors[runtimeID] {
		return nil
	}
	previous := c.runtimeCursors[runtimeID]
	c.runtimeCursors[runtimeID] = seq
	if err := c.persistLocked(); err != nil {
		c.runtimeCursors[runtimeID] = previous
		return err
	}
	return nil
}

func (c *reminderCache) snapshot(runtimeID, agentID string, watermark int64, jobs []protocol.ReminderTimerJob) (int, error) {
	if c == nil || runtimeID == "" || agentID == "" || watermark < 0 {
		return 0, nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if watermark < c.runtimeCursors[runtimeID] {
		return 0, nil
	}
	previousFences := cloneReminderFences(c.fences)
	previousCursors := cloneReminderCursors(c.runtimeCursors)
	acceptedJobs := make(map[string]protocol.ReminderTimerJob)
	for _, job := range jobs {
		if job.OwnerAgentID != agentID || job.ReminderID == "" || job.Version < 1 {
			continue
		}
		if _, err := time.Parse(time.RFC3339, job.FireAt); err != nil {
			continue
		}
		fence, exists := c.fences[job.ReminderID]
		if exists && (fence.LastSeq > watermark || job.Version < fence.Version || (fence.Terminal && job.Version <= fence.Version)) {
			continue
		}
		if pendingVersion, pending := c.pendingFires[job.ReminderID]; pending && pendingVersion == job.Version {
			continue
		}
		acceptedJobs[job.ReminderID] = job
		c.fences[job.ReminderID] = reminderVersionFence{OwnerAgentID: agentID, Version: job.Version, LastSeq: watermark}
	}
	for id, entry := range c.entries {
		if entry.job.OwnerAgentID != agentID {
			continue
		}
		if _, present := acceptedJobs[id]; present {
			continue
		}
		fence := c.fences[id]
		if fence.LastSeq <= watermark {
			fence.OwnerAgentID = agentID
			fence.Terminal = true
			fence.LastSeq = watermark
			c.fences[id] = fence
		}
	}
	c.runtimeCursors[runtimeID] = watermark
	if err := c.persistLocked(); err != nil {
		c.fences = previousFences
		c.runtimeCursors = previousCursors
		return 0, err
	}
	accepted := 0
	for id, entry := range c.entries {
		if entry.job.OwnerAgentID == agentID {
			if entry.timer != nil {
				entry.timer.Stop()
			}
			delete(c.entries, id)
		}
	}
	for _, job := range acceptedJobs {
		fireAt, _ := time.Parse(time.RFC3339, job.FireAt)
		c.entries[job.ReminderID] = reminderCacheEntry{job: job}
		c.armLocked(job, fireAt)
		accepted++
	}
	return accepted, nil
}

func (c *reminderCache) clear() {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, entry := range c.entries {
		if entry.timer != nil {
			entry.timer.Stop()
		}
	}
	clear(c.entries)
	clear(c.pendingFires)
}

func (c *reminderCache) beginConnection() {
	if c == nil {
		return
	}
	c.mu.Lock()
	clear(c.pendingFires)
	c.mu.Unlock()
}

func (c *reminderCache) removeOwner(agentID string) error {
	if c == nil || agentID == "" {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	previous := cloneReminderFences(c.fences)
	previousPending := clonePendingFires(c.pendingFires)
	for id, fence := range c.fences {
		if fence.OwnerAgentID == agentID {
			delete(c.fences, id)
			delete(c.pendingFires, id)
		}
	}
	if err := c.persistLocked(); err != nil {
		c.fences = previous
		c.pendingFires = previousPending
		return err
	}
	for id, entry := range c.entries {
		if entry.job.OwnerAgentID != agentID {
			continue
		}
		if entry.timer != nil {
			entry.timer.Stop()
		}
		delete(c.entries, id)
	}
	return nil
}

func (c *reminderCache) get(reminderID string) (protocol.ReminderTimerJob, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[reminderID]
	return entry.job, ok
}

func (c *reminderCache) armLocked(job protocol.ReminderTimerJob, fireAt time.Time) {
	delay := fireAt.Sub(c.clock.Now())
	if delay < 0 {
		delay = 0
	}
	timer := c.clock.AfterFunc(delay, func() {
		c.mu.Lock()
		current, ok := c.entries[job.ReminderID]
		if !ok || current.job.Version != job.Version {
			c.mu.Unlock()
			return
		}
		delete(c.entries, job.ReminderID)
		c.pendingFires[job.ReminderID] = job.Version
		c.mu.Unlock()
		if c.onFire != nil {
			c.onFire(job)
		}
	})
	entry := c.entries[job.ReminderID]
	entry.timer = timer
	c.entries[job.ReminderID] = entry
}
