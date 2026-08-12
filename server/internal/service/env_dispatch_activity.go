package service

import "sync"

// EnvDispatchActivityTracker mirrors the idempotent state transition required
// by persistent run delivery obligations. It is used for in-process ordering
// checks; the database remains authoritative across restarts.
type EnvDispatchActivityTracker struct {
	mu         sync.Mutex
	deliveries map[string]bool
	pending    int64
}

func NewEnvDispatchActivityTracker() *EnvDispatchActivityTracker {
	return &EnvDispatchActivityTracker{deliveries: make(map[string]bool)}
}

func (t *EnvDispatchActivityTracker) CreateDeliveryObligation(deliveryID string) bool {
	if t == nil || deliveryID == "" {
		return false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if _, exists := t.deliveries[deliveryID]; exists {
		return false
	}
	t.deliveries[deliveryID] = false
	t.pending++
	return true
}

func (t *EnvDispatchActivityTracker) SettleDeliveryObligation(deliveryID string) bool {
	if t == nil || deliveryID == "" {
		return false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	settled, exists := t.deliveries[deliveryID]
	if !exists || settled {
		return false
	}
	t.deliveries[deliveryID] = true
	if t.pending > 0 {
		t.pending--
	}
	return true
}

func (t *EnvDispatchActivityTracker) PendingDeliveries() int64 {
	if t == nil {
		return 0
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.pending
}
