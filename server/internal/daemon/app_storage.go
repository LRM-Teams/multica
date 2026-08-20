package daemon

import (
	"errors"
	"path/filepath"
	"strings"
)

const (
	reminderInboxAppID = "system.reminder"
	agentInboxAppID    = "system.agent-inbox"
)

func builtInAppStorageAgentsRoot(bindingsRoot, machineID, workspaceID, appID string) (string, error) {
	if strings.TrimSpace(bindingsRoot) == "" {
		return "", errors.New("BindingsRoot is required for App storage")
	}
	if appID != reminderInboxAppID && appID != agentInboxAppID {
		return "", errors.New("App storage authority is not registered")
	}
	for _, segment := range []string{machineID, workspaceID} {
		if !safeAppStorageSegment(segment) {
			return "", errors.New("App storage identity contains an invalid path segment")
		}
	}
	return filepath.Join(bindingsRoot, "app-storage", "v1", machineID, workspaceID, appID, "agents"), nil
}

func safeAppStorageSegment(value string) bool {
	value = strings.TrimSpace(value)
	return value != "" && value != "." && value != ".." && filepath.Base(value) == value && !strings.ContainsAny(value, "/\\\x00")
}
