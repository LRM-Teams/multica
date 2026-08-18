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

type BindingRunnerLauncher struct {
	Executable      func() (string, error)
	ComputerID      string
	Environment     string
	Profile         string
	ServerBaseURL   string
	ServiceEndpoint string
	BindingsRoot    string
	WorkspacesRoot  string
}

func (launcher BindingRunnerLauncher) Spawn(workspaceID, startIdentity string) (BindingChild, error) {
	bootstrap := BindingChildBootstrap{
		ProtocolVersion: BindingChildProtocolVersion, WorkspaceID: workspaceID,
		ComputerID: launcher.ComputerID, StartIdentity: startIdentity,
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
	return StartBindingRunner(exe, bootstrap)
}

type BindingChildSpawner func(workspaceID, startIdentity string) (BindingChild, error)

type activatableBindingChild interface {
	Activate()
}

type BindingSupervisorConfig struct {
	Spawn        BindingChildSpawner
	StateRoot    string
	Now          func() time.Time
	ReadyTimeout time.Duration
	Logger       *slog.Logger
	Released     func(BindingChildIdentity)
}

// BindingSupervisor is the Computer Host's desired-vs-actual Binding Module.
// Production children are supervised operating-system processes.
type BindingSupervisor struct {
	config BindingSupervisorConfig

	mu       sync.RWMutex
	records  map[string]*RunnerRecord
	children map[string]BindingChild
	cancels  map[string]context.CancelFunc
	controls map[string]string
	desired  map[string]struct{}
}

type bindingSupervisorStart struct {
	workspaceID   string
	startIdentity string
	ctx           context.Context
	cancel        context.CancelFunc
}

type bindingMachineControlTarget struct {
	identity   BindingChildIdentity
	controlURL string
}

func NewBindingSupervisor(config BindingSupervisorConfig) (*BindingSupervisor, error) {
	if config.Spawn == nil {
		return nil, errors.New("Binding child spawner is required")
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	if config.ReadyTimeout <= 0 {
		config.ReadyTimeout = 30 * time.Second
	}
	if err := recoverRunnerStates(config.StateRoot, config.Logger); err != nil {
		return nil, fmt.Errorf("recover persisted Binding Runner state: %w", err)
	}
	return &BindingSupervisor{
		config: config, records: make(map[string]*RunnerRecord),
		children: make(map[string]BindingChild), cancels: make(map[string]context.CancelFunc), controls: make(map[string]string), desired: make(map[string]struct{}),
	}, nil
}

func (supervisor *BindingSupervisor) Reconcile(parent context.Context, desiredWorkspaceIDs []string) {
	if supervisor == nil {
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
		identity BindingChildIdentity
		child    BindingChild
		cancel   context.CancelFunc
	}
	var stops []stoppedChild
	var starts []bindingSupervisorStart
	now := supervisor.config.Now()
	supervisor.mu.Lock()
	supervisor.desired = desired
	for workspaceID, cancel := range supervisor.cancels {
		if _, wanted := desired[workspaceID]; wanted {
			continue
		}
		child := supervisor.children[workspaceID]
		record := supervisor.records[workspaceID]
		identity := BindingChildIdentity{WorkspaceID: workspaceID}
		if record != nil {
			identity.StartIdentity = record.StartIdentity()
			record.ObserveExit(now, RunnerExitGraceful)
		}
		if child != nil {
			identity.PID = child.PID()
		}
		delete(supervisor.cancels, workspaceID)
		delete(supervisor.children, workspaceID)
		delete(supervisor.controls, workspaceID)
		stops = append(stops, stoppedChild{identity: identity, child: child, cancel: cancel})
	}
	workspaceIDs := make([]string, 0, len(desired))
	for workspaceID := range desired {
		workspaceIDs = append(workspaceIDs, workspaceID)
	}
	sort.Strings(workspaceIDs)
	for _, workspaceID := range workspaceIDs {
		if _, running := supervisor.cancels[workspaceID]; running {
			continue
		}
		record := supervisor.recordLocked(workspaceID)
		if !record.CanSpawn(true, now) {
			continue
		}
		childCtx, cancel := context.WithCancel(parent)
		startIdentity := record.ObserveSpawn()
		supervisor.cancels[workspaceID] = cancel
		starts = append(starts, bindingSupervisorStart{workspaceID: workspaceID, startIdentity: startIdentity, ctx: childCtx, cancel: cancel})
	}
	supervisor.mu.Unlock()
	for _, stopped := range stops {
		_ = removeRunnerState(supervisor.config.StateRoot, stopped.identity.WorkspaceID, stopped.identity.StartIdentity, stopped.identity.PID)
		if stopped.identity.Validate() == nil && supervisor.config.Released != nil {
			supervisor.config.Released(stopped.identity)
		}
		if stopped.child != nil {
			_ = stopped.child.Stop()
		}
		stopped.cancel()
	}
	for _, next := range starts {
		supervisor.spawn(next)
	}
}

func (supervisor *BindingSupervisor) spawn(next bindingSupervisorStart) {
	child, err := supervisor.config.Spawn(next.workspaceID, next.startIdentity)
	if err != nil {
		if supervisor.config.Logger != nil {
			supervisor.config.Logger.Warn("Workspace Runner child spawn failed", "workspace_id", next.workspaceID, "error", err)
		}
		next.cancel()
		supervisor.observeExit(next.workspaceID, next.startIdentity, nil, RunnerExitCrash)
		return
	}
	supervisor.mu.Lock()
	record := supervisor.records[next.workspaceID]
	if record == nil || !record.HasChild() || record.StartIdentity() != next.startIdentity {
		supervisor.mu.Unlock()
		next.cancel()
		_ = child.Stop()
		_ = discardRunnerStateAfterSpawnFailure(supervisor.config.StateRoot, next.workspaceID, next.startIdentity, child.PID())
		supervisor.observeExit(next.workspaceID, next.startIdentity, child, RunnerExitCrash)
		return
	}
	supervisor.children[next.workspaceID] = child
	supervisor.mu.Unlock()
	stateErr := writeRunnerState(supervisor.config.StateRoot, persistedRunnerState{
		WorkspaceID: next.workspaceID, StartIdentity: next.startIdentity,
		OwnerPID: os.Getpid(), RunnerPID: child.PID(),
		RunnerIdentity: processIdentityValue(child.PID()), StartedAt: supervisor.config.Now().UTC(),
	})
	if stateErr == nil {
		stateErr = writeRunnerPID(supervisor.config.StateRoot, next.workspaceID, child.PID())
	}
	if stateErr != nil {
		if supervisor.config.Logger != nil {
			supervisor.config.Logger.Error("could not persist Binding Runner state", "workspace_id", next.workspaceID, "error", stateErr)
		}
		next.cancel()
		_ = child.Stop()
		_ = discardRunnerStateAfterSpawnFailure(supervisor.config.StateRoot, next.workspaceID, next.startIdentity, child.PID())
		supervisor.observeExit(next.workspaceID, next.startIdentity, child, RunnerExitCrash)
		return
	}
	if activatable, ok := child.(activatableBindingChild); ok {
		activatable.Activate()
	}
	go supervisor.supervise(next.workspaceID, next.startIdentity, child, next.ctx, next.cancel)
}

func (supervisor *BindingSupervisor) supervise(workspaceID, startIdentity string, child BindingChild, ctx context.Context, cancel context.CancelFunc) {
	class := RunnerExitGraceful
	if readyChild, ok := child.(ReadyBindingChild); ok {
		readyCtx, stopReady := context.WithTimeout(ctx, supervisor.config.ReadyTimeout)
		ready, err := readyChild.AwaitReady(readyCtx)
		stopReady()
		if err != nil {
			if ctx.Err() == nil && supervisor.config.Logger != nil {
				supervisor.config.Logger.Warn("Workspace Runner child readiness failed", "workspace_id", workspaceID, "error", err)
			}
			if ctx.Err() == nil {
				class = RunnerExitCrash
			}
			_ = child.Stop()
		} else {
			supervisor.observeReady(workspaceID, startIdentity, child, ready.RunnerEndpoint)
		}
	} else {
		supervisor.observeReady(workspaceID, startIdentity, child, "")
	}
	waitClass := child.Wait()
	if ctx.Err() == nil && class != RunnerExitCrash {
		class = waitClass
	}
	if cancel != nil {
		cancel()
	}
	supervisor.observeExit(workspaceID, startIdentity, child, class)
}

func (supervisor *BindingSupervisor) observeReady(workspaceID, startIdentity string, child BindingChild, controlEndpoint string) {
	supervisor.mu.Lock()
	defer supervisor.mu.Unlock()
	record := supervisor.records[workspaceID]
	if record == nil || supervisor.children[workspaceID] != child {
		return
	}
	if record.ObserveReady(startIdentity) && controlEndpoint != "" {
		supervisor.controls[workspaceID] = controlEndpoint
	}
	if child != nil {
		startedAt := supervisor.config.Now().UTC()
		state := persistedRunnerState{}
		if state, err := readRunnerState(runnerStatePath(supervisor.config.StateRoot, workspaceID)); err == nil && !state.StartedAt.IsZero() {
			startedAt = state.StartedAt
		}
		state = persistedRunnerState{
			WorkspaceID: workspaceID, StartIdentity: startIdentity,
			OwnerPID: os.Getpid(), RunnerPID: child.PID(),
			RunnerIdentity: processIdentityValue(child.PID()), StartedAt: startedAt,
		}
		_ = writeRunnerState(supervisor.config.StateRoot, state)
		_ = writeRunnerConnected(supervisor.config.StateRoot, workspaceID, persistedRunnerConnected{PID: child.PID(), ConnectedAt: supervisor.config.Now().UTC(), RunnerEndpoint: controlEndpoint})
	}
}

func (supervisor *BindingSupervisor) observeExit(workspaceID, startIdentity string, child BindingChild, class RunnerExitClass) {
	supervisor.mu.Lock()
	record := supervisor.records[workspaceID]
	if record == nil || !record.HasChild() || record.StartIdentity() != startIdentity {
		supervisor.mu.Unlock()
		return
	}
	if child != nil && supervisor.children[workspaceID] != child {
		supervisor.mu.Unlock()
		return
	}
	identity := BindingChildIdentity{WorkspaceID: workspaceID, StartIdentity: startIdentity}
	if child != nil {
		identity.PID = child.PID()
	}
	record.ObserveExit(supervisor.config.Now(), class)
	delete(supervisor.cancels, workspaceID)
	delete(supervisor.children, workspaceID)
	delete(supervisor.controls, workspaceID)
	supervisor.mu.Unlock()
	if child != nil {
		_ = removeRunnerState(supervisor.config.StateRoot, workspaceID, startIdentity, child.PID())
	}
	if identity.Validate() == nil && supervisor.config.Released != nil {
		supervisor.config.Released(identity)
	}
}

func (supervisor *BindingSupervisor) Stop() {
	if supervisor == nil {
		return
	}
	supervisor.Reconcile(context.Background(), nil)
}

func (supervisor *BindingSupervisor) Current(identity BindingChildIdentity) bool {
	if supervisor == nil || identity.Validate() != nil {
		return false
	}
	supervisor.mu.RLock()
	record := supervisor.records[identity.WorkspaceID]
	child := supervisor.children[identity.WorkspaceID]
	current := record != nil && record.HasChild() && record.StartIdentity() == identity.StartIdentity && child != nil && child.PID() == identity.PID
	supervisor.mu.RUnlock()
	return current
}

func (supervisor *BindingSupervisor) Snapshot(workspaceID string) (RunnerRecord, int, bool) {
	if supervisor == nil {
		return RunnerRecord{}, 0, false
	}
	supervisor.mu.RLock()
	defer supervisor.mu.RUnlock()
	record := supervisor.records[strings.TrimSpace(workspaceID)]
	if record == nil {
		return RunnerRecord{}, 0, false
	}
	pid := 0
	if child := supervisor.children[strings.TrimSpace(workspaceID)]; child != nil {
		pid = child.PID()
	}
	return *record, pid, true
}

// PrepareMachineUpgrade asks every live Binding child to close its execution
// admission barrier and drain/terminate work through the child's own runtime
// implementation. If any child rejects preparation, already-prepared siblings
// are released so a failed machine operation cannot strand them paused.
func (supervisor *BindingSupervisor) PrepareMachineUpgrade(ctx context.Context, controlToken string) error {
	return supervisor.PrepareSiblingMachineUpgrade(ctx, controlToken, "")
}

func (supervisor *BindingSupervisor) PrepareSiblingMachineUpgrade(ctx context.Context, controlToken, initiatorWorkspaceID string) error {
	targets, err := supervisor.machineControlTargets(nil)
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
	prepared := make([]bindingMachineControlTarget, 0, len(targets))
	for _, current := range targets {
		if err := RequestBindingPrepareMachineUpgrade(ctx, current.controlURL, controlToken, current.identity); err != nil {
			for _, prior := range prepared {
				_ = RequestBindingReleaseMachineUpgrade(context.Background(), prior.controlURL, controlToken, prior.identity)
			}
			return fmt.Errorf("prepare Binding %s for Machine Upgrade: %w", current.identity.WorkspaceID, err)
		}
		prepared = append(prepared, current)
	}
	return nil
}

func (supervisor *BindingSupervisor) ReleaseMachineUpgrade(ctx context.Context, controlToken string) error {
	targets := supervisor.availableMachineControlTargets()
	var failures []error
	for _, current := range targets {
		if err := RequestBindingReleaseMachineUpgrade(ctx, current.controlURL, controlToken, current.identity); err != nil {
			failures = append(failures, fmt.Errorf("release Binding %s after Machine Upgrade: %w", current.identity.WorkspaceID, err))
		}
	}
	return errors.Join(failures...)
}

func (supervisor *BindingSupervisor) PrepareEnvironmentSwitch(ctx context.Context, controlToken string) error {
	targets, err := supervisor.machineControlTargets(nil)
	if err != nil {
		return err
	}
	prepared := make([]bindingMachineControlTarget, 0, len(targets))
	for _, current := range targets {
		if err := RequestBindingPrepareEnvironmentSwitch(ctx, current.controlURL, controlToken, current.identity); err != nil {
			for _, prior := range prepared {
				_ = RequestBindingReleaseEnvironmentSwitch(context.Background(), prior.controlURL, controlToken, prior.identity)
			}
			return fmt.Errorf("prepare Binding %s for environment switch: %w", current.identity.WorkspaceID, err)
		}
		prepared = append(prepared, current)
	}
	return nil
}

func (supervisor *BindingSupervisor) ReleaseEnvironmentSwitch(ctx context.Context, controlToken string) error {
	targets := supervisor.availableMachineControlTargets()
	var failures []error
	for _, current := range targets {
		if err := RequestBindingReleaseEnvironmentSwitch(ctx, current.controlURL, controlToken, current.identity); err != nil {
			failures = append(failures, fmt.Errorf("release Binding %s after environment switch: %w", current.identity.WorkspaceID, err))
		}
	}
	return errors.Join(failures...)
}

func (supervisor *BindingSupervisor) DeliverComputerUpgrade(ctx context.Context, controlToken string, command protocol.ComputerUpgradePayload) error {
	targets := supervisor.availableMachineControlTargets()
	if len(targets) == 0 {
		return errors.New("Computer has no ready Binding for Machine Upgrade")
	}
	return RequestBindingComputerUpgrade(ctx, targets[0].controlURL, controlToken, targets[0].identity, command)
}

func (supervisor *BindingSupervisor) DeliverComputerUpgradeEvent(ctx context.Context, controlToken string, identity BindingChildIdentity, eventType string, payload any) error {
	supervisor.mu.RLock()
	endpoint := supervisor.controls[identity.WorkspaceID]
	child := supervisor.children[identity.WorkspaceID]
	record := supervisor.records[identity.WorkspaceID]
	supervisor.mu.RUnlock()
	if child == nil || record == nil || record.Lifecycle != RunnerLifecycleRunning || endpoint == "" || record.StartIdentity() != identity.StartIdentity || child.PID() != identity.PID {
		return errors.New("Computer runner is unavailable for upgrade event")
	}
	return RequestBindingComputerUpgradeEvent(ctx, endpoint, controlToken, identity, eventType, payload)
}

func (supervisor *BindingSupervisor) ReregisterBindings(ctx context.Context, controlToken string, workspaceIDs []string) error {
	wanted := make(map[string]struct{}, len(workspaceIDs))
	for _, workspaceID := range workspaceIDs {
		if workspaceID = strings.TrimSpace(workspaceID); workspaceID != "" {
			wanted[workspaceID] = struct{}{}
		}
	}
	targets, err := supervisor.machineControlTargets(wanted)
	if err != nil {
		return err
	}
	for _, current := range targets {
		if err := RequestBindingReregisterRuntime(ctx, current.controlURL, controlToken, current.identity); err != nil {
			return fmt.Errorf("re-register Binding %s Runtime set: %w", current.identity.WorkspaceID, err)
		}
	}
	return nil
}

func (supervisor *BindingSupervisor) machineControlTargets(wanted map[string]struct{}) ([]bindingMachineControlTarget, error) {
	supervisor.mu.RLock()
	defer supervisor.mu.RUnlock()
	scope := wanted
	if len(scope) == 0 {
		scope = supervisor.desired
	}
	targets := make([]bindingMachineControlTarget, 0, len(scope))
	for workspaceID := range scope {
		child := supervisor.children[workspaceID]
		record := supervisor.records[workspaceID]
		controlURL := supervisor.controls[workspaceID]
		if record == nil || record.Lifecycle != RunnerLifecycleRunning || child == nil || controlURL == "" {
			return nil, fmt.Errorf("Binding %s is not ready for machine control", workspaceID)
		}
		targets = append(targets, bindingMachineControlTarget{
			identity:   BindingChildIdentity{WorkspaceID: workspaceID, StartIdentity: record.StartIdentity(), PID: child.PID()},
			controlURL: controlURL,
		})
	}
	sort.Slice(targets, func(i, j int) bool { return targets[i].identity.WorkspaceID < targets[j].identity.WorkspaceID })
	return targets, nil
}

func (supervisor *BindingSupervisor) availableMachineControlTargets() []bindingMachineControlTarget {
	supervisor.mu.RLock()
	defer supervisor.mu.RUnlock()
	targets := make([]bindingMachineControlTarget, 0, len(supervisor.controls))
	for workspaceID, controlURL := range supervisor.controls {
		child := supervisor.children[workspaceID]
		record := supervisor.records[workspaceID]
		if child == nil || record == nil || record.Lifecycle != RunnerLifecycleRunning || controlURL == "" {
			continue
		}
		targets = append(targets, bindingMachineControlTarget{
			identity:   BindingChildIdentity{WorkspaceID: workspaceID, StartIdentity: record.StartIdentity(), PID: child.PID()},
			controlURL: controlURL,
		})
	}
	sort.Slice(targets, func(i, j int) bool { return targets[i].identity.WorkspaceID < targets[j].identity.WorkspaceID })
	return targets
}

func (supervisor *BindingSupervisor) recordLocked(workspaceID string) *RunnerRecord {
	record := supervisor.records[workspaceID]
	if record == nil {
		record = &RunnerRecord{Lifecycle: RunnerLifecycleStopped}
		supervisor.records[workspaceID] = record
	}
	return record
}
