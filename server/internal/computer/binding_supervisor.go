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
)

type BindingRunnerLauncher struct {
	Executable         func() (string, error)
	DaemonID           string
	ComputerGeneration int64
	Environment        string
	Profile            string
	ServerBaseURL      string
	HostControlURL     string
	BindingsRoot       string
	WorkspacesRoot     string
}

func (launcher BindingRunnerLauncher) Spawn(workspaceID string, runnerGeneration int64) (BindingChild, error) {
	executable := launcher.Executable
	if executable == nil {
		executable = os.Executable
	}
	exe, err := executable()
	if err != nil {
		return nil, err
	}
	return StartBindingRunner(exe, BindingChildBootstrap{
		ProtocolVersion: BindingChildProtocolVersion, WorkspaceID: workspaceID,
		DaemonID: launcher.DaemonID, ComputerGeneration: launcher.ComputerGeneration,
		RunnerGeneration: runnerGeneration, Environment: launcher.Environment, Profile: launcher.Profile,
		ServerBaseURL: launcher.ServerBaseURL, HostControlURL: launcher.HostControlURL,
		BindingsRoot: launcher.BindingsRoot, WorkspacesRoot: launcher.WorkspacesRoot,
	})
}

type BindingChildSpawner func(workspaceID string, runnerGeneration int64) (BindingChild, error)

type BindingSupervisorConfig struct {
	Spawn        BindingChildSpawner
	Now          func() time.Time
	ReadyTimeout time.Duration
	Logger       *slog.Logger
	Released     func(BindingChildIdentity)
}

// BindingSupervisor is the Computer Host's desired-vs-actual process Module.
// It owns no Workspace execution object; its only child handle is an OS
// process plus the immutable generation/PID fence.
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
	workspaceID string
	generation  int64
	ctx         context.Context
	cancel      context.CancelFunc
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
			identity.RunnerGeneration = record.Generation()
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
		record.ObserveSpawn()
		supervisor.cancels[workspaceID] = cancel
		starts = append(starts, bindingSupervisorStart{workspaceID: workspaceID, generation: record.Generation(), ctx: childCtx, cancel: cancel})
	}
	supervisor.mu.Unlock()
	for _, stopped := range stops {
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
	child, err := supervisor.config.Spawn(next.workspaceID, next.generation)
	if err != nil {
		if supervisor.config.Logger != nil {
			supervisor.config.Logger.Warn("Workspace Runner child spawn failed", "workspace_id", next.workspaceID, "error", err)
		}
		next.cancel()
		supervisor.observeExit(next.workspaceID, next.generation, nil, RunnerExitCrash)
		return
	}
	supervisor.mu.Lock()
	record := supervisor.records[next.workspaceID]
	if record == nil || !record.HasChild() || record.Generation() != next.generation {
		supervisor.mu.Unlock()
		next.cancel()
		_ = child.Stop()
		return
	}
	supervisor.children[next.workspaceID] = child
	supervisor.mu.Unlock()
	go supervisor.supervise(next.workspaceID, next.generation, child, next.ctx, next.cancel)
}

func (supervisor *BindingSupervisor) supervise(workspaceID string, generation int64, child BindingChild, ctx context.Context, cancel context.CancelFunc) {
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
			supervisor.observeReady(workspaceID, generation, child, ready.ControlURL)
		}
	} else {
		supervisor.observeReady(workspaceID, generation, child, "")
	}
	waitClass := child.Wait()
	if ctx.Err() == nil && class != RunnerExitCrash {
		class = waitClass
	}
	if cancel != nil {
		cancel()
	}
	supervisor.observeExit(workspaceID, generation, child, class)
}

func (supervisor *BindingSupervisor) observeReady(workspaceID string, generation int64, child BindingChild, controlURL string) {
	supervisor.mu.Lock()
	defer supervisor.mu.Unlock()
	record := supervisor.records[workspaceID]
	if record == nil || supervisor.children[workspaceID] != child {
		return
	}
	if record.ObserveReady(generation) && controlURL != "" {
		supervisor.controls[workspaceID] = controlURL
	}
}

func (supervisor *BindingSupervisor) observeExit(workspaceID string, generation int64, child BindingChild, class RunnerExitClass) {
	supervisor.mu.Lock()
	record := supervisor.records[workspaceID]
	if record == nil || !record.HasChild() || record.Generation() != generation {
		supervisor.mu.Unlock()
		return
	}
	if child != nil && supervisor.children[workspaceID] != child {
		supervisor.mu.Unlock()
		return
	}
	identity := BindingChildIdentity{WorkspaceID: workspaceID, RunnerGeneration: generation}
	if child != nil {
		identity.PID = child.PID()
	}
	record.ObserveExit(supervisor.config.Now(), class)
	delete(supervisor.cancels, workspaceID)
	delete(supervisor.children, workspaceID)
	delete(supervisor.controls, workspaceID)
	supervisor.mu.Unlock()
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
	current := record != nil && record.HasChild() && record.Generation() == identity.RunnerGeneration && child != nil && child.PID() == identity.PID
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
	targets, err := supervisor.machineControlTargets(nil)
	if err != nil {
		return err
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
			identity:   BindingChildIdentity{WorkspaceID: workspaceID, RunnerGeneration: record.Generation(), PID: child.PID()},
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
			identity:   BindingChildIdentity{WorkspaceID: workspaceID, RunnerGeneration: record.Generation(), PID: child.PID()},
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
