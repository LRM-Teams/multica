package daemon

import (
	"context"
	"sync"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

// workspaceRunnerMessageTransport is a connection-scoped sender. It carries
// canonical Message protocol frames only; lifecycle and reminder frames retain
// their own bounded channels.
type workspaceRunnerMessageTransport struct {
	generation uint64
	send       func(string, any) error
}

// workspaceRunnerDeliveryDispatcher keeps the Runner socket reader independent
// from provider startup and Message turns. Deliveries for one Agent remain
// ordered, while different Agents cannot head-of-line block each other.
type workspaceRunnerDeliveryDispatcher struct {
	ctx    context.Context
	handle func(context.Context, protocol.AgentDeliverPayload)

	mu      sync.Mutex
	queues  map[string][]protocol.AgentDeliverPayload
	running map[string]bool
}

func newWorkspaceRunnerDeliveryDispatcher(ctx context.Context, handle func(context.Context, protocol.AgentDeliverPayload)) *workspaceRunnerDeliveryDispatcher {
	return &workspaceRunnerDeliveryDispatcher{
		ctx: ctx, handle: handle,
		queues: make(map[string][]protocol.AgentDeliverPayload), running: make(map[string]bool),
	}
}

func (d *workspaceRunnerDeliveryDispatcher) Enqueue(delivery protocol.AgentDeliverPayload) bool {
	if d == nil || d.ctx == nil || d.handle == nil || delivery.AgentID == "" {
		return false
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.ctx.Err() != nil {
		return false
	}
	d.queues[delivery.AgentID] = append(d.queues[delivery.AgentID], delivery)
	if !d.running[delivery.AgentID] {
		d.running[delivery.AgentID] = true
		go d.drain(delivery.AgentID)
	}
	return true
}

func (d *workspaceRunnerDeliveryDispatcher) drain(agentID string) {
	for {
		d.mu.Lock()
		if d.ctx.Err() != nil {
			delete(d.queues, agentID)
			delete(d.running, agentID)
			d.mu.Unlock()
			return
		}
		queue := d.queues[agentID]
		if len(queue) == 0 {
			delete(d.queues, agentID)
			delete(d.running, agentID)
			d.mu.Unlock()
			return
		}
		delivery := queue[0]
		if len(queue) == 1 {
			delete(d.queues, agentID)
		} else {
			d.queues[agentID] = queue[1:]
		}
		d.mu.Unlock()
		d.handle(d.ctx, delivery)
	}
}

func (d *Daemon) attachWorkspaceRunnerMessageTransport(workspaceID string, send func(string, any) error) uint64 {
	if d == nil || workspaceID == "" || send == nil {
		return 0
	}
	d.mu.Lock()
	if d.runnerMessageGeneration == nil {
		d.runnerMessageGeneration = make(map[string]uint64)
	}
	if d.runnerMessageTransports == nil {
		d.runnerMessageTransports = make(map[string]workspaceRunnerMessageTransport)
	}
	d.runnerMessageGeneration[workspaceID]++
	generation := d.runnerMessageGeneration[workspaceID]
	d.runnerMessageTransports[workspaceID] = workspaceRunnerMessageTransport{generation: generation, send: send}
	d.mu.Unlock()
	return generation
}

func (d *Daemon) detachWorkspaceRunnerMessageTransport(workspaceID string, generation uint64) {
	if d == nil || workspaceID == "" || generation == 0 {
		return
	}
	d.mu.Lock()
	if current := d.runnerMessageTransports[workspaceID]; current.generation == generation {
		delete(d.runnerMessageTransports, workspaceID)
	}
	d.mu.Unlock()
}

func (d *Daemon) sendAgentMessageRunnerFrame(agentID, eventType string, payload any) bool {
	return d.sendWorkspaceRunnerAgentFrame(agentID, eventType, payload)
}

// sendWorkspaceRunnerAgentFrame resolves an Agent through its resident runtime
// and sends one frame on that Workspace's current serialized Runner writer.
// Message delivery, launch reconciliation, and Activity share this transport
// without falling back to the legacy Task wakeup socket.
func (d *Daemon) sendWorkspaceRunnerAgentFrame(agentID, eventType string, payload any) bool {
	if d == nil || agentID == "" {
		return false
	}
	d.messageCoordinatorMu.RLock()
	runtimeID := d.messageRuntimeIDs[agentID]
	d.messageCoordinatorMu.RUnlock()
	if runtimeID == "" {
		return false
	}
	d.mu.Lock()
	workspaceID := d.runtimeIndex[runtimeID].WorkspaceID
	transport := d.runnerMessageTransports[workspaceID]
	d.mu.Unlock()
	if transport.send == nil {
		return false
	}
	return transport.send(eventType, payload) == nil
}
