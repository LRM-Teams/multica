package daemon

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

// workspaceDaemonDeliveryDispatcher keeps the WorkspaceDaemon socket reader independent
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

func newWorkspaceSessionDeliveryDispatcher(ctx context.Context, handle func(context.Context, protocol.AgentDeliverPayload)) *workspaceDaemonDeliveryDispatcher {
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

// adoptWorkspaceSession publishes the Binding child's owned session as the
// Credential Proxy / handoff lookup. The Binding child constructs this session
// before Run; if it is not adopted, ensureWorkspaceSession could mint a second
// empty Inbox and message send would return 409 "Message coordinator is unavailable".
func (d *WorkspaceDaemonCore) adoptWorkspaceSession(runner *workspaceSession) error {
	if d == nil {
		return errors.New("WorkspaceDaemonCore is required")
	}
	if runner == nil || runner.WorkspaceID() == "" {
		return errors.New("WorkspaceDaemon identity is required")
	}
	workspaceID := strings.TrimSpace(runner.WorkspaceID())
	if configured := strings.TrimSpace(d.cfg.WorkspaceID); configured != "" && configured != workspaceID {
		return fmt.Errorf("WorkspaceDaemon %q does not belong to WorkspaceDaemonCore %q", workspaceID, configured)
	}
	d.workspaceSessionMu.Lock()
	if current := d.workspaceSession; current != nil && current != runner && current.WorkspaceID() != workspaceID {
		d.workspaceSessionMu.Unlock()
		return fmt.Errorf("WorkspaceDaemonCore already owns WorkspaceDaemon %q", current.WorkspaceID())
	}
	d.workspaceSession = runner
	d.workspaceSessionMu.Unlock()
	return nil
}

func (d *WorkspaceDaemonCore) detachWorkspaceSession(runner *workspaceSession) {
	if d == nil || runner == nil {
		return
	}
	d.workspaceSessionMu.Lock()
	if d.workspaceSession == runner {
		d.workspaceSession = nil
	}
	d.workspaceSessionMu.Unlock()
}

func (d *WorkspaceDaemonCore) currentWorkspaceSession(workspaceID string) *workspaceSession {
	if d == nil {
		return nil
	}
	d.workspaceSessionMu.RLock()
	runner := d.workspaceSession
	d.workspaceSessionMu.RUnlock()
	if runner == nil || runner.WorkspaceID() != strings.TrimSpace(workspaceID) {
		return nil
	}
	return runner
}

// ensureWorkspaceSession creates the state-owning session before a legacy
// lifecycle path opens an Inbox. The session is not started here: socket
// ownership remains exclusively with workspaceSession.Run.
func (d *WorkspaceDaemonCore) ensureWorkspaceSession(workspaceID string) (*workspaceSession, error) {
	workspaceID = strings.TrimSpace(workspaceID)
	if workspaceID == "" {
		return nil, errors.New("Workspace identity is required")
	}
	if runner := d.currentWorkspaceSession(workspaceID); runner != nil {
		return runner, nil
	}
	runner, err := d.newWorkspaceSession(workspaceID)
	if err != nil {
		return nil, err
	}
	d.workspaceSessionMu.Lock()
	if current := d.workspaceSession; current != nil {
		d.workspaceSessionMu.Unlock()
		if current.WorkspaceID() == workspaceID {
			return current, nil
		}
		return nil, fmt.Errorf("WorkspaceDaemonCore already owns WorkspaceDaemon %q", current.WorkspaceID())
	}
	d.workspaceSession = runner
	d.workspaceSessionMu.Unlock()
	return runner, nil
}

// resolveWorkspaceSessionByAgent preserves machine-local callers that predate a
// Workspace parameter. It returns the owning session, never its Inbox internals,
// and fails closed instead of selecting an ambiguous Workspace implicitly.
func (d *WorkspaceDaemonCore) resolveWorkspaceSessionByAgent(agentID string) (*workspaceSession, error) {
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return nil, errors.New("Agent identity is required")
	}
	d.workspaceSessionMu.RLock()
	runner := d.workspaceSession
	d.workspaceSessionMu.RUnlock()
	if runner == nil || !runner.hasMessageInbox(agentID) {
		return nil, fmt.Errorf("Message Inbox for Agent %q is unavailable", agentID)
	}
	return runner, nil
}

// sendWorkspaceDaemonAgentFrame resolves one unambiguous Agent Inbox and sends
// a frame on that Workspace's current serialized WorkspaceDaemon writer.
// Message delivery, launch reconciliation, and Activity share this current
// WorkspaceDaemon connection without falling back to the legacy Task wakeup socket.
func (d *WorkspaceDaemonCore) sendWorkspaceDaemonAgentFrame(agentID, eventType string, payload any) bool {
	if d == nil || agentID == "" {
		return false
	}
	runner, err := d.resolveWorkspaceSessionByAgent(agentID)
	if err != nil {
		return false
	}
	return runner.sendAgentFrame(eventType, payload)
}
