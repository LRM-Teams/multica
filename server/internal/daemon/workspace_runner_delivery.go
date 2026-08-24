package daemon

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

// workspaceDaemonDeliveryDispatcher keeps the Runner socket reader independent
// from provider startup and Message turns. Deliveries for one Agent remain
// ordered, while different Agents cannot head-of-line block each other.
type workspaceDaemonDeliveryDispatcher struct {
	ctx    context.Context
	handle func(context.Context, protocol.AgentDeliverPayload)

	mu      sync.Mutex
	queues  map[string][]protocol.AgentDeliverPayload
	running map[string]bool
	paused  map[string]workspaceDaemonDeliveryPause
}

type workspaceDaemonDeliveryPause struct {
	launchID string
	stop     bool
}

func newWorkspaceDaemonDeliveryDispatcher(ctx context.Context, handle func(context.Context, protocol.AgentDeliverPayload)) *workspaceDaemonDeliveryDispatcher {
	return &workspaceDaemonDeliveryDispatcher{
		ctx: ctx, handle: handle,
		queues: make(map[string][]protocol.AgentDeliverPayload), running: make(map[string]bool), paused: make(map[string]workspaceDaemonDeliveryPause),
	}
}

// Pause holds only this Agent's deliveries while agent:start establishes its
// provider. The socket reader and every other Agent remain independent.
func (d *workspaceDaemonDeliveryDispatcher) Pause(agentID, launchID string) bool {
	if d == nil || strings.TrimSpace(agentID) == "" || strings.TrimSpace(launchID) == "" {
		return false
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	current, paused := d.paused[agentID]
	if d.ctx.Err() != nil || paused && current.stop && current.launchID == launchID {
		return false
	}
	if paused && !current.stop && current.launchID == launchID {
		// The same immutable dispatch may be replayed before startup settles.
		// Keep the buffer paused while allowing the socket reader to ACK it.
		return true
	}
	d.paused[agentID] = workspaceDaemonDeliveryPause{launchID: launchID}
	return true
}

// Resume releases deliveries in their original order after agent:start has
// published Active, provider session, and initial Activity. No delivery can
// race those lifecycle facts onto the wire.
func (d *workspaceDaemonDeliveryDispatcher) Resume(agentID, launchID string) {
	if d == nil {
		return
	}
	d.mu.Lock()
	if pause, ok := d.paused[agentID]; !ok || pause.stop || pause.launchID != launchID {
		d.mu.Unlock()
		return
	}
	delete(d.paused, agentID)
	if len(d.queues[agentID]) > 0 && !d.running[agentID] && d.ctx.Err() == nil {
		d.running[agentID] = true
		go d.drain(agentID)
	}
	d.mu.Unlock()
}

// FenceStop supersedes a start pause with a stop-owned token. A late startup
// completion can therefore never Resume deliveries after stop has begun.
func (d *workspaceDaemonDeliveryDispatcher) FenceStop(agentID, launchID string) {
	if d == nil || strings.TrimSpace(agentID) == "" || strings.TrimSpace(launchID) == "" {
		return
	}
	d.mu.Lock()
	d.paused[agentID] = workspaceDaemonDeliveryPause{launchID: launchID, stop: true}
	delete(d.queues, agentID)
	d.mu.Unlock()
}

// RejectStart forgets only the volatile buffer. The server still owns every
// unACKed delivery and will replay it after a later successful start.
func (d *workspaceDaemonDeliveryDispatcher) RejectStart(agentID, launchID string) {
	if d == nil {
		return
	}
	d.mu.Lock()
	if pause, ok := d.paused[agentID]; !ok || pause.stop || pause.launchID != launchID {
		d.mu.Unlock()
		return
	}
	delete(d.paused, agentID)
	delete(d.queues, agentID)
	d.mu.Unlock()
}

func (d *workspaceDaemonDeliveryDispatcher) Enqueue(delivery protocol.AgentDeliverPayload) bool {
	if d == nil || d.ctx == nil || d.handle == nil || delivery.AgentID == "" {
		return false
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.ctx.Err() != nil {
		return false
	}
	d.queues[delivery.AgentID] = append(d.queues[delivery.AgentID], delivery)
	if _, paused := d.paused[delivery.AgentID]; !paused && !d.running[delivery.AgentID] {
		d.running[delivery.AgentID] = true
		go d.drain(delivery.AgentID)
	}
	return true
}

func (d *workspaceDaemonDeliveryDispatcher) drain(agentID string) {
	for {
		d.mu.Lock()
		if d.ctx.Err() != nil {
			delete(d.queues, agentID)
			delete(d.running, agentID)
			d.mu.Unlock()
			return
		}
		queue := d.queues[agentID]
		if _, paused := d.paused[agentID]; paused {
			delete(d.running, agentID)
			d.mu.Unlock()
			return
		}
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

func (d *Daemon) attachWorkspaceDaemon(runner *WorkspaceDaemon) {
	if d == nil || runner == nil || runner.WorkspaceID() == "" {
		return
	}
	d.workspaceDaemonMu.Lock()
	if d.workspaceDaemons == nil {
		d.workspaceDaemons = make(map[string]*WorkspaceDaemon)
	}
	d.workspaceDaemons[runner.WorkspaceID()] = runner
	d.workspaceDaemonMu.Unlock()
}

// adoptWorkspaceDaemon publishes the Binding child's owned Runner as the
// Credential Proxy / handoff lookup. Binding child constructs that Runner
// before Run; if it stays off this map, ensureWorkspaceDaemon mints a second
// empty inbox and message send returns 409 "Message coordinator is unavailable".
func (d *Daemon) adoptWorkspaceDaemon(runner *WorkspaceDaemon) error {
	if d == nil {
		return errors.New("Workspace Runner Daemon is required")
	}
	if runner == nil || runner.WorkspaceID() == "" {
		return errors.New("Workspace Runner identity is required")
	}
	d.attachWorkspaceDaemon(runner)
	return nil
}

func (d *Daemon) detachWorkspaceDaemon(runner *WorkspaceDaemon) {
	if d == nil || runner == nil {
		return
	}
	d.workspaceDaemonMu.Lock()
	if d.workspaceDaemons[runner.WorkspaceID()] == runner {
		delete(d.workspaceDaemons, runner.WorkspaceID())
	}
	d.workspaceDaemonMu.Unlock()
}

func (d *Daemon) currentWorkspaceDaemon(workspaceID string) *WorkspaceDaemon {
	if d == nil {
		return nil
	}
	d.workspaceDaemonMu.RLock()
	runner := d.workspaceDaemons[strings.TrimSpace(workspaceID)]
	d.workspaceDaemonMu.RUnlock()
	return runner
}

// ensureWorkspaceDaemon creates a state-owning Runner before a legacy
// lifecycle path opens an Inbox. The Runner is not started here: socket
// ownership remains exclusively with WorkspaceDaemon.Run.
func (d *Daemon) ensureWorkspaceDaemon(workspaceID string) (*WorkspaceDaemon, error) {
	workspaceID = strings.TrimSpace(workspaceID)
	if workspaceID == "" {
		return nil, errors.New("Workspace identity is required")
	}
	if runner := d.currentWorkspaceDaemon(workspaceID); runner != nil {
		return runner, nil
	}
	runner, err := d.newWorkspaceDaemon(workspaceID)
	if err != nil {
		return nil, err
	}
	d.workspaceDaemonMu.Lock()
	if current := d.workspaceDaemons[workspaceID]; current != nil {
		d.workspaceDaemonMu.Unlock()
		return current, nil
	}
	if d.workspaceDaemons == nil {
		d.workspaceDaemons = make(map[string]*WorkspaceDaemon)
	}
	d.workspaceDaemons[workspaceID] = runner
	d.workspaceDaemonMu.Unlock()
	return runner, nil
}

func (d *Daemon) currentWorkspaceDaemons() []*WorkspaceDaemon {
	if d == nil {
		return nil
	}
	d.workspaceDaemonMu.RLock()
	workspaceIDs := make([]string, 0, len(d.workspaceDaemons))
	for workspaceID := range d.workspaceDaemons {
		workspaceIDs = append(workspaceIDs, workspaceID)
	}
	sort.Strings(workspaceIDs)
	runners := make([]*WorkspaceDaemon, 0, len(workspaceIDs))
	for _, workspaceID := range workspaceIDs {
		if runner := d.workspaceDaemons[workspaceID]; runner != nil {
			runners = append(runners, runner)
		}
	}
	d.workspaceDaemonMu.RUnlock()
	return runners
}

// resolveWorkspaceDaemonByAgent preserves machine-local callers that predate a
// Workspace parameter. It returns the owning Runner, never its Inbox internals,
// and fails closed instead of selecting an ambiguous Workspace implicitly.
func (d *Daemon) resolveWorkspaceDaemonByAgent(agentID string) (*WorkspaceDaemon, error) {
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return nil, errors.New("Agent identity is required")
	}
	var matchedRunner *WorkspaceDaemon
	for _, runner := range d.currentWorkspaceDaemons() {
		if !runner.hasMessageInbox(agentID) {
			continue
		}
		if matchedRunner != nil {
			return nil, fmt.Errorf("Message Inbox for Agent %q is ambiguous across Workspace Runners", agentID)
		}
		matchedRunner = runner
	}
	if matchedRunner == nil {
		return nil, fmt.Errorf("Message Inbox for Agent %q is unavailable", agentID)
	}
	return matchedRunner, nil
}

// sendWorkspaceDaemonAgentFrame resolves one unambiguous Agent Inbox and sends
// a frame on that Workspace's current serialized Runner writer.
// Message delivery, launch reconciliation, and Activity share this current
// Runner connection without falling back to the legacy Task wakeup socket.
func (d *Daemon) sendWorkspaceDaemonAgentFrame(agentID, eventType string, payload any) bool {
	if d == nil || agentID == "" {
		return false
	}
	runner, err := d.resolveWorkspaceDaemonByAgent(agentID)
	if err != nil {
		return false
	}
	return runner.sendAgentFrame(eventType, payload)
}
