package computer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/multica-ai/multica/server/internal/diagnosticlog"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// ComputerCoreConfig is the Computer's process-supervision boundary. Binding execution
// details enter only through the child process launcher and control callbacks.
type ComputerCoreConfig struct {
	Spawn             BindingChildSpawner
	ResidentRoot      string
	Now               func() time.Time
	ReadyTimeout      time.Duration
	ReconcileInterval time.Duration
	Logger            *slog.Logger
	ControlToken      string
	MaxAgentProcesses int
	ControlCallbacks  HostControlCallbacks
}

// ComputerCore is the Computer-level owner. Machine lifecycle, upgrade, diagnostics,
// and local service control stay here; Agent-system supervision is delegated to
// its local DaemonCore.
type ComputerCore struct {
	daemonCore         *DaemonCore
	logger             *slog.Logger
	upgrade            *hostMachineUpgrade
	diagnosticStore    *diagnosticlog.Store
	diagnosticMu       sync.Mutex
	diagnosticLoggers  map[string]*diagnosticlog.Logger
	processIdentity    HostProcessIdentity
	workJournalMu      sync.Mutex
	workJournalEnabled bool
	workJournalHome    string
	workJournalRoot    string
}

func (host *ComputerCore) RegisterControlRPCHandlers(registry *LocalControlRegistry) {
	if host != nil && host.daemonCore != nil && host.daemonCore.control != nil {
		host.daemonCore.control.RegisterRPCHandlers(registry)
	}
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

func NewComputerCore(config ComputerCoreConfig) (*ComputerCore, error) {
	if config.Spawn == nil {
		return nil, errors.New("Computer Binding child spawner is required")
	}
	if config.ReconcileInterval <= 0 {
		config.ReconcileInterval = RunnerReconcileInterval
	}
	host := &ComputerCore{
		logger:            config.Logger,
		diagnosticLoggers: make(map[string]*diagnosticlog.Logger),
	}
	var core *DaemonCore
	core, err := newDaemonCore(daemonCoreConfig{
		Spawn: config.Spawn, StateRoot: config.ResidentRoot, Now: config.Now, ReadyTimeout: config.ReadyTimeout, Logger: config.Logger,
		// Computer owns the local credential; DaemonCore only receives the
		// operation needed to drain one WorkspaceDaemonCore.
		DrainRunner: func(ctx context.Context, endpoint string, identity BindingChildIdentity) error {
			return RequestBindingRunnerDrain(ctx, endpoint, config.ControlToken, identity)
		},
		Released: func(identity BindingChildIdentity) {
			if core != nil && core.control != nil {
				core.control.Release(identity)
			}
		},
	})
	if err != nil {
		return nil, err
	}
	core.reconcileInterval = config.ReconcileInterval
	host.daemonCore = core
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
		core.runtimeMu.Lock()
		core.runtimeSets[identity.WorkspaceID] = hostBindingRuntimeSet{
			Identity: identity, Runtimes: append([]hostBindingRuntime(nil), runtimes...),
			DaemonToken: strings.TrimSpace(token), ExpiresAt: expiresAt,
		}
		core.runtimeMu.Unlock()
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
	callbacks.WorkDigest = func(ctx context.Context, identity BindingChildIdentity, command protocol.ComputerWorkDigestPayload) (protocol.WorkDigest, error) {
		if external.WorkDigest != nil {
			return external.WorkDigest(ctx, identity, command)
		}
		return host.HarvestWorkDigest(ctx, command)
	}
	callbacks.WorkJournal = func(ctx context.Context, identity BindingChildIdentity, command protocol.ComputerWorkJournalPayload) (bool, error) {
		if external.WorkJournal != nil {
			return external.WorkJournal(ctx, identity, command)
		}
		if err := host.SetWorkJournalEnabled(command.Enabled); err != nil {
			return false, err
		}
		return host.WorkJournalEnabled(), nil
	}
	callbacks.ComputerUpgrade = func(ctx context.Context, identity BindingChildIdentity, raw json.RawMessage) error {
		if external.ComputerUpgrade != nil {
			return external.ComputerUpgrade(ctx, identity, raw)
		}
		var command protocol.ComputerUpgradePayload
		if err := json.Unmarshal(raw, &command); err != nil {
			return err
		}
		return host.upgrade.startServiceUpgrade(identity, command)
	}
	callbacks.Released = func(identity BindingChildIdentity) {
		core.runtimeMu.Lock()
		if current, ok := core.runtimeSets[identity.WorkspaceID]; ok && current.Identity == identity {
			delete(core.runtimeSets, identity.WorkspaceID)
		}
		core.runtimeMu.Unlock()
		if external.Released != nil {
			external.Released(identity)
		}
	}
	callbacks.Current = core.Current
	core.control = NewHostControl(config.ControlToken, NewProcessCapacity(config.MaxAgentProcesses), callbacks)
	host.upgrade = newHostMachineUpgrade(host, hostMachineUpgradeConfig{})
	return host, nil
}

// Run continuously reconciles the desired Binding set until the Computer
// process exits. The periodic repair policy belongs to the Computer, not to a
// Binding daemon.
func (host *ComputerCore) Run(ctx context.Context, desired func() []string, changes <-chan struct{}) {
	if host == nil || host.daemonCore == nil || desired == nil {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	host.daemonCore.Reconcile(ctx, desired())
	ticker := time.NewTicker(host.daemonCore.reconcileInterval)
	defer ticker.Stop()
	defer host.daemonCore.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case _, ok := <-changes:
			if !ok {
				changes = nil
				continue
			}
			host.daemonCore.Reconcile(ctx, desired())
		case <-ticker.C:
			host.daemonCore.Reconcile(ctx, desired())
		}
	}
}

func (host *ComputerCore) Reconcile(ctx context.Context, desiredWorkspaceIDs []string) {
	if host != nil && host.daemonCore != nil {
		host.daemonCore.Reconcile(ctx, desiredWorkspaceIDs)
	}
}

func (host *ComputerCore) Stop() {
	if host != nil && host.daemonCore != nil {
		host.daemonCore.Stop()
	}
}

func (host *ComputerCore) Current(identity BindingChildIdentity) bool {
	return host != nil && host.daemonCore != nil && host.daemonCore.Current(identity)
}

func (host *ComputerCore) Snapshot(workspaceID string) (RunnerRecord, int, bool) {
	if host == nil || host.daemonCore == nil {
		return RunnerRecord{}, 0, false
	}
	return host.daemonCore.Snapshot(workspaceID)
}

func (host *ComputerCore) DesiredWorkspaceIDs() []string {
	if host == nil || host.daemonCore == nil {
		return nil
	}
	return host.daemonCore.DesiredWorkspaceIDs()
}

// WaitReady fences Computer readiness on every desired Binding child reaching
// its real WorkspaceDaemon Ready seam. A degraded child is terminal for this
// startup attempt; crash/backoff remains retryable until ctx expires.
func (host *ComputerCore) WaitReady(ctx context.Context, desiredWorkspaceIDs []string) error {
	if host == nil || host.daemonCore == nil {
		return errors.New("ComputerCore is unavailable")
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
			record, _, ok := host.daemonCore.Snapshot(workspaceID)
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

func (host *ComputerCore) Release(identity BindingChildIdentity) {
	if host != nil && host.daemonCore != nil && host.daemonCore.control != nil {
		host.daemonCore.control.Release(identity)
	}
}

func (host *ComputerCore) PrepareMachineUpgrade(ctx context.Context) error {
	if host == nil || host.daemonCore == nil || host.daemonCore.control == nil {
		return errors.New("ComputerCore is unavailable")
	}
	return host.daemonCore.PrepareMachineUpgrade(ctx, host.daemonCore.control.token)
}

func (host *ComputerCore) PrepareSiblingMachineUpgrade(ctx context.Context, initiatorWorkspaceID string) error {
	if host == nil || host.daemonCore == nil || host.daemonCore.control == nil {
		return errors.New("ComputerCore is unavailable")
	}
	return host.daemonCore.PrepareSiblingMachineUpgrade(ctx, host.daemonCore.control.token, initiatorWorkspaceID)
}

func (host *ComputerCore) ReleaseMachineUpgrade(ctx context.Context) error {
	if host == nil || host.daemonCore == nil || host.daemonCore.control == nil {
		return errors.New("ComputerCore is unavailable")
	}
	return host.daemonCore.ReleaseMachineUpgrade(ctx, host.daemonCore.control.token)
}

func (host *ComputerCore) PrepareEnvironmentSwitch(ctx context.Context) error {
	if host == nil || host.daemonCore == nil || host.daemonCore.control == nil {
		return errors.New("ComputerCore is unavailable")
	}
	return host.daemonCore.PrepareEnvironmentSwitch(ctx, host.daemonCore.control.token)
}

func (host *ComputerCore) ReleaseEnvironmentSwitch(ctx context.Context) error {
	if host == nil || host.daemonCore == nil || host.daemonCore.control == nil {
		return errors.New("ComputerCore is unavailable")
	}
	return host.daemonCore.ReleaseEnvironmentSwitch(ctx, host.daemonCore.control.token)
}

func (host *ComputerCore) ReregisterBindings(ctx context.Context, workspaceIDs []string) error {
	if host == nil || host.daemonCore == nil || host.daemonCore.control == nil {
		return errors.New("ComputerCore is unavailable")
	}
	return host.daemonCore.ReregisterBindings(ctx, host.daemonCore.control.token, workspaceIDs)
}

func (host *ComputerCore) DeliverComputerUpgrade(ctx context.Context, command protocol.ComputerUpgradePayload) error {
	if host == nil || host.daemonCore == nil || host.daemonCore.control == nil {
		return errors.New("ComputerCore is unavailable")
	}
	return host.daemonCore.DeliverComputerUpgrade(ctx, host.daemonCore.control.token, command)
}
