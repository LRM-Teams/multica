package daemon

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/multica-ai/multica/server/internal/computer"
	"github.com/multica-ai/multica/server/internal/diagnosticlog"
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
