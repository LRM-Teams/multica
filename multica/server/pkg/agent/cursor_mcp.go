package agent

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
)

const (
	cursorMcpRelPath       = ".cursor/mcp.json"
	cursorMcpOwnedMarker   = ".cursor/mcp.json.multica-owned"
)

// ensureCursorMcpConfig materialises (or clears) the daemon-managed
// `{workDir}/.cursor/mcp.json` that cursor-agent discovers for the
// `--workspace` directory.
//
// Cursor has no `--mcp-config` flag like Claude/Pi; the project file is the
// only injection point Multica controls without touching the daemon user's
// `~/.cursor/mcp.json`. When agent.mcp_config is present (including an empty
// `{}` / `{"mcpServers":{}}`), that value is written authoritatively and a
// sibling marker records Multica ownership so a later clear can remove the
// file without deleting a user-authored mcp.json that Multica never wrote.
//
// File mode is 0o600 because mcpServers.*.env may carry secrets (Figma PAT,
// API keys). Directory mode is 0o700 so the secret-bearing file is not
// world-traversable on shared hosts.
func ensureCursorMcpConfig(workDir string, mcpConfig json.RawMessage, logger *slog.Logger) error {
	managed := hasManagedMcpConfig(mcpConfig)
	if workDir == "" {
		if managed {
			return fmt.Errorf("cursor: mcp_config is set but workspace cwd is empty; cannot materialise %s", cursorMcpRelPath)
		}
		return nil
	}

	mcpPath := filepath.Join(workDir, cursorMcpRelPath)
	markerPath := filepath.Join(workDir, cursorMcpOwnedMarker)

	if !managed {
		if _, err := os.Stat(markerPath); err == nil {
			if rmErr := os.Remove(mcpPath); rmErr != nil && !os.IsNotExist(rmErr) {
				return fmt.Errorf("remove managed %s: %w", cursorMcpRelPath, rmErr)
			}
			if rmErr := os.Remove(markerPath); rmErr != nil && !os.IsNotExist(rmErr) {
				return fmt.Errorf("remove %s marker: %w", cursorMcpRelPath, rmErr)
			}
			if logger != nil {
				logger.Debug("cursor: cleared managed mcp.json", "path", mcpPath)
			}
		}
		return nil
	}

	trimmed := bytes.TrimSpace(mcpConfig)
	var parsed any
	if err := json.Unmarshal(trimmed, &parsed); err != nil {
		return fmt.Errorf("cursor: invalid mcp_config json: %w", err)
	}
	if parsed == nil {
		parsed = map[string]any{}
	}
	pretty, err := json.MarshalIndent(parsed, "", "  ")
	if err != nil {
		return fmt.Errorf("cursor: marshal mcp_config: %w", err)
	}
	pretty = append(pretty, '\n')

	if err := os.MkdirAll(filepath.Dir(mcpPath), 0o700); err != nil {
		return fmt.Errorf("create .cursor dir: %w", err)
	}
	if err := os.WriteFile(mcpPath, pretty, 0o600); err != nil {
		return fmt.Errorf("write %s: %w", cursorMcpRelPath, err)
	}
	if err := os.Chmod(mcpPath, 0o600); err != nil {
		return fmt.Errorf("chmod %s to 0600: %w", cursorMcpRelPath, err)
	}
	if err := os.WriteFile(markerPath, []byte("multica-owned\n"), 0o600); err != nil {
		return fmt.Errorf("write %s marker: %w", cursorMcpRelPath, err)
	}
	if logger != nil {
		logger.Debug("cursor: wrote managed mcp.json", "path", mcpPath)
	}
	return nil
}

// hasManagedMcpConfig reports whether agent.mcp_config is "present" in the
// API three-state sense: a non-null JSON value. Both `{}` and
// `{"mcpServers":{}}` count as present (admin saved an empty managed set);
// only absent / SQL NULL / JSON `null` count as absent (CLI default).
func hasManagedMcpConfig(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return false
	}
	if bytes.Equal(trimmed, []byte("null")) {
		return false
	}
	return true
}
