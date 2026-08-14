package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/multica-ai/multica/server/internal/computer"
)

func (d *Daemon) listenBindingCredentialProxy() (net.Listener, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("listen for Binding child Credential Proxy: %w", err)
	}
	addr, ok := listener.Addr().(*net.TCPAddr)
	if !ok || addr.Port < 1 {
		_ = listener.Close()
		return nil, errors.New("Binding child Credential Proxy did not receive a TCP port")
	}
	d.cfg.HealthPort = addr.Port
	return listener, nil
}

func (d *Daemon) serveBindingCredentialProxy(ctx context.Context, listener net.Listener) {
	mux := http.NewServeMux()
	d.registerCredentialProxyRoutes(mux)
	d.serveLocalHTTP(ctx, listener, mux, "Binding child Credential Proxy")
}

// BindingChildRunConfig contains the complete process-level interface for one
// Workspace Execution Binding child. The bootstrap fixes immutable identity;
// Config supplies provider implementation settings inherited from the host.
type BindingChildRunConfig struct {
	Daemon         Config
	Bootstrap      computer.BindingChildBootstrap
	Logger         *slog.Logger
	PublishReady   func(computer.BindingChildReady) error
	RefreshEvery   time.Duration
	HostLeaseEvery time.Duration
}

// RunBindingChild owns one Workspace Runner and all of its workspace-scoped
// execution state until ctx ends. It deliberately does not acquire the
// machine-wide resident lease or bind the Computer health listener.
func RunBindingChild(ctx context.Context, config BindingChildRunConfig) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if config.Logger == nil {
		return errors.New("Binding child logger is required")
	}
	if config.PublishReady == nil {
		return errors.New("Binding child Ready publisher is required")
	}
	bootstrap := config.Bootstrap
	workspaceID := strings.TrimSpace(bootstrap.WorkspaceID)
	if workspaceID == "" || strings.TrimSpace(config.Daemon.DaemonID) != strings.TrimSpace(bootstrap.ComputerID) {
		return errors.New("Binding child identity does not match daemon config")
	}
	if config.Daemon.ComputerGeneration != bootstrap.ComputerGeneration || config.Daemon.Environment != bootstrap.Environment || config.Daemon.ServerBaseURL != bootstrap.ServerBaseURL || config.Daemon.BindingsRoot != bootstrap.BindingsRoot || config.Daemon.WorkspacesRoot != bootstrap.WorkspacesRoot {
		return errors.New("Binding child bootstrap does not match daemon config")
	}
	config.Daemon.BindingStateRoot = filepath.Join(bootstrap.BindingsRoot, "binding-children", bootstrap.Environment, workspaceID)
	bindingLease, err := computer.AcquireBindingChildLease(ctx, bootstrap.BindingsRoot, bootstrap.Environment, workspaceID)
	if err != nil {
		return err
	}
	defer bindingLease.Close()
	config.Daemon.WorkspaceID = workspaceID
	d := newDaemonForRole(config.Daemon, config.Logger, daemonProcessBindingChild)
	d.rootCtx = ctx
	hostControl := newBindingHostControlClient(bootstrap.HostControlURL, config.Daemon.LocalControlToken, bindingChildControlIdentity{
		WorkspaceID:      workspaceID,
		RunnerGeneration: bootstrap.RunnerGeneration,
		PID:              os.Getpid(),
	})
	attestCtx, stopAttest := context.WithTimeout(ctx, 5*time.Second)
	err = hostControl.AwaitAttest(attestCtx)
	stopAttest()
	if err != nil {
		return err
	}
	remoteAdmission := newRemoteAgentProcessAdmission(hostControl)
	defer remoteAdmission.Close()
	d.processAdmission = remoteAdmission
	d.canonicalRuntimes.setMachineProcessAdmission(workspaceID, remoteAdmission)
	d.bindingHostControl = hostControl
	d.bindingDiagnostics = newBindingChildDiagnosticForwarder(hostControl)
	defer d.bindingDiagnostics.Close()
	d.runnerDiagnostics = d.bindingDiagnostics
	credentialProxyListener, err := d.listenBindingCredentialProxy()
	if err != nil {
		return err
	}
	defer credentialProxyListener.Close()
	childControlURL := "http://" + credentialProxyListener.Addr().String()
	go d.serveBindingRuntimeHTTP(ctx, credentialProxyListener, bootstrap)
	defer func() { _ = d.canonicalRuntimes.closeAll() }()

	bindings, err := d.configuredWorkspaceBindings()
	if err != nil {
		return err
	}
	binding, ok := bindings[workspaceID]
	if !ok {
		return fmt.Errorf("Workspace Binding %q is not active for this Computer generation", workspaceID)
	}
	temporaryProfileAuth, err := d.prepareBindingExecutionCredential(binding, bootstrap.PreviousPackageUpgradeBootstrap)
	if err != nil {
		return err
	}
	response := &RegisterResponse{
		DaemonToken: binding.Credential, DaemonTokenExpiresAt: binding.CredentialExpiresAt.UTC().Format(time.RFC3339Nano),
	}
	response, err = func() (*RegisterResponse, error) {
		if temporaryProfileAuth {
			defer d.client.SetToken("")
		}
		if len(d.cfg.Agents) == 0 {
			if temporaryProfileAuth {
				return nil, fmt.Errorf("repair previous-package Workspace Binding %q: no provider Runtime can rotate its credential", workspaceID)
			}
			return response, nil
		}
		return d.registerRuntimesForWorkspace(ctx, workspaceID)
	}()
	if err != nil {
		return err
	}
	runtimeIDs := make([]string, 0, len(response.Runtimes))
	d.mu.Lock()
	d.workspaces[workspaceID] = newWorkspaceState(workspaceID, nil, response.ServerCapabilities...)
	for _, runtime := range response.Runtimes {
		runtimeIDs = append(runtimeIDs, runtime.ID)
		d.runtimeIndex[runtime.ID] = runtime
	}
	d.workspaces[workspaceID].runtimeIDs = append([]string(nil), runtimeIDs...)
	d.mu.Unlock()
	if err := hostControl.reportRuntimeSet(ctx, response.Runtimes, response.DaemonToken, response.DaemonTokenExpiresAt); err != nil {
		return fmt.Errorf("report Binding child Runtime set: %w", err)
	}
	d.notifyRuntimeSetChanged()
	if len(runtimeIDs) > 0 {
		defer d.deregisterRuntimes()
	}
	for _, runtimeID := range runtimeIDs {
		if err := d.client.RecoverOrphans(ctx, runtimeID); err != nil && d.logger != nil {
			d.logger.Warn("Binding child orphan recovery failed", "workspace_id", workspaceID, "runtime_id", runtimeID, "error", err)
		}
	}
	runner, err := d.newWorkspaceRunner(workspaceID)
	if err != nil {
		return err
	}
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	var (
		readyOnce sync.Once
		readyErr  error
	)
	publishReady := func() {
		readyOnce.Do(func() {
			readyErr = config.PublishReady(computer.BindingChildReady{
				ProtocolVersion: computer.BindingChildProtocolVersion,
				WorkspaceID:     workspaceID, RunnerGeneration: bootstrap.RunnerGeneration,
				PID: os.Getpid(), ControlURL: childControlURL,
			})
			if readyErr != nil {
				cancel()
			}
		})
	}
	// DaemonCore liveness is the Workspace Runner socket, matching Raft
	// /daemon/connect. Ready waits for that connect, including zero-runtime
	// Computers.
	runner.onReady = publishReady
	taskWakeups := make(chan taskWakeup, 256)
	go d.taskWakeupLoop(runCtx, taskWakeups)
	go d.residentCrashWatchLoop(runCtx)
	go d.sharedSkillsSyncLoop(runCtx)
	pollDone := make(chan error, 1)
	go func() {
		pollDone <- d.pollLoop(runCtx, taskWakeups)
	}()
	refreshEvery := config.RefreshEvery
	if refreshEvery <= 0 {
		refreshEvery = DefaultWorkspaceSyncInterval
	}
	refreshDone := make(chan error, 1)
	go func() {
		refreshDone <- d.bindingWorkspaceRefreshLoop(runCtx, workspaceID, response.DaemonToken, refreshEvery)
	}()
	hostLeaseEvery := config.HostLeaseEvery
	if hostLeaseEvery <= 0 {
		hostLeaseEvery = time.Second
	}
	hostLeaseDone := make(chan error, 1)
	go func() {
		hostLeaseDone <- bindingHostLeaseLoop(runCtx, hostControl, hostLeaseEvery)
	}()
	runnerDone := make(chan struct{})
	go func() {
		defer close(runnerDone)
		runner.Run(runCtx)
	}()
	select {
	case <-ctx.Done():
	case err := <-pollDone:
		if err != nil && !errors.Is(err, context.Canceled) {
			cancel()
			<-runnerDone
			return fmt.Errorf("Binding child task execution: %w", err)
		}
	case err := <-refreshDone:
		if err != nil && !errors.Is(err, context.Canceled) {
			cancel()
			<-runnerDone
			return fmt.Errorf("Binding child membership refresh: %w", err)
		}
	case err := <-hostLeaseDone:
		if err != nil && !errors.Is(err, context.Canceled) {
			cancel()
			<-runnerDone
			return fmt.Errorf("Binding child lost Computer Host lease: %w", err)
		}
	case <-runnerDone:
		if readyErr != nil {
			return fmt.Errorf("publish Binding child Ready: %w", readyErr)
		}
		if ctx.Err() == nil {
			return errors.New("Binding child Workspace Runner stopped unexpectedly")
		}
	}
	cancel()
	<-runnerDone
	if readyErr != nil {
		return fmt.Errorf("publish Binding child Ready: %w", readyErr)
	}
	return nil
}

func (d *Daemon) prepareBindingExecutionCredential(binding computer.WorkspaceBinding, previousPackageBootstrap bool) (bool, error) {
	workspaceID := strings.TrimSpace(binding.WorkspaceID)
	if strings.TrimSpace(binding.Credential) == "" {
		return false, fmt.Errorf("Workspace Binding %q has no live execution credential", workspaceID)
	}
	if binding.CredentialExpiresAt.After(time.Now()) {
		d.client.SetWorkspaceDaemonToken(workspaceID, binding.Credential, binding.CredentialExpiresAt)
		return false, nil
	}
	// TODO(previous-package-bootstrap): Remove after v0.4.24-alpha.55 is no
	// longer a supported direct self-upgrade source. alpha.55 refreshed scoped
	// credentials only in memory, so its successor may see an expired persisted
	// credential. Use profile auth only for this marked bootstrap, rotate and
	// persist the scoped credential, then clear profile auth before Runner Ready.
	if !previousPackageBootstrap {
		return false, fmt.Errorf("Workspace Binding %q has no live execution credential", workspaceID)
	}
	if d.client.Token() == "" {
		if err := d.resolveAuth(); err != nil {
			return false, fmt.Errorf("repair previous-package Workspace Binding %q: %w", workspaceID, err)
		}
	}
	if d.client.Token() == "" {
		return false, fmt.Errorf("repair previous-package Workspace Binding %q: profile auth is unavailable", workspaceID)
	}
	return true, nil
}

func bindingHostLeaseLoop(ctx context.Context, host *bindingHostControlClient, interval time.Duration) error {
	if host == nil {
		return errors.New("Binding child Host control is unavailable")
	}
	if interval <= 0 {
		interval = time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			attestCtx, cancel := context.WithTimeout(ctx, interval)
			err := host.Attest(attestCtx)
			cancel()
			if err != nil {
				return err
			}
		}
	}
}

func (d *Daemon) bindingWorkspaceRefreshLoop(ctx context.Context, workspaceID, initialCredential string, interval time.Duration) error {
	if interval <= 0 {
		interval = DefaultWorkspaceSyncInterval
	}
	lastBindingCredential := strings.TrimSpace(initialCredential)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			bindings, err := d.configuredWorkspaceBindings()
			if err != nil {
				if d.logger != nil {
					d.logger.Warn("Binding child could not read its connection", "workspace_id", workspaceID, "error", err)
				}
				continue
			}
			binding, ok := bindings[workspaceID]
			if !ok || binding.Credential == "" || !binding.CredentialExpiresAt.After(time.Now()) {
				return fmt.Errorf("Workspace Binding %q is no longer active", workspaceID)
			}
			credentialChanged := strings.TrimSpace(binding.Credential) != lastBindingCredential
			if credentialChanged || d.client.WorkspaceDaemonTokenNeedsRefresh(workspaceID, time.Now()) {
				if err := d.reregisterBindingWorkspace(ctx, workspaceID); err != nil {
					if d.logger != nil {
						d.logger.Warn("Binding child Runtime refresh failed; will retry", "workspace_id", workspaceID, "error", err)
					}
					continue
				}
				lastBindingCredential = strings.TrimSpace(binding.Credential)
				continue
			}
			// TODO(computer-liveness): Remove after v0.4.24-alpha.55 is no
			// longer a supported direct self-upgrade source. Socket ownership
			// is liveness; this HTTP probe only refreshes leftover last_seen.
			if err := d.client.ComputerHeartbeat(ctx, workspaceID, d.cfg.DaemonID, d.cfg.ComputerGeneration); err != nil && d.logger != nil {
				d.logger.Warn("Binding child heartbeat failed; will retry", "workspace_id", workspaceID, "error", err)
			}
		}
	}
}

func (d *Daemon) serveBindingRuntimeHTTP(ctx context.Context, listener net.Listener, bootstrap computer.BindingChildBootstrap) {
	mux := http.NewServeMux()
	d.registerCredentialProxyRoutes(mux)
	d.registerBindingMachineControlRoutes(mux, bootstrap)
	d.serveLocalHTTP(ctx, listener, mux, "Binding child local control")
}

func (d *Daemon) registerBindingMachineControlRoutes(mux *http.ServeMux, bootstrap computer.BindingChildBootstrap) {
	decodeIdentity := func(w http.ResponseWriter, r *http.Request) bool {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return false
		}
		if !d.localControlAuthorized(r) {
			http.Error(w, "local control authentication failed", http.StatusUnauthorized)
			return false
		}
		var request computer.BindingMachineControlRequest
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&request); err != nil || request.Identity.WorkspaceID != bootstrap.WorkspaceID || request.Identity.RunnerGeneration != bootstrap.RunnerGeneration || request.Identity.PID != os.Getpid() {
			http.Error(w, "inactive Binding child generation", http.StatusConflict)
			return false
		}
		return true
	}
	handle := func(prepare bool) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if !decodeIdentity(w, r) {
				return
			}
			if prepare {
				if err := d.beginBindingDrain(r.Context()); err != nil {
					http.Error(w, err.Error(), http.StatusConflict)
					return
				}
			} else {
				d.releaseClaimBarrier()
			}
			w.WriteHeader(http.StatusNoContent)
		}
	}
	mux.HandleFunc(computer.BindingPrepareMachineUpgradePath, handle(true))
	mux.HandleFunc(computer.BindingReleaseMachineUpgradePath, handle(false))
	mux.HandleFunc(computer.BindingPrepareEnvironmentSwitchPath, func(w http.ResponseWriter, r *http.Request) {
		if !decodeIdentity(w, r) {
			return
		}
		if !d.trySetEnvironmentSwitchBarrier() {
			http.Error(w, "another Binding handoff is already in progress", http.StatusConflict)
			return
		}
		prepared := false
		defer func() {
			if !prepared {
				d.releaseEnvironmentSwitchBarrier()
			}
		}()
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		for !d.claimBarrierDrained() {
			select {
			case <-r.Context().Done():
				http.Error(w, "environment switch cancelled while waiting for active work", http.StatusRequestTimeout)
				return
			case <-ticker.C:
			}
		}
		prepared = true
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc(computer.BindingReleaseEnvironmentSwitchPath, func(w http.ResponseWriter, r *http.Request) {
		if !decodeIdentity(w, r) {
			return
		}
		d.releaseEnvironmentSwitchBarrier()
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc(computer.BindingReregisterRuntimePath, func(w http.ResponseWriter, r *http.Request) {
		if !decodeIdentity(w, r) {
			return
		}
		if err := d.reregisterBindingWorkspace(r.Context(), bootstrap.WorkspaceID); err != nil {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
}

func (d *Daemon) reregisterBindingWorkspace(ctx context.Context, workspaceID string) error {
	bindings, err := d.configuredWorkspaceBindings()
	if err != nil {
		return err
	}
	binding, ok := bindings[strings.TrimSpace(workspaceID)]
	if !ok || binding.Credential == "" || !binding.CredentialExpiresAt.After(time.Now()) {
		return fmt.Errorf("Workspace Binding %q has no live execution credential", workspaceID)
	}
	d.client.SetWorkspaceDaemonToken(workspaceID, binding.Credential, binding.CredentialExpiresAt)
	// TODO(computer-liveness): Remove after v0.4.24-alpha.55 is no
	// longer a supported direct self-upgrade source. Binding validity is
	// the credential plus the Runner socket, not this HTTP probe.
	if err := d.client.ComputerHeartbeat(ctx, workspaceID, d.cfg.DaemonID, d.cfg.ComputerGeneration); err != nil && d.logger != nil {
		d.logger.Warn("Binding child heartbeat failed during Runtime refresh", "workspace_id", workspaceID, "error", err)
	}
	if len(d.cfg.Agents) > 0 {
		return d.reregisterWorkspaceAfterRuntimeGone(ctx, workspaceID)
	}
	expiresAt := binding.CredentialExpiresAt.UTC().Format(time.RFC3339Nano)
	if d.bindingHostControl == nil {
		return errors.New("Binding child Host control is unavailable")
	}
	if err := d.bindingHostControl.reportRuntimeSet(ctx, []Runtime{}, binding.Credential, expiresAt); err != nil {
		return fmt.Errorf("report zero-Agent Binding Runtime set: %w", err)
	}
	return nil
}
