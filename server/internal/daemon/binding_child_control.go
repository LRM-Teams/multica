package daemon

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/multica-ai/multica/server/internal/computer"
	"github.com/multica-ai/multica/server/internal/diagnosticlog"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

type bindingChildControlIdentity = computer.BindingChildIdentity

// bindingHostControlClient adapts the Computer-owned process Interface to the
// daemon package's Workspace execution interfaces.
type bindingHostControlClient struct{ client *computer.HostControlClient }

func newBindingHostControlClient(baseURL, token string, identity bindingChildControlIdentity) *bindingHostControlClient {
	return &bindingHostControlClient{client: computer.NewHostControlClient(baseURL, token, identity)}
}

func (client *bindingHostControlClient) Attest(ctx context.Context) error {
	return client.client.Attest(ctx)
}

func (client *bindingHostControlClient) AwaitAttest(ctx context.Context) error {
	return client.client.AwaitAttest(ctx)
}

func (client *bindingHostControlClient) recordDiagnostic(ctx context.Context, workspaceID string, event diagnosticlog.Event) error {
	return client.client.RecordDiagnostic(ctx, workspaceID, event)
}

func (client *bindingHostControlClient) recordLifecycleDiagnostic(ctx context.Context, transition agentLifecycleTransition) error {
	return client.client.RecordLifecycleDiagnostic(ctx, transition)
}

func (client *bindingHostControlClient) forwardMachineActions(ctx context.Context, ack HeartbeatResponse) error {
	return client.client.ForwardMachineActions(ctx, ack)
}

// handleComputerControlCommand is the Raft 1.0.16 child callback: the
// DaemonCore connect socket received computer:upgrade / computer:restart.
// Binding children do not swap the machine binary; they forward the command
// to Computer Host through the injected Host control seam.
func (d *Daemon) handleWSHeartbeatAck(ctx context.Context, ack *HeartbeatResponse) {
	if ack == nil || ack.RuntimeID == "" {
		return
	}
	if ack.RuntimeGone {
		go d.handleRuntimeGone(ack.RuntimeID)
		return
	}
	d.handleHeartbeatActions(ctx, ack.RuntimeID, ack)
}

func (d *Daemon) handleWorkspaceRunnerControlAck(ctx context.Context, ack *HeartbeatResponse) {
	if d == nil || ack == nil {
		return
	}
	if d.bindingHostControl == nil {
		d.handleWSHeartbeatAck(ctx, ack)
		return
	}
	local := *ack
	local.PendingUpdate = nil
	local.PendingMachineUpgrade = nil
	local.PendingRestart = nil
	local.ReleaseManifestBaseURL = ""
	d.handleWSHeartbeatAck(ctx, &local)

	machine := HeartbeatResponse{
		RuntimeID:              ack.RuntimeID,
		Status:                 ack.Status,
		PendingUpdate:          ack.PendingUpdate,
		PendingMachineUpgrade:  ack.PendingMachineUpgrade,
		PendingRestart:         ack.PendingRestart,
		ReleaseManifestBaseURL: ack.ReleaseManifestBaseURL,
	}
	if machine.PendingUpdate == nil && machine.PendingMachineUpgrade == nil && machine.PendingRestart == nil && machine.ReleaseManifestBaseURL == "" {
		return
	}
	forwardCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := d.bindingHostControl.forwardMachineActions(forwardCtx, machine); err != nil && d.logger != nil {
		d.logger.Warn("forward Binding child machine action to Host failed", "runtime_id", ack.RuntimeID, "reason", "host_unavailable")
	}
}

func (d *Daemon) controlPlaneHeartbeatPayload(runtimeID string) protocol.DaemonHeartbeatRequestPayload {
	return protocol.DaemonHeartbeatRequestPayload{
		RuntimeID:                 runtimeID,
		ComputerGeneration:        d.cfg.ComputerGeneration,
		SupportsBatchImport:       true,
		SupportsMemoryCuration:    true,
		ActiveMemoryCurationRunID: d.activeMemoryCurationRun(runtimeID),
	}
}

func (d *Daemon) handleComputerControlCommand(ctx context.Context, action string, command protocol.ComputerUpgradePayload) error {
	if d == nil {
		return errors.New("DaemonCore is unavailable")
	}
	ack := HeartbeatResponse{Status: "ok"}
	switch action {
	case protocol.EventComputerUpgrade:
		ack.PendingMachineUpgrade = &PendingMachineUpgrade{ID: command.Operation(), TargetVersion: command.TargetVersion}
	case protocol.EventComputerRestart:
		ack.PendingRestart = &PendingRestart{ID: command.Operation()}
	default:
		return fmt.Errorf("unsupported Computer control action %q", action)
	}
	if d.bindingHostControl == nil {
		return errors.New("Computer Host callback is unavailable")
	}
	forwardCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := d.bindingHostControl.forwardMachineActions(forwardCtx, ack); err != nil {
		return err
	}
	return nil
}

func (client *bindingHostControlClient) reportRuntimeSet(ctx context.Context, runtimes []Runtime, daemonToken, expiresAt string) error {
	return client.client.ReportRuntimeSet(ctx, runtimes, daemonToken, expiresAt)
}

type bindingChildDiagnosticEnvelope struct {
	workspaceID string
	event       *diagnosticlog.Event
	transition  *agentLifecycleTransition
}

type bindingChildDiagnosticForwarder struct {
	client *bindingHostControlClient
	ctx    context.Context
	cancel context.CancelFunc
	queue  chan bindingChildDiagnosticEnvelope
	done   chan struct{}
	once   sync.Once
}

func newBindingChildDiagnosticForwarder(client *bindingHostControlClient) *bindingChildDiagnosticForwarder {
	ctx, cancel := context.WithCancel(context.Background())
	forwarder := &bindingChildDiagnosticForwarder{
		client: client, ctx: ctx, cancel: cancel,
		queue: make(chan bindingChildDiagnosticEnvelope, 256), done: make(chan struct{}),
	}
	go forwarder.run()
	return forwarder
}

func (forwarder *bindingChildDiagnosticForwarder) record(workspaceID string, event diagnosticlog.Event) error {
	if forwarder == nil || forwarder.client == nil {
		return errors.New("Binding Host diagnostic aggregation is unavailable")
	}
	envelope := bindingChildDiagnosticEnvelope{workspaceID: workspaceID, event: &event}
	select {
	case <-forwarder.ctx.Done():
		return errors.New("Binding Host diagnostic aggregation is closed")
	case forwarder.queue <- envelope:
		return nil
	default:
		return errors.New("Binding Host diagnostic aggregation queue is full")
	}
}

func (forwarder *bindingChildDiagnosticForwarder) recordLifecycle(transition agentLifecycleTransition) error {
	if forwarder == nil || forwarder.client == nil {
		return errors.New("Binding Host lifecycle diagnostic aggregation is unavailable")
	}
	envelope := bindingChildDiagnosticEnvelope{transition: &transition}
	select {
	case <-forwarder.ctx.Done():
		return errors.New("Binding Host lifecycle diagnostic aggregation is closed")
	case forwarder.queue <- envelope:
		return nil
	default:
		return errors.New("Binding Host diagnostic aggregation queue is full")
	}
}

func (forwarder *bindingChildDiagnosticForwarder) run() {
	defer close(forwarder.done)
	for {
		select {
		case <-forwarder.ctx.Done():
			return
		case envelope := <-forwarder.queue:
			if envelope.event != nil {
				_ = forwarder.client.recordDiagnostic(forwarder.ctx, envelope.workspaceID, *envelope.event)
			}
			if envelope.transition != nil {
				_ = forwarder.client.recordLifecycleDiagnostic(forwarder.ctx, *envelope.transition)
			}
		}
	}
}

func (forwarder *bindingChildDiagnosticForwarder) Close() {
	if forwarder == nil {
		return
	}
	forwarder.once.Do(func() {
		forwarder.cancel()
		<-forwarder.done
	})
}

type remoteAgentProcessAdmission struct {
	client *bindingHostControlClient
	ctx    context.Context
	cancel context.CancelFunc

	mu    sync.Mutex
	polls map[string]context.CancelFunc
}

func newRemoteAgentProcessAdmission(client *bindingHostControlClient) *remoteAgentProcessAdmission {
	ctx, cancel := context.WithCancel(context.Background())
	return &remoteAgentProcessAdmission{client: client, ctx: ctx, cancel: cancel, polls: make(map[string]context.CancelFunc)}
}

func (admission *remoteAgentProcessAdmission) Acquire(request agentProcessCapacityRequest) (agentProcessCapacityGrant, bool) {
	grant := agentProcessCapacityGrant{ID: request.LaunchID, LaunchID: request.LaunchID, AgentID: request.AgentID, RuntimeID: request.RuntimeID}
	if admission == nil || admission.client == nil || strings.TrimSpace(request.LaunchID) == "" {
		return grant, false
	}
	remoteGrant, admitted, err := admission.client.client.AcquireCapacity(admission.ctx, computer.ProcessCapacityRequest{
		WorkspaceID: request.WorkspaceID, AgentID: request.AgentID, RuntimeID: request.RuntimeID, LaunchID: request.LaunchID,
	})
	if err == nil && remoteGrant.LaunchID != "" {
		grant = remoteGrant
	}
	if err == nil && admitted {
		return grant, true
	}
	admission.pollUntilActive(grant, request, request.Waiter)
	return grant, false
}

func (admission *remoteAgentProcessAdmission) Cancel(grant agentProcessCapacityGrant) {
	admission.stopPoll(grant.LaunchID)
	if admission == nil || admission.client == nil || grant.LaunchID == "" {
		return
	}
	_ = admission.client.client.CancelCapacity(context.Background(), grant)
}

func (admission *remoteAgentProcessAdmission) Release(grant agentProcessCapacityGrant) {
	admission.stopPoll(grant.LaunchID)
	if admission == nil || admission.client == nil || grant.LaunchID == "" {
		return
	}
	_ = admission.client.client.ReleaseCapacity(context.Background(), grant)
}

func (admission *remoteAgentProcessAdmission) Active(grant agentProcessCapacityGrant) bool {
	if admission == nil || admission.client == nil || grant.LaunchID == "" {
		return false
	}
	active, err := admission.client.client.CapacityActive(admission.ctx, grant)
	return err == nil && active
}

func (admission *remoteAgentProcessAdmission) pollUntilActive(grant agentProcessCapacityGrant, request agentProcessCapacityRequest, waiter agentProcessCapacityWaiter) {
	if admission == nil || grant.LaunchID == "" || waiter == nil {
		return
	}
	admission.mu.Lock()
	if _, exists := admission.polls[grant.LaunchID]; exists {
		admission.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(admission.ctx)
	admission.polls[grant.LaunchID] = cancel
	admission.mu.Unlock()
	go func() {
		ticker := time.NewTicker(25 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				current, active, err := admission.client.client.AcquireCapacity(ctx, computer.ProcessCapacityRequest{
					WorkspaceID: request.WorkspaceID, AgentID: request.AgentID,
					RuntimeID: request.RuntimeID, LaunchID: request.LaunchID,
				})
				if err != nil || !active {
					continue
				}
				admission.stopPoll(grant.LaunchID)
				waiter(current)
				return
			}
		}
	}()
}

func (admission *remoteAgentProcessAdmission) stopPoll(launchID string) {
	if admission == nil || launchID == "" {
		return
	}
	admission.mu.Lock()
	cancel := admission.polls[launchID]
	delete(admission.polls, launchID)
	admission.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (admission *remoteAgentProcessAdmission) Close() {
	if admission == nil {
		return
	}
	admission.cancel()
	admission.mu.Lock()
	for launchID, cancel := range admission.polls {
		cancel()
		delete(admission.polls, launchID)
	}
	admission.mu.Unlock()
}
