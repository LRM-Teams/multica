package daemon

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"

	"github.com/multica-ai/multica/server/internal/delivery"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// deliveryBoundaryRelPath is the machine-local Context Boundary file for the
// direct delivery chain, stored under the agent's durable workspace root. It
// is written atomically by the daemon and read by the server coordinator's
// recovery seam. It is the machine's cursor — the Server never stores it.
const deliveryBoundaryRelPath = "runtime/delivery/boundary.json"

// deliveryInboxRelPath is the runtime input boundary for T1: the daemon hands
// a handoff batch's concrete bodies into this inbox file (append, newline
// separated JSON). A resident runtime would consume this file; the T1 harness
// asserts its contents directly as the runtime input boundary behavior.
const deliveryInboxRelPath = "runtime/delivery/inbox.jsonl"

// agentDelivery holds the per-agent delivery receiver state the daemon keeps
// so it can handle server-driven agent:deliver and agent:deliver:handoff
// frames without coupling to the whole reminder/lifecycle machinery.
type agentDelivery struct {
	mu        sync.Mutex
	boundary  *delivery.BoundaryStore
	inboxPath string
}

// handleAgentDeliver processes a server→daemon agent:deliver push. Per the
// wire contract this is transport acceptance only: the machine replies with
// agent:deliver:ack and performs no boundary/Activity side effects.
func (d *Daemon) handleAgentDeliver(p protocol.AgentDeliverPayload, writes chan<- []byte) {
	rec := d.deliveryReceiver(p.WorkspaceID, p.AgentID)
	ack := rec.HandleDeliver(p)
	d.sendDaemonFrame(protocol.EventAgentDeliverAck, ack, "", writes)
}

// handleAgentDeliverHandoff processes a server→daemon agent:deliver:handoff
// RPC: it feeds the concrete bodies into the runtime input boundary, atomically
// advances the machine-local Context Boundary, then replies with the correlated
// agent:deliver:handoff_ack.
func (d *Daemon) handleAgentDeliverHandoff(p protocol.AgentDeliverHandoffPayload, writes chan<- []byte) {
	rec := d.deliveryReceiver(p.WorkspaceID, p.AgentID)
	ack, _, _ := rec.HandleHandoff(p)
	ack.RequestID = p.RequestID
	d.sendDaemonFrame(protocol.EventAgentDeliverHandoffAck, ack, p.RequestID, writes)
}

// deliveryReceiver returns (creating on first use) the receiver for a
// (workspace, agent) pair, rooted at its durable agent workspace.
func (d *Daemon) deliveryReceiver(workspaceID, agentID string) *delivery.Receiver {
	d.deliveryMu.Lock()
	defer d.deliveryMu.Unlock()
	key := workspaceID + "/" + agentID
	if r, ok := d.deliveryReceivers[key]; ok {
		return r
	}
	agentRoot := multicaAgentRoot(d.cfg, workspaceID, agentID)
	boundary := delivery.NewBoundaryStore(filepath.Join(agentRoot, filepath.FromSlash(deliveryBoundaryRelPath)))
	receiver := delivery.NewReceiver(boundary, &runtimeInboxInput{path: filepath.Join(agentRoot, filepath.FromSlash(deliveryInboxRelPath))})
	d.deliveryReceivers[key] = receiver
	return receiver
}

// runtimeInboxInput is the T1 fake runtime input boundary: it appends each
// handoff batch as a newline-delimited JSON record to the runtime inbox file
// (atomically flushed). In a later vertical this becomes the resident
// process's input protocol, but the boundary contract — accept a batch, then
// the boundary can advance — is unchanged.
type runtimeInboxInput struct {
	path string
}

// AcceptBatch appends the batch to the runtime inbox, one JSON object per
// line, creating the file (and parent dirs) as needed.
func (r *runtimeInboxInput) AcceptBatch(batch []delivery.AgentBody, target string) error {
	if r.path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(r.path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(r.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	for _, b := range batch {
		line, err := json.Marshal(map[string]any{
			"target":     target,
			"message_id": b.MessageID,
			"seq":        b.Seq,
			"role":       b.Role,
			"content":    b.Content,
		})
		if err != nil {
			return err
		}
		if _, err := f.Write(append(line, '\n')); err != nil {
			return err
		}
	}
	return f.Sync()
}
