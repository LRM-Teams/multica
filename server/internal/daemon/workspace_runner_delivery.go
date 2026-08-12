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

// workspaceRunnerDeliveryDispatcher keeps the Runner socket reader independent
// from provider startup and Message turns. Deliveries for one Agent remain
// ordered, while different Agents cannot head-of-line block each other.
type workspaceRunnerDeliveryDispatcher struct {
	ctx    context.Context
	handle func(context.Context, protocol.AgentDeliverPayload)

	mu      sync.Mutex
	queues  map[string][]protocol.AgentDeliverPayload
	running map[string]bool
	paused  map[string]string
}

func newWorkspaceRunnerDeliveryDispatcher(ctx context.Context, handle func(context.Context, protocol.AgentDeliverPayload)) *workspaceRunnerDeliveryDispatcher {
	return &workspaceRunnerDeliveryDispatcher{
		ctx: ctx, handle: handle,
		queues: make(map[string][]protocol.AgentDeliverPayload), running: make(map[string]bool), paused: make(map[string]string),
	}
}

// Pause holds only this Agent's deliveries while agent:start establishes its
// provider. The socket reader and every other Agent remain independent.
func (d *workspaceRunnerDeliveryDispatcher) Pause(agentID, launchID string) bool {
	if d == nil || strings.TrimSpace(agentID) == "" || strings.TrimSpace(launchID) == "" {
		return false
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.ctx.Err() != nil || d.paused[agentID] == launchID {
		return false
	}
	d.paused[agentID] = launchID
	return true
}

// Resume releases deliveries in their original order after agent:start has
// been accepted. No delivery can be ACKed before that point.
func (d *workspaceRunnerDeliveryDispatcher) Resume(agentID, launchID string) {
	if d == nil {
		return
	}
	d.mu.Lock()
	if d.paused[agentID] != launchID {
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

// RejectStart forgets only the volatile buffer. The server still owns every
// unACKed delivery and will replay it after a later successful start.
func (d *workspaceRunnerDeliveryDispatcher) RejectStart(agentID, launchID string) {
	if d == nil {
		return
	}
	d.mu.Lock()
	if d.paused[agentID] != launchID {
		d.mu.Unlock()
		return
	}
	delete(d.paused, agentID)
	delete(d.queues, agentID)
	d.mu.Unlock()
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
	if d.paused[delivery.AgentID] == "" && !d.running[delivery.AgentID] {
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
		if d.paused[agentID] != "" {
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

func (d *Daemon) attachWorkspaceRunner(runner *WorkspaceRunner) {
	if d == nil || runner == nil || runner.WorkspaceID() == "" {
		return
	}
	d.workspaceRunnerMu.Lock()
	if d.workspaceRunners == nil {
		d.workspaceRunners = make(map[string]*WorkspaceRunner)
	}
	d.workspaceRunners[runner.WorkspaceID()] = runner
	d.workspaceRunnerMu.Unlock()
}

func (d *Daemon) detachWorkspaceRunner(runner *WorkspaceRunner) {
	if d == nil || runner == nil {
		return
	}
	d.workspaceRunnerMu.Lock()
	if d.workspaceRunners[runner.WorkspaceID()] == runner {
		delete(d.workspaceRunners, runner.WorkspaceID())
	}
	d.workspaceRunnerMu.Unlock()
}

func (d *Daemon) currentWorkspaceRunner(workspaceID string) *WorkspaceRunner {
	if d == nil {
		return nil
	}
	d.workspaceRunnerMu.RLock()
	runner := d.workspaceRunners[strings.TrimSpace(workspaceID)]
	d.workspaceRunnerMu.RUnlock()
	return runner
}

// ensureWorkspaceRunner creates a state-owning Runner before a legacy
// lifecycle path opens an Inbox. The Runner is not started here: socket
// ownership remains exclusively with WorkspaceRunner.Run.
func (d *Daemon) ensureWorkspaceRunner(workspaceID string) (*WorkspaceRunner, error) {
	workspaceID = strings.TrimSpace(workspaceID)
	if workspaceID == "" {
		return nil, errors.New("Workspace identity is required")
	}
	if runner := d.currentWorkspaceRunner(workspaceID); runner != nil {
		return runner, nil
	}
	runner, err := d.newWorkspaceRunner(workspaceID)
	if err != nil {
		return nil, err
	}
	d.workspaceRunnerMu.Lock()
	if current := d.workspaceRunners[workspaceID]; current != nil {
		d.workspaceRunnerMu.Unlock()
		return current, nil
	}
	if d.workspaceRunners == nil {
		d.workspaceRunners = make(map[string]*WorkspaceRunner)
	}
	d.workspaceRunners[workspaceID] = runner
	d.workspaceRunnerMu.Unlock()
	return runner, nil
}

func (d *Daemon) currentWorkspaceRunners() []*WorkspaceRunner {
	if d == nil {
		return nil
	}
	d.workspaceRunnerMu.RLock()
	workspaceIDs := make([]string, 0, len(d.workspaceRunners))
	for workspaceID := range d.workspaceRunners {
		workspaceIDs = append(workspaceIDs, workspaceID)
	}
	sort.Strings(workspaceIDs)
	runners := make([]*WorkspaceRunner, 0, len(workspaceIDs))
	for _, workspaceID := range workspaceIDs {
		if runner := d.workspaceRunners[workspaceID]; runner != nil {
			runners = append(runners, runner)
		}
	}
	d.workspaceRunnerMu.RUnlock()
	return runners
}

// resolveWorkspaceRunnerByAgent preserves machine-local callers that predate a
// Workspace parameter. It returns the owning Runner, never its Inbox internals,
// and fails closed instead of selecting an ambiguous Workspace implicitly.
func (d *Daemon) resolveWorkspaceRunnerByAgent(agentID string) (*WorkspaceRunner, error) {
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return nil, errors.New("Agent identity is required")
	}
	var matchedRunner *WorkspaceRunner
	for _, runner := range d.currentWorkspaceRunners() {
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

// sendWorkspaceRunnerAgentFrame resolves one unambiguous Agent Inbox and sends
// a frame on that Workspace's current serialized Runner writer.
// Message delivery, launch reconciliation, and Activity share this current
// Runner connection without falling back to the legacy Task wakeup socket.
func (d *Daemon) sendWorkspaceRunnerAgentFrame(agentID, eventType string, payload any) bool {
	if d == nil || agentID == "" {
		return false
	}
	runner, err := d.resolveWorkspaceRunnerByAgent(agentID)
	if err != nil {
		return false
	}
	return runner.sendAgentFrame(eventType, payload)
}
