package delivery

import (
	"sync"
)

// MessageReceivedReporter is the default Activity port. It records each
// accepted handoff batch so the harness can assert the exactly-once "Message
// received" projection, and it renders the user-facing display label.
//
// In a full production wiring this reporter would delegate to the Server
// AgentActivity persistence path. The T1 harness keeps it as a deterministic
// recorder and assert the narrative directly at the Activity projection seam
// (acceptance 4) without depending on transport/Notice/boundary internals.
type MessageReceivedReporter struct {
	mu       sync.Mutex
	received []ActivityReceived
}

// NewMessageReceivedReporter builds a reporter with no prior Activity.
func NewMessageReceivedReporter() *MessageReceivedReporter {
	return &MessageReceivedReporter{}
}

// ReportReceived appends one "Message received" fact for a batch.
func (r *MessageReceivedReporter) ReportReceived(ev ActivityReceived) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.received = append(r.received, ev)
	return nil
}

// Received returns a copy of the recorded facts.
func (r *MessageReceivedReporter) Received() []ActivityReceived {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]ActivityReceived, len(r.received))
	copy(out, r.received)
	return out
}

// Count returns the number of recorded "Message received" facts.
func (r *MessageReceivedReporter) Count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.received)
}

// DisplayLabel is the user-facing copy for a concrete runtime handoff. It is
// frontend/projection copy, not a wire event name or canonical state
// transition (per the #2282 spec).
func DisplayLabel() string {
	return "Message received"
}
