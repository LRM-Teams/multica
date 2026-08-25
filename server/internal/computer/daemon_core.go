package computer

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

type WorkspaceDaemonLauncher struct {
	Executable      func() (string, error)
	ComputerID      string
	Environment     string
	Profile         string
	ServerBaseURL   string
	ServiceEndpoint string
	BindingsRoot    string
	WorkspacesRoot  string
}

func (launcher WorkspaceDaemonLauncher) Spawn(workspaceID string) (WorkspaceDaemonProcess, error) {
	bootstrap := WorkspaceDaemonBootstrap{
		ProtocolVersion: WorkspaceDaemonProtocolVersion, WorkspaceID: workspaceID,
		ComputerID:  launcher.ComputerID,
		Environment: launcher.Environment, Profile: launcher.Profile,
		ServerBaseURL: launcher.ServerBaseURL, ServiceEndpoint: launcher.ServiceEndpoint,
		BindingsRoot: launcher.BindingsRoot, WorkspacesRoot: launcher.WorkspacesRoot,
	}
	executable := launcher.Executable
	if executable == nil {
		executable = os.Executable
	}
	exe, err := executable()
	if err != nil {
		return nil, err
	}
	return StartWorkspaceDaemon(exe, bootstrap)
}

type WorkspaceDaemonProcessSpawner func(workspaceID string) (WorkspaceDaemonProcess, error)

type activatableWorkspaceDaemonProcess interface {
	Activate()
}

type DaemonCoreConfig struct {
	Spawn        WorkspaceDaemonProcessSpawner
	StateRoot    string
	Now          func() time.Time
	ReadyTimeout time.Duration
	Logger       *slog.Logger
	Released     func(WorkspaceDaemonIdentity)

	// DrainWorkspaceDaemon asks a WorkspaceDaemon to close its admission
	// barrier before DaemonCore terminates it. ComputerCore injects the
	// credential-bearing call; DaemonCore never owns the token. nil (or a
	// process with no known endpoint) skips straight to termination by
	// signal.
	DrainWorkspaceDaemon func(ctx context.Context, endpoint string, identity WorkspaceDaemonIdentity) error
	// Sleep blocks between terminateProcess's exit-confirmation polls;
	// defaults to time.Sleep. Tests substitute a no-op alongside a tiny
	// TerminatePollInterval/TerminateGrace.
	Sleep func(time.Duration)
	// TerminatePollInterval is how often terminateProcess re-checks whether
	// an orphaned WorkspaceDaemon has exited after SIGTERM/SIGKILL; defaults
	// to 200ms.
	TerminatePollInterval time.Duration
	// TerminateGrace bounds how long terminateProcess waits after SIGTERM
	// (and again after SIGKILL, to confirm exit) before giving up; defaults
	// to 2s.
	TerminateGrace time.Duration
}

// DaemonCore owns the desired Workspace set and every WorkspaceDaemon process.
type DaemonCore struct {
	config DaemonCoreConfig

	mu                  sync.RWMutex
	workspaceDaemons    map[string]*managedWorkspaceDaemon
	desiredWorkspaceIDs []string
	exits               sync.WaitGroup
}

type managedWorkspaceDaemon struct {
	process          WorkspaceDaemonProcess
	cancel           context.CancelFunc
	daemonInstanceID string
	controlEndpoint  string
	restart          workspaceDaemonRestart
}

type WorkspaceDaemonSnapshot struct {
	Status           WorkspaceDaemonStatus
	DaemonInstanceID string
}

func (daemon *managedWorkspaceDaemon) status() WorkspaceDaemonStatus {
	if daemon == nil {
		return WorkspaceDaemonStopped
	}
	if daemon.process != nil || daemon.cancel != nil {
		if daemon.daemonInstanceID == "" {
			return WorkspaceDaemonStarting
		}
		return WorkspaceDaemonRunning
	}
	if daemon.restart.degraded {
		return WorkspaceDaemonDegraded
	}
	if !daemon.restart.notBefore.IsZero() {
		return WorkspaceDaemonCrashed
	}
	return WorkspaceDaemonStopped
}

type workspaceDaemonStart struct {
	workspaceID string
	ctx         context.Context
	cancel      context.CancelFunc
}

type workspaceDaemonControlTarget struct {
	identity   WorkspaceDaemonIdentity
	controlURL string
}

func NewDaemonCore(config DaemonCoreConfig) (*DaemonCore, error) {
	if config.Spawn == nil {
		return nil, errors.New("WorkspaceDaemon process spawner is required")
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	if config.Sleep == nil {
		config.Sleep = time.Sleep
	}
	if config.ReadyTimeout <= 0 {
		config.ReadyTimeout = 30 * time.Second
	}
	if config.TerminatePollInterval <= 0 {
		config.TerminatePollInterval = 200 * time.Millisecond
	}
	if config.TerminateGrace <= 0 {
		config.TerminateGrace = 2 * time.Second
	}
	reclaimable, err := findReclaimableRunners(config.StateRoot, config.Logger)
	if err != nil {
		return nil, fmt.Errorf("recover persisted WorkspaceDaemon state: %w", err)
	}
	daemonCore := &DaemonCore{
		config:           config,
		workspaceDaemons: make(map[string]*managedWorkspaceDaemon),
	}
	// Reclaim every orphaned WorkspaceDaemon concurrently — each one is an
	// independent drain+terminate against its own pid/endpoint, and doing
	// them one at a time would multiply Computer startup latency by the orphan
	// count. NewDaemonCore still blocks on wg.Wait(): every orphan
	// must be confirmed dead (or backed off) before this function returns,
	// so Reconcile can never spawn a replacement while its predecessor is
	// still being killed.
	var wg sync.WaitGroup
	for _, runner := range reclaimable {
		wg.Add(1)
		go func(runner reclaimableRunner) {
			defer wg.Done()
			daemonCore.reclaimOrphanedRunner(runner)
		}(runner)
	}
	wg.Wait()
	return daemonCore, nil
}

// reclaimOrphanedRunner reclaims a WorkspaceDaemon process left behind by a
// previous Computer generation so the first Reconcile can spawn a fresh process.
// One Workspace holds at most one WorkspaceDaemon on this machine; a live
// process found at startup is
// reclaimed here, never adopted.
func (daemonCore *DaemonCore) reclaimOrphanedRunner(runner reclaimableRunner) {
	config := daemonCore.config
	start := config.Now()
	err := reclaimRunnerProcess(runner, runnerReclaimOptions{
		StateRoot:    config.StateRoot,
		Drain:        config.DrainWorkspaceDaemon,
		PollInterval: config.TerminatePollInterval,
		Grace:        config.TerminateGrace,
		Sleep:        config.Sleep,
		Logger:       config.Logger,
	})
	if err != nil {
		if config.Logger != nil {
			config.Logger.Warn("orphaned WorkspaceDaemon could not be confirmed dead; leaving this Workspace out of spawn rotation this cycle", "workspace_id", runner.WorkspaceID, "pid", runner.PID, "error", err)
		}
		daemonCore.backOffAfterFailedReclaim(runner)
		return
	}
	if config.Logger != nil {
		config.Logger.Info("orphaned WorkspaceDaemon terminated", "workspace_id", runner.WorkspaceID, "pid", runner.PID, "elapsed", config.Now().Sub(start))
	}
}

// backOffAfterFailedReclaim leaves the persisted state in place (so a later
// Computer generation can retry reclaiming it) and puts this Workspace
// into the normal crash/backoff state instead of leaving it immediately
// spawnable, so this reconcile cycle cannot start a second WorkspaceDaemon next to a
// still-live one.
func (daemonCore *DaemonCore) backOffAfterFailedReclaim(runner reclaimableRunner) {
	daemonCore.mu.Lock()
	daemon := daemonCore.workspaceDaemonLocked(runner.WorkspaceID)
	daemon.restart.notBefore = daemonCore.config.Now().Add(WorkspaceDaemonRestartBackoff)
	daemonCore.mu.Unlock()
}

func (daemonCore *DaemonCore) Reconcile(parent context.Context, desiredWorkspaceIDs []string) {
	if daemonCore == nil {
		return
	}
	if parent == nil {
		parent = context.Background()
	}
	desired := make(map[string]struct{}, len(desiredWorkspaceIDs))
	for _, workspaceID := range desiredWorkspaceIDs {
		workspaceID = strings.TrimSpace(workspaceID)
		if workspaceID != "" {
			desired[workspaceID] = struct{}{}
		}
	}
	type stoppedChild struct {
		identity WorkspaceDaemonIdentity
		child    WorkspaceDaemonProcess
		cancel   context.CancelFunc
	}
	var stops []stoppedChild
	var starts []workspaceDaemonStart
	now := daemonCore.config.Now()
	daemonCore.mu.Lock()
	daemonCore.desiredWorkspaceIDs = daemonCore.desiredWorkspaceIDs[:0]
	for workspaceID := range desired {
		daemonCore.desiredWorkspaceIDs = append(daemonCore.desiredWorkspaceIDs, workspaceID)
	}
	sort.Strings(daemonCore.desiredWorkspaceIDs)
	for workspaceID, daemon := range daemonCore.workspaceDaemons {
		if _, wanted := desired[workspaceID]; wanted || daemon.cancel == nil {
			continue
		}
		identity := WorkspaceDaemonIdentity{WorkspaceID: workspaceID}
		identity.DaemonInstanceID = daemon.daemonInstanceID
		if daemon.process != nil {
			identity.PID = daemon.process.PID()
		}
		cancel := daemon.cancel
		daemon.cancel = nil
		stops = append(stops, stoppedChild{identity: identity, child: daemon.process, cancel: cancel})
	}
	workspaceIDs := make([]string, 0, len(desired))
	for workspaceID := range desired {
		workspaceIDs = append(workspaceIDs, workspaceID)
	}
	sort.Strings(workspaceIDs)
	for _, workspaceID := range workspaceIDs {
		daemon := daemonCore.workspaceDaemonLocked(workspaceID)
		if daemon.cancel != nil || daemon.process != nil {
			continue
		}
		if !daemon.restart.canStart(now) {
			continue
		}
		childCtx, cancel := context.WithCancel(parent)
		daemon.cancel = cancel
		daemon.daemonInstanceID = ""
		daemon.controlEndpoint = ""
		daemon.restart.notBefore = time.Time{}
		daemonCore.exits.Add(1)
		starts = append(starts, workspaceDaemonStart{workspaceID: workspaceID, ctx: childCtx, cancel: cancel})
	}
	daemonCore.mu.Unlock()
	for _, stopped := range stops {
		if stopped.child != nil {
			_ = stopped.child.Stop()
		}
		stopped.cancel()
	}
	for _, next := range starts {
		daemonCore.spawn(next)
	}
}

// Run continuously reconciles the desired Workspace set and owns shutdown of
// every WorkspaceDaemon process before returning.
func (daemonCore *DaemonCore) Run(ctx context.Context, desired func() []string, changes <-chan struct{}, interval time.Duration) {
	if daemonCore == nil || desired == nil {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if interval <= 0 {
		interval = WorkspaceDaemonReconcileInterval
	}
	daemonCore.Reconcile(ctx, desired())
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	defer daemonCore.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case _, ok := <-changes:
			if !ok {
				changes = nil
				continue
			}
			daemonCore.Reconcile(ctx, desired())
		case <-ticker.C:
			daemonCore.Reconcile(ctx, desired())
		}
	}
}

func (daemonCore *DaemonCore) spawn(next workspaceDaemonStart) {
	child, err := daemonCore.config.Spawn(next.workspaceID)
	if err != nil {
		if daemonCore.config.Logger != nil {
			daemonCore.config.Logger.Warn("WorkspaceDaemon process spawn failed", "workspace_id", next.workspaceID, "error", err)
		}
		next.cancel()
		daemonCore.observeExit(next.workspaceID, "", nil, WorkspaceDaemonExitCrash)
		daemonCore.exits.Done()
		return
	}
	daemonCore.mu.Lock()
	daemon := daemonCore.workspaceDaemons[next.workspaceID]
	if daemon == nil || daemon.cancel == nil || daemon.process != nil {
		daemonCore.mu.Unlock()
		next.cancel()
		_ = child.Stop()
		_ = discardRunnerStateAfterSpawnFailure(daemonCore.config.StateRoot, next.workspaceID, "", child.PID())
		daemonCore.observeExit(next.workspaceID, "", nil, WorkspaceDaemonExitCrash)
		daemonCore.exits.Done()
		return
	}
	daemon.process = child
	daemonCore.mu.Unlock()
	stateErr := writeRunnerState(daemonCore.config.StateRoot, persistedRunnerState{
		WorkspaceID: next.workspaceID,
		OwnerPID:    os.Getpid(), RunnerPID: child.PID(), StartedAt: daemonCore.config.Now().UTC(),
	})
	if stateErr == nil {
		stateErr = writeRunnerPID(daemonCore.config.StateRoot, next.workspaceID, child.PID())
	}
	if stateErr != nil {
		if daemonCore.config.Logger != nil {
			daemonCore.config.Logger.Error("could not persist WorkspaceDaemon state", "workspace_id", next.workspaceID, "error", stateErr)
		}
		next.cancel()
		_ = child.Stop()
		_ = discardRunnerStateAfterSpawnFailure(daemonCore.config.StateRoot, next.workspaceID, "", child.PID())
		daemonCore.observeExit(next.workspaceID, "", child, WorkspaceDaemonExitCrash)
		daemonCore.exits.Done()
		return
	}
	if activatable, ok := child.(activatableWorkspaceDaemonProcess); ok {
		activatable.Activate()
	}
	go func() {
		defer daemonCore.exits.Done()
		daemonCore.observeProcess(next.workspaceID, child, next.ctx, next.cancel)
	}()
}

func (daemonCore *DaemonCore) observeProcess(workspaceID string, child WorkspaceDaemonProcess, ctx context.Context, cancel context.CancelFunc) {
	class := WorkspaceDaemonExitGraceful
	recordedIdentity := ""
	if readyChild, ok := child.(ReadyWorkspaceDaemonProcess); ok {
		readyCtx, stopReady := context.WithTimeout(ctx, daemonCore.config.ReadyTimeout)
		ready, err := readyChild.AwaitReady(readyCtx)
		stopReady()
		if err != nil {
			if ctx.Err() == nil && daemonCore.config.Logger != nil {
				daemonCore.config.Logger.Warn("WorkspaceDaemon process readiness failed", "workspace_id", workspaceID, "error", err)
			}
			if ctx.Err() == nil {
				class = WorkspaceDaemonExitCrash
			}
			_ = child.Stop()
		} else {
			recordedIdentity = ready.DaemonInstanceID
			daemonCore.observeReady(workspaceID, child, ready)
		}
	} else {
		daemonCore.observeReady(workspaceID, child, WorkspaceDaemonReady{PID: child.PID()})
	}
	waitClass := child.Wait()
	if ctx.Err() == nil && class != WorkspaceDaemonExitCrash {
		class = waitClass
	}
	if cancel != nil {
		cancel()
	}
	if recordedIdentity == "" {
		if snapshot, _, ok := daemonCore.Snapshot(workspaceID); ok {
			recordedIdentity = snapshot.DaemonInstanceID
		}
	}
	daemonCore.observeExit(workspaceID, recordedIdentity, child, class)
}

func (daemonCore *DaemonCore) observeReady(workspaceID string, child WorkspaceDaemonProcess, ready WorkspaceDaemonReady) {
	daemonCore.mu.Lock()
	defer daemonCore.mu.Unlock()
	daemon := daemonCore.workspaceDaemons[workspaceID]
	if daemon == nil || daemon.process != child {
		return
	}
	if child != nil && ready.PID != 0 && ready.PID != child.PID() {
		return
	}
	daemonInstanceID := strings.TrimSpace(ready.DaemonInstanceID)
	if daemonInstanceID == "" || daemon.daemonInstanceID != "" {
		return
	}
	daemon.daemonInstanceID = daemonInstanceID
	if ready.RunnerEndpoint != "" {
		daemon.controlEndpoint = ready.RunnerEndpoint
	}
	if child != nil {
		startedAt := daemonCore.config.Now().UTC()
		state := persistedRunnerState{}
		if state, err := readRunnerState(runnerStatePath(daemonCore.config.StateRoot, workspaceID)); err == nil && !state.StartedAt.IsZero() {
			startedAt = state.StartedAt
		}
		state = persistedRunnerState{
			WorkspaceID: workspaceID, DaemonInstanceID: daemonInstanceID,
			OwnerPID: os.Getpid(), RunnerPID: child.PID(), StartedAt: startedAt,
		}
		_ = writeRunnerState(daemonCore.config.StateRoot, state)
		_ = writeRunnerConnected(daemonCore.config.StateRoot, workspaceID, persistedRunnerConnected{PID: child.PID(), ConnectedAt: daemonCore.config.Now().UTC(), RunnerEndpoint: ready.RunnerEndpoint})
	}
}

func (daemonCore *DaemonCore) observeExit(workspaceID, daemonInstanceID string, child WorkspaceDaemonProcess, class WorkspaceDaemonExitClass) {
	daemonCore.mu.Lock()
	daemon := daemonCore.workspaceDaemons[workspaceID]
	if daemon == nil || daemon.daemonInstanceID != daemonInstanceID {
		daemonCore.mu.Unlock()
		return
	}
	if child != nil && daemon.process != child {
		daemonCore.mu.Unlock()
		return
	}
	identity := WorkspaceDaemonIdentity{WorkspaceID: workspaceID, DaemonInstanceID: daemonInstanceID}
	if child != nil {
		identity.PID = child.PID()
	}
	daemon.restart.recordExit(daemonCore.config.Now(), class)
	daemon.cancel = nil
	daemon.process = nil
	daemon.daemonInstanceID = ""
	daemon.controlEndpoint = ""
	daemonCore.mu.Unlock()
	if child != nil {
		_ = removeRunnerState(daemonCore.config.StateRoot, workspaceID, daemonInstanceID, child.PID())
	}
	if identity.Validate() == nil && daemonCore.config.Released != nil {
		daemonCore.config.Released(identity)
	}
}

// Stop tears down every WorkspaceDaemon process DaemonCore spawned and waits
// until each process exits.
func (daemonCore *DaemonCore) Stop() {
	if daemonCore == nil {
		return
	}
	daemonCore.Reconcile(context.Background(), nil)
	daemonCore.exits.Wait()
}

func (daemonCore *DaemonCore) Current(identity WorkspaceDaemonIdentity) bool {
	if daemonCore == nil || identity.Validate() != nil {
		return false
	}
	daemonCore.mu.RLock()
	daemon := daemonCore.workspaceDaemons[identity.WorkspaceID]
	if daemon == nil || daemon.process == nil || daemon.process.PID() != identity.PID {
		daemonCore.mu.RUnlock()
		return false
	}
	recorded := daemon.daemonInstanceID
	current := recorded == "" || recorded == identity.DaemonInstanceID
	daemonCore.mu.RUnlock()
	return current
}

func (daemonCore *DaemonCore) DesiredWorkspaceIDs() []string {
	if daemonCore == nil {
		return nil
	}
	daemonCore.mu.RLock()
	defer daemonCore.mu.RUnlock()
	ids := append([]string(nil), daemonCore.desiredWorkspaceIDs...)
	return ids
}

func (daemonCore *DaemonCore) Snapshot(workspaceID string) (WorkspaceDaemonSnapshot, int, bool) {
	if daemonCore == nil {
		return WorkspaceDaemonSnapshot{}, 0, false
	}
	daemonCore.mu.RLock()
	defer daemonCore.mu.RUnlock()
	daemon := daemonCore.workspaceDaemons[strings.TrimSpace(workspaceID)]
	if daemon == nil {
		return WorkspaceDaemonSnapshot{}, 0, false
	}
	pid := 0
	if daemon.process != nil {
		pid = daemon.process.PID()
	}
	return WorkspaceDaemonSnapshot{Status: daemon.status(), DaemonInstanceID: daemon.daemonInstanceID}, pid, true
}

// PrepareMachineUpgrade asks every live WorkspaceDaemon to close its execution
// admission barrier and drain/terminate work through the child's own runtime
// implementation. If any child rejects preparation, already-prepared siblings
// are released so a failed machine operation cannot strand them paused.
func (daemonCore *DaemonCore) PrepareMachineUpgrade(ctx context.Context, controlToken string) error {
	return daemonCore.PrepareSiblingMachineUpgrade(ctx, controlToken, "")
}

func (daemonCore *DaemonCore) PrepareSiblingMachineUpgrade(ctx context.Context, controlToken, initiatorWorkspaceID string) error {
	targets, err := daemonCore.machineControlTargets(nil)
	if err != nil {
		return err
	}
	if initiator := strings.TrimSpace(initiatorWorkspaceID); initiator != "" {
		filtered := targets[:0]
		for _, current := range targets {
			if current.identity.WorkspaceID == initiator {
				continue
			}
			filtered = append(filtered, current)
		}
		targets = filtered
	}
	prepared := make([]workspaceDaemonControlTarget, 0, len(targets))
	for _, current := range targets {
		if err := RequestWorkspaceDaemonDrain(ctx, current.controlURL, controlToken, current.identity); err != nil {
			for _, prior := range prepared {
				_ = RequestWorkspaceDaemonReleaseMachineUpgrade(context.Background(), prior.controlURL, controlToken, prior.identity)
			}
			return fmt.Errorf("prepare WorkspaceDaemon %s for Machine Upgrade: %w", current.identity.WorkspaceID, err)
		}
		prepared = append(prepared, current)
	}
	return nil
}

func (daemonCore *DaemonCore) ReleaseMachineUpgrade(ctx context.Context, controlToken string) error {
	targets := daemonCore.availableMachineControlTargets()
	var failures []error
	for _, current := range targets {
		if err := RequestWorkspaceDaemonReleaseMachineUpgrade(ctx, current.controlURL, controlToken, current.identity); err != nil {
			failures = append(failures, fmt.Errorf("release WorkspaceDaemon %s after Machine Upgrade: %w", current.identity.WorkspaceID, err))
		}
	}
	return errors.Join(failures...)
}

func (daemonCore *DaemonCore) PrepareEnvironmentSwitch(ctx context.Context, controlToken string) error {
	targets, err := daemonCore.machineControlTargets(nil)
	if err != nil {
		return err
	}
	prepared := make([]workspaceDaemonControlTarget, 0, len(targets))
	for _, current := range targets {
		if err := RequestWorkspaceDaemonPrepareEnvironmentSwitch(ctx, current.controlURL, controlToken, current.identity); err != nil {
			for _, prior := range prepared {
				_ = RequestWorkspaceDaemonReleaseEnvironmentSwitch(context.Background(), prior.controlURL, controlToken, prior.identity)
			}
			return fmt.Errorf("prepare WorkspaceDaemon %s for environment switch: %w", current.identity.WorkspaceID, err)
		}
		prepared = append(prepared, current)
	}
	return nil
}

func (daemonCore *DaemonCore) ReleaseEnvironmentSwitch(ctx context.Context, controlToken string) error {
	targets := daemonCore.availableMachineControlTargets()
	var failures []error
	for _, current := range targets {
		if err := RequestWorkspaceDaemonReleaseEnvironmentSwitch(ctx, current.controlURL, controlToken, current.identity); err != nil {
			failures = append(failures, fmt.Errorf("release WorkspaceDaemon %s after environment switch: %w", current.identity.WorkspaceID, err))
		}
	}
	return errors.Join(failures...)
}

func (daemonCore *DaemonCore) DeliverComputerUpgrade(ctx context.Context, controlToken string, command protocol.ComputerUpgradePayload) error {
	targets := daemonCore.availableMachineControlTargets()
	if len(targets) == 0 {
		return errors.New("Computer has no ready WorkspaceDaemon for Machine Upgrade")
	}
	return RequestWorkspaceDaemonComputerUpgrade(ctx, targets[0].controlURL, controlToken, targets[0].identity, command)
}

func (daemonCore *DaemonCore) DeliverComputerUpgradeEvent(ctx context.Context, controlToken string, identity WorkspaceDaemonIdentity, eventType string, payload any) error {
	daemonCore.mu.RLock()
	daemon := daemonCore.workspaceDaemons[identity.WorkspaceID]
	var endpoint string
	var child WorkspaceDaemonProcess
	var daemonInstanceID string
	var status WorkspaceDaemonStatus
	if daemon != nil {
		endpoint = daemon.controlEndpoint
		child = daemon.process
		daemonInstanceID = daemon.daemonInstanceID
		status = daemon.status()
	}
	daemonCore.mu.RUnlock()
	if child == nil || status != WorkspaceDaemonRunning || endpoint == "" || daemonInstanceID != identity.DaemonInstanceID || child.PID() != identity.PID {
		return errors.New("WorkspaceDaemon is unavailable for upgrade event")
	}
	return RequestWorkspaceDaemonComputerUpgradeEvent(ctx, endpoint, controlToken, identity, eventType, payload)
}

func (daemonCore *DaemonCore) ReregisterBindings(ctx context.Context, controlToken string, workspaceIDs []string) error {
	wanted := make(map[string]struct{}, len(workspaceIDs))
	for _, workspaceID := range workspaceIDs {
		if workspaceID = strings.TrimSpace(workspaceID); workspaceID != "" {
			wanted[workspaceID] = struct{}{}
		}
	}
	targets, err := daemonCore.machineControlTargets(wanted)
	if err != nil {
		return err
	}
	for _, current := range targets {
		if err := RequestWorkspaceDaemonReregisterRuntime(ctx, current.controlURL, controlToken, current.identity); err != nil {
			return fmt.Errorf("re-register WorkspaceDaemon %s Runtime set: %w", current.identity.WorkspaceID, err)
		}
	}
	return nil
}

func (daemonCore *DaemonCore) machineControlTargets(wanted map[string]struct{}) ([]workspaceDaemonControlTarget, error) {
	daemonCore.mu.RLock()
	defer daemonCore.mu.RUnlock()
	scope := wanted
	if len(scope) == 0 {
		scope = make(map[string]struct{}, len(daemonCore.desiredWorkspaceIDs))
		for _, workspaceID := range daemonCore.desiredWorkspaceIDs {
			scope[workspaceID] = struct{}{}
		}
	}
	targets := make([]workspaceDaemonControlTarget, 0, len(scope))
	for workspaceID := range scope {
		daemon := daemonCore.workspaceDaemons[workspaceID]
		if daemon == nil || daemon.status() != WorkspaceDaemonRunning || daemon.process == nil || daemon.controlEndpoint == "" {
			return nil, fmt.Errorf("WorkspaceDaemon %s is not ready for machine control", workspaceID)
		}
		targets = append(targets, workspaceDaemonControlTarget{
			identity:   WorkspaceDaemonIdentity{WorkspaceID: workspaceID, DaemonInstanceID: daemon.daemonInstanceID, PID: daemon.process.PID()},
			controlURL: daemon.controlEndpoint,
		})
	}
	sort.Slice(targets, func(i, j int) bool { return targets[i].identity.WorkspaceID < targets[j].identity.WorkspaceID })
	return targets, nil
}

func (daemonCore *DaemonCore) availableMachineControlTargets() []workspaceDaemonControlTarget {
	daemonCore.mu.RLock()
	defer daemonCore.mu.RUnlock()
	targets := make([]workspaceDaemonControlTarget, 0, len(daemonCore.workspaceDaemons))
	for workspaceID, daemon := range daemonCore.workspaceDaemons {
		if daemon.process == nil || daemon.status() != WorkspaceDaemonRunning || daemon.controlEndpoint == "" {
			continue
		}
		targets = append(targets, workspaceDaemonControlTarget{
			identity:   WorkspaceDaemonIdentity{WorkspaceID: workspaceID, DaemonInstanceID: daemon.daemonInstanceID, PID: daemon.process.PID()},
			controlURL: daemon.controlEndpoint,
		})
	}
	sort.Slice(targets, func(i, j int) bool { return targets[i].identity.WorkspaceID < targets[j].identity.WorkspaceID })
	return targets
}

func (daemonCore *DaemonCore) workspaceDaemonLocked(workspaceID string) *managedWorkspaceDaemon {
	daemon := daemonCore.workspaceDaemons[workspaceID]
	if daemon == nil {
		daemon = &managedWorkspaceDaemon{}
		daemonCore.workspaceDaemons[workspaceID] = daemon
	}
	return daemon
}
