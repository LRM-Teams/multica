package daemon

import (
	"bytes"
	"context"
	"crypto/subtle"
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
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func (d *Daemon) listenWorkspaceDaemonCredentialProxy() (net.Listener, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("listen for WorkspaceDaemon Credential Proxy: %w", err)
	}
	addr, ok := listener.Addr().(*net.TCPAddr)
	if !ok || addr.Port < 1 {
		_ = listener.Close()
		return nil, errors.New("WorkspaceDaemon Credential Proxy did not receive a TCP port")
	}
	d.cfg.HealthPort = addr.Port
	return listener, nil
}

func (d *Daemon) serveWorkspaceDaemonCredentialProxy(ctx context.Context, listener net.Listener) {
	mux := http.NewServeMux()
	d.registerLocalControlRoutes(mux)
	d.serveLocalHTTP(ctx, listener, mux, "WorkspaceDaemon Credential Proxy")
}

// WorkspaceDaemonProcessConfig contains the process-level interface for one
// WorkspaceDaemon. The bootstrap fixes immutable identity; Config supplies
// provider settings inherited from ComputerCore.
type WorkspaceDaemonProcessConfig struct {
	Daemon       Config
	Bootstrap    computer.WorkspaceDaemonBootstrap
	Logger       *slog.Logger
	PublishReady func(computer.WorkspaceDaemonReady) error
	RefreshEvery time.Duration
}

// RunWorkspaceDaemonProcess owns one WorkspaceDaemon and all workspace-scoped
// execution state until ctx ends. It deliberately does not acquire the
// machine-wide resident lease or bind the Computer health listener.
func RunWorkspaceDaemonProcess(ctx context.Context, config WorkspaceDaemonProcessConfig) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if config.Logger == nil {
		return errors.New("WorkspaceDaemon logger is required")
	}
	if config.PublishReady == nil {
		return errors.New("WorkspaceDaemon Ready publisher is required")
	}
	bootstrap := config.Bootstrap
	workspaceID := strings.TrimSpace(bootstrap.WorkspaceID)
	if workspaceID == "" || strings.TrimSpace(config.Daemon.DaemonID) != strings.TrimSpace(bootstrap.ComputerID) {
		return errors.New("WorkspaceDaemon identity does not match daemon config")
	}
	if config.Daemon.Environment != bootstrap.Environment || config.Daemon.ServerBaseURL != bootstrap.ServerBaseURL || config.Daemon.BindingsRoot != bootstrap.BindingsRoot || config.Daemon.WorkspacesRoot != bootstrap.WorkspacesRoot {
		return errors.New("WorkspaceDaemon bootstrap does not match daemon config")
	}
	config.Daemon.BindingStateRoot = filepath.Join(bootstrap.BindingsRoot, "binding-children", bootstrap.Environment, workspaceID)
	config.Daemon.WorkspaceID = workspaceID
	d := newDaemonForRole(config.Daemon, config.Logger, daemonProcessWorkspaceDaemon)
	d.rootCtx = ctx
	identity := workspaceDaemonControlIdentity{
		WorkspaceID:      workspaceID,
		DaemonInstanceID: d.instanceID,
		PID:              os.Getpid(),
	}
	computerControl := newWorkspaceDaemonComputerControl(bootstrap.ServiceEndpoint, config.Daemon.LocalControlToken, identity)
	d.computerControl = computerControl
	d.workspaceDaemonDiagnostics = newWorkspaceDaemonDiagnosticForwarder(computerControl)
	defer d.workspaceDaemonDiagnostics.Close()
	d.runnerDiagnostics = d.workspaceDaemonDiagnostics
	credentialProxyListener, err := d.listenWorkspaceDaemonCredentialProxy()
	if err != nil {
		return err
	}
	defer credentialProxyListener.Close()
	go d.serveWorkspaceDaemonCredentialProxy(ctx, credentialProxyListener)
	controlEndpoint := computer.WorkspaceDaemonControlEndpoint(bootstrap.BindingsRoot, identity)
	controlListener, err := computer.ListenLocalControl(controlEndpoint)
	if err != nil {
		return fmt.Errorf("listen for WorkspaceDaemon IPC: %w", err)
	}
	defer controlListener.Close()
	go d.serveWorkspaceDaemonControlRPC(ctx, controlListener, bootstrap)
	defer func() {
		d.canonicalRuntimes.revokeAllLaunchCredentials()
		_ = d.canonicalRuntimes.forceTerminateAll()
		_ = d.canonicalRuntimes.closeAll()
	}()

	bindings, err := d.configuredWorkspaceBindings()
	if err != nil {
		return err
	}
	binding, ok := bindings[workspaceID]
	if !ok {
		return fmt.Errorf("Workspace Binding %q is not active for this Computer", workspaceID)
	}
	if err := d.prepareBindingExecutionCredential(binding); err != nil {
		return err
	}
	response := &RegisterResponse{
		DaemonToken: binding.Credential, DaemonTokenExpiresAt: binding.CredentialExpiresAt.UTC().Format(time.RFC3339Nano),
	}
	response, err = func() (*RegisterResponse, error) {
		if len(d.cfg.Agents) == 0 {
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
	if err := computerControl.reportRuntimeSet(ctx, response.Runtimes, response.DaemonToken, response.DaemonTokenExpiresAt); err != nil {
		return fmt.Errorf("report WorkspaceDaemon Runtime set: %w", err)
	}
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	d.notifyRuntimeSetChanged()
	if len(runtimeIDs) > 0 {
		defer d.deregisterRuntimes()
	}
	for _, runtimeID := range runtimeIDs {
		if err := d.client.RecoverOrphans(ctx, runtimeID); err != nil && d.logger != nil {
			d.logger.Warn("WorkspaceDaemon orphan recovery failed", "workspace_id", workspaceID, "runtime_id", runtimeID, "error", err)
		}
	}
	workspaceDaemon, err := d.newWorkspaceDaemon(workspaceID)
	if err != nil {
		return err
	}
	if err := d.adoptWorkspaceDaemon(workspaceDaemon); err != nil {
		return err
	}
	defer d.detachWorkspaceDaemon(workspaceDaemon)
	var (
		readyOnce sync.Once
		readyErr  error
	)
	publishReady := func() {
		readyOnce.Do(func() {
			readyErr = config.PublishReady(computer.WorkspaceDaemonReady{
				ProtocolVersion: computer.WorkspaceDaemonProtocolVersion,
				WorkspaceID:     workspaceID, DaemonInstanceID: identity.DaemonInstanceID,
				PID: os.Getpid(), RunnerEndpoint: controlEndpoint,
			})
			if readyErr != nil {
				cancel()
			}
		})
	}
	// DaemonCore liveness is the WorkspaceDaemon socket, matching Raft
	// /daemon/connect. Ready waits for that connect, including zero-runtime
	// Computers.
	workspaceDaemon.onReady = publishReady
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
		refreshDone <- d.workspaceRefreshLoop(runCtx, workspaceID, response.DaemonToken, refreshEvery)
	}()
	workspaceDaemonDone := make(chan struct{})
	go func() {
		defer close(workspaceDaemonDone)
		workspaceDaemon.Run(runCtx)
	}()
	select {
	case <-ctx.Done():
	case err := <-pollDone:
		if err != nil && !errors.Is(err, context.Canceled) {
			cancel()
			<-workspaceDaemonDone
			return fmt.Errorf("WorkspaceDaemon task execution: %w", err)
		}
	case err := <-refreshDone:
		if err != nil && !errors.Is(err, context.Canceled) {
			cancel()
			<-workspaceDaemonDone
			return fmt.Errorf("WorkspaceDaemon membership refresh: %w", err)
		}
	case <-workspaceDaemonDone:
		if readyErr != nil {
			return fmt.Errorf("publish WorkspaceDaemon Ready: %w", readyErr)
		}
		if ctx.Err() == nil {
			return errors.New("WorkspaceDaemon stopped unexpectedly")
		}
	}
	cancel()
	<-workspaceDaemonDone
	if readyErr != nil {
		return fmt.Errorf("publish WorkspaceDaemon Ready: %w", readyErr)
	}
	return nil
}

func (d *Daemon) prepareBindingExecutionCredential(binding computer.WorkspaceBinding) error {
	workspaceID := strings.TrimSpace(binding.WorkspaceID)
	if strings.TrimSpace(binding.Credential) == "" || !binding.CredentialExpiresAt.After(time.Now()) {
		return fmt.Errorf("Workspace Binding %q has no live execution credential", workspaceID)
	}
	d.client.SetWorkspaceDaemonToken(workspaceID, binding.Credential, binding.CredentialExpiresAt)
	return nil
}

func (d *Daemon) workspaceRefreshLoop(ctx context.Context, workspaceID, initialCredential string, interval time.Duration) error {
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
					d.logger.Warn("WorkspaceDaemon could not read its connection", "workspace_id", workspaceID, "error", err)
				}
				continue
			}
			binding, ok := bindings[workspaceID]
			if !ok || binding.Credential == "" || !binding.CredentialExpiresAt.After(time.Now()) {
				return fmt.Errorf("Workspace Binding %q is no longer active", workspaceID)
			}
			credentialChanged := strings.TrimSpace(binding.Credential) != lastBindingCredential
			if credentialChanged || d.client.WorkspaceDaemonTokenNeedsRefresh(workspaceID, time.Now()) {
				if err := d.reregisterWorkspace(ctx, workspaceID); err != nil {
					if d.logger != nil {
						d.logger.Warn("WorkspaceDaemon Runtime refresh failed; will retry", "workspace_id", workspaceID, "error", err)
					}
					continue
				}
				lastBindingCredential = strings.TrimSpace(binding.Credential)
				continue
			}
			// TODO(computer-liveness): Remove after v0.4.24-alpha.55 is no
			// longer a supported direct self-upgrade source. Socket ownership
			// is liveness; this HTTP probe only refreshes leftover last_seen.
			if err := d.client.ComputerHeartbeat(ctx, workspaceID, d.cfg.DaemonID); err != nil && d.logger != nil {
				d.logger.Warn("WorkspaceDaemon heartbeat failed; will retry", "workspace_id", workspaceID, "error", err)
			}
		}
	}
}

func (d *Daemon) serveWorkspaceDaemonControlRPC(ctx context.Context, listener net.Listener, bootstrap computer.WorkspaceDaemonBootstrap) {
	registry := d.workspaceDaemonControlRegistry(bootstrap)
	d.logger.Info("WorkspaceDaemon IPC listening", "addr", listener.Addr().String())
	if err := computer.ServeLocalControlRPC(ctx, listener, registry); err != nil && d.logger != nil {
		d.logger.Warn("WorkspaceDaemon IPC error", "error", err)
	}
}

func (d *Daemon) workspaceDaemonControlRegistry(bootstrap computer.WorkspaceDaemonBootstrap) *computer.LocalControlRegistry {
	registry := computer.NewLocalControlRegistry()
	authorized := func(headers map[string]string) error {
		token := strings.TrimSpace(d.cfg.LocalControlToken)
		provided := strings.TrimSpace(headers["X-Multica-Control-Token"])
		if token == "" || provided == "" || subtle.ConstantTimeCompare([]byte(token), []byte(provided)) != 1 {
			return errors.New("local control authentication failed")
		}
		return nil
	}
	decodeIdentity := func(headers map[string]string, raw json.RawMessage) (computer.WorkspaceDaemonIdentity, error) {
		if err := authorized(headers); err != nil {
			return computer.WorkspaceDaemonIdentity{}, err
		}
		var request computer.WorkspaceDaemonControlRequest
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&request); err != nil || request.Identity.WorkspaceID != bootstrap.WorkspaceID || request.Identity.DaemonInstanceID != d.instanceID || request.Identity.PID != os.Getpid() {
			return computer.WorkspaceDaemonIdentity{}, errors.New("inactive WorkspaceDaemon process")
		}
		return request.Identity, nil
	}
	register := func(operation string, handler computer.LocalControlHandler) {
		if err := registry.Register(operation, handler); err != nil {
			panic(err)
		}
	}
	register(computer.LocalControlRunnerDrainOperation, func(ctx context.Context, headers map[string]string, raw json.RawMessage) (any, error) {
		if _, err := decodeIdentity(headers, raw); err != nil {
			return nil, err
		}
		return nil, d.beginWorkspaceDaemonDrain(ctx)
	})
	register(computer.LocalControlRunnerReleaseOperation, func(_ context.Context, headers map[string]string, raw json.RawMessage) (any, error) {
		if _, err := decodeIdentity(headers, raw); err != nil {
			return nil, err
		}
		d.releaseClaimBarrier()
		return nil, nil
	})
	register(computer.LocalControlUpgradeStartOperation, func(ctx context.Context, headers map[string]string, raw json.RawMessage) (any, error) {
		if err := authorized(headers); err != nil {
			return nil, err
		}
		var request struct {
			Identity computer.WorkspaceDaemonIdentity `json:"identity"`
			Command  protocol.ComputerUpgradePayload  `json:"command"`
		}
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&request); err != nil || request.Identity.WorkspaceID != bootstrap.WorkspaceID || request.Identity.DaemonInstanceID != d.instanceID || request.Identity.PID != os.Getpid() {
			return nil, errors.New("inactive WorkspaceDaemon process")
		}
		return nil, d.handleComputerControlCommand(ctx, protocol.EventComputerUpgrade, request.Command)
	})
	register(computer.LocalControlUpgradeEventOperation, func(_ context.Context, headers map[string]string, raw json.RawMessage) (any, error) {
		if err := authorized(headers); err != nil {
			return nil, err
		}
		var request struct {
			Identity  computer.WorkspaceDaemonIdentity `json:"identity"`
			EventType string                           `json:"eventType"`
			Payload   json.RawMessage                  `json:"payload"`
		}
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&request); err != nil || request.Identity.WorkspaceID != bootstrap.WorkspaceID || request.Identity.DaemonInstanceID != d.instanceID || request.Identity.PID != os.Getpid() {
			return nil, errors.New("inactive WorkspaceDaemon process")
		}
		var payload any
		if err := json.Unmarshal(request.Payload, &payload); err != nil {
			return nil, errors.New("invalid upgrade event")
		}
		d.emitComputerUpgrade(request.EventType, payload)
		return nil, nil
	})
	register(computer.LocalControlWorkspaceEnvironmentOperation, func(ctx context.Context, headers map[string]string, raw json.RawMessage) (any, error) {
		identity, err := decodeIdentity(headers, raw)
		if err != nil {
			return nil, err
		}
		var request struct {
			Identity computer.WorkspaceDaemonIdentity `json:"identity"`
			Action   string                           `json:"action"`
		}
		if err := json.Unmarshal(raw, &request); err != nil || request.Identity != identity {
			return nil, errors.New("invalid environment switch request")
		}
		switch request.Action {
		case "release":
			d.releaseEnvironmentSwitchBarrier()
			return nil, nil
		case "prepare":
		default:
			return nil, errors.New("unsupported environment switch action")
		}
		if !d.trySetEnvironmentSwitchBarrier() {
			return nil, errors.New("another Binding handoff is already in progress")
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
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-ticker.C:
			}
		}
		prepared = true
		return nil, nil
	})
	register(computer.LocalControlRunnerReadyOperation, func(ctx context.Context, headers map[string]string, raw json.RawMessage) (any, error) {
		identity, err := decodeIdentity(headers, raw)
		if err != nil {
			return nil, err
		}
		return nil, d.reregisterWorkspace(ctx, identity.WorkspaceID)
	})
	return registry
}

func (d *Daemon) reregisterWorkspace(ctx context.Context, workspaceID string) error {
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
	// the credential plus the WorkspaceDaemon socket, not this HTTP probe.
	if err := d.client.ComputerHeartbeat(ctx, workspaceID, d.cfg.DaemonID); err != nil && d.logger != nil {
		d.logger.Warn("WorkspaceDaemon heartbeat failed during Runtime refresh", "workspace_id", workspaceID, "error", err)
	}
	if len(d.cfg.Agents) > 0 {
		return d.reregisterWorkspaceAfterRuntimeGone(ctx, workspaceID)
	}
	expiresAt := binding.CredentialExpiresAt.UTC().Format(time.RFC3339Nano)
	if d.computerControl == nil {
		return errors.New("WorkspaceDaemon Computer control is unavailable")
	}
	if err := d.computerControl.reportRuntimeSet(ctx, []Runtime{}, binding.Credential, expiresAt); err != nil {
		return fmt.Errorf("report zero-Agent Binding Runtime set: %w", err)
	}
	return nil
}
