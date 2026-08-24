package computer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type persistedRunnerState struct {
	WorkspaceID      string    `json:"workspaceId"`
	DaemonInstanceID string    `json:"daemonInstanceId"`
	OwnerPID         int       `json:"ownerPid"`
	RunnerPID        int       `json:"runnerPid"`
	StartedAt        time.Time `json:"startedAt"`
}

func runnerStateDir(root, workspaceID string) string {
	root = strings.TrimSpace(root)
	workspaceID = strings.TrimSpace(workspaceID)
	if root == "" || workspaceID == "" {
		return ""
	}
	digest := sha256.Sum256([]byte(workspaceID))
	return filepath.Join(root, "run", "runners", hex.EncodeToString(digest[:]))
}

func runnerStatePath(root, workspaceID string) string {
	dir := runnerStateDir(root, workspaceID)
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, "runner.state.json")
}

func runnerPIDPath(root, workspaceID string) string {
	dir := runnerStateDir(root, workspaceID)
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, "runner.pid")
}

func runnerConnectedPath(root, workspaceID string) string {
	dir := runnerStateDir(root, workspaceID)
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, "runner.connected")
}

type persistedRunnerConnected struct {
	PID            int       `json:"pid"`
	ConnectedAt    time.Time `json:"connectedAt"`
	RunnerEndpoint string    `json:"runnerEndpoint,omitempty"`
}

func writeRunnerState(root string, state persistedRunnerState) error {
	path := runnerStatePath(root, state.WorkspaceID)
	if path == "" {
		return nil
	}
	if state.OwnerPID < 1 {
		return errors.New("runner state identity is incomplete")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return writePrivateJSON(path, state)
}

func writeRunnerPID(root, workspaceID string, pid int) error {
	path := runnerPIDPath(root, workspaceID)
	if path == "" {
		return nil
	}
	if pid < 1 {
		return errors.New("runner pid is invalid")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return writePrivateBytes(path, []byte(fmt.Sprintf("%d\n", pid)))
}

func writeRunnerConnected(root, workspaceID string, connected persistedRunnerConnected) error {
	path := runnerConnectedPath(root, workspaceID)
	if path == "" {
		return nil
	}
	if connected.PID < 1 {
		return errors.New("runner connected pid is invalid")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return writePrivateJSON(path, connected)
}

func writePrivateJSON(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return writePrivateBytes(path, data)
}

func writePrivateBytes(path string, data []byte) error {
	temporary, err := os.CreateTemp(filepath.Dir(path), ".runner-*.tmp")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(data); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryPath, path)
}

func removeRunnerState(root, workspaceID, daemonInstanceID string, pid int) error {
	path := runnerStatePath(root, workspaceID)
	if path == "" {
		return nil
	}
	state, err := readRunnerState(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	storedPID, err := readRunnerPID(runnerPIDPath(root, workspaceID))
	if err != nil || storedPID != pid {
		return nil
	}
	if daemonInstanceID != "" && state.DaemonInstanceID != daemonInstanceID {
		return nil
	}
	for _, current := range []string{runnerConnectedPath(root, workspaceID), runnerPIDPath(root, workspaceID), path} {
		if err := os.Remove(current); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return os.Remove(filepath.Dir(path))
}

func discardRunnerStateAfterSpawnFailure(root, workspaceID, daemonInstanceID string, pid int) error {
	path := runnerStatePath(root, workspaceID)
	if path == "" {
		return nil
	}
	state, err := readRunnerState(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil || state.RunnerPID != pid {
		return err
	}
	if daemonInstanceID != "" && state.DaemonInstanceID != daemonInstanceID {
		return err
	}
	for _, current := range []string{runnerConnectedPath(root, workspaceID), runnerPIDPath(root, workspaceID), path} {
		if err := os.Remove(current); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return os.Remove(filepath.Dir(path))
}

func readRunnerState(path string) (persistedRunnerState, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return persistedRunnerState{}, err
	}
	var state persistedRunnerState
	if err := json.Unmarshal(data, &state); err != nil {
		return persistedRunnerState{}, fmt.Errorf("parse runner state: %w", err)
	}
	return state, nil
}

func readRunnerPID(path string) (int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	var pid int
	if _, err := fmt.Sscanf(strings.TrimSpace(string(data)), "%d", &pid); err != nil || pid < 1 {
		return 0, errors.New("runner pid is invalid")
	}
	return pid, nil
}

func readRunnerConnected(path string) (persistedRunnerConnected, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return persistedRunnerConnected{}, err
	}
	var connected persistedRunnerConnected
	if err := json.Unmarshal(data, &connected); err != nil {
		return persistedRunnerConnected{}, fmt.Errorf("parse runner connected evidence: %w", err)
	}
	return connected, nil
}

// listRunnerStates reads every persisted Binding Runner state file under
// root without mutating anything. It is used by read-only evidence
// gathering (doctor); findReclaimableRunners is the mutating counterpart
// used at Host startup. Corrupt or unreadable entries are silently skipped.
func listRunnerStates(root string) ([]persistedRunnerState, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return nil, nil
	}
	dir := filepath.Join(root, "run", "runners")
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var states []persistedRunnerState
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		state, err := readRunnerState(filepath.Join(dir, entry.Name(), "runner.state.json"))
		if err != nil {
			continue
		}
		states = append(states, state)
	}
	return states, nil
}

// reclaimableRunner is one Workspace slot whose previous-generation Binding
// Runner process is still alive on this machine. A live process here is
// never adopted; the current Host drains and terminates it, then spawns its
// own child through the normal CanSpawn/Reconcile path.
type reclaimableRunner struct {
	WorkspaceID      string
	DaemonInstanceID string
	PID              int
	// RunnerEndpoint is the runner's local control unix socket, read from
	// runner.connected when it still matches the live pidfile owner. It is
	// empty when the runner never reached Ready or the evidence is stale, in
	// which case the caller terminates by signal without asking it to drain.
	RunnerEndpoint string
}

// findReclaimableRunners reads every persisted Binding Runner state
// directory and reports which ones still have a live OS process to reclaim.
// It never adopts a live process into this Host's own bookkeeping: a live
// runner found here is handed back to the caller so it can be drained and
// killed before this Host spawns a replacement.
//
// Side effect: any state directory whose process is already dead is deleted
// in place as it is scanned (state file, pid file, connected file, and the
// directory itself) — there is nothing left to reclaim for those, so this
// doubles as the startup sweep for stale state.
func findReclaimableRunners(root string, logger *slog.Logger) ([]reclaimableRunner, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return nil, nil
	}
	dir := filepath.Join(root, "run", "runners")
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var reclaimable []reclaimableRunner
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		path := filepath.Join(dir, entry.Name(), "runner.state.json")
		state, readErr := readRunnerState(path)
		if readErr != nil {
			if logger != nil {
				logger.Warn("ignoring invalid persisted Runner state", "path", path, "error", readErr)
			}
			continue
		}
		ownerAlive, ownerKnown := processAlive(state.OwnerPID)
		if ownerKnown && ownerAlive {
			if logger != nil {
				logger.Warn("persisted Runner owner is still alive; refusing takeover", "workspace_id", state.WorkspaceID, "owner_pid", state.OwnerPID)
			}
			continue
		}
		pid, pidErr := readRunnerPID(filepath.Join(filepath.Dir(path), "runner.pid"))
		alive, known := false, false
		if pidErr == nil {
			alive, known = processAlive(pid)
		}
		if known && alive {
			endpoint := ""
			if connected, err := readRunnerConnected(filepath.Join(filepath.Dir(path), "runner.connected")); err == nil && connected.PID == pid {
				endpoint = connected.RunnerEndpoint
			}
			if logger != nil {
				logger.Info("found reclaimable Binding Runner left by a previous Host generation", "workspace_id", state.WorkspaceID, "pid", pid, "has_endpoint", endpoint != "")
			}
			reclaimable = append(reclaimable, reclaimableRunner{
				WorkspaceID: state.WorkspaceID, DaemonInstanceID: state.DaemonInstanceID, PID: pid, RunnerEndpoint: endpoint,
			})
			continue
		}
		for _, current := range []string{filepath.Join(filepath.Dir(path), "runner.connected"), filepath.Join(filepath.Dir(path), "runner.pid"), path} {
			if err := os.Remove(current); err != nil && !os.IsNotExist(err) {
				return reclaimable, err
			}
		}
		_ = os.Remove(filepath.Dir(path))
	}
	return reclaimable, nil
}

// runnerReclaimOptions bounds one orphaned WorkspaceDaemon reclaim. Drain is
// optional: when it is nil, or the runner never published a control endpoint,
// the reclaim goes straight to signal termination.
type runnerReclaimOptions struct {
	StateRoot    string
	Drain        func(context.Context, string, BindingChildIdentity) error
	PollInterval time.Duration
	Grace        time.Duration
	Sleep        func(time.Duration)
	Logger       *slog.Logger
}

// reclaimRunnerProcess drains and then force-terminates one WorkspaceDaemon
// process left behind by a previous Host generation, and on confirmed death
// removes its persisted state so the slot is free for a fresh child.
//
// Drain (runner:drain) only closes the runner's own admission barrier and
// cancels in-flight work on the runner side; it never makes the runner process
// exit on its own. So there is nothing to poll for after a drain attempt — the
// next step is always terminateProcess, which owns its own bounded
// SIGTERM/SIGKILL wait and exit confirmation.
//
// It returns an error only when the process could not be confirmed dead; the
// caller decides what that means for its own bookkeeping.
func reclaimRunnerProcess(runner reclaimableRunner, options runnerReclaimOptions) error {
	logger := options.Logger
	identity := BindingChildIdentity{WorkspaceID: runner.WorkspaceID, DaemonInstanceID: runner.DaemonInstanceID, PID: runner.PID}

	if runner.RunnerEndpoint != "" && options.Drain != nil && identity.Validate() == nil {
		if logger != nil {
			logger.Info("reclaiming orphaned WorkspaceDaemon: requesting drain", "workspace_id", runner.WorkspaceID, "pid", runner.PID, "endpoint", runner.RunnerEndpoint)
		}
		// No extra timeout wrapping here: the local control RPC transport
		// already bounds this call (see callLocalJSONWithTimeout), and drain
		// itself may legitimately take close to its own internal grace period
		// to finish closing out in-flight work.
		if err := options.Drain(context.Background(), runner.RunnerEndpoint, identity); err != nil {
			if logger != nil {
				logger.Warn("orphaned WorkspaceDaemon drain request failed; will terminate by signal", "workspace_id", runner.WorkspaceID, "pid", runner.PID, "error", err)
			}
		} else if logger != nil {
			logger.Info("orphaned WorkspaceDaemon drained via runner:drain", "workspace_id", runner.WorkspaceID, "pid", runner.PID)
		}
	} else if logger != nil {
		logger.Warn("orphaned WorkspaceDaemon has no reachable control endpoint; will terminate by signal", "workspace_id", runner.WorkspaceID, "pid", runner.PID)
	}

	if logger != nil {
		logger.Info("terminating orphaned WorkspaceDaemon: sending SIGTERM/SIGKILL", "workspace_id", runner.WorkspaceID, "pid", runner.PID)
	}
	if err := terminateProcess(runner.PID, options.PollInterval, options.Grace, options.Sleep); err != nil {
		return err
	}
	if err := removeRunnerState(options.StateRoot, runner.WorkspaceID, runner.DaemonInstanceID, runner.PID); err != nil && logger != nil {
		logger.Warn("could not remove reclaimed WorkspaceDaemon state", "workspace_id", runner.WorkspaceID, "pid", runner.PID, "error", err)
	}
	return nil
}

// terminateProcess makes a bounded, best-effort attempt to stop pid: SIGTERM
// first, then poll for exit every pollInterval up to grace; if it is still
// alive, SIGKILL, then poll for exit the same way one more time (SIGKILL
// delivery plus the kernel reaping the process is not instantaneous, so a
// check made immediately after Kill() returning nil can still observe the
// process as alive). It returns nil once the process is confirmed gone (or
// was never running), and an error only if it could not be confirmed dead
// within the bounded wait.
func terminateProcess(pid int, pollInterval, grace time.Duration, sleep func(time.Duration)) error {
	if pid < 1 {
		return nil
	}
	if sleep == nil {
		sleep = time.Sleep
	}
	if pollInterval <= 0 {
		pollInterval = 200 * time.Millisecond
	}
	process, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	if err := stopBindingProcess(process); err != nil {
		if alive, known := processAlive(pid); known && !alive {
			return nil
		}
		// SIGTERM failed to even reach a still-alive process (e.g. race with
		// its own exit, or a signal it cannot receive) — fall through and
		// let SIGKILL have a try instead of giving up here.
	}
	if waitForProcessExit(pid, pollInterval, grace, sleep) {
		return nil
	}
	if err := process.Kill(); err != nil {
		if alive, known := processAlive(pid); known && !alive {
			return nil
		}
		return err
	}
	if waitForProcessExit(pid, pollInterval, grace, sleep) {
		return nil
	}
	return fmt.Errorf("process %d did not exit after SIGKILL", pid)
}

// waitForProcessExit polls processAlive every pollInterval until pid is
// confirmed gone or timeout elapses.
func waitForProcessExit(pid int, pollInterval, timeout time.Duration, sleep func(time.Duration)) bool {
	deadline := time.Now().Add(timeout)
	for {
		if alive, known := processAlive(pid); known && !alive {
			return true
		}
		if !time.Now().Before(deadline) {
			return false
		}
		sleep(pollInterval)
	}
}
