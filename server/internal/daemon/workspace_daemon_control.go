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

type workspaceDaemonControlIdentity = computer.WorkspaceDaemonIdentity

// workspaceDaemonComputerControl adapts ComputerCore control to WorkspaceDaemon interfaces.
type workspaceDaemonComputerControl struct {
	client *computer.ComputerControlClient
}

func newWorkspaceDaemonComputerControl(endpoint, token string, identity workspaceDaemonControlIdentity) *workspaceDaemonComputerControl {
	return &workspaceDaemonComputerControl{client: computer.NewComputerControlClient(endpoint, token, identity)}
}

func (client *workspaceDaemonComputerControl) recordDiagnostic(ctx context.Context, workspaceID string, event diagnosticlog.Event) error {
	return client.client.RecordDiagnostic(ctx, workspaceID, event)
}

func (client *workspaceDaemonComputerControl) forwardMachineActions(ctx context.Context, ack HeartbeatResponse) error {
	return client.client.ForwardComputerControl(ctx, ack)
}

// handleComputerControlCommand is the Raft 1.0.16 child callback: the
// DaemonCore connect socket received computer:upgrade / computer:restart.
// The WorkspaceDaemon forwards the machine action to ComputerCore, which
// drains sibling WorkspaceDaemons and performs the restart or upgrade.
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

func (d *Daemon) handleWorkspaceDaemonControlAck(ctx context.Context, ack *HeartbeatResponse) {
	if d == nil || ack == nil {
		return
	}
	if d.computerControl == nil {
		d.handleWSHeartbeatAck(ctx, ack)
		return
	}
	local := *ack
	local.PendingUpdate = nil
	local.PendingRestart = nil
	local.ReleaseManifestBaseURL = ""
	d.handleWSHeartbeatAck(ctx, &local)

	machine := HeartbeatResponse{
		RuntimeID:              ack.RuntimeID,
		Status:                 ack.Status,
		PendingUpdate:          ack.PendingUpdate,
		PendingRestart:         ack.PendingRestart,
		ReleaseManifestBaseURL: ack.ReleaseManifestBaseURL,
	}
	if machine.PendingUpdate == nil && machine.PendingRestart == nil && machine.ReleaseManifestBaseURL == "" {
		return
	}
	forwardCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := d.computerControl.forwardMachineActions(forwardCtx, machine); err != nil && d.logger != nil {
		d.logger.Warn("forward WorkspaceDaemon machine action to ComputerCore failed", "runtime_id", ack.RuntimeID, "reason", "computer_unavailable")
	}
}

func (d *Daemon) controlPlaneHeartbeatPayload(runtimeID string) protocol.DaemonHeartbeatRequestPayload {
	return protocol.DaemonHeartbeatRequestPayload{
		RuntimeID:                 runtimeID,
		SupportsBatchImport:       true,
		SupportsMemoryCuration:    true,
		ActiveMemoryCurationRunID: d.activeMemoryCurationRun(runtimeID),
	}
}

func (d *Daemon) setComputerUpgradeEmit(emit func(string, any) error) {
	if d == nil {
		return
	}
	d.mu.Lock()
	d.computerUpgradeEmit = emit
	d.mu.Unlock()
}

func (d *Daemon) emitComputerUpgrade(eventType string, payload any) error {
	if d == nil {
		return errors.New("DaemonCore is unavailable")
	}
	d.mu.Lock()
	emit := d.computerUpgradeEmit
	d.mu.Unlock()
	if emit == nil {
		return errors.New("Binding socket is not connected")
	}
	return emit(eventType, payload)
}

func (d *Daemon) handleComputerControlCommand(ctx context.Context, action string, command protocol.ComputerUpgradePayload) error {
	if d == nil {
		return errors.New("DaemonCore is unavailable")
	}
	switch action {
	case protocol.EventComputerUpgrade:
		if d.computerControl == nil {
			// Raft 1.0.16: a DaemonCore not constructed by Computer
			// ignores computer:upgrade instead of inventing a ComputerCore path.
			if d.logger != nil {
				d.logger.Info("ignoring computer:upgrade — not launched by a Computer service")
			}
			return nil
		}
		// Drain is WorkspaceDaemon-owned and may wait for active provider work; do not
		// hold the service IPC request open while the service claims the
		// machine-wide operation.
		go func() { _ = d.beginWorkspaceDaemonDrain(ctx) }()
		forwardCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		if err := d.computerControl.client.RequestComputerUpgrade(forwardCtx, command); err != nil {
			d.releaseClaimBarrier()
			return err
		}
		return nil
	case protocol.EventComputerRestart:
		ack := HeartbeatResponse{Status: "ok", PendingRestart: &PendingRestart{ID: strings.TrimSpace(command.RequestID)}}
		if d.computerControl == nil {
			return errors.New("ComputerCore callback is unavailable")
		}
		forwardCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		return d.computerControl.forwardMachineActions(forwardCtx, ack)
	default:
		return fmt.Errorf("unsupported Computer control action %q", action)
	}
}

func (d *Daemon) handleComputerWorkDigestCommand(ctx context.Context, command protocol.ComputerWorkDigestPayload) (protocol.WorkDigest, error) {
	if d == nil {
		return protocol.WorkDigest{}, errors.New("DaemonCore is unavailable")
	}
	if d.computerControl == nil {
		return protocol.WorkDigest{}, errors.New("ComputerCore callback is unavailable")
	}
	return d.computerControl.client.HarvestWorkDigest(ctx, command)
}

func (d *Daemon) handleComputerWorkJournalCommand(ctx context.Context, command protocol.ComputerWorkJournalPayload) (bool, error) {
	if d == nil {
		return false, errors.New("DaemonCore is unavailable")
	}
	if d.computerControl == nil {
		return false, errors.New("ComputerCore callback is unavailable")
	}
	return d.computerControl.client.SetWorkJournalEnabled(ctx, command)
}

func (client *workspaceDaemonComputerControl) reportRuntimeSet(ctx context.Context, runtimes []Runtime, daemonToken, expiresAt string) error {
	return client.client.ReportRuntimeSet(ctx, runtimes, daemonToken, expiresAt)
}

type workspaceDaemonDiagnosticEnvelope struct {
	workspaceID string
	event       *diagnosticlog.Event
}

type workspaceDaemonDiagnosticForwarder struct {
	client *workspaceDaemonComputerControl
	ctx    context.Context
	cancel context.CancelFunc
	queue  chan workspaceDaemonDiagnosticEnvelope
	done   chan struct{}
	once   sync.Once
}

func newWorkspaceDaemonDiagnosticForwarder(client *workspaceDaemonComputerControl) *workspaceDaemonDiagnosticForwarder {
	ctx, cancel := context.WithCancel(context.Background())
	forwarder := &workspaceDaemonDiagnosticForwarder{
		client: client, ctx: ctx, cancel: cancel,
		queue: make(chan workspaceDaemonDiagnosticEnvelope, 256), done: make(chan struct{}),
	}
	go forwarder.run()
	return forwarder
}

func (forwarder *workspaceDaemonDiagnosticForwarder) record(workspaceID string, event diagnosticlog.Event) error {
	if forwarder == nil || forwarder.client == nil {
		return errors.New("Computer diagnostic aggregation is unavailable")
	}
	envelope := workspaceDaemonDiagnosticEnvelope{workspaceID: workspaceID, event: &event}
	select {
	case <-forwarder.ctx.Done():
		return errors.New("Computer diagnostic aggregation is closed")
	case forwarder.queue <- envelope:
		return nil
	default:
		return errors.New("Computer diagnostic aggregation queue is full")
	}
}

func (forwarder *workspaceDaemonDiagnosticForwarder) run() {
	defer close(forwarder.done)
	for {
		select {
		case <-forwarder.ctx.Done():
			return
		case envelope := <-forwarder.queue:
			if envelope.event != nil {
				_ = forwarder.client.recordDiagnostic(forwarder.ctx, envelope.workspaceID, *envelope.event)
			}
		}
	}
}

func (forwarder *workspaceDaemonDiagnosticForwarder) Close() {
	if forwarder == nil {
		return
	}
	forwarder.once.Do(func() {
		forwarder.cancel()
		<-forwarder.done
	})
}
