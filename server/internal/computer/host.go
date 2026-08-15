package computer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/multica-ai/multica/server/internal/diagnosticlog"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// HostConfig is the Computer's process-supervision boundary. Binding execution
// details enter only through the child process launcher and control callbacks.
type HostConfig struct {
	Spawn             BindingChildSpawner
	Now               func() time.Time
	ReadyTimeout      time.Duration
	ReconcileInterval time.Duration
	Logger            *slog.Logger
	ControlToken      string
	MaxAgentProcesses int
	ControlCallbacks  HostControlCallbacks
}

// Host owns every machine-scoped Binding concern: desired-vs-actual child
// processes, generation/PID fencing, crash policy, capacity admission, local
// control, and cross-child Machine Upgrade preparation.
type Host struct {
	supervisor        *BindingSupervisor
	control           *HostControl
	reconcileInterval time.Duration
	logger            *slog.Logger

	runtimeMu         sync.RWMutex
	runtimeSets       map[string]hostBindingRuntimeSet
	upgrade           *hostMachineUpgrade
	diagnosticStore   *diagnosticlog.Store
	diagnosticMu      sync.Mutex
	diagnosticLoggers map[string]*diagnosticlog.Logger
	processIdentity   HostProcessIdentity
}

type hostBindingRuntime struct {
	ID          string `json:"id"`
	WorkspaceID string `json:"workspace_id"`
	Provider    string `json:"provider"`
}

type hostBindingRuntimeSet struct {
	Identity    BindingChildIdentity
	Runtimes    []hostBindingRuntime
	DaemonToken string
	ExpiresAt   time.Time
}

func NewHost(config HostConfig) (*Host, error) {
	if config.Spawn == nil {
		return nil, errors.New("Computer Binding child spawner is required")
	}
	if config.ReconcileInterval <= 0 {
		config.ReconcileInterval = RunnerReconcileInterval
	}
	host := &Host{
		reconcileInterval: config.ReconcileInterval,
		logger:            config.Logger,
		runtimeSets:       make(map[string]hostBindingRuntimeSet),
		diagnosticLoggers: make(map[string]*diagnosticlog.Logger),
	}
	supervisor, err := NewBindingSupervisor(BindingSupervisorConfig{
		Spawn: config.Spawn, Now: config.Now, ReadyTimeout: config.ReadyTimeout, Logger: config.Logger,
		Released: func(identity BindingChildIdentity) {
			if host.control != nil {
				host.control.Release(identity)
			}
		},
	})
	if err != nil {
		return nil, err
	}
	external := config.ControlCallbacks
	callbacks := external
	callbacks.RuntimeSet = func(ctx context.Context, identity BindingChildIdentity, raw json.RawMessage, token string, expiresAt time.Time) error {
		var runtimes []hostBindingRuntime
		if err := json.Unmarshal(raw, &runtimes); err != nil {
			return errors.New("invalid Binding child Runtime set")
		}
		seen := make(map[string]struct{}, len(runtimes))
		for i := range runtimes {
			runtimes[i].ID = strings.TrimSpace(runtimes[i].ID)
			runtimes[i].WorkspaceID = strings.TrimSpace(runtimes[i].WorkspaceID)
			if runtimes[i].ID == "" || runtimes[i].WorkspaceID != strings.TrimSpace(identity.WorkspaceID) {
				return errors.New("Binding child Runtime belongs to another Workspace")
			}
			if _, duplicate := seen[runtimes[i].ID]; duplicate {
				return errors.New("Binding child Runtime set contains duplicates")
			}
			seen[runtimes[i].ID] = struct{}{}
		}
		host.runtimeMu.Lock()
		host.runtimeSets[identity.WorkspaceID] = hostBindingRuntimeSet{
			Identity: identity, Runtimes: append([]hostBindingRuntime(nil), runtimes...),
			DaemonToken: strings.TrimSpace(token), ExpiresAt: expiresAt,
		}
		host.runtimeMu.Unlock()
		if external.RuntimeSet != nil {
			return external.RuntimeSet(ctx, identity, raw, token, expiresAt)
		}
		return nil
	}
	callbacks.Diagnostic = func(ctx context.Context, identity BindingChildIdentity, workspaceID string, event diagnosticlog.Event) error {
		host.recordBindingDiagnostic(identity, workspaceID, event)
		if external.Diagnostic != nil {
			return external.Diagnostic(ctx, identity, workspaceID, event)
		}
		return nil
	}
	callbacks.LifecycleDiagnostic = func(ctx context.Context, identity BindingChildIdentity, raw json.RawMessage) error {
		if external.LifecycleDiagnostic != nil {
			return external.LifecycleDiagnostic(ctx, identity, raw)
		}
		return nil
	}
	callbacks.MachineActions = func(ctx context.Context, identity BindingChildIdentity, raw json.RawMessage) error {
		if host.upgrade != nil {
			if err := host.upgrade.handleChildAction(ctx, identity, raw); err != nil {
				return err
			}
		}
		if external.MachineActions != nil {
			return external.MachineActions(ctx, identity, raw)
		}
		return nil
	}
	callbacks.PrepareUpgrade = func(ctx context.Context, identity BindingChildIdentity, raw json.RawMessage) (any, error) {
		if host.upgrade != nil {
			return host.upgrade.prepareChildUpgrade(ctx, identity, raw)
		}
		if external.PrepareUpgrade != nil {
			return external.PrepareUpgrade(ctx, identity, raw)
		}
		return nil, errors.New("Computer Machine Upgrade coordinator is unavailable")
	}
	callbacks.Released = func(identity BindingChildIdentity) {
		host.runtimeMu.Lock()
		if current, ok := host.runtimeSets[identity.WorkspaceID]; ok && current.Identity == identity {
			delete(host.runtimeSets, identity.WorkspaceID)
		}
		host.runtimeMu.Unlock()
		if host.upgrade != nil {
			host.upgrade.observeInitiatorExit(identity)
		}
		if external.Released != nil {
			external.Released(identity)
		}
	}
	callbacks.Current = supervisor.Current
	host.supervisor = supervisor
	host.control = NewHostControl(config.ControlToken, NewProcessCapacity(config.MaxAgentProcesses), callbacks)
	host.upgrade = newHostMachineUpgrade(host, hostMachineUpgradeConfig{})
	return host, nil
}

// Run continuously reconciles the desired Binding set until the Computer
// process exits. The periodic repair policy belongs to the Computer, not to a
// Binding daemon.
func (host *Host) Run(ctx context.Context, desired func() []string, changes <-chan struct{}) {
	if host == nil || host.supervisor == nil || desired == nil {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	host.supervisor.Reconcile(ctx, desired())
	ticker := time.NewTicker(host.reconcileInterval)
	defer ticker.Stop()
	defer host.supervisor.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case _, ok := <-changes:
			if !ok {
				changes = nil
				continue
			}
			host.supervisor.Reconcile(ctx, desired())
		case <-ticker.C:
			host.supervisor.Reconcile(ctx, desired())
		}
	}
}

func (host *Host) Reconcile(ctx context.Context, desiredWorkspaceIDs []string) {
	if host != nil && host.supervisor != nil {
		host.supervisor.Reconcile(ctx, desiredWorkspaceIDs)
	}
}

func (host *Host) Stop() {
	if host != nil && host.supervisor != nil {
		host.supervisor.Stop()
	}
}

func (host *Host) Current(identity BindingChildIdentity) bool {
	return host != nil && host.supervisor != nil && host.supervisor.Current(identity)
}

func (host *Host) Snapshot(workspaceID string) (RunnerRecord, int, bool) {
	if host == nil || host.supervisor == nil {
		return RunnerRecord{}, 0, false
	}
	return host.supervisor.Snapshot(workspaceID)
}

// WaitReady fences Computer readiness on every desired Binding child reaching
// its real Workspace Runner Ready seam. A degraded child is terminal for this
// startup attempt; crash/backoff remains retryable until ctx expires.
func (host *Host) WaitReady(ctx context.Context, desiredWorkspaceIDs []string) error {
	if host == nil || host.supervisor == nil {
		return errors.New("Computer Host is unavailable")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		allReady := true
		for _, workspaceID := range desiredWorkspaceIDs {
			workspaceID = strings.TrimSpace(workspaceID)
			if workspaceID == "" {
				continue
			}
			record, _, ok := host.supervisor.Snapshot(workspaceID)
			if ok && record.Lifecycle == RunnerLifecycleDegraded {
				return fmt.Errorf("Binding %s is degraded", workspaceID)
			}
			if !ok || record.Lifecycle != RunnerLifecycleRunning {
				allReady = false
			}
		}
		if allReady {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("wait for Computer Binding children: %w", ctx.Err())
		case <-ticker.C:
		}
	}
}

func (host *Host) RegisterRoutes(mux *http.ServeMux) {
	if host != nil && host.control != nil {
		host.control.RegisterRoutes(mux)
	}
}

func (host *Host) Release(identity BindingChildIdentity) {
	if host != nil && host.control != nil {
		host.control.Release(identity)
	}
}

func (host *Host) PrepareMachineUpgrade(ctx context.Context) error {
	if host == nil || host.supervisor == nil || host.control == nil {
		return errors.New("Computer Host is unavailable")
	}
	return host.supervisor.PrepareMachineUpgrade(ctx, host.control.token)
}

func (host *Host) PrepareSiblingMachineUpgrade(ctx context.Context, initiatorWorkspaceID string) error {
	if host == nil || host.supervisor == nil || host.control == nil {
		return errors.New("Computer Host is unavailable")
	}
	return host.supervisor.PrepareSiblingMachineUpgrade(ctx, host.control.token, initiatorWorkspaceID)
}

func (host *Host) ReleaseMachineUpgrade(ctx context.Context) error {
	if host == nil || host.supervisor == nil || host.control == nil {
		return errors.New("Computer Host is unavailable")
	}
	return host.supervisor.ReleaseMachineUpgrade(ctx, host.control.token)
}

func (host *Host) PrepareEnvironmentSwitch(ctx context.Context) error {
	if host == nil || host.supervisor == nil || host.control == nil {
		return errors.New("Computer Host is unavailable")
	}
	return host.supervisor.PrepareEnvironmentSwitch(ctx, host.control.token)
}

func (host *Host) ReleaseEnvironmentSwitch(ctx context.Context) error {
	if host == nil || host.supervisor == nil || host.control == nil {
		return errors.New("Computer Host is unavailable")
	}
	return host.supervisor.ReleaseEnvironmentSwitch(ctx, host.control.token)
}

func (host *Host) ReregisterBindings(ctx context.Context, workspaceIDs []string) error {
	if host == nil || host.supervisor == nil || host.control == nil {
		return errors.New("Computer Host is unavailable")
	}
	return host.supervisor.ReregisterBindings(ctx, host.control.token, workspaceIDs)
}

func (host *Host) DeliverComputerUpgrade(ctx context.Context, command protocol.ComputerUpgradePayload) error {
	if host == nil || host.supervisor == nil || host.control == nil {
		return errors.New("Computer Host is unavailable")
	}
	return host.supervisor.DeliverComputerUpgrade(ctx, host.control.token, command)
}

func (host *Host) PrepareChildUpgrade(ctx context.Context, identity BindingChildIdentity, pending protocol.DaemonHeartbeatPendingMachineUpgrade) (BindingMachineUpgradePrepared, error) {
	if host == nil || host.upgrade == nil {
		return BindingMachineUpgradePrepared{}, errors.New("Computer Machine Upgrade coordinator is unavailable")
	}
	raw, err := json.Marshal(pending)
	if err != nil {
		return BindingMachineUpgradePrepared{}, err
	}
	return host.upgrade.prepareChildUpgrade(ctx, identity, raw)
}
