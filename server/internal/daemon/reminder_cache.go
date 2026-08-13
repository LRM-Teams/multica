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

const defaultReminderFireRetryDelay = time.Second

var errReminderProjectionGap = errors.New("reminder projection sequence gap")

// reminderDueIdentity is the Raft 1.0.16 due key: owner + reminder + version.
type reminderDueIdentity struct {
	OwnerAgentID string `json:"owner_agent_id"`
	ReminderID   string `json:"reminder_id"`
	Version      int64  `json:"version"`
}

// reminderDueReceipt is the persisted local due fact. It stays until both the
// owner wake is enqueued and the server has acknowledged the same identity.
type reminderDueReceipt struct {
	Job           protocol.ReminderTimerJob `json:"job"`
	FiredAtClient string                    `json:"fired_at_client"`
	Catchup       bool                      `json:"catchup"`
	WakeEnqueued  bool                      `json:"wake_enqueued"`
	ServerAcked   bool                      `json:"server_acked"`
	ItemConsumed  bool                      `json:"item_consumed"`
}

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
	Fences           map[string]reminderVersionFence `json:"fences"`
	RuntimeCursors   map[string]int64                `json:"runtime_cursors"`
	RuntimeResets    map[string]bool                 `json:"runtime_resets,omitempty"`
	RecoveryRequired bool                            `json:"recovery_required,omitempty"`
	Receipts         map[string][]reminderDueReceipt `json:"receipts,omitempty"`
}

// reminderCache is the owner-daemon timer projection. Entries are ephemeral,
// while the per-ID version fences and per-runtime projection cursors are
// persisted together before an event is ACKed. That makes deleted timers
// non-resurrectable without retaining unbounded terminal definitions server-side.
type reminderCache struct {
	mu               sync.Mutex
	entries          map[string]reminderCacheEntry
	fences           map[string]reminderVersionFence
	runtimeCursors   map[string]int64
	runtimeResets    map[string]bool
	recoveryRequired bool
	attemptedFires   map[string]int64
	receipts         map[string][]reminderDueReceipt
	suspended        bool
	clock            reminderClock
	onFire           func(protocol.ReminderTimerJob)
	onFireDelivery   func(protocol.ReminderTimerJob) bool
	fireRetryDelay   time.Duration
	fireRetryTimers  map[string]reminderTimer
	dispatching      map[string]bool
	logger           *slog.Logger
	path             string
	loadErr          error
	writeState       func(string, []byte) error
}

func newReminderCache(clock reminderClock, logger *slog.Logger, onFire func(protocol.ReminderTimerJob)) *reminderCache {
	if clock == nil {
		clock = systemReminderClock{}
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &reminderCache{
		entries:         make(map[string]reminderCacheEntry),
		fences:          make(map[string]reminderVersionFence),
		runtimeCursors:  make(map[string]int64),
		runtimeResets:   make(map[string]bool),
		attemptedFires:  make(map[string]int64),
		receipts:        make(map[string][]reminderDueReceipt),
		fireRetryTimers: make(map[string]reminderTimer),
		dispatching:     make(map[string]bool),
		fireRetryDelay:  defaultReminderFireRetryDelay,
		clock:           clock,
		onFire:          onFire,
		logger:          logger,
		writeState:      writeDaemonStateAtomically,
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
		if errors.Is(err, os.ErrNotExist) {
			quarantined, globErr := filepath.Glob(c.path + ".corrupt-*")
			if globErr != nil {
				c.loadErr = fmt.Errorf("find quarantined reminder cache state: %w", globErr)
			} else if len(quarantined) > 0 {
				c.initializeRecoveryStateLocked(fmt.Errorf("primary reminder cache state missing after quarantine"))
			}
		} else {
			c.recoverCorruptStateLocked(fmt.Errorf("load reminder cache state: %w", err))
		}
		return
	}
	var state reminderCacheState
	if err := json.Unmarshal(raw, &state); err != nil {
		c.recoverCorruptStateLocked(fmt.Errorf("decode reminder cache state: %w", err))
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
	for runtimeID, required := range state.RuntimeResets {
		if runtimeID != "" && required {
			c.runtimeResets[runtimeID] = true
		}
	}
	c.recoveryRequired = state.RecoveryRequired
	if state.Receipts != nil {
		c.receipts = cloneReminderReceipts(state.Receipts)
	}
}

func (c *reminderCache) recoverCorruptStateLocked(cause error) {
	quarantinePath := fmt.Sprintf("%s.corrupt-%d", c.path, time.Now().UTC().UnixNano())
	if err := os.Rename(c.path, quarantinePath); err != nil {
		c.loadErr = fmt.Errorf("quarantine corrupt reminder cache state: %w", err)
		return
	}
	c.initializeRecoveryStateLocked(cause)
}

func (c *reminderCache) initializeRecoveryStateLocked(cause error) {
	clear(c.fences)
	clear(c.runtimeCursors)
	clear(c.runtimeResets)
	clear(c.attemptedFires)
	clear(c.receipts)
	c.recoveryRequired = true
	c.loadErr = nil
	if err := c.persistLocked(); err != nil {
		c.loadErr = fmt.Errorf("persist reminder cache recovery marker: %w", err)
		return
	}
	if c.logger != nil {
		c.logger.Warn("reminder cache canonical reset required", "path", c.path, "error", cause)
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
	state := reminderCacheState{
		Fences: c.fences, RuntimeCursors: c.runtimeCursors, RuntimeResets: c.runtimeResets,
		RecoveryRequired: c.recoveryRequired, Receipts: c.receipts,
	}
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

func cloneAttemptedFires(in map[string]int64) map[string]int64 {
	out := make(map[string]int64, len(in))
	for id, version := range in {
		out[id] = version
	}
	return out
}

func cloneReminderReceipts(in map[string][]reminderDueReceipt) map[string][]reminderDueReceipt {
	out := make(map[string][]reminderDueReceipt, len(in))
	for id, receipts := range in {
		if len(receipts) == 0 {
			continue
		}
		copied := make([]reminderDueReceipt, len(receipts))
		copy(copied, receipts)
		out[id] = copied
	}
	return out
}

func reminderReceiptKey(identity reminderDueIdentity) string {
	return identity.OwnerAgentID + "\x00" + identity.ReminderID + "\x00" + fmt.Sprintf("%d", identity.Version)
}

func receiptIdentity(receipt reminderDueReceipt) reminderDueIdentity {
	return reminderDueIdentity{
		OwnerAgentID: receipt.Job.OwnerAgentID,
		ReminderID:   receipt.Job.ReminderID,
		Version:      receipt.Job.Version,
	}
}

func sameDueIdentity(a, b reminderDueIdentity) bool {
	return a.OwnerAgentID == b.OwnerAgentID && a.ReminderID == b.ReminderID && a.Version == b.Version
}

func findReceiptIndex(receipts []reminderDueReceipt, identity reminderDueIdentity) int {
	for i, receipt := range receipts {
		if sameDueIdentity(receiptIdentity(receipt), identity) {
			return i
		}
	}
	return -1
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
	if event.PrevSeq != c.runtimeCursors[event.RuntimeID] {
		return false, fmt.Errorf("%w: runtime %s has %d, event %d follows %d", errReminderProjectionGap, event.RuntimeID, c.runtimeCursors[event.RuntimeID], event.Seq, event.PrevSeq)
	}
	previousFences := cloneReminderFences(c.fences)
	previousCursors := cloneReminderCursors(c.runtimeCursors)
	previousAttempt, hadAttempted := c.attemptedFires[event.ReminderID]

	fence, exists := c.fences[event.ReminderID]
	apply := false
	if event.Terminal {
		apply = !exists || event.Version >= fence.Version
	} else if !exists || event.Version > fence.Version {
		apply = true
	} else if event.EventType == "fire_result" && event.Version == fence.Version && hadAttempted && previousAttempt == event.Version {
		apply = true
	}
	if apply {
		c.fences[event.ReminderID] = reminderVersionFence{
			OwnerAgentID: event.AgentID, Version: event.Version, LastSeq: event.Seq, Terminal: event.Terminal,
		}
		delete(c.attemptedFires, event.ReminderID)
		if event.EventType == "fire_result" {
			for _, receipt := range append([]reminderDueReceipt(nil), c.receipts[event.ReminderID]...) {
				if receipt.Job.OwnerAgentID == event.AgentID {
					c.ackFireReceiptLocked(receiptIdentity(receipt))
				}
			}
		}
	}
	c.runtimeCursors[event.RuntimeID] = event.Seq
	if err := c.persistLocked(); err != nil {
		c.fences = previousFences
		c.runtimeCursors = previousCursors
		if hadAttempted {
			c.attemptedFires[event.ReminderID] = previousAttempt
		} else {
			delete(c.attemptedFires, event.ReminderID)
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
		if !c.suspended {
			c.armLocked(event.Reminder, fireAt)
		}
	}
	return true, nil
}

// upsert/cancel remain small unit-test helpers. Production frames always use
// applyProjection so sequence and durable ACK fencing cannot be bypassed.
func (c *reminderCache) upsert(job protocol.ReminderTimerJob) bool {
	seq := c.nextTestSeq()
	event := protocol.ReminderProjectionEvent{Seq: seq, PrevSeq: seq - 1, RuntimeID: "test", AgentID: job.OwnerAgentID, EventType: "upsert", ReminderID: job.ReminderID, Version: job.Version, FireAt: job.FireAt, Reminder: job}
	ok, _ := c.applyProjection(event)
	return ok
}

func (c *reminderCache) cancel(reminderID string, version int64) bool {
	fence := c.highWatermark(reminderID)
	seq := c.nextTestSeq()
	event := protocol.ReminderProjectionEvent{Seq: seq, PrevSeq: seq - 1, RuntimeID: "test", AgentID: fence.OwnerAgentID, EventType: "cancel", ReminderID: reminderID, Version: version, Terminal: true}
	if event.AgentID == "" {
		event.AgentID = "test"
	}
	ok, _ := c.applyProjection(event)
	return ok
}

func (c *reminderCache) cancelOwned(reminderID string, version int64, ownerAgentID string) bool {
	if c == nil || reminderID == "" || version < 1 {
		return false
	}
	fence := c.highWatermark(reminderID)
	if fence.OwnerAgentID != "" && ownerAgentID != "" && fence.OwnerAgentID != ownerAgentID {
		return false
	}
	if fence.Version > version {
		return false
	}
	return c.cancel(reminderID, version)
}

func (c *reminderCache) pendingFireReceipts() []reminderDueReceipt {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]reminderDueReceipt, 0)
	for _, receipts := range c.receipts {
		out = append(out, receipts...)
	}
	return out
}

func (c *reminderCache) ackFireReceipt(identity reminderDueIdentity) bool {
	if c == nil {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.ackFireReceiptLocked(identity)
}

func (c *reminderCache) ackFireReceiptLocked(identity reminderDueIdentity) bool {
	receipts := c.receipts[identity.ReminderID]
	index := findReceiptIndex(receipts, identity)
	if index < 0 {
		return false
	}
	receipts[index].ServerAcked = true
	if receipts[index].WakeEnqueued {
		c.clearFireRetryLocked(identity)
		receipts = append(receipts[:index], receipts[index+1:]...)
		if len(receipts) == 0 {
			delete(c.receipts, identity.ReminderID)
		} else {
			c.receipts[identity.ReminderID] = receipts
		}
	} else {
		c.receipts[identity.ReminderID] = receipts
	}
	_ = c.persistLocked()
	return true
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

func (c *reminderCache) projectionReplayState(runtimeIDs []string) (map[string]int64, map[string]bool, error) {
	if c == nil {
		return map[string]int64{}, map[string]bool{}, nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	previousResets := make(map[string]bool, len(c.runtimeResets))
	for runtimeID, required := range c.runtimeResets {
		previousResets[runtimeID] = required
	}
	previousRecoveryRequired := c.recoveryRequired
	if c.recoveryRequired && len(runtimeIDs) > 0 {
		for _, runtimeID := range runtimeIDs {
			if runtimeID != "" {
				c.runtimeResets[runtimeID] = true
			}
		}
		c.recoveryRequired = false
		if err := c.persistLocked(); err != nil {
			c.runtimeResets = previousResets
			c.recoveryRequired = previousRecoveryRequired
			return nil, nil, err
		}
	}
	cursors := make(map[string]int64, len(runtimeIDs))
	resets := make(map[string]bool)
	for _, runtimeID := range runtimeIDs {
		if runtimeID == "" {
			continue
		}
		if c.runtimeResets[runtimeID] {
			cursors[runtimeID] = 0
			resets[runtimeID] = true
			continue
		}
		cursors[runtimeID] = c.runtimeCursors[runtimeID]
	}
	return cursors, resets, nil
}

func (c *reminderCache) requiredRuntimeResets() map[string]bool {
	if c == nil {
		return map[string]bool{}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	resets := make(map[string]bool, len(c.runtimeResets))
	for runtimeID, required := range c.runtimeResets {
		if required {
			resets[runtimeID] = true
		}
	}
	return resets
}

func (c *reminderCache) reconcileRuntimeSet(allowed map[string]bool, retiredOwnerIDs []string) (bool, error) {
	if c == nil {
		return false, nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	retiredOwners := make(map[string]bool, len(retiredOwnerIDs))
	for _, agentID := range retiredOwnerIDs {
		if agentID != "" {
			retiredOwners[agentID] = true
		}
	}
	previousFences := cloneReminderFences(c.fences)
	previousCursors := cloneReminderCursors(c.runtimeCursors)
	previousResets := make(map[string]bool, len(c.runtimeResets))
	for runtimeID, required := range c.runtimeResets {
		previousResets[runtimeID] = required
	}
	previousAttempted := cloneAttemptedFires(c.attemptedFires)
	changed := false
	for reminderID, fence := range c.fences {
		if retiredOwners[fence.OwnerAgentID] {
			delete(c.fences, reminderID)
			delete(c.attemptedFires, reminderID)
			changed = true
		}
	}
	for runtimeID := range c.runtimeCursors {
		if !allowed[runtimeID] {
			delete(c.runtimeCursors, runtimeID)
			changed = true
		}
	}
	for runtimeID := range c.runtimeResets {
		if !allowed[runtimeID] {
			delete(c.runtimeResets, runtimeID)
			changed = true
		}
	}
	for _, entry := range c.entries {
		if retiredOwners[entry.job.OwnerAgentID] {
			changed = true
			break
		}
	}
	if !changed {
		return false, nil
	}
	if err := c.persistLocked(); err != nil {
		c.fences = previousFences
		c.runtimeCursors = previousCursors
		c.runtimeResets = previousResets
		c.attemptedFires = previousAttempted
		return false, err
	}
	for reminderID, entry := range c.entries {
		if !retiredOwners[entry.job.OwnerAgentID] {
			continue
		}
		if entry.timer != nil {
			entry.timer.Stop()
		}
		delete(c.entries, reminderID)
	}
	return true, nil
}

func (c *reminderCache) advanceProjectionCursor(runtimeID string, prevSeq, seq int64) error {
	if c == nil || runtimeID == "" || seq < 1 {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if seq <= c.runtimeCursors[runtimeID] {
		return nil
	}
	if prevSeq != c.runtimeCursors[runtimeID] {
		return fmt.Errorf("%w: runtime %s has %d, event %d follows %d", errReminderProjectionGap, runtimeID, c.runtimeCursors[runtimeID], seq, prevSeq)
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
		// Task #69: this whole snapshot used to be dropped with zero trace.
		// A server watermark behind the daemon's own persisted cursor should
		// be rare (it means the server regressed relative to what this
		// daemon already ACKed) — worth a Warn every time it happens, not
		// silence, since a genuinely stale watermark here means the daemon
		// is about to miss every job in this snapshot.
		if c.logger != nil {
			c.logger.Warn("reminder snapshot rejected: server watermark behind local cursor",
				"runtime_id", runtimeID, "agent_id", agentID, "watermark", watermark, "local_cursor", c.runtimeCursors[runtimeID])
		}
		return 0, nil
	}
	previousFences := cloneReminderFences(c.fences)
	previousAttempted := cloneAttemptedFires(c.attemptedFires)
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
			// Task #69: also silent before. Legitimate most of the time
			// (a stale/superseded job the daemon already knows is done),
			// but silence is exactly what let three separate drop points in
			// this file go unnoticed for 20+ hours — logging one skip is
			// cheap, re-discovering "which of four silent branches did it"
			// from scratch is not.
			if c.logger != nil {
				c.logger.Debug("reminder snapshot skipped job behind local fence",
					"reminder_id", job.ReminderID, "agent_id", agentID, "job_version", job.Version,
					"fence_version", fence.Version, "fence_terminal", fence.Terminal, "fence_last_seq", fence.LastSeq, "watermark", watermark)
			}
			continue
		}
		if attemptedVersion, attempted := c.attemptedFires[job.ReminderID]; attempted && attemptedVersion == job.Version {
			// One connection may attempt a due version only once. A reconnect
			// clears this ephemeral fence so a server-uncommitted version remains
			// recoverable from the next canonical snapshot.
			if c.logger != nil {
				c.logger.Info("reminder snapshot skipped job already attempted on current connection",
					"reminder_id", job.ReminderID, "agent_id", agentID, "version", job.Version)
			}
			continue
		}
		if attemptedVersion, attempted := c.attemptedFires[job.ReminderID]; attempted && job.Version > attemptedVersion {
			delete(c.attemptedFires, job.ReminderID)
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
	if err := c.persistLocked(); err != nil {
		c.fences = previousFences
		c.attemptedFires = previousAttempted
		return 0, err
	}
	accepted := 0
	for id, entry := range c.entries {
		if entry.job.OwnerAgentID != agentID {
			continue
		}
		if entry.timer != nil {
			entry.timer.Stop()
		}
		delete(c.entries, id)
	}
	for _, job := range acceptedJobs {
		fireAt, _ := time.Parse(time.RFC3339, job.FireAt)
		c.entries[job.ReminderID] = reminderCacheEntry{job: job}
		if !c.suspended {
			c.armLocked(job, fireAt)
		}
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
	c.suspended = true
	clear(c.entries)
	clear(c.attemptedFires)
}

func (c *reminderCache) beginConnection() {
	if c == nil {
		return
	}
	c.mu.Lock()
	for _, entry := range c.entries {
		if entry.timer != nil {
			entry.timer.Stop()
		}
	}
	c.suspended = true
	clear(c.entries)
	clear(c.attemptedFires)
	c.mu.Unlock()
}

func (c *reminderCache) suspend() {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for id, entry := range c.entries {
		if entry.timer != nil {
			entry.timer.Stop()
			entry.timer = nil
			c.entries[id] = entry
		}
	}
	c.suspended = true
}

func (c *reminderCache) markRuntimeReset(runtimeID string) error {
	if c == nil || runtimeID == "" {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.runtimeResets[runtimeID] {
		return nil
	}
	previous := c.runtimeResets[runtimeID]
	c.runtimeResets[runtimeID] = true
	if err := c.persistLocked(); err != nil {
		if previous {
			c.runtimeResets[runtimeID] = true
		} else {
			delete(c.runtimeResets, runtimeID)
		}
		return err
	}
	return nil
}

func (c *reminderCache) resetRuntime(runtimeID string, reset protocol.ReminderRuntimeReset) error {
	if c == nil || runtimeID == "" || reset.ProjectionWatermark < 0 {
		return nil
	}
	ownerIDs := make(map[string]bool, len(reset.Owners))
	jobs := make([]protocol.ReminderTimerJob, 0)
	for _, owner := range reset.Owners {
		if owner.AgentID == "" || owner.PlacementGeneration < 1 {
			return errors.New("invalid reminder runtime reset owner")
		}
		ownerIDs[owner.AgentID] = true
		if owner.Terminal && len(owner.Reminders) > 0 {
			return errors.New("terminal reminder runtime reset owner has jobs")
		}
		for _, job := range owner.Reminders {
			if job.OwnerAgentID != owner.AgentID || job.ReminderID == "" || job.Version < 1 {
				return errors.New("invalid reminder runtime reset job")
			}
			if _, err := time.Parse(time.RFC3339, job.FireAt); err != nil {
				return err
			}
			jobs = append(jobs, job)
		}
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	previousFences := cloneReminderFences(c.fences)
	previousCursors := cloneReminderCursors(c.runtimeCursors)
	previousResets := make(map[string]bool, len(c.runtimeResets))
	for id, required := range c.runtimeResets {
		previousResets[id] = required
	}
	previousAttempted := cloneAttemptedFires(c.attemptedFires)
	for id, fence := range c.fences {
		if ownerIDs[fence.OwnerAgentID] {
			delete(c.fences, id)
			delete(c.attemptedFires, id)
		}
	}
	for _, job := range jobs {
		c.fences[job.ReminderID] = reminderVersionFence{
			OwnerAgentID: job.OwnerAgentID, Version: job.Version, LastSeq: reset.ProjectionWatermark,
		}
	}
	c.runtimeCursors[runtimeID] = reset.ProjectionWatermark
	delete(c.runtimeResets, runtimeID)
	if err := c.persistLocked(); err != nil {
		c.fences = previousFences
		c.runtimeCursors = previousCursors
		c.runtimeResets = previousResets
		c.attemptedFires = previousAttempted
		return err
	}
	for id, entry := range c.entries {
		if !ownerIDs[entry.job.OwnerAgentID] {
			continue
		}
		if entry.timer != nil {
			entry.timer.Stop()
		}
		delete(c.entries, id)
	}
	for _, job := range jobs {
		fireAt, _ := time.Parse(time.RFC3339, job.FireAt)
		c.entries[job.ReminderID] = reminderCacheEntry{job: job}
		if !c.suspended {
			c.armLocked(job, fireAt)
		}
	}
	return nil
}

func (c *reminderCache) resume() {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.suspended {
		return
	}
	c.suspended = false
	for _, entry := range c.entries {
		fireAt, err := time.Parse(time.RFC3339, entry.job.FireAt)
		if err == nil {
			c.armLocked(entry.job, fireAt)
		}
	}
}

func (c *reminderCache) removeOwner(agentID string) error {
	if c == nil || agentID == "" {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	previous := cloneReminderFences(c.fences)
	previousAttempted := cloneAttemptedFires(c.attemptedFires)
	for id, fence := range c.fences {
		if fence.OwnerAgentID == agentID {
			delete(c.fences, id)
			delete(c.attemptedFires, id)
		}
	}
	if err := c.persistLocked(); err != nil {
		c.fences = previous
		c.attemptedFires = previousAttempted
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
	timer := c.clock.AfterFunc(delay, func() { c.attemptFireOnce(job) })
	entry := c.entries[job.ReminderID]
	entry.timer = timer
	c.entries[job.ReminderID] = entry
	// Task #69: whether a timer ever actually got armed for a given
	// reminder was previously answerable nowhere — not in the persisted
	// fence (which only records confirmed projection state, not local
	// timer state) and not in any debug/introspection endpoint (none
	// exists). This is the only place in the codebase that arms a timer.
	if c.logger != nil {
		c.logger.Debug("reminder timer armed", "reminder_id", job.ReminderID, "agent_id", job.OwnerAgentID, "version", job.Version, "delay", delay)
	}
}

// attemptFireOnce removes the due timer, persists a local due receipt, then
// dispatches the owner wake. The receipt stays until wake is enqueued and the
// server acknowledges the same identity. The in-memory attempt fence still
// prevents a second timer from being armed for this version on the current
// connection; reconnect recovery is the snapshot of the persisted receipt.
func (c *reminderCache) attemptFireOnce(job protocol.ReminderTimerJob) {
	c.mu.Lock()
	current, ok := c.entries[job.ReminderID]
	if !ok || current.job.Version != job.Version {
		c.mu.Unlock()
		return
	}
	delete(c.entries, job.ReminderID)
	c.attemptedFires[job.ReminderID] = job.Version
	fireAt, _ := time.Parse(time.RFC3339, job.FireAt)
	receipt := reminderDueReceipt{
		Job:           job,
		FiredAtClient: c.clock.Now().UTC().Format(time.RFC3339Nano),
		Catchup:       fireAt.Before(c.clock.Now()),
	}
	identity := receiptIdentity(receipt)
	if findReceiptIndex(c.receipts[job.ReminderID], identity) < 0 {
		c.receipts[job.ReminderID] = append(c.receipts[job.ReminderID], receipt)
		_ = c.persistLocked()
	} else {
		receipt = c.receipts[job.ReminderID][findReceiptIndex(c.receipts[job.ReminderID], identity)]
	}
	c.mu.Unlock()
	c.dispatchFire(receipt)
}

func (c *reminderCache) deliverFire(job protocol.ReminderTimerJob) bool {
	if c.onFireDelivery != nil {
		return c.onFireDelivery(job)
	}
	if c.onFire != nil {
		c.onFire(job)
		return true
	}
	return false
}

func (c *reminderCache) dispatchFire(receipt reminderDueReceipt) {
	identity := receiptIdentity(receipt)
	key := reminderReceiptKey(identity)
	c.mu.Lock()
	if c.dispatching[key] {
		c.mu.Unlock()
		return
	}
	c.dispatching[key] = true
	c.clearFireRetryLocked(identity)
	c.mu.Unlock()

	wakeEnqueued := false
	func() {
		defer func() {
			if recovered := recover(); recovered != nil && c.logger != nil {
				c.logger.Error("reminder onFire panicked", "reminder_id", receipt.Job.ReminderID, "error", recovered)
			}
		}()
		wakeEnqueued = c.deliverFire(receipt.Job)
	}()

	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.dispatching, key)
	index := findReceiptIndex(c.receipts[receipt.Job.ReminderID], identity)
	if index < 0 {
		return
	}
	pending := c.receipts[receipt.Job.ReminderID]
	if !wakeEnqueued {
		c.scheduleFireRetryLocked(pending[index])
		return
	}
	pending[index].WakeEnqueued = true
	if pending[index].ServerAcked {
		c.clearFireRetryLocked(identity)
		pending = append(pending[:index], pending[index+1:]...)
		if len(pending) == 0 {
			delete(c.receipts, receipt.Job.ReminderID)
		} else {
			c.receipts[receipt.Job.ReminderID] = pending
		}
	} else {
		c.receipts[receipt.Job.ReminderID] = pending
	}
	_ = c.persistLocked()
}

func (c *reminderCache) scheduleFireRetryLocked(receipt reminderDueReceipt) {
	if receipt.WakeEnqueued {
		return
	}
	identity := receiptIdentity(receipt)
	if findReceiptIndex(c.receipts[receipt.Job.ReminderID], identity) < 0 {
		return
	}
	key := reminderReceiptKey(identity)
	if _, exists := c.fireRetryTimers[key]; exists {
		return
	}
	delay := c.fireRetryDelay
	if delay <= 0 {
		delay = defaultReminderFireRetryDelay
	}
	timer := c.clock.AfterFunc(delay, func() {
		c.mu.Lock()
		if current, ok := c.fireRetryTimers[key]; ok {
			delete(c.fireRetryTimers, key)
			_ = current
		}
		index := findReceiptIndex(c.receipts[receipt.Job.ReminderID], identity)
		var pending reminderDueReceipt
		if index >= 0 {
			pending = c.receipts[receipt.Job.ReminderID][index]
		}
		c.mu.Unlock()
		if index >= 0 {
			c.dispatchFire(pending)
		}
	})
	c.fireRetryTimers[key] = timer
}

func (c *reminderCache) clearFireRetryLocked(identity reminderDueIdentity) {
	key := reminderReceiptKey(identity)
	if timer, ok := c.fireRetryTimers[key]; ok {
		timer.Stop()
		delete(c.fireRetryTimers, key)
	}
}
