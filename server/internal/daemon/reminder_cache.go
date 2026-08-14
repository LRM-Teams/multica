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

// reminderDueIdentity is the Raft 1.0.16 due key: owner + reminder + version.
type reminderDueIdentity struct {
	OwnerAgentID string `json:"owner_agent_id"`
	ReminderID   string `json:"reminder_id"`
	Version      int64  `json:"version"`
}

// reminderDueReceipt is the persisted local due fact. It stays until both the
// owner wake is enqueued and the server has acknowledged the same identity.
type reminderDueReceipt struct {
	RuntimeID     string                    `json:"runtime_id,omitempty"`
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
	runtimeID string
	job       protocol.ReminderTimerJob
	timer     reminderTimer
}

type reminderVersionFence struct {
	OwnerAgentID string `json:"owner_agent_id"`
	Version      int64  `json:"version"`
	Terminal     bool   `json:"terminal"`
}

type reminderCacheState struct {
	Fences   map[string]reminderVersionFence `json:"fences"`
	Receipts map[string][]reminderDueReceipt `json:"receipts,omitempty"`
}

// reminderCache is the Computer-local Raft-shaped timer cache. A reconnect
// replaces one Runtime from a full snapshot; per-Reminder versions fence
// duplicate and delayed live upserts/cancels.
type reminderCache struct {
	mu              sync.Mutex
	entries         map[string]reminderCacheEntry
	fences          map[string]reminderVersionFence
	attemptedFires  map[string]int64
	receipts        map[string][]reminderDueReceipt
	suspended       bool
	clock           reminderClock
	onFire          func(protocol.ReminderTimerJob)
	onFireDelivery  func(protocol.ReminderTimerJob) bool
	onFireReceipt   func(reminderDueReceipt) bool
	fireRetryDelay  time.Duration
	fireRetryTimers map[string]reminderTimer
	dispatching     map[string]bool
	logger          *slog.Logger
	path            string
	loadErr         error
	writeState      func(string, []byte) error
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
				c.initializeEmptyStateLocked(fmt.Errorf("primary reminder cache state missing after quarantine"))
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
	c.initializeEmptyStateLocked(cause)
}

func (c *reminderCache) initializeEmptyStateLocked(cause error) {
	clear(c.fences)
	clear(c.attemptedFires)
	clear(c.receipts)
	c.loadErr = nil
	if err := c.persistLocked(); err != nil {
		c.loadErr = fmt.Errorf("persist empty reminder cache state: %w", err)
		return
	}
	if c.logger != nil {
		c.logger.Warn("reminder cache state reset; reconnect snapshot required", "path", c.path, "error", cause)
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
	state := reminderCacheState{Fences: c.fences, Receipts: c.receipts}
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

func (c *reminderCache) upsert(job protocol.ReminderTimerJob) bool {
	ok, _ := c.upsertForRuntime("test", job)
	return ok
}

func (c *reminderCache) upsertForRuntime(runtimeID string, job protocol.ReminderTimerJob) (bool, error) {
	if c == nil || runtimeID == "" || job.OwnerAgentID == "" || job.ReminderID == "" || job.Version < 1 {
		return false, nil
	}
	fireAt, err := time.Parse(time.RFC3339, job.FireAt)
	if err != nil {
		return false, nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	fence, exists := c.fences[job.ReminderID]
	if exists && (job.Version < fence.Version || (job.Version == fence.Version && !fence.Terminal)) {
		return false, nil
	}
	previousFences := cloneReminderFences(c.fences)
	c.fences[job.ReminderID] = reminderVersionFence{OwnerAgentID: job.OwnerAgentID, Version: job.Version}
	delete(c.attemptedFires, job.ReminderID)
	if err := c.persistLocked(); err != nil {
		c.fences = previousFences
		return false, err
	}
	if current, ok := c.entries[job.ReminderID]; ok && current.timer != nil {
		current.timer.Stop()
	}
	c.entries[job.ReminderID] = reminderCacheEntry{runtimeID: runtimeID, job: job}
	if !c.suspended {
		c.armLocked(job, fireAt)
	}
	return true, nil
}

func (c *reminderCache) cancel(reminderID string, version int64) bool {
	fence := c.highWatermark(reminderID)
	ok, _ := c.cancelForRuntime("test", fence.OwnerAgentID, reminderID, version)
	return ok
}

func (c *reminderCache) cancelForRuntime(runtimeID, ownerAgentID, reminderID string, version int64) (bool, error) {
	if c == nil || runtimeID == "" || reminderID == "" || version < 1 {
		return false, nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	fence, exists := c.fences[reminderID]
	if exists && (version < fence.Version || (ownerAgentID != "" && fence.OwnerAgentID != "" && ownerAgentID != fence.OwnerAgentID)) {
		return false, nil
	}
	if ownerAgentID == "" {
		ownerAgentID = fence.OwnerAgentID
	}
	previousFences := cloneReminderFences(c.fences)
	c.fences[reminderID] = reminderVersionFence{OwnerAgentID: ownerAgentID, Version: version, Terminal: true}
	delete(c.attemptedFires, reminderID)
	if err := c.persistLocked(); err != nil {
		c.fences = previousFences
		return false, err
	}
	if current, ok := c.entries[reminderID]; ok && current.timer != nil {
		current.timer.Stop()
	}
	delete(c.entries, reminderID)
	return true, nil
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

func (c *reminderCache) replaceRuntime(runtimeID string, jobs []protocol.ReminderTimerJob) error {
	if c == nil || runtimeID == "" {
		return nil
	}
	incoming := make(map[string]protocol.ReminderTimerJob, len(jobs))
	for _, job := range jobs {
		if job.OwnerAgentID == "" || job.ReminderID == "" || job.Version < 1 {
			return errors.New("invalid reminder snapshot job")
		}
		if _, err := time.Parse(time.RFC3339, job.FireAt); err != nil {
			return err
		}
		incoming[job.ReminderID] = job
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	previousFences := cloneReminderFences(c.fences)
	previousReceipts := cloneReminderReceipts(c.receipts)
	for id, entry := range c.entries {
		if entry.runtimeID != runtimeID {
			continue
		}
		if _, present := incoming[id]; !present {
			fence := c.fences[id]
			fence.OwnerAgentID = entry.job.OwnerAgentID
			fence.Version = entry.job.Version
			fence.Terminal = true
			c.fences[id] = fence
		}
	}
	for _, job := range incoming {
		c.fences[job.ReminderID] = reminderVersionFence{OwnerAgentID: job.OwnerAgentID, Version: job.Version}
		identity := reminderDueIdentity{OwnerAgentID: job.OwnerAgentID, ReminderID: job.ReminderID, Version: job.Version}
		if index := findReceiptIndex(c.receipts[job.ReminderID], identity); index >= 0 {
			receipts := c.receipts[job.ReminderID]
			receipts[index].RuntimeID = runtimeID
			c.receipts[job.ReminderID] = receipts
		}
	}
	if err := c.persistLocked(); err != nil {
		c.fences = previousFences
		c.receipts = previousReceipts
		return err
	}
	for id, entry := range c.entries {
		if entry.runtimeID != runtimeID {
			continue
		}
		if entry.timer != nil {
			entry.timer.Stop()
		}
		delete(c.entries, id)
	}
	for _, job := range incoming {
		identity := reminderDueIdentity{OwnerAgentID: job.OwnerAgentID, ReminderID: job.ReminderID, Version: job.Version}
		if findReceiptIndex(c.receipts[job.ReminderID], identity) >= 0 {
			continue
		}
		fireAt, _ := time.Parse(time.RFC3339, job.FireAt)
		c.entries[job.ReminderID] = reminderCacheEntry{runtimeID: runtimeID, job: job}
		if !c.suspended {
			c.armLocked(job, fireAt)
		}
	}
	return nil
}

func (c *reminderCache) runtimeFor(job protocol.ReminderTimerJob) (string, bool) {
	if c == nil {
		return "", false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[job.ReminderID]
	if ok && entry.job.OwnerAgentID == job.OwnerAgentID && entry.job.Version == job.Version && entry.runtimeID != "" {
		return entry.runtimeID, true
	}
	identity := reminderDueIdentity{OwnerAgentID: job.OwnerAgentID, ReminderID: job.ReminderID, Version: job.Version}
	if index := findReceiptIndex(c.receipts[job.ReminderID], identity); index >= 0 {
		runtimeID := c.receipts[job.ReminderID][index].RuntimeID
		return runtimeID, runtimeID != ""
	}
	return "", false
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

func (c *reminderCache) highWatermark(reminderID string) reminderVersionFence {
	if c == nil {
		return reminderVersionFence{}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.fences[reminderID]
}

func (c *reminderCache) reconcileRuntimeSet(allowed map[string]bool) (bool, error) {
	if c == nil {
		return false, nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	changed := false
	for id, entry := range c.entries {
		if allowed[entry.runtimeID] {
			continue
		}
		if entry.timer != nil {
			entry.timer.Stop()
		}
		delete(c.entries, id)
		fence := c.fences[id]
		fence.OwnerAgentID = entry.job.OwnerAgentID
		fence.Version = entry.job.Version
		fence.Terminal = true
		c.fences[id] = fence
		changed = true
	}
	if changed {
		if err := c.persistLocked(); err != nil {
			return false, err
		}
	}
	return changed, nil
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

func (c *reminderCache) resume() {
	if c == nil {
		return
	}
	c.mu.Lock()
	if !c.suspended {
		c.mu.Unlock()
		return
	}
	c.suspended = false
	for _, entry := range c.entries {
		fireAt, err := time.Parse(time.RFC3339, entry.job.FireAt)
		if err == nil {
			c.armLocked(entry.job, fireAt)
		}
	}
	receipts := make([]reminderDueReceipt, 0)
	for _, pending := range c.receipts {
		receipts = append(receipts, pending...)
	}
	c.mu.Unlock()
	for _, receipt := range receipts {
		c.dispatchFire(receipt)
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
	// This is the only place in the codebase that arms a timer.
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
		RuntimeID:     current.runtimeID,
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

	// A successfully accepted local Inbox item is never injected twice. The
	// durable receipt may still be replayed until the server acknowledges it.
	if receipt.WakeEnqueued {
		if !receipt.ServerAcked && c.onFireReceipt != nil {
			c.deliverFireReceipt(receipt)
		}
		c.mu.Lock()
		delete(c.dispatching, key)
		c.mu.Unlock()
		return
	}

	wakeEnqueued := false
	func() {
		defer func() {
			if recovered := recover(); recovered != nil && c.logger != nil {
				c.logger.Error("reminder onFire panicked", "reminder_id", receipt.Job.ReminderID, "error", recovered)
			}
		}()
		wakeEnqueued = c.deliverFire(receipt.Job)
	}()
	if !receipt.ServerAcked && c.onFireReceipt != nil {
		c.deliverFireReceipt(receipt)
	}
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

func (c *reminderCache) deliverFireReceipt(receipt reminderDueReceipt) (queued bool) {
	defer func() {
		if recovered := recover(); recovered != nil && c.logger != nil {
			c.logger.Error("reminder fire receipt panicked", "reminder_id", receipt.Job.ReminderID, "error", recovered)
		}
	}()
	return c.onFireReceipt(receipt)
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

func writeDaemonStateAtomically(path string, raw []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpPath)
		}
	}()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(raw); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return err
	}
	cleanup = false
	return nil
}
