package delivery

import (
	"fmt"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

// RuntimeInput is the runtime input boundary: the seam through which concrete
// Message bodies are handed to the Agent runtime. The harness uses a fake
// deterministic input; a production wiring would feed the resident process's
// input/protocol path.
type RuntimeInput interface {
	// AcceptBatch hands a batch of concrete bodies (already in target seq
	// order) to the runtime. Returns an error only if the runtime input
	// boundary refused the batch (e.g. unsafe busy state in a later vertical).
	AcceptBatch(batch []AgentBody, target string) error
}

// AgentBody is a single concrete body handed to the runtime input boundary.
type AgentBody struct {
	MessageID string
	Seq       int
	Role      string
	Content   string
}

// Receiver is the machine-side counterpart to the server Coordinator. It owns
// the runtime input boundary and the machine-local Context Boundary file. It
// is deliberately narrow for T1: it accepts deliveries (transport-only ack,
// no boundary/activity side effects), and it accepts handoff batches (feed
// runtime input, atomically advance the Context Boundary). Handoff completion
// is what ultimately lets the Coordinator emit exactly one "Message received"
// Activity.
type Receiver struct {
	boundary *BoundaryStore
	input    RuntimeInput
}

// NewReceiver wires a Receiver to a BoundaryStore and a runtime input.
func NewReceiver(boundary *BoundaryStore, input RuntimeInput) *Receiver {
	return &Receiver{boundary: boundary, input: input}
}

// HandleDeliver processes an agent:deliver frame. Per the wire contract this
// is transport acceptance only: it proves the machine held the body, it never
// advances the Context Boundary and never produces Activity. Returns the ack
// the machine sends back.
func (r *Receiver) HandleDeliver(p protocol.AgentDeliverPayload) protocol.AgentDeliverAckPayload {
	return protocol.AgentDeliverAckPayload{
		WorkspaceID: p.WorkspaceID,
		AgentID:     p.AgentID,
		RuntimeID:   p.RuntimeID,
		DeliveryID:  p.DeliveryID,
		MessageID:   p.MessageID,
		Accepted:    true,
	}
}

// BoundaryCurrent reads the current machine-local Context Boundary coverage
// for a target from the boundary file.
func (r *Receiver) BoundaryCurrent(target string) (int, error) {
	if r.boundary == nil {
		return 0, nil
	}
	return r.boundary.Current(target)
}

// HandleHandoff accepts an agent:deliver:handoff batch: it feeds the concrete
// bodies to the runtime input boundary in target seq order, then atomically
// advances the machine-local Context Boundary to the batch's inclusive last
// sequence. On success it returns the handoff ack with the new boundary
// value. A handoff that fails the runtime input or the boundary write fails
// closed and returns the stage that failed.
func (r *Receiver) HandleHandoff(p protocol.AgentDeliverHandoffPayload) (protocol.AgentDeliverHandoffAckPayload, Stage, error) {
	ack := protocol.AgentDeliverHandoffAckPayload{
		WorkspaceID: p.WorkspaceID,
		AgentID:     p.AgentID,
		RuntimeID:   p.RuntimeID,
		Target:      p.Target,
	}

	if r.input != nil && len(p.Messages) > 0 {
		bodies := make([]AgentBody, 0, len(p.Messages))
		for _, m := range p.Messages {
			bodies = append(bodies, AgentBody{
				MessageID: m.MessageID,
				Seq:       m.Seq,
				Role:      m.Role,
				Content:   m.Content,
			})
		}
		if err := r.input.AcceptBatch(bodies, p.Target); err != nil {
			ack.Accepted = false
			ack.Reason = err.Error()
			return ack, StageRuntimeHandoff, err
		}
	}

	if len(p.Messages) > 0 {
		inclusiveLast := p.Messages[len(p.Messages)-1].Seq
		after, err := r.boundary.Advance(p.Target, inclusiveLast)
		if err != nil {
			ack.Accepted = false
			ack.Reason = err.Error()
			return ack, StageContextBoundary, fmt.Errorf("boundary advance: %w", err)
		}
		ack.BoundaryAfter = after
	}
	ack.Accepted = true
	return ack, "", nil
}
