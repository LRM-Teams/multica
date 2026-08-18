package computer

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

type persistedServiceState struct {
	ComputerID        string    `json:"computerId"`
	ServiceGeneration string    `json:"serviceGeneration"`
	PID               int       `json:"pid"`
	StartedAt         time.Time `json:"startedAt"`
}

func servicePIDPath(root string) string {
	if root == "" {
		return ""
	}
	return filepath.Join(root, "run", "service.pid")
}

func serviceStatePath(root string) string {
	if root == "" {
		return ""
	}
	return filepath.Join(root, "run", "service.state.json")
}

func writeServiceState(root string, state persistedServiceState) error {
	path := serviceStatePath(root)
	if path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return writePrivateJSON(path, state)
}

func removeServiceState(root string, pid int) error {
	path := serviceStatePath(root)
	if path == "" {
		return nil
	}
	state, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var current persistedServiceState
	if err := json.Unmarshal(state, &current); err != nil || current.PID != pid {
		return nil
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
