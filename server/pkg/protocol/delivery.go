package protocol

// Direct Agent Message Delivery wire events and payloads.
//
// This is the Raft-style idle/through delivery surface (T1 of the #2282
// vertical split). Unlike the legacy task/lease path, delivery is built
// around one communication truth: the Server canonical Message. The Server
// never stores an Agent-facing seen cursor; the machine keeps a local,
// target-scoped Context Boundary and hands it to an internal recovery read on
// startup.
//
// Wire semantics (T1 scope):
//
//	Server -> Machine : agent:deliver        (concrete Message body, accepted into Pending)
//	Machine -> Server : agent:deliver:ack    (transport-only acceptance, NOT read/handoff)
//	Server -> Machine : agent:deliver:handoff (runtime body batch, per target seq order)
//	Machine -> Server : agent:deliver:handoff:ack (boundary persisted, Pending removed)
//
// The harness asserts externally observable protocol, Message, file,
// runtime-input, and Activity behavior rather than internal helper calls or
// direct table state.

// AgentDeliverPayload is the concrete body received into a target's Pending
// projection. DeliveryID is the coordinator's idempotency key: resending the
// same DeliveryID is a no-op accept. MessageID is the canonical Message
// identity; Target identifies the conversation surface (channel / DM /
// thread). Seq is the canonical Message sequence within Target and defines
// handoff ordering.
type AgentDeliverPayload struct {
	WorkspaceID string        `json:"workspace_id"`
	AgentID     string        `json:"agent_id"`
	RuntimeID   string        `json:"runtime_id"`
	Target      string        `json:"target"`
	MessageID   string        `json:"message_id"`
	Seq         int           `json:"seq"`
	DeliveryID  string        `json:"delivery_id"`
	Role        string        `json:"role"`    // "user" | "assistant" ...
	Content     string        `json:"content"` // canonical body text (redacted in logs)
	Parts       []MessagePart `json:"parts,omitempty"`
	CreatedAt   string        `json:"created_at,omitempty"`
}

// AgentDeliverAckPayload acks transport acceptance of a single delivery.
type AgentDeliverAckPayload struct {
	WorkspaceID string `json:"workspace_id"`
	AgentID     string `json:"agent_id"`
	RuntimeID   string `json:"runtime_id"`
	DeliveryID  string `json:"delivery_id"`
	MessageID   string `json:"message_id"`
	Accepted    bool   `json:"accepted"`
	Reason      string `json:"reason,omitempty"`
}

// AgentDeliverHandoffPayload is a batch of concrete bodies for one Target in
// target sequence order. boundary_before is the machine-local Context
// Boundary value the batch advances from; inclusive_last_seq is the highest
// covered target sequence.
type AgentDeliverHandoffPayload struct {
	WorkspaceID string                `json:"workspace_id"`
	AgentID     string                `json:"agent_id"`
	RuntimeID   string                `json:"runtime_id"`
	Target      string                `json:"target"`
	Messages    []AgentDeliverPayload `json:"messages"`
	// BoundaryBefore is the previous machine-local Context Boundary for the
	// target (0 when no coverage yet).
	BoundaryBefore int `json:"boundary_before"`
	// RequestID correlates the handoff RPC with the machine's ack, mirroring
	// the existing file-op request/response protocol.
	RequestID string `json:"request_id"`
}

// AgentDeliverHandoffAckPayload confirms the boundary file was atomically
// replaced and Pending removed. boundary_after is the new Context Boundary.
type AgentDeliverHandoffAckPayload struct {
	WorkspaceID   string `json:"workspace_id"`
	AgentID       string `json:"agent_id"`
	RuntimeID     string `json:"runtime_id"`
	Target        string `json:"target"`
	BoundaryAfter int    `json:"boundary_after"`
	Accepted      bool   `json:"accepted"`
	Reason        string `json:"reason,omitempty"`
	// RequestID echoes the handoff request's correlation id so the server can
	// route the ack to the waiting RPC caller.
	RequestID string `json:"request_id"`
}
