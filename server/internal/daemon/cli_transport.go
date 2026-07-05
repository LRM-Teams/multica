package daemon

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func prepareTaskCLITransport(cfg Config, workspaceID, agentID, runID, multicaBin, token string) (string, string, error) {
	if workspaceID == "" || agentID == "" || runID == "" {
		return "", "", fmt.Errorf("workspace_id, agent_id, and run_id are required")
	}
	if strings.TrimSpace(multicaBin) == "" {
		return "", "", fmt.Errorf("multica binary path is required")
	}
	if token == "" {
		return "", "", fmt.Errorf("task token is required")
	}

	root := filepath.Join(multicaAgentRoot(cfg, workspaceID, agentID), "runtime", "cli-transport", runID)
	if err := os.MkdirAll(root, 0o700); err != nil {
		return "", "", fmt.Errorf("create cli transport dir: %w", err)
	}

	tokenFile := filepath.Join(root, "token")
	if err := os.WriteFile(tokenFile, []byte(token), 0o600); err != nil {
		return "", "", fmt.Errorf("write cli token file: %w", err)
	}
	if err := os.Chmod(tokenFile, 0o600); err != nil {
		return "", "", fmt.Errorf("chmod cli token file: %w", err)
	}

	wrapper := filepath.Join(root, "multica")
	body := "#!/bin/sh\n" +
		"unset MULTICA_TOKEN\n" +
		"export MULTICA_TOKEN_FILE=" + shellQuote(tokenFile) + "\n" +
		"exec " + shellQuote(multicaBin) + " \"$@\"\n"
	if err := os.WriteFile(wrapper, []byte(body), 0o700); err != nil {
		return "", "", fmt.Errorf("write cli wrapper: %w", err)
	}
	if err := os.Chmod(wrapper, 0o700); err != nil {
		return "", "", fmt.Errorf("chmod cli wrapper: %w", err)
	}

	return root, tokenFile, nil
}

func shellQuote(s string) string {
	if s == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}
