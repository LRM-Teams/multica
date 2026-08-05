package daemonws

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

// AgentDeliverAckHandler is invoked when the machine acknowledges transport
// acceptance of an agent:deliver frame (daemon → server). It is purely
// observational: an ack proves transport acceptance, never read or handoff.
type AgentDeliverAckHandler func(ack protocol.AgentDeliverAckPayload)

// SetAgentDeliverAckHandler installs the machine-to-server ack callback.
func (h *Hub) SetAgentDeliverAckHandler(fn AgentDeliverAckHandler) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.agentDeliverAck = fn
}

// RequestAgentDeliverAck delivers a machine agent:deliver ack to the handler.
func (h *Hub) handleAgentDeliverAck(raw json.RawMessage) {
	if h == nil {
		return
	}
	h.mu.RLock()
	fn := h.agentDeliverAck
	h.mu.RUnlock()
	if fn == nil {
		slog.Debug("daemon websocket: agent:deliver ack received but no handler installed")
		return
	}
	var ack protocol.AgentDeliverAckPayload
	if err := json.Unmarshal(raw, &ack); err != nil {
		slog.Debug("daemon websocket: invalid agent:deliver ack payload", "error", err)
		return
	}
	fn(ack)
}

// HubAgentTransport adapts the daemon websocket Hub to the delivery.Transport
// port so the server Coordinator can drive a runtime's delivery chain.
//
// Deliver pushes an agent:deliver frame (transport acceptance; fire-and-forget
// — the machine replies asynchronously with agent:deliver:ack via
// handleAgentDeliverAck). Handoff issues a synchronous agent:deliver:handoff
// RPC and blocks for the machine's agent:deliver:handoff_ack.
type HubAgentTransport struct {
	hub *Hub
}

// NewHubAgentTransport builds a delivery.Transport backed by the Hub.
func NewHubAgentTransport(hub *Hub) *HubAgentTransport {
	return &HubAgentTransport{hub: hub}
}

// Deliver pushes one concrete body as an agent:deliver frame. It is scoped to
// the frame's runtime and is delivered at-most-once per connection.
func (t *HubAgentTransport) Deliver(p protocol.AgentDeliverPayload) error {
	if t == nil || t.hub == nil {
		return ErrRuntimeOffline
	}
	frame, err := json.Marshal(protocol.Message{
		Type:    protocol.EventAgentDeliver,
		Payload: mustMarshalRaw(p),
	})
	if err != nil {
		return err
	}
	// eventID "" disables dedup so transport acceptance is always observable.
	if delivered, _ := t.hub.notifyFrame(p.RuntimeID, frame, ""); !delivered {
		return ErrRuntimeOffline
	}
	return nil
}

// Handoff issues an agent:deliver:handoff RPC and waits for the correlated
// agent:deliver:handoff_ack. The caller owns the returned ack's Accepted flag.
func (t *HubAgentTransport) Handoff(hf protocol.AgentDeliverHandoffPayload) error {
	if t == nil || t.hub == nil {
		return ErrRuntimeOffline
	}
	hf.RequestID = ulid.Make().String()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	raw, err := t.hub.requestDaemon(ctx, hf.RuntimeID, hf.RequestID, protocol.EventAgentDeliverHandoff, hf)
	if err != nil {
		return err
	}
	var ack protocol.AgentDeliverHandoffAckPayload
	if err := json.Unmarshal(raw, &ack); err != nil {
		return fmt.Errorf("handoff ack parse: %w", err)
	}
	if !ack.Accepted {
		return fmt.Errorf("handoff rejected: %s", ack.Reason)
	}
	return nil
}

// handoffAckRequestID extracts the correlation RequestID from an inbound
// agent:deliver:handoff_ack so the Hub can route it to the waiting RPC caller.
func handoffAckRequestID(raw json.RawMessage) string {
	var idOnly struct {
		RequestID string `json:"request_id"`
	}
	if err := json.Unmarshal(raw, &idOnly); err != nil || idOnly.RequestID == "" {
		return ""
	}
	return idOnly.RequestID
}
