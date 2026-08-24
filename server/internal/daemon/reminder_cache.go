package daemon

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

const (
	reminderCacheStateFile    = "state.json"
	reminderCacheStateVersion = 6
)

const (
	defaultReminderFireRetryDelay       = time.Second
	defaultReminderFireRetryMaxDelay    = 60 * time.Second
	defaultReminderFireRetryMaxAttempts = 8
	defaultReminderFireRetryDeadline    = 15 * time.Minute

	reminderDeliveryRetryExhaustedCode = "REMINDER_DELIVERY_RETRY_EXHAUSTED"
)

// reminderDueIdentity is the durable due key: owner + reminder + version.
type reminderDueIdentity struct {
	OwnerAgentID string `json:"ownerAgentId"`
	ReminderID   string `json:"reminderId"`
	Version      int64  `json:"version"`
}

// reminderRetryExhaustion is the REMINDER_DELIVERY_RETRY_EXHAUSTED terminal.
// A due receipt keeps this fact until wake+ack converge or the
// occurrence is replaced; it must not 1s-retry after the budget is spent.
type reminderRetryExhaustion struct {
	Code         string `json:"code"`
	Stage        string `json:"stage"`
	OwnerAgentID string `json:"ownerAgentId"`
	ReminderID   string `json:"reminderId"`
	Version      int64  `json:"version"`
	Attempts     int    `json:"attempts"`
	DeadlineAt   string `json:"deadlineAt"`
	ExhaustedAt  string `json:"exhaustedAt"`
}

// reminderDueReceipt is the persisted local due fact. It stays until the
// server authorized the fire, the app-item wake was enqueued, and the exact
// app item was consumed.
type reminderDueReceipt struct {
	RuntimeID          string                    `json:"runtimeId,omitempty"`
	Job                protocol.ReminderTimerJob `json:"job"`
	RequestID          string                    `json:"requestId"`
	FiredAtClient      string                    `json:"firedAtClient"`
	Catchup            bool                      `json:"catchup"`
	WakeEnqueued       bool                      `json:"wakeEnqueued"`
	ServerAcked        bool                      `json:"serverAcked"`
	ServerFired        bool                      `json:"serverFired"`
	ItemConsumed       bool                      `json:"itemConsumed"`
	RetryAttempt       int                       `json:"retryAttempt"`
	RetryNextAttemptAt string                    `json:"retryNextAttemptAt,omitempty"`
	RetryDeadlineAt    string                    `json:"retryDeadlineAt,omitempty"`
	RetryTerminal      *reminderRetryExhaustion  `json:"retryTerminal,omitempty"`
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
	OwnerAgentID string `json:"ownerAgentId"`
	Version      int64  `json:"version"`
	Terminal     bool   `json:"terminal"`
}

type reminderCacheState struct {
	Version  int                             `json:"version"`
	Fences   map[string]reminderVersionFence `json:"fences"`
	Receipts map[string][]reminderDueReceipt `json:"receipts"`
}

// reminderCache is the Computer-local Raft-shaped timer cache. A reconnect
// replaces one Runtime from a full snapshot; per-Reminder versions fence
// duplicate and delayed live upserts/cancels.
type reminderCache struct {
	mu                   sync.Mutex
	entries              map[string]reminderCacheEntry
	fences               map[string]reminderVersionFence
	attemptedFires       map[string]int64
	receipts             map[string][]reminderDueReceipt
	suspended            bool
	clock                reminderClock
	onFire               func(protocol.ReminderTimerJob)
	onFireDelivery       func(protocol.ReminderTimerJob) bool
	onFireReceipt        func(reminderDueReceipt) bool
	onRetryExhausted     func(reminderRetryExhaustion)
	fireRetryDelay       time.Duration
	fireRetryMaxDelay    time.Duration
	fireRetryMaxAttempts int
	fireRetryDeadline    time.Duration
	fireRetryTimers      map[string]reminderTimer
	dispatching          map[string]bool
	logger               *slog.Logger
	storageRoot          string
	storageAgents        map[string]struct{}
	loadErr              error
	writeState           func(string, []byte) error
}

func newReminderCache(clock reminderClock, logger *slog.Logger, onFire func(protocol.ReminderTimerJob)) *reminderCache {
	if clock == nil {
		clock = systemReminderClock{}
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &reminderCache{
		entries:              make(map[string]reminderCacheEntry),
		fences:               make(map[string]reminderVersionFence),
		attemptedFires:       make(map[string]int64),
		receipts:             make(map[string][]reminderDueReceipt),
		fireRetryTimers:      make(map[string]reminderTimer),
		dispatching:          make(map[string]bool),
		storageAgents:        make(map[string]struct{}),
		fireRetryDelay:       defaultReminderFireRetryDelay,
		fireRetryMaxDelay:    defaultReminderFireRetryMaxDelay,
		fireRetryMaxAttempts: defaultReminderFireRetryMaxAttempts,
		fireRetryDeadline:    defaultReminderFireRetryDeadline,
		clock:                clock,
		onFire:               onFire,
		logger:               logger,
		writeState:           writeDaemonStateAtomically,
	}
}

func (c *reminderCache) setPersistence(root string) {
	if c == nil || root == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.storageRoot = root
	entries, err := os.ReadDir(root)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return
		}
		c.loadErr = fmt.Errorf("read Reminder App storage: %w", err)
		return
	}
	for _, entry := range entries {
		if !entry.IsDir() || !safeAppStorageSegment(entry.Name()) {
			continue
		}
		agentID := entry.Name()
		path := filepath.Join(root, agentID, reminderCacheStateFile)
		raw, readErr := osReadFile(path)
		if errors.Is(readErr, os.ErrNotExist) {
			continue
		}
		if readErr != nil {
			c.loadErr = fmt.Errorf("read Reminder App state for Agent %s: %w", agentID, readErr)
			return
		}
		var state reminderCacheState
		if decodeErr := json.Unmarshal(raw, &state); decodeErr != nil || state.Version != reminderCacheStateVersion || state.Fences == nil || state.Receipts == nil {
			c.loadErr = fmt.Errorf("invalid Reminder App state for Agent %s", agentID)
			return
		}
		for reminderID, fence := range state.Fences {
			if fence.OwnerAgentID != agentID || fence.Version <= 0 {
				c.loadErr = fmt.Errorf("Reminder App fence owner mismatch for Agent %s", agentID)
				return
			}
			c.fences[reminderID] = fence
		}
		for reminderID, receipts := range state.Receipts {
			for _, receipt := range receipts {
				if receipt.Job.OwnerAgentID != agentID || receipt.Job.ReminderID != reminderID {
					c.loadErr = fmt.Errorf("Reminder App state owner mismatch for Agent %s", agentID)
					return
				}
			}
			c.receipts[reminderID] = append(c.receipts[reminderID], receipts...)
		}
		c.storageAgents[agentID] = struct{}{}
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
	if c.storageRoot == "" {
		return nil
	}
	states := make(map[string]*reminderCacheState)
	stateForAgent := func(agentID string) *reminderCacheState {
		state := states[agentID]
		if state == nil {
			state = &reminderCacheState{
				Version: reminderCacheStateVersion,
				Fences:  make(map[string]reminderVersionFence), Receipts: make(map[string][]reminderDueReceipt),
			}
			states[agentID] = state
		}
		return state
	}
	for reminderID, fence := range c.fences {
		stateForAgent(fence.OwnerAgentID).Fences[reminderID] = fence
		c.storageAgents[fence.OwnerAgentID] = struct{}{}
	}
	for reminderID, receipts := range c.receipts {
		for _, receipt := range receipts {
			agentID := receipt.Job.OwnerAgentID
			state := stateForAgent(agentID)
			state.Receipts[reminderID] = append(state.Receipts[reminderID], receipt)
			c.storageAgents[agentID] = struct{}{}
		}
	}
	if c.writeState == nil {
		return errors.New("reminder cache state writer is not configured")
	}
	for agentID := range c.storageAgents {
		raw, err := json.Marshal(stateForAgent(agentID))
		if err != nil {
			return err
		}
		if err := c.writeState(filepath.Join(c.storageRoot, agentID, reminderCacheStateFile), append(raw, '\n')); err != nil {
			return err
		}
	}
	return nil
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
	if reminderReceiptComplete(receipts[index]) {
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

func (c *reminderCache) acceptFireRequest(identity reminderDueIdentity, requestID string, fired, catchup bool) bool {
	if c == nil {
		return false
	}
	c.mu.Lock()
	receipts := c.receipts[identity.ReminderID]
	index := findReceiptIndex(receipts, identity)
	if index < 0 || receipts[index].RequestID != requestID {
		c.mu.Unlock()
		return false
	}
	receipts[index].ServerAcked = true
	receipts[index].ServerFired = fired
	receipts[index].Catchup = catchup
	c.clearFireRetryLocked(identity)
	if !fired {
		receipts = append(receipts[:index], receipts[index+1:]...)
		if len(receipts) == 0 {
			delete(c.receipts, identity.ReminderID)
		} else {
			c.receipts[identity.ReminderID] = receipts
		}
		_ = c.persistLocked()
		c.mu.Unlock()
		return true
	}
	c.receipts[identity.ReminderID] = receipts
	_ = c.persistLocked()
	receipt := receipts[index]
	c.mu.Unlock()
	c.dispatchFire(receipt)
	return true
}

func (c *reminderCache) discardFireRequest(identity reminderDueIdentity, requestID string) bool {
	if c == nil {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	receipts := c.receipts[identity.ReminderID]
	index := findReceiptIndex(receipts, identity)
	if index < 0 || receipts[index].RequestID != requestID {
		return false
	}
	c.clearFireRetryLocked(identity)
	receipts = append(receipts[:index], receipts[index+1:]...)
	if len(receipts) == 0 {
		delete(c.receipts, identity.ReminderID)
	} else {
		c.receipts[identity.ReminderID] = receipts
	}
	_ = c.persistLocked()
	return true
}

func (c *reminderCache) rearmFireRequest(identity reminderDueIdentity, requestID string, retryAfter time.Duration) bool {
	if c == nil || retryAfter <= 0 {
		return false
	}
	c.mu.Lock()
	receipts := c.receipts[identity.ReminderID]
	index := findReceiptIndex(receipts, identity)
	if index < 0 || receipts[index].RequestID != requestID {
		c.mu.Unlock()
		return false
	}
	receipt := receipts[index]
	c.clearFireRetryLocked(identity)
	receipts = append(receipts[:index], receipts[index+1:]...)
	if len(receipts) == 0 {
		delete(c.receipts, identity.ReminderID)
	} else {
		c.receipts[identity.ReminderID] = receipts
	}
	if fence := c.fences[identity.ReminderID]; fence.OwnerAgentID == identity.OwnerAgentID && fence.Version == identity.Version && !fence.Terminal {
		entry := reminderCacheEntry{runtimeID: receipt.RuntimeID, job: receipt.Job}
		entry.timer = c.clock.AfterFunc(retryAfter, func() { c.attemptFireOnce(receipt.Job) })
		c.entries[identity.ReminderID] = entry
		delete(c.attemptedFires, identity.ReminderID)
	}
	_ = c.persistLocked()
	c.mu.Unlock()
	return true
}

func (c *reminderCache) consumeFireReceipt(identity reminderDueIdentity) bool {
	if c == nil {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	receipts := c.receipts[identity.ReminderID]
	index := findReceiptIndex(receipts, identity)
	if index < 0 {
		fence, ok := c.fences[identity.ReminderID]
		return ok && fence.OwnerAgentID == identity.OwnerAgentID && fence.Version == identity.Version
	}
	previous := append([]reminderDueReceipt(nil), receipts...)
	next := append([]reminderDueReceipt(nil), receipts...)
	next[index].ItemConsumed = true
	complete := reminderReceiptComplete(next[index])
	if complete {
		next = append(next[:index], next[index+1:]...)
		if len(next) == 0 {
			delete(c.receipts, identity.ReminderID)
		} else {
			c.receipts[identity.ReminderID] = next
		}
	} else {
		c.receipts[identity.ReminderID] = next
	}
	if err := c.persistLocked(); err != nil {
		c.receipts[identity.ReminderID] = previous
		return false
	}
	if complete {
		c.clearFireRetryLocked(identity)
	}
	return true
}

func reminderReceiptComplete(receipt reminderDueReceipt) bool {
	return receipt.ServerAcked && receipt.WakeEnqueued && receipt.ItemConsumed
}

// materializeReminderFire projects one authorized Reminder due fact into the
// generic per-Agent App Inbox. It never injects Reminder content into a
// provider runtime or advances Message coverage.
func (d *Daemon) materializeReminderFire(job protocol.ReminderTimerJob) bool {
	if d == nil || d.agentAppInboxes == nil || strings.TrimSpace(job.Title) == "" {
		return false
	}
	runtimeID, ok := d.reminderCache.runtimeFor(job)
	if !ok {
		return false
	}
	store, err := d.agentAppInboxes.Store(job.OwnerAgentID)
	if err != nil {
		if d.logger != nil {
			d.logger.Error("open Agent app Inbox", "agent_id", job.OwnerAgentID, "error", err)
		}
		return false
	}
	summary := "Reminder due"
	if fireAt, parseErr := time.Parse(time.RFC3339, job.FireAt); parseErr == nil && fireAt.Before(time.Now()) {
		summary = "Overdue reminder recovered locally"
	}
	_, err = store.Mint(AgentAppInboxMintInput{
		AppID: reminderInboxAppID, NotificationClass: reminderDueClass,
		SourceRef: AgentAppInboxSourceRef{Kind: "reminder", ID: job.ReminderID, Revision: fmt.Sprint(job.Version)},
		Title:     projectReminderInboxTitle(job.Title), Summary: summary,
	})
	if err != nil && d.logger != nil {
		d.logger.Error("mint Reminder app Inbox item", "agent_id", job.OwnerAgentID, "reminder_id", job.ReminderID, "version", job.Version, "error", err)
	}
	return err == nil && d.enqueueAgentAppInboxNotice(job.OwnerAgentID, runtimeID)
}

func projectReminderInboxTitle(title string) string {
	normalized := strings.Join(strings.Fields(title), " ")
	runes := []rune(normalized)
	if len(runes) > agentAppInboxPreviewLimit {
		runes = runes[:agentAppInboxPreviewLimit]
	}
	return string(runes)
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
	now := c.clock.Now()
	receipt := reminderDueReceipt{
		RuntimeID:       current.runtimeID,
		Job:             job,
		RequestID:       uuid.NewString(),
		FiredAtClient:   now.UTC().Format(time.RFC3339Nano),
		Catchup:         fireAt.Before(now),
		RetryDeadlineAt: now.Add(c.retryDeadline()).UTC().Format(time.RFC3339Nano),
	}
	identity := receiptIdentity(receipt)
	existingReceipts := c.receipts[job.ReminderID]
	if findReceiptIndex(existingReceipts, identity) < 0 {
		c.receipts[job.ReminderID] = append(existingReceipts, receipt)
		if err := c.persistLocked(); err != nil {
			if len(existingReceipts) == 0 {
				delete(c.receipts, job.ReminderID)
			} else {
				c.receipts[job.ReminderID] = existingReceipts
			}
			delete(c.attemptedFires, job.ReminderID)
			current.timer = c.clock.AfterFunc(c.retryDelay(), func() { c.attemptFireOnce(job) })
			c.entries[job.ReminderID] = current
			if c.logger != nil {
				c.logger.Error("Reminder due receipt persistence failed", "reminder_id", job.ReminderID, "agent_id", job.OwnerAgentID, "version", job.Version, "error", err)
			}
			c.mu.Unlock()
			return
		}
	} else {
		receipt = existingReceipts[findReceiptIndex(existingReceipts, identity)]
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
	index := findReceiptIndex(c.receipts[receipt.Job.ReminderID], identity)
	if index < 0 {
		c.mu.Unlock()
		return
	}
	pending := c.receipts[receipt.Job.ReminderID][index]
	if c.dispatching[key] || pending.RetryTerminal != nil {
		c.mu.Unlock()
		return
	}
	if pending.WakeEnqueued {
		c.dispatching[key] = true
		c.mu.Unlock()
		if !pending.ServerAcked && c.onFireReceipt != nil {
			c.deliverFireReceipt(pending)
		}
		c.mu.Lock()
		delete(c.dispatching, key)
		c.mu.Unlock()
		return
	}
	if c.retryBudgetExhaustedLocked(pending) {
		exhaustion := c.exhaustRetryLocked(pending, c.defaultRetryStage(pending))
		c.mu.Unlock()
		c.notifyRetryExhausted(exhaustion)
		return
	}
	c.dispatching[key] = true
	c.clearFireRetryLocked(identity)
	pending.RetryAttempt++
	pending.RetryNextAttemptAt = ""
	c.receipts[receipt.Job.ReminderID][index] = pending
	_ = c.persistLocked()
	c.mu.Unlock()

	if !pending.ServerAcked {
		if c.onFireReceipt != nil {
			c.deliverFireReceipt(pending)
		}
		c.mu.Lock()
		delete(c.dispatching, key)
		index = findReceiptIndex(c.receipts[pending.Job.ReminderID], identity)
		if index < 0 {
			c.mu.Unlock()
			return
		}
		exhaustion := c.scheduleFireRetryLocked(c.receipts[pending.Job.ReminderID][index])
		c.mu.Unlock()
		c.notifyRetryExhausted(exhaustion)
		return
	}
	if !pending.ServerFired {
		c.mu.Lock()
		delete(c.dispatching, key)
		c.mu.Unlock()
		return
	}

	wakeEnqueued := false
	func() {
		defer func() {
			if recovered := recover(); recovered != nil && c.logger != nil {
				c.logger.Error("reminder onFire panicked", "reminder_id", pending.Job.ReminderID, "error", recovered)
			}
		}()
		wakeEnqueued = c.deliverFire(pending.Job)
	}()
	c.mu.Lock()
	delete(c.dispatching, key)
	index = findReceiptIndex(c.receipts[pending.Job.ReminderID], identity)
	if index < 0 {
		c.mu.Unlock()
		return
	}
	current := c.receipts[pending.Job.ReminderID]
	if !wakeEnqueued {
		exhaustion := c.scheduleFireRetryLocked(current[index])
		c.mu.Unlock()
		c.notifyRetryExhausted(exhaustion)
		return
	}
	current[index].WakeEnqueued = true
	current[index].RetryNextAttemptAt = ""
	if reminderReceiptComplete(current[index]) {
		c.clearFireRetryLocked(identity)
		current = append(current[:index], current[index+1:]...)
		if len(current) == 0 {
			delete(c.receipts, pending.Job.ReminderID)
		} else {
			c.receipts[pending.Job.ReminderID] = current
		}
	} else {
		c.receipts[pending.Job.ReminderID] = current
	}
	_ = c.persistLocked()
	c.mu.Unlock()
}

func (c *reminderCache) deliverFireReceipt(receipt reminderDueReceipt) (queued bool) {
	defer func() {
		if recovered := recover(); recovered != nil && c.logger != nil {
			c.logger.Error("reminder fire receipt panicked", "reminder_id", receipt.Job.ReminderID, "error", recovered)
		}
	}()
	return c.onFireReceipt(receipt)
}

func (c *reminderCache) scheduleFireRetryLocked(receipt reminderDueReceipt) *reminderRetryExhaustion {
	if reminderReceiptComplete(receipt) {
		return nil
	}
	identity := receiptIdentity(receipt)
	index := findReceiptIndex(c.receipts[receipt.Job.ReminderID], identity)
	if index < 0 {
		return nil
	}
	pending := c.receipts[receipt.Job.ReminderID][index]
	if pending.RetryTerminal != nil {
		return nil
	}
	if pending.WakeEnqueued {
		return nil
	}
	if c.retryBudgetExhaustedLocked(pending) {
		return c.exhaustRetryLocked(pending, c.defaultRetryStage(pending))
	}
	key := reminderReceiptKey(identity)
	if _, exists := c.fireRetryTimers[key]; exists {
		return nil
	}
	delay := c.fireRetryBackoffLocked(pending)
	pending.RetryNextAttemptAt = c.clock.Now().Add(delay).UTC().Format(time.RFC3339Nano)
	c.receipts[receipt.Job.ReminderID][index] = pending
	_ = c.persistLocked()
	timer := c.clock.AfterFunc(delay, func() {
		c.mu.Lock()
		if _, ok := c.fireRetryTimers[key]; ok {
			delete(c.fireRetryTimers, key)
		}
		currentIndex := findReceiptIndex(c.receipts[receipt.Job.ReminderID], identity)
		var next reminderDueReceipt
		if currentIndex >= 0 {
			next = c.receipts[receipt.Job.ReminderID][currentIndex]
		}
		c.mu.Unlock()
		if currentIndex >= 0 {
			c.dispatchFire(next)
		}
	})
	c.fireRetryTimers[key] = timer
	return nil
}

func (c *reminderCache) retryDelay() time.Duration {
	if c == nil || c.fireRetryDelay <= 0 {
		return defaultReminderFireRetryDelay
	}
	return c.fireRetryDelay
}

func (c *reminderCache) retryMaxDelay() time.Duration {
	if c == nil || c.fireRetryMaxDelay < c.retryDelay() {
		return defaultReminderFireRetryMaxDelay
	}
	return c.fireRetryMaxDelay
}

func (c *reminderCache) retryMaxAttempts() int {
	if c == nil || c.fireRetryMaxAttempts < 1 {
		return defaultReminderFireRetryMaxAttempts
	}
	return c.fireRetryMaxAttempts
}

func (c *reminderCache) retryDeadline() time.Duration {
	if c == nil || c.fireRetryDeadline <= 0 {
		return defaultReminderFireRetryDeadline
	}
	return c.fireRetryDeadline
}

func (c *reminderCache) retryBudgetExhaustedLocked(receipt reminderDueReceipt) bool {
	if receipt.RetryAttempt >= c.retryMaxAttempts() {
		return true
	}
	deadline, ok := parseReminderTime(receipt.RetryDeadlineAt)
	return ok && !c.clock.Now().Before(deadline)
}

func (c *reminderCache) fireRetryBackoffLocked(receipt reminderDueReceipt) time.Duration {
	shift := receipt.RetryAttempt - 1
	if shift < 0 {
		shift = 0
	}
	delay := c.retryDelay()
	for i := 0; i < shift; i++ {
		if delay >= c.retryMaxDelay() {
			delay = c.retryMaxDelay()
			break
		}
		delay *= 2
	}
	if delay > c.retryMaxDelay() {
		delay = c.retryMaxDelay()
	}
	if deadline, ok := parseReminderTime(receipt.RetryDeadlineAt); ok {
		remaining := deadline.Sub(c.clock.Now())
		if remaining < 0 {
			remaining = 0
		}
		if delay > remaining {
			delay = remaining
		}
	}
	return delay
}

func (c *reminderCache) defaultRetryStage(receipt reminderDueReceipt) string {
	if receipt.ServerAcked {
		return "inbox_materialization"
	}
	return "fire_request"
}

func (c *reminderCache) exhaustRetryLocked(receipt reminderDueReceipt, stage string) *reminderRetryExhaustion {
	identity := receiptIdentity(receipt)
	index := findReceiptIndex(c.receipts[receipt.Job.ReminderID], identity)
	if index < 0 {
		return nil
	}
	pending := c.receipts[receipt.Job.ReminderID][index]
	if pending.RetryTerminal != nil {
		return nil
	}
	exhaustion := reminderRetryExhaustion{
		Code:         reminderDeliveryRetryExhaustedCode,
		Stage:        stage,
		OwnerAgentID: pending.Job.OwnerAgentID,
		ReminderID:   pending.Job.ReminderID,
		Version:      pending.Job.Version,
		Attempts:     pending.RetryAttempt,
		DeadlineAt:   pending.RetryDeadlineAt,
		ExhaustedAt:  c.clock.Now().UTC().Format(time.RFC3339Nano),
	}
	pending.RetryTerminal = &exhaustion
	pending.RetryNextAttemptAt = ""
	c.receipts[receipt.Job.ReminderID][index] = pending
	c.clearFireRetryLocked(identity)
	_ = c.persistLocked()
	return &exhaustion
}

func (c *reminderCache) notifyRetryExhausted(exhaustion *reminderRetryExhaustion) {
	if exhaustion == nil || c == nil {
		return
	}
	if c.onRetryExhausted != nil {
		c.onRetryExhausted(*exhaustion)
	}
	if c.logger != nil {
		c.logger.Error("reminder delivery retry exhausted",
			"code", exhaustion.Code,
			"stage", exhaustion.Stage,
			"ownerAgentId", exhaustion.OwnerAgentID,
			"reminderId", exhaustion.ReminderID,
			"version", exhaustion.Version,
			"attempts", exhaustion.Attempts,
			"deadlineAt", exhaustion.DeadlineAt,
		)
	}
}

func parseReminderTime(value string) (time.Time, bool) {
	if value == "" {
		return time.Time{}, false
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		parsed, err = time.Parse(time.RFC3339, value)
	}
	return parsed, err == nil
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
