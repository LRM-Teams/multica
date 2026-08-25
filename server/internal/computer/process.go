package computer

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/multica-ai/multica/server/internal/cli"
	"github.com/multica-ai/multica/server/internal/diagnosticlog"
)

// ComputerIdentity is the immutable machine identity projected by the
// Computer process. WorkspaceDaemon execution identity remains in each process.
type ComputerIdentity struct {
	ComputerID        string
	ServiceGeneration string
	Environment       string
	Version           string
	ServerURL         string
	DeviceName        string
	SourceServicePID  int
}

func (identity ComputerIdentity) releaseChannel() cli.ReleaseChannel {
	return cli.ReleaseChannelForEnvironment(cli.ServiceEnvironment(identity.Environment))
}

// ComputerProcessConfig contains the machine-wide dependencies for running
// ComputerCore. No Workspace execution object crosses this interface.
type ComputerProcessConfig struct {
	Listener            net.Listener // test adapter; production binds ServiceEndpoint
	ServiceEndpoint     string
	ResidentRoot        string
	Identity            ComputerIdentity
	DesiredWorkspaceIDs func() ([]string, error)
	Changes             <-chan struct{}
	ReadyTimeout        time.Duration
	ReleaseManifestURL  string
}

type computerProcessState struct {
	mu        sync.RWMutex
	identity  ComputerIdentity
	startedAt time.Time
	ready     bool
	desired   []string
	cancel    context.CancelFunc
}

// Run owns the resident Computer control plane and waits for DaemonCore to
// finish stopping every WorkspaceDaemon before returning.
func (computerCore *ComputerCore) Run(ctx context.Context, config ComputerProcessConfig) error {
	if computerCore == nil || computerCore.daemonCore == nil || computerCore.control == nil {
		return errors.New("ComputerCore is unavailable")
	}
	if config.DesiredWorkspaceIDs == nil {
		return errors.New("Computer desired Binding source is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if strings.TrimSpace(config.ResidentRoot) != "" {
		leaseCtx, stopLease := context.WithTimeout(ctx, 2*time.Second)
		lease, leaseErr := AcquireResidentLease(leaseCtx, config.ResidentRoot)
		stopLease()
		if leaseErr != nil {
			return fmt.Errorf("another Computer resident owns this machine: %w", leaseErr)
		}
		defer lease.Close()
	}
	if config.Listener == nil {
		if strings.TrimSpace(config.ServiceEndpoint) == "" {
			return errors.New("Computer service endpoint is required")
		}
		listener, listenErr := ListenLocalControl(config.ServiceEndpoint)
		if listenErr != nil {
			return fmt.Errorf("listen for Computer service IPC: %w", listenErr)
		}
		config.Listener = listener
	}
	initial, err := config.DesiredWorkspaceIDs()
	if err != nil {
		return fmt.Errorf("load desired Computer Bindings: %w", err)
	}
	initial = normalizedWorkspaceIDs(initial)
	processCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	state := &computerProcessState{
		identity: config.Identity, startedAt: time.Now(), desired: append([]string(nil), initial...),
		cancel: cancel,
	}
	if err := writeServiceState(config.ResidentRoot, persistedServiceState{
		ComputerID: config.Identity.ComputerID, ServiceGeneration: config.Identity.ServiceGeneration,
		PID: os.Getpid(), StartedAt: state.startedAt,
	}); err != nil && computerCore.logger != nil {
		computerCore.logger.Warn("could not persist Computer Service state", "error", err)
	}
	defer func() { _ = removeServiceState(config.ResidentRoot, os.Getpid()) }()
	computerCore.processIdentity = config.Identity
	if strings.TrimSpace(config.ResidentRoot) != "" {
		store, storeErr := diagnosticlog.Open(diagnosticlog.Config{Root: filepath.Join(config.ResidentRoot, "logs")})
		if storeErr != nil {
			if computerCore.logger != nil {
				computerCore.logger.Warn("Computer diagnostic aggregation is degraded", "error", storeErr)
			}
		} else {
			computerCore.diagnosticStore = store
			defer store.Close()
			go store.RunCleanup(processCtx)
		}
	}
	if computerCore.upgrade == nil {
		computerCore.upgrade = newComputerMachineUpgrade(computerCore, computerMachineUpgradeConfig{})
	}
	computerCore.upgrade.config.identity = config.Identity
	computerCore.upgrade.config.releaseManifestURL = config.ReleaseManifestURL
	computerCore.upgrade.config.residentRoot = config.ResidentRoot
	computerCore.upgrade.config.cancel = cancel
	computerCore.workJournalRoot = config.ResidentRoot
	computerCore.loadWorkJournalSetting()

	loadDesired := func() []string {
		ids, loadErr := config.DesiredWorkspaceIDs()
		if loadErr != nil {
			if computerCore.logger != nil {
				computerCore.logger.Warn("Computer could not refresh desired Bindings", "error", loadErr)
			}
			state.mu.RLock()
			defer state.mu.RUnlock()
			return append([]string(nil), state.desired...)
		}
		ids = normalizedWorkspaceIDs(ids)
		state.mu.Lock()
		state.desired = append(state.desired[:0], ids...)
		state.mu.Unlock()
		return ids
	}

	mux := http.NewServeMux()
	computerCore.registerProcessRoutes(mux, state)
	registry := computerCore.LocalControlRegistry(state)
	var server *http.Server
	serveControl := func() error {
		if strings.TrimSpace(config.ServiceEndpoint) == "" || strings.HasPrefix(config.ServiceEndpoint, "http://") {
			server = &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
			return server.Serve(config.Listener)
		}
		return ServeLocalControlRPC(processCtx, config.Listener, registry)
	}
	serveErr := make(chan error, 1)
	go func() {
		err := serveControl()
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			serveErr <- err
			return
		}
		serveErr <- nil
	}()

	daemonCoreDone := make(chan struct{})
	go func() {
		defer close(daemonCoreDone)
		computerCore.daemonCore.Run(processCtx, loadDesired, config.Changes, computerCore.reconcileInterval)
	}()
	readyTimeout := config.ReadyTimeout
	if readyTimeout <= 0 {
		readyTimeout = 35 * time.Second
	}
	readyCtx, stopReady := context.WithTimeout(processCtx, readyTimeout)
	err = computerCore.WaitReady(readyCtx, initial)
	stopReady()
	if err != nil {
		cancel()
		if server != nil {
			_ = server.Close()
		} else {
			_ = config.Listener.Close()
		}
		<-daemonCoreDone
		<-serveErr
		return err
	}
	if err := computerCore.upgrade.recoverSuccessor(processCtx); err != nil {
		cancel()
		if server != nil {
			_ = server.Close()
		} else {
			_ = config.Listener.Close()
		}
		<-daemonCoreDone
		<-serveErr
		return fmt.Errorf("recover Computer Machine Upgrade: %w", err)
	}
	state.mu.Lock()
	state.ready = true
	state.mu.Unlock()

	serveResultRead := false
	select {
	case <-processCtx.Done():
		if computerCore.logger != nil {
			computerCore.logger.Info("Computer shutdown context canceled", "source", "context_cancellation", "error", processCtx.Err())
		}
	case err = <-serveErr:
		serveResultRead = true
		if err != nil {
			cancel()
		}
	}
	shutdownCtx, stopShutdown := context.WithTimeout(context.Background(), 5*time.Second)
	if server != nil {
		_ = server.Shutdown(shutdownCtx)
	} else {
		_ = config.Listener.Close()
	}
	stopShutdown()
	<-daemonCoreDone
	if !serveResultRead {
		serveResult := <-serveErr
		if err == nil {
			err = serveResult
		}
	}
	return err
}

func (computerCore *ComputerCore) registerProcessRoutes(mux *http.ServeMux, state *computerProcessState) {
	mux.HandleFunc("/health", computerCore.processHealthHandler(state))
	mux.HandleFunc("/shutdown", computerCore.processShutdownHandler(state))
	mux.HandleFunc("/environment-switch/prepare", computerCore.processEnvironmentSwitchHandler(true))
	mux.HandleFunc("/environment-switch/release", computerCore.processEnvironmentSwitchHandler(false))
}

func normalizedWorkspaceIDs(ids []string) []string {
	seen := make(map[string]struct{}, len(ids))
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if _, duplicate := seen[id]; duplicate {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

func (computerCore *ComputerCore) processHealthHandler(state *computerProcessState) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		state.mu.RLock()
		identity := state.identity
		startedAt := state.startedAt
		ready := state.ready
		desired := append([]string(nil), state.desired...)
		state.mu.RUnlock()
		status := "starting"
		if ready {
			status = "running"
		}
		workspaces := make([]map[string]any, 0, len(desired))
		for _, workspaceID := range desired {
			snapshot, pid, ok := computerCore.Snapshot(workspaceID)
			entry := map[string]any{"id": workspaceID, "runtimes": computerCore.runtimeIDs(workspaceID)}
			if ok {
				entry["daemonInstanceId"] = snapshot.DaemonInstanceID
				entry["runnerPid"] = pid
				entry["runnerStatus"] = snapshot.Status
			}
			workspaces = append(workspaces, entry)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status": status, "pid": os.Getpid(), "os": runtime.GOOS,
			"uptime":   time.Since(startedAt).Truncate(time.Second).String(),
			"daemonId": identity.ComputerID, "computerId": identity.ComputerID,
			"serviceGeneration": identity.ServiceGeneration,
			"deviceName":        identity.DeviceName, "serverUrl": identity.ServerURL,
			"environment": identity.Environment, "releaseChannel": identity.releaseChannel(),
			"cliVersion": identity.Version, "connected": ready && len(desired) > 0,
			"activeTaskCount": int64(0), "agents": []string{}, "workspaces": workspaces,
		})
	}
}

func (computerCore *ComputerCore) runtimeIDs(workspaceID string) []string {
	computerCore.runtimeMu.RLock()
	report := computerCore.runtimeSets[strings.TrimSpace(workspaceID)]
	computerCore.runtimeMu.RUnlock()
	ids := make([]string, 0, len(report.Runtimes))
	for _, runtime := range report.Runtimes {
		ids = append(ids, runtime.ID)
	}
	sort.Strings(ids)
	return ids
}

func (computerCore *ComputerCore) recordBindingDiagnostic(_ WorkspaceDaemonIdentity, workspaceID string, event diagnosticlog.Event) {
	if computerCore == nil || computerCore.diagnosticStore == nil {
		return
	}
	computerCore.diagnosticMu.Lock()
	logger := computerCore.diagnosticLoggers[workspaceID]
	if logger == nil {
		created, err := computerCore.diagnosticStore.Runner(diagnosticlog.RunnerOptions{
			Environment: diagnosticlog.Environment(computerCore.processIdentity.Environment),
			WorkspaceID: workspaceID, DaemonInstanceID: computerCore.processIdentity.ServiceGeneration,
			ComputerID: computerCore.processIdentity.ComputerID, ServiceGeneration: computerCore.processIdentity.ServiceGeneration,
		})
		if err == nil {
			logger = created
			computerCore.diagnosticLoggers[workspaceID] = logger
		} else if computerCore.logger != nil {
			computerCore.logger.Warn("Computer could not open Binding diagnostic stream", "workspace_id", workspaceID, "error", err)
		}
	}
	computerCore.diagnosticMu.Unlock()
	if logger != nil {
		if err := logger.Record(event); err != nil && computerCore.logger != nil {
			computerCore.logger.Warn("Computer could not aggregate Binding diagnostic", "workspace_id", workspaceID, "error", err)
		}
	}
}

func (computerCore *ComputerCore) processShutdownHandler(state *computerProcessState) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		source := strings.TrimSpace(r.Header.Get(shutdownSourceHeader))
		if source == "" {
			source = "unknown"
		}
		action := strings.TrimSpace(r.Header.Get(shutdownActionHeader))
		if action == "" {
			action = "unknown"
		}
		requestPID := strings.TrimSpace(r.Header.Get(shutdownRequestPIDHeader))
		if requestPID == "" {
			requestPID = "unknown"
		}
		if computerCore.logger != nil {
			computerCore.logger.Info("Computer shutdown requested",
				"source", source,
				"action", action,
				"request_pid", requestPID,
				"remote_address", r.RemoteAddr,
			)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "shutting down"})
		go state.cancel()
	}
}

func (computerCore *ComputerCore) processEnvironmentSwitchHandler(prepare bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		provided := strings.TrimSpace(r.Header.Get("X-Multica-Control-Token"))
		expected := strings.TrimSpace(computerCore.control.token)
		if provided == "" || expected == "" || subtle.ConstantTimeCompare([]byte(provided), []byte(expected)) != 1 {
			http.Error(w, "local control authentication failed", http.StatusUnauthorized)
			return
		}
		var err error
		if prepare {
			err = computerCore.PrepareEnvironmentSwitch(r.Context())
		} else {
			err = computerCore.ReleaseEnvironmentSwitch(r.Context())
		}
		if err != nil {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "prepared"})
	}
}
