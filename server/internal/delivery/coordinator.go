package delivery

import (
	"sort"
	"sync"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

// Stage enumerates the distinguishable failure/truth layers of a delivery so
// one green layer cannot conceal another layer's failure (acceptance 5).
type Stage string

const (
	StageMessageTruth    Stage = "message_truth"       // canonical Message exists / is visible
	StageTransportAck    Stage = "transport_ack"       // agent:deliver accepted into Pending
	StageRuntimeHandoff  Stage = "runtime_handoff"     // concrete bodies handed to runtime
	StageContextBoundary Stage = "context_boundary"    // boundary file atomically replaced
	StageActivityProj    Stage = "activity_projection" // exactly-one Message received
)

// pendingEntry is a concrete delivery accepted into the in-memory Pending
// projection. It is keyed by DeliveryID for idempotency and ordered by target
// sequence for handoff.
type pendingEntry struct {
	Delivery  protocol.AgentDeliverPayload
	Acked     bool // transport now acknowledged by the machine
	HandedOff bool
}

// coordinatorState is the in-memory Pending projection for one runtime.
type coordinatorState struct {
	// byTarget holds Pending entries grouped by Target, ordered by Seq.
	byTarget map[string][]*pendingEntry
	// deliveryIndex dedupes by DeliveryID across all targets.
	deliveryIndex map[string]*pendingEntry
}

func newCoordinatorState() *coordinatorState {
	return &coordinatorState{
		byTarget:      map[string][]*pendingEntry{},
		deliveryIndex: map[string]*pendingEntry{},
	}
}

// Coordinator is the server-side Raft-style coordinator for one runtime. It
// accepts at-least-once Deliveries, keeps an in-memory Pending projection,
// gives the runtime concrete Message bodies in target sequence order, and
// records machine-local Context Boundary progress only after a handoff has
// been acknowledged.
//
// The Coordinator depends on two ports:
//
//	Transport  - emits agent:deliver frames to the machine and observes acks.
//	Boundary   - reads the machine-local Context Boundary (recovery seam).
//	Activity   - the exactly-once "Message received" projection.
//
// It does not itself know how the runtime input boundary consumes bodies; the
// machine (daemon) owns that and acks handoff after atomic boundary replace.
type Coordinator struct {
	mu sync.Mutex

	workspaceID string
	agentID     string
	runtimeID   string

	state *coordinatorState

	transport Transport
	boundary  BoundaryReader
	activity  ActivityReporter
}

// Transport emits deliveries toward a runtime and observes acks.
type Transport interface {
	// Deliver emits an agent:deliver frame for one concrete body. It returns
	// transport acceptance synchronously where supported; a nil call always
	// schedules the delivery for the machine's async ack.
	Deliver(frame protocol.AgentDeliverPayload) error
	// Handoff emits an agent:deliver:handoff batch. The runtime boundary is
	// expected to accept it, persist the boundary atomically, then acknowledge.
	Handoff(frame protocol.AgentDeliverHandoffPayload) error
}

// BoundaryReader reads the current machine-local Context Boundary for a target.
type BoundaryReader interface {
	Current(target string) (int, error)
}

// ActivityReporter emits the user-facing narrative. Exactly one "Message
// received" must be emitted per accepted handoff batch.
type ActivityReporter interface {
	ReportReceived(ev ActivityReceived) error
}

// ActivityReceived is the fact a concrete runtime handoff completed for a
// target: the runtime input boundary accepted the batch and the coordinator
// removed it from Pending. MessageIDs carries the canonical Message identity
// of each body in the batch so the projection can attach to the right
// messages while remaining independent of runtime/Notice internals.
type ActivityReceived struct {
	AgentID       string
	RuntimeID     string
	Target        string
	MessageIDs    []string
	BoundaryAfter int
}

// NewCoordinator builds a coordinator bound to one runtime identity.
func NewCoordinator(workspaceID, agentID, runtimeID string, transport Transport, boundary BoundaryReader, activity ActivityReporter) *Coordinator {
	return &Coordinator{
		workspaceID: workspaceID,
		agentID:     agentID,
		runtimeID:   runtimeID,
		state:       newCoordinatorState(),
		transport:   transport,
		boundary:    boundary,
		activity:    activity,
	}
}

// DeliverResult describes the outcome of accepting a delivery.
type DeliverResult struct {
	// Accepted is true when the delivery entered Pending (or was already known).
	Accepted bool
	// Duplicate is true when the DeliveryID was already accepted previously.
	Duplicate bool
}

// Deliver accepts an at-least-once Delivery into Pending and emits the
// agent:deliver wire event. Resending the same DeliveryID is idempotent.
// Acceptance proves only that the coordinator holds the body in Pending
// (transport acceptance); it never implies read or handoff.
func (c *Coordinator) Deliver(p protocol.AgentDeliverPayload) (DeliverResult, error) {
	c.mu.Lock()
	_, dup := c.state.deliveryIndex[p.DeliveryID]
	if !dup {
		// Insert preserving target sequence order.
		entry := &pendingEntry{Delivery: p}
		c.state.deliveryIndex[p.DeliveryID] = entry
		c.state.byTarget[p.Target] = append(c.state.byTarget[p.Target], entry)
		sort.SliceStable(c.state.byTarget[p.Target], func(i, j int) bool {
			return c.state.byTarget[p.Target][i].Delivery.Seq < c.state.byTarget[p.Target][j].Delivery.Seq
		})
	}
	c.mu.Unlock()

	if err := c.transport.Deliver(p); err != nil {
		return DeliverResult{}, err
	}
	return DeliverResult{Accepted: true, Duplicate: dup}, nil
}

// Ack marks a delivery as transport-acknowledged by the machine. It is
// distinct from read/handoff: an ack only proves the machine accepted the
// wire frame (acceptance 2).
func (c *Coordinator) Ack(p protocol.AgentDeliverAckPayload) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if e, ok := c.state.deliveryIndex[p.DeliveryID]; ok {
		e.Acked = true
	}
}

// PendingCount returns the number of Pending bodies for a target (or overall
// when target is empty). Used by the harness to assert Pending lifecycle.
func (c *Coordinator) PendingCount(target string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	if target == "" {
		return len(c.state.deliveryIndex)
	}
	return len(c.state.byTarget[target])
}

// HandoffReady reports whether a target has at least one non-handled-off
// Pending body.
func (c *Coordinator) HandoffReady(target string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, e := range c.state.byTarget[target] {
		if !e.HandedOff {
			return true
		}
	}
	return false
}

// Handoff gathers the next un-handled-off batch for a target in target
// sequence order, emits the agent:deliver:handoff wire event, and (assuming
// the machine acks) advances the context boundary and emits exactly one
// "Message received" Activity. concurrency guards the exactly-once activity
// guarantee.
//
// It returns the Stage the operation failed at, or empty string on success,
// so failure-path tests can distinguish each layer (acceptance 5).
func (c *Coordinator) Handoff(target string) (Stage, error) {
	// Collect the batch under lock; the batch is point-in-time ordered.
	c.mu.Lock()
	var batch []protocol.AgentDeliverPayload
	for _, e := range c.state.byTarget[target] {
		if !e.HandedOff {
			batch = append(batch, e.Delivery)
		}
	}
	c.mu.Unlock()

	if len(batch) == 0 {
		return "", nil
	}

	boundaryBefore := 0
	if c.boundary != nil {
		if cur, err := c.boundary.Current(target); err == nil {
			boundaryBefore = cur
		}
	}

	frame := protocol.AgentDeliverHandoffPayload{
		WorkspaceID:    c.workspaceID,
		AgentID:        c.agentID,
		RuntimeID:      c.runtimeID,
		Target:         target,
		Messages:       batch,
		BoundaryBefore: boundaryBefore,
	}

	if err := c.transport.Handoff(frame); err != nil {
		return StageRuntimeHandoff, err
	}

	// Runtime input boundary has accepted the batch and atomically replaced
	// the boundary. Advance the coordinator's view and remove Pending.
	inclusiveLast := batch[len(batch)-1].Seq
	c.mu.Lock()
	for _, e := range c.state.byTarget[target] {
		if !e.HandedOff && e.Delivery.Seq <= inclusiveLast {
			e.HandedOff = true
		}
	}
	// Prune fully-handled-off entries from the projection.
	kept := c.state.byTarget[target][:0]
	for _, e := range c.state.byTarget[target] {
		if !e.HandedOff {
			kept = append(kept, e)
		}
	}
	c.state.byTarget[target] = kept
	c.mu.Unlock()

	// Exactly-once "Message received" Activity per accepted batch.
	if c.activity != nil {
		ids := make([]string, 0, len(batch))
		for _, m := range batch {
			ids = append(ids, m.MessageID)
		}
		if err := c.activity.ReportReceived(ActivityReceived{
			AgentID:       c.agentID,
			RuntimeID:     c.runtimeID,
			Target:        target,
			MessageIDs:    ids,
			BoundaryAfter: inclusiveLast,
		}); err != nil {
			return StageActivityProj, err
		}
	}
	return "", nil
}

// IsAcked reports whether a delivery's transport acceptance was acknowledged
// by the machine (agent:deliver:ack observed). Proof that an ack is
// transport-only: a delivery can be acked while its Pending entry and body
// handoff are unchanged.
func (c *Coordinator) IsAcked(deliveryID string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.state.deliveryIndex[deliveryID]
	return ok && e.Acked
}

// AckCount returns how many distinct deliveries have been transport-acked.
func (c *Coordinator) AckCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := 0
	for _, e := range c.state.deliveryIndex {
		if e.Acked {
			n++
		}
	}
	return n
}
