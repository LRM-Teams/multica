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

// HostProcessIdentity is the immutable machine identity projected by the
// Computer resident. Binding execution identity remains inside each child.
type HostProcessIdentity struct {
	ComputerID             string
	ComputerGeneration     int64
	Environment            string
	Version                string
	ServerURL              string
	DeviceName             string
	MachineAttestationFrom int
}

func (identity HostProcessIdentity) releaseChannel() cli.ReleaseChannel {
	return cli.ReleaseChannelForEnvironment(cli.ServiceEnvironment(identity.Environment))
}

// HostProcessConfig is the process boundary around Host. It contains only
// machine-wide dependencies; no daemon or Workspace execution object crosses
// this interface.
type HostProcessConfig struct {
	Listener            net.Listener
	ControlPort         int
	ResidentRoot        string
	Identity            HostProcessIdentity
	DesiredWorkspaceIDs func() ([]string, error)
	Changes             <-chan struct{}
	ReadyTimeout        time.Duration
	ReleaseManifestURL  string
	// TODO(previous-package-bootstrap): Remove after v0.4.24-alpha.55 is no
	// longer a supported direct self-upgrade source.
	PreviousPackageUpgradeBootstrap bool
}

type hostProcessState struct {
	mu        sync.RWMutex
	identity  HostProcessIdentity
	startedAt time.Time
	ready     bool
	desired   []string
	cancel    context.CancelFunc
	// TODO(previous-package-bootstrap): Remove these two fields with the
	// v0.4.24-alpha.55 health projection.
	previousPackageUpgradeBootstrap bool
	sourceProcessAlive              func(int) (bool, bool)
}

// RunProcess owns the resident Computer control plane around Host. The
// execution plane is never constructed here: Host can only supervise Binding
// child process handles and expose machine-scoped control.
func (host *Host) RunProcess(ctx context.Context, config HostProcessConfig) error {
	if host == nil || host.supervisor == nil || host.control == nil {
		return errors.New("Computer Host is unavailable")
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
		if config.ControlPort < 1 {
			return errors.New("Computer Host control port is required")
		}
		listener, listenErr := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", config.ControlPort))
		if listenErr != nil {
			return fmt.Errorf("another Computer resident is already listening on 127.0.0.1:%d: %w", config.ControlPort, listenErr)
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
	state := &hostProcessState{
		identity: config.Identity, startedAt: time.Now(), desired: append([]string(nil), initial...),
		cancel: cancel, previousPackageUpgradeBootstrap: config.PreviousPackageUpgradeBootstrap,
		sourceProcessAlive: processAlive,
	}
	host.processIdentity = config.Identity
	if strings.TrimSpace(config.ResidentRoot) != "" {
		store, storeErr := diagnosticlog.Open(diagnosticlog.Config{Root: filepath.Join(config.ResidentRoot, "logs")})
		if storeErr != nil {
			if host.logger != nil {
				host.logger.Warn("Computer diagnostic aggregation is degraded", "error", storeErr)
			}
		} else {
			host.diagnosticStore = store
			defer store.Close()
			go store.RunCleanup(processCtx)
		}
	}
	if host.upgrade == nil {
		host.upgrade = newHostMachineUpgrade(host, hostMachineUpgradeConfig{})
	}
	host.upgrade.config.identity = config.Identity
	host.upgrade.config.releaseManifestURL = config.ReleaseManifestURL
	host.upgrade.config.residentRoot = config.ResidentRoot
	host.upgrade.config.cancel = cancel
	host.upgrade.config.previousPackageUpgradeBootstrap = config.PreviousPackageUpgradeBootstrap

	loadDesired := func() []string {
		ids, loadErr := config.DesiredWorkspaceIDs()
		if loadErr != nil {
			if host.logger != nil {
				host.logger.Warn("Computer could not refresh desired Bindings", "error", loadErr)
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
	host.RegisterRoutes(mux)
	mux.HandleFunc("/health", host.processHealthHandler(state))
	mux.HandleFunc(MachineAttestationPath, host.processAttestationHandler(state))
	mux.HandleFunc("/shutdown", host.processShutdownHandler(state))
	mux.HandleFunc("/environment-switch/prepare", host.processEnvironmentSwitchHandler(true))
	mux.HandleFunc("/environment-switch/release", host.processEnvironmentSwitchHandler(false))
	mux.HandleFunc("/machine-upgrades", host.upgrade.localRequestHandler())
	server := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	serveErr := make(chan error, 1)
	go func() {
		err := server.Serve(config.Listener)
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			serveErr <- err
			return
		}
		serveErr <- nil
	}()

	hostDone := make(chan struct{})
	go func() {
		defer close(hostDone)
		host.Run(processCtx, loadDesired, config.Changes)
	}()
	readyTimeout := config.ReadyTimeout
	if readyTimeout <= 0 {
		readyTimeout = 35 * time.Second
	}
	readyCtx, stopReady := context.WithTimeout(processCtx, readyTimeout)
	err = host.WaitReady(readyCtx, initial)
	stopReady()
	if err != nil {
		cancel()
		_ = server.Close()
		<-hostDone
		<-serveErr
		return err
	}
	if err := host.upgrade.recoverSuccessor(processCtx); err != nil {
		cancel()
		_ = server.Close()
		<-hostDone
		<-serveErr
		return fmt.Errorf("recover Computer Machine Upgrade: %w", err)
	}
	state.mu.Lock()
	state.ready = true
	state.mu.Unlock()

	select {
	case <-processCtx.Done():
	case err = <-serveErr:
		if err != nil {
			cancel()
		}
	}
	shutdownCtx, stopShutdown := context.WithTimeout(context.Background(), 5*time.Second)
	_ = server.Shutdown(shutdownCtx)
	stopShutdown()
	<-hostDone
	select {
	case serveResult := <-serveErr:
		if err == nil {
			err = serveResult
		}
	default:
	}
	return err
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

func (host *Host) processHealthHandler(state *hostProcessState) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		state.mu.RLock()
		identity := state.identity
		startedAt := state.startedAt
		ready := state.ready
		desired := append([]string(nil), state.desired...)
		previousPackageUpgradeBootstrap := state.previousPackageUpgradeBootstrap
		sourceProcessAlive := state.sourceProcessAlive
		state.mu.RUnlock()
		status := "starting"
		if ready {
			status = "running"
			// TODO(previous-package-bootstrap): Remove after v0.4.24-alpha.55
			// is no longer a supported direct self-upgrade source.
			// v0.4.24-alpha.55 waits for this historical readiness spelling
			// before it accepts the successor's machine attestation. Keep the
			// projection only while that exact launcher process is still alive;
			// after it releases the child and exits, normal health is "running".
			if previousPackageUpgradeBootstrap && identity.MachineAttestationFrom > 0 && sourceProcessAlive != nil {
				alive, known := sourceProcessAlive(identity.MachineAttestationFrom)
				if alive || !known {
					status = "takeover_ready"
				}
			}
		}
		workspaces := make([]map[string]any, 0, len(desired))
		for _, workspaceID := range desired {
			record, pid, ok := host.Snapshot(workspaceID)
			entry := map[string]any{"id": workspaceID, "runtimes": host.runtimeIDs(workspaceID)}
			if ok {
				entry["runner_generation"] = record.Generation()
				entry["runner_pid"] = pid
				entry["runner_status"] = record.Lifecycle
			}
			workspaces = append(workspaces, entry)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status": status, "pid": os.Getpid(), "os": runtime.GOOS,
			"uptime":    time.Since(startedAt).Truncate(time.Second).String(),
			"daemon_id": identity.ComputerID, "computer_id": identity.ComputerID,
			"computer_generation": identity.ComputerGeneration,
			"device_name":         identity.DeviceName, "server_url": identity.ServerURL,
			"environment": identity.Environment, "release_channel": identity.releaseChannel(),
			"cli_version": identity.Version, "connected": ready && len(desired) > 0,
			"active_task_count": int64(0), "agents": []string{}, "workspaces": workspaces,
		})
	}
}

func (host *Host) runtimeIDs(workspaceID string) []string {
	host.runtimeMu.RLock()
	report := host.runtimeSets[strings.TrimSpace(workspaceID)]
	host.runtimeMu.RUnlock()
	ids := make([]string, 0, len(report.Runtimes))
	for _, runtime := range report.Runtimes {
		ids = append(ids, runtime.ID)
	}
	sort.Strings(ids)
	return ids
}

func (host *Host) recordBindingDiagnostic(_ BindingChildIdentity, workspaceID string, event diagnosticlog.Event) {
	if host == nil || host.diagnosticStore == nil {
		return
	}
	host.diagnosticMu.Lock()
	logger := host.diagnosticLoggers[workspaceID]
	if logger == nil {
		created, err := host.diagnosticStore.Runner(diagnosticlog.RunnerOptions{
			Environment: diagnosticlog.Environment(host.processIdentity.Environment),
			WorkspaceID: workspaceID, RunnerGeneration: fmt.Sprintf("computer-%d", host.processIdentity.ComputerGeneration),
			ComputerID:         host.processIdentity.ComputerID,
			ComputerGeneration: fmt.Sprintf("%d", host.processIdentity.ComputerGeneration),
		})
		if err == nil {
			logger = created
			host.diagnosticLoggers[workspaceID] = logger
		} else if host.logger != nil {
			host.logger.Warn("Computer could not open Binding diagnostic stream", "workspace_id", workspaceID, "error", err)
		}
	}
	host.diagnosticMu.Unlock()
	if logger != nil {
		if err := logger.Record(event); err != nil && host.logger != nil {
			host.logger.Warn("Computer could not aggregate Binding diagnostic", "workspace_id", workspaceID, "error", err)
		}
	}
}

func (host *Host) processAttestationHandler(state *hostProcessState) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		state.mu.RLock()
		identity := state.identity
		startedAt := state.startedAt
		workspaceIDs := append([]string(nil), state.desired...)
		state.mu.RUnlock()
		attestation := MachineAttestation{
			ComputerVersion: identity.Version, ServiceGeneration: fmt.Sprintf("computer-%d", identity.ComputerGeneration),
			ComputerGeneration: identity.ComputerGeneration, ServicePID: os.Getpid(),
			ManagedWorkspaceIDs: workspaceIDs,
			ManagedSetRevision:  startedAt.UTC().Format(time.RFC3339Nano) + ":" + strings.Join(workspaceIDs, ","),
			SourceServicePID:    identity.MachineAttestationFrom,
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(attestation)
	}
}

func (host *Host) processShutdownHandler(state *hostProcessState) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "shutting down"})
		go state.cancel()
	}
}

func (host *Host) processEnvironmentSwitchHandler(prepare bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		provided := strings.TrimSpace(r.Header.Get("X-Multica-Control-Token"))
		expected := strings.TrimSpace(host.control.token)
		if provided == "" || expected == "" || subtle.ConstantTimeCompare([]byte(provided), []byte(expected)) != 1 {
			http.Error(w, "local control authentication failed", http.StatusUnauthorized)
			return
		}
		var err error
		if prepare {
			err = host.PrepareEnvironmentSwitch(r.Context())
		} else {
			err = host.ReleaseEnvironmentSwitch(r.Context())
		}
		if err != nil {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "prepared"})
	}
}
