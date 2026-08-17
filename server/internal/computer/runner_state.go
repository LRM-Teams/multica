package computer

import (
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
	WorkspaceID      string    `json:"workspace_id"`
	RunnerGeneration int64     `json:"runner_generation"`
	OwnerPID         int       `json:"owner_pid"`
	RunnerPID        int       `json:"runner_pid"`
	RunnerIdentity   string    `json:"runner_identity,omitempty"`
	StartedAt        time.Time `json:"started_at"`
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
	ConnectedAt    time.Time `json:"connected_at"`
	RunnerEndpoint string    `json:"runner_endpoint,omitempty"`
}

func writeRunnerState(root string, state persistedRunnerState) error {
	path := runnerStatePath(root, state.WorkspaceID)
	if path == "" {
		return nil
	}
	if state.OwnerPID < 1 || state.RunnerGeneration < 1 {
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

func removeRunnerState(root, workspaceID string, generation int64, pid int) error {
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
	if err != nil || state.RunnerGeneration != generation || storedPID != pid {
		return nil
	}
	for _, current := range []string{runnerConnectedPath(root, workspaceID), runnerPIDPath(root, workspaceID), path} {
		if err := os.Remove(current); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return os.Remove(filepath.Dir(path))
}

func discardRunnerStateAfterSpawnFailure(root, workspaceID string, generation int64, pid int) error {
	path := runnerStatePath(root, workspaceID)
	if path == "" {
		return nil
	}
	state, err := readRunnerState(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil || state.RunnerGeneration != generation || state.RunnerPID != pid {
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

func recoverRunnerStates(root string, logger *slog.Logger) error {
	root = strings.TrimSpace(root)
	if root == "" {
		return nil
	}
	dir := filepath.Join(root, "run", "runners")
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
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
		identityMatches := false
		if pidErr == nil && state.RunnerIdentity != "" {
			identityMatches = processIdentityValue(pid) == state.RunnerIdentity
		}
		if known && alive && identityMatches {
			if err := terminateProcess(pid); err != nil && logger != nil {
				logger.Warn("could not terminate orphaned Runner", "workspace_id", state.WorkspaceID, "pid", pid, "error", err)
			}
		} else if known && alive && logger != nil {
			logger.Warn("refusing to terminate orphaned Runner with mismatched process identity", "workspace_id", state.WorkspaceID, "pid", pid)
		}
		for _, current := range []string{filepath.Join(filepath.Dir(path), "runner.connected"), filepath.Join(filepath.Dir(path), "runner.pid"), path} {
			if err := os.Remove(current); err != nil && !os.IsNotExist(err) {
				return err
			}
		}
		_ = os.Remove(filepath.Dir(path))
	}
	return nil
}

func terminateProcess(pid int) error {
	if pid < 1 {
		return nil
	}
	process, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return process.Kill()
}
