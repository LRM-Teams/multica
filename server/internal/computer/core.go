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

// ComputerCoreConfig is the Computer's machine-lifecycle boundary. Workspace
// execution details enter only through DaemonCore callbacks.
type ComputerCoreConfig struct {
	Spawn             WorkspaceDaemonProcessSpawner
	ResidentRoot      string
	Now               func() time.Time
	ReadyTimeout      time.Duration
	ReconcileInterval time.Duration
	Logger            *slog.Logger
	ControlToken      string
	ControlCallbacks  ComputerControlCallbacks
}

// ComputerCore owns machine identity, local control, restart, and upgrade.
type ComputerCore struct {
	daemonCore        *DaemonCore
	control           *ComputerControl
	reconcileInterval time.Duration
	logger            *slog.Logger

	runtimeMu          sync.RWMutex
	runtimeSets        map[string]workspaceDaemonRuntimeSet
	upgrade            *computerMachineUpgrade
	diagnosticStore    *diagnosticlog.Store
	diagnosticMu       sync.Mutex
	diagnosticLoggers  map[string]*diagnosticlog.Logger
	processIdentity    ComputerIdentity
	workJournalMu      sync.Mutex
	workJournalEnabled bool
	workJournalHome    string
	workJournalRoot    string
}

func (computerCore *ComputerCore) RegisterControlRPCHandlers(registry *LocalControlRegistry) {
	if computerCore != nil && computerCore.control != nil {
		computerCore.control.RegisterRPCHandlers(registry)
	}
}

type workspaceDaemonRuntime struct {
	ID          string `json:"id"`
	WorkspaceID string `json:"workspace_id"`
	Provider    string `json:"provider"`
}

type workspaceDaemonRuntimeSet struct {
	Identity    WorkspaceDaemonIdentity
	Runtimes    []workspaceDaemonRuntime
	DaemonToken string
	ExpiresAt   time.Time
}

func NewComputerCore(config ComputerCoreConfig) (*ComputerCore, error) {
	if config.Spawn == nil {
		return nil, errors.New("WorkspaceDaemon process spawner is required")
	}
	if config.ReconcileInterval <= 0 {
		config.ReconcileInterval = WorkspaceDaemonReconcileInterval
	}
	computerCore := &ComputerCore{
		reconcileInterval: config.ReconcileInterval,
		logger:            config.Logger,
		runtimeSets:       make(map[string]workspaceDaemonRuntimeSet),
		diagnosticLoggers: make(map[string]*diagnosticlog.Logger),
	}
	daemonCore, err := NewDaemonCore(DaemonCoreConfig{
		Spawn: config.Spawn, StateRoot: config.ResidentRoot, Now: config.Now, ReadyTimeout: config.ReadyTimeout, Logger: config.Logger,
		// ComputerCore owns the control credential. DaemonCore only asks
		// for the identified WorkspaceDaemon to drain.
		DrainWorkspaceDaemon: func(ctx context.Context, endpoint string, identity WorkspaceDaemonIdentity) error {
			return RequestWorkspaceDaemonDrain(ctx, endpoint, config.ControlToken, identity)
		},
		Released: func(identity WorkspaceDaemonIdentity) {
			if computerCore.control != nil {
				computerCore.control.Release(identity)
			}
		},
	})
	if err != nil {
		return nil, err
	}
	external := config.ControlCallbacks
	callbacks := external
	callbacks.RuntimeSet = func(ctx context.Context, identity WorkspaceDaemonIdentity, raw json.RawMessage, token string, expiresAt time.Time) error {
		var runtimes []workspaceDaemonRuntime
		if err := json.Unmarshal(raw, &runtimes); err != nil {
			return errors.New("invalid WorkspaceDaemon Runtime set")
		}
		seen := make(map[string]struct{}, len(runtimes))
		for i := range runtimes {
			runtimes[i].ID = strings.TrimSpace(runtimes[i].ID)
			runtimes[i].WorkspaceID = strings.TrimSpace(runtimes[i].WorkspaceID)
			if runtimes[i].ID == "" || runtimes[i].WorkspaceID != strings.TrimSpace(identity.WorkspaceID) {
				return errors.New("WorkspaceDaemon Runtime belongs to another Workspace")
			}
			if _, duplicate := seen[runtimes[i].ID]; duplicate {
				return errors.New("WorkspaceDaemon Runtime set contains duplicates")
			}
			seen[runtimes[i].ID] = struct{}{}
		}
		computerCore.runtimeMu.Lock()
		computerCore.runtimeSets[identity.WorkspaceID] = workspaceDaemonRuntimeSet{
			Identity: identity, Runtimes: append([]workspaceDaemonRuntime(nil), runtimes...),
			DaemonToken: strings.TrimSpace(token), ExpiresAt: expiresAt,
		}
		computerCore.runtimeMu.Unlock()
		if external.RuntimeSet != nil {
			return external.RuntimeSet(ctx, identity, raw, token, expiresAt)
		}
		return nil
	}
	callbacks.Diagnostic = func(ctx context.Context, identity WorkspaceDaemonIdentity, workspaceID string, event diagnosticlog.Event) error {
		computerCore.recordBindingDiagnostic(identity, workspaceID, event)
		if external.Diagnostic != nil {
			return external.Diagnostic(ctx, identity, workspaceID, event)
		}
		return nil
	}
	callbacks.MachineActions = func(ctx context.Context, identity WorkspaceDaemonIdentity, raw json.RawMessage) error {
		if computerCore.upgrade != nil {
			if err := computerCore.upgrade.handleChildAction(ctx, identity, raw); err != nil {
				return err
			}
		}
		if external.MachineActions != nil {
			return external.MachineActions(ctx, identity, raw)
		}
		return nil
	}
	callbacks.WorkDigest = func(ctx context.Context, identity WorkspaceDaemonIdentity, command protocol.ComputerWorkDigestPayload) (protocol.WorkDigest, error) {
		if external.WorkDigest != nil {
			return external.WorkDigest(ctx, identity, command)
		}
		return computerCore.HarvestWorkDigest(ctx, command)
	}
	callbacks.WorkJournal = func(ctx context.Context, identity WorkspaceDaemonIdentity, command protocol.ComputerWorkJournalPayload) (bool, error) {
		if external.WorkJournal != nil {
			return external.WorkJournal(ctx, identity, command)
		}
		if err := computerCore.SetWorkJournalEnabled(command.Enabled); err != nil {
			return false, err
		}
		return computerCore.WorkJournalEnabled(), nil
	}
	callbacks.ComputerUpgrade = func(ctx context.Context, identity WorkspaceDaemonIdentity, raw json.RawMessage) error {
		if external.ComputerUpgrade != nil {
			return external.ComputerUpgrade(ctx, identity, raw)
		}
		var command protocol.ComputerUpgradePayload
		if err := json.Unmarshal(raw, &command); err != nil {
			return err
		}
		return computerCore.upgrade.startServiceUpgrade(identity, command)
	}
	callbacks.Released = func(identity WorkspaceDaemonIdentity) {
		computerCore.runtimeMu.Lock()
		if current, ok := computerCore.runtimeSets[identity.WorkspaceID]; ok && current.Identity == identity {
			delete(computerCore.runtimeSets, identity.WorkspaceID)
		}
		computerCore.runtimeMu.Unlock()
		if external.Released != nil {
			external.Released(identity)
		}
	}
	callbacks.Current = daemonCore.Current
	computerCore.daemonCore = daemonCore
	computerCore.control = NewComputerControl(config.ControlToken, callbacks)
	computerCore.upgrade = newComputerMachineUpgrade(computerCore, computerMachineUpgradeConfig{})
	return computerCore, nil
}

func (computerCore *ComputerCore) Reconcile(ctx context.Context, desiredWorkspaceIDs []string) {
	if computerCore != nil && computerCore.daemonCore != nil {
		computerCore.daemonCore.Reconcile(ctx, desiredWorkspaceIDs)
	}
}

func (computerCore *ComputerCore) Stop() {
	if computerCore != nil && computerCore.daemonCore != nil {
		computerCore.daemonCore.Stop()
	}
}

func (computerCore *ComputerCore) Current(identity WorkspaceDaemonIdentity) bool {
	return computerCore != nil && computerCore.daemonCore != nil && computerCore.daemonCore.Current(identity)
}

func (computerCore *ComputerCore) Snapshot(workspaceID string) (WorkspaceDaemonSnapshot, int, bool) {
	if computerCore == nil || computerCore.daemonCore == nil {
		return WorkspaceDaemonSnapshot{}, 0, false
	}
	return computerCore.daemonCore.Snapshot(workspaceID)
}

func (computerCore *ComputerCore) DesiredWorkspaceIDs() []string {
	if computerCore == nil || computerCore.daemonCore == nil {
		return nil
	}
	return computerCore.daemonCore.DesiredWorkspaceIDs()
}

// WaitReady fences Computer readiness on every desired WorkspaceDaemon reaching
// its real Ready seam. A degraded WorkspaceDaemon is terminal for this
// startup attempt; crash/backoff remains retryable until ctx expires.
func (computerCore *ComputerCore) WaitReady(ctx context.Context, desiredWorkspaceIDs []string) error {
	if computerCore == nil || computerCore.daemonCore == nil {
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
			snapshot, _, ok := computerCore.daemonCore.Snapshot(workspaceID)
			if ok && snapshot.Status == WorkspaceDaemonDegraded {
				return fmt.Errorf("WorkspaceDaemon %s is degraded", workspaceID)
			}
			if !ok || snapshot.Status != WorkspaceDaemonRunning {
				allReady = false
			}
		}
		if allReady {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("wait for WorkspaceDaemons: %w", ctx.Err())
		case <-ticker.C:
		}
	}
}

func (computerCore *ComputerCore) Release(identity WorkspaceDaemonIdentity) {
	if computerCore != nil && computerCore.control != nil {
		computerCore.control.Release(identity)
	}
}

func (computerCore *ComputerCore) PrepareMachineUpgrade(ctx context.Context) error {
	if computerCore == nil || computerCore.daemonCore == nil || computerCore.control == nil {
		return errors.New("ComputerCore is unavailable")
	}
	return computerCore.daemonCore.PrepareMachineUpgrade(ctx, computerCore.control.token)
}

func (computerCore *ComputerCore) PrepareSiblingMachineUpgrade(ctx context.Context, initiatorWorkspaceID string) error {
	if computerCore == nil || computerCore.daemonCore == nil || computerCore.control == nil {
		return errors.New("ComputerCore is unavailable")
	}
	return computerCore.daemonCore.PrepareSiblingMachineUpgrade(ctx, computerCore.control.token, initiatorWorkspaceID)
}

func (computerCore *ComputerCore) ReleaseMachineUpgrade(ctx context.Context) error {
	if computerCore == nil || computerCore.daemonCore == nil || computerCore.control == nil {
		return errors.New("ComputerCore is unavailable")
	}
	return computerCore.daemonCore.ReleaseMachineUpgrade(ctx, computerCore.control.token)
}

func (computerCore *ComputerCore) PrepareEnvironmentSwitch(ctx context.Context) error {
	if computerCore == nil || computerCore.daemonCore == nil || computerCore.control == nil {
		return errors.New("ComputerCore is unavailable")
	}
	return computerCore.daemonCore.PrepareEnvironmentSwitch(ctx, computerCore.control.token)
}

func (computerCore *ComputerCore) ReleaseEnvironmentSwitch(ctx context.Context) error {
	if computerCore == nil || computerCore.daemonCore == nil || computerCore.control == nil {
		return errors.New("ComputerCore is unavailable")
	}
	return computerCore.daemonCore.ReleaseEnvironmentSwitch(ctx, computerCore.control.token)
}

func (computerCore *ComputerCore) ReregisterBindings(ctx context.Context, workspaceIDs []string) error {
	if computerCore == nil || computerCore.daemonCore == nil || computerCore.control == nil {
		return errors.New("ComputerCore is unavailable")
	}
	return computerCore.daemonCore.ReregisterBindings(ctx, computerCore.control.token, workspaceIDs)
}

func (computerCore *ComputerCore) DeliverComputerUpgrade(ctx context.Context, command protocol.ComputerUpgradePayload) error {
	if computerCore == nil || computerCore.daemonCore == nil || computerCore.control == nil {
		return errors.New("ComputerCore is unavailable")
	}
	return computerCore.daemonCore.DeliverComputerUpgrade(ctx, computerCore.control.token, command)
}
