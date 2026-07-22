package daemon

import (
	"log/slog"
	"sync"
	"time"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

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

// reminderCache is the owner-daemon timer projection. Durable Reminder state
// remains server-owned; this cache only fences versioned projections and emits
// fire attempts when the current timer becomes due.
type reminderCache struct {
	mu      sync.Mutex
	entries map[string]reminderCacheEntry
	clock   reminderClock
	onFire  func(protocol.ReminderTimerJob)
	logger  *slog.Logger
}

func newReminderCache(clock reminderClock, logger *slog.Logger, onFire func(protocol.ReminderTimerJob)) *reminderCache {
	if clock == nil {
		clock = systemReminderClock{}
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &reminderCache{
		entries: make(map[string]reminderCacheEntry),
		clock:   clock,
		onFire:  onFire,
		logger:  logger,
	}
}

func (c *reminderCache) upsert(job protocol.ReminderTimerJob) bool {
	if c == nil || job.ReminderID == "" || job.OwnerAgentID == "" || job.Version < 1 {
		return false
	}
	fireAt, err := time.Parse(time.RFC3339, job.FireAt)
	if err != nil {
		c.logger.Warn("reminder cache rejected invalid fire_at", "reminder_id", job.ReminderID, "fire_at", job.FireAt)
		return false
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if existing, ok := c.entries[job.ReminderID]; ok {
		if existing.job.Version >= job.Version {
			return false
		}
		if existing.timer != nil {
			existing.timer.Stop()
		}
	}
	c.entries[job.ReminderID] = reminderCacheEntry{job: job}
	c.armLocked(job, fireAt)
	return true
}

func (c *reminderCache) cancel(reminderID string, version int64) bool {
	if c == nil || reminderID == "" || version < 1 {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	existing, ok := c.entries[reminderID]
	if !ok || existing.job.Version > version {
		return false
	}
	if existing.timer != nil {
		existing.timer.Stop()
	}
	delete(c.entries, reminderID)
	return true
}

func (c *reminderCache) snapshot(agentID string, jobs []protocol.ReminderTimerJob) int {
	if c == nil || agentID == "" {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for reminderID, entry := range c.entries {
		if entry.job.OwnerAgentID != agentID {
			continue
		}
		if entry.timer != nil {
			entry.timer.Stop()
		}
		delete(c.entries, reminderID)
	}
	accepted := 0
	for _, job := range jobs {
		if job.OwnerAgentID != agentID || job.ReminderID == "" || job.Version < 1 {
			continue
		}
		fireAt, err := time.Parse(time.RFC3339, job.FireAt)
		if err != nil {
			continue
		}
		c.entries[job.ReminderID] = reminderCacheEntry{job: job}
		c.armLocked(job, fireAt)
		accepted++
	}
	return accepted
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
}

func (c *reminderCache) removeOwner(agentID string) {
	if c == nil || agentID == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for reminderID, entry := range c.entries {
		if entry.job.OwnerAgentID != agentID {
			continue
		}
		if entry.timer != nil {
			entry.timer.Stop()
		}
		delete(c.entries, reminderID)
	}
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
		c.mu.Unlock()
		if c.onFire != nil {
			c.onFire(job)
		}
	})
	entry := c.entries[job.ReminderID]
	entry.timer = timer
	c.entries[job.ReminderID] = entry
}
