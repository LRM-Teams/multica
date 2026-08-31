package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/multica-ai/multica/server/internal/cli"
	"github.com/multica-ai/multica/server/internal/daemon"
)

// computerMode is set while running under `multica computer ...`. The
// Computer lifecycle is machine-wide and never profile-scoped, so every
// resolved profile is forced to "" (the machine-wide default) — a Workspace
// selector is scoping/readiness only, never a profile or second resident
// (#2487, #2490).
var computerMode bool

func resolveProfile(cmd *cobra.Command) string {
	if computerMode {
		return ""
	}
	val, _ := cmd.Flags().GetString("profile")
	return val
}

func newAPIClient(cmd *cobra.Command) (*cli.APIClient, error) {
	workspaceID := resolveWorkspaceID(cmd)
	if inAgentExecutionContext() {
		agentID := strings.TrimSpace(os.Getenv("MULTICA_AGENT_ID"))
		if agentID == "" {
			return nil, fmt.Errorf("agent API requests require MULTICA_AGENT_ID")
		}
		if workspaceID == "" {
			return nil, fmt.Errorf("agent API requests require MULTICA_WORKSPACE_ID")
		}
		proxyURL, err := localAgentAPIProxyURL()
		if err != nil {
			return nil, err
		}
		client := cli.NewAPIClient(proxyURL, workspaceID, "")
		client.AgentID = agentID
		tokenFile := strings.TrimSpace(os.Getenv(daemon.AgentProxyTokenFileEnv))
		if tokenFile == "" {
			return nil, fmt.Errorf("agent API requests require %s", daemon.AgentProxyTokenFileEnv)
		}
		rawToken, err := os.ReadFile(tokenFile)
		if err != nil {
			return nil, fmt.Errorf("read Agent Proxy credential: %w", err)
		}
		client.AgentProxyToken = strings.TrimSpace(string(rawToken))
		if client.AgentProxyToken == "" {
			return nil, fmt.Errorf("Agent Proxy credential is empty")
		}
		return client, nil
	}

	serverURL := resolveServerURL(cmd)
	token := resolveToken(cmd)

	if serverURL == "" {
		return nil, fmt.Errorf("server URL not set: use --server-url flag, MULTICA_SERVER_URL env, or 'multica config set server_url <url>'")
	}

	client := cli.NewAPIClient(serverURL, workspaceID, token)
	return client, nil
}

// localAgentAPIProxyURL returns the daemon-owned local API boundary used by
// every agent CLI command. The daemon supplies the durable credential; task,
// inbox-delivery, and lease identity never leave the agent process.
func localAgentAPIProxyURL() (string, error) {
	portRaw := strings.TrimSpace(os.Getenv("MULTICA_DAEMON_PORT"))
	port, err := strconv.Atoi(portRaw)
	if err != nil || port < 1 || port > 65535 {
		return "", fmt.Errorf("agent API requests require a valid MULTICA_DAEMON_PORT")
	}
	return fmt.Sprintf("http://127.0.0.1:%d", port), nil
}

func resolveServerURL(cmd *cobra.Command) string {
	val := cli.FlagOrEnv(cmd, "server-url", "MULTICA_SERVER_URL", "")
	if val != "" {
		return normalizeAPIBaseURL(val)
	}
	profile := resolveProfile(cmd)
	cfg, err := cli.LoadCLIConfigForProfile(profile)
	if err == nil && cfg.ServerURL != "" {
		return normalizeAPIBaseURL(cfg.ServerURL)
	}
	fmt.Fprintln(os.Stderr, "No server configured. Run 'multica setup' first.")
	os.Exit(1)
	return "" // unreachable
}

func normalizeAPIBaseURL(raw string) string {
	normalized, err := daemon.NormalizeServerBaseURL(raw)
	if err == nil {
		return normalized
	}
	return raw
}

// inAgentExecutionContext reports whether the CLI is being invoked from
// inside a daemon-managed agent run. In that context the workspace must be
// provided explicitly by the daemon — falling back to user-global
// ~/.multica/config.json would let the agent act on whatever workspace the
// user last configured, which is how cross-workspace contamination happens
// when multiple workspaces share a host.
func inAgentExecutionContext() bool {
	return strings.TrimSpace(os.Getenv("MULTICA_AGENT_ID")) != ""
}

func resolveWorkspaceID(cmd *cobra.Command) string {
	val := cli.FlagOrEnv(cmd, "workspace-id", "MULTICA_WORKSPACE_ID", "")
	if val != "" {
		return val
	}
	// Inside an agent task the daemon is the only authority on workspace
	// identity. Never read the user-global CLI config here.
	if inAgentExecutionContext() {
		return ""
	}
	profile := resolveProfile(cmd)
	cfg, _ := cli.LoadCLIConfigForProfile(profile)
	return cfg.WorkspaceID
}

// requireWorkspaceID resolves the workspace ID and returns an error with
// actionable instructions if it is empty (e.g. user has multiple workspaces
// but no default configured).
func requireWorkspaceID(cmd *cobra.Command) (string, error) {
	id := resolveWorkspaceID(cmd)
	if id == "" {
		if inAgentExecutionContext() {
			return "", fmt.Errorf("workspace_id is required: MULTICA_WORKSPACE_ID must be set by the daemon in agent execution context (no fallback to user config)")
		}
		return "", fmt.Errorf("workspace_id is required: use --workspace-id flag, set MULTICA_WORKSPACE_ID env, or run 'multica config set workspace_id <id>'")
	}
	return id, nil
}

// ---------------------------------------------------------------------------
// Agent commands
// ---------------------------------------------------------------------------

func strVal(m map[string]any, key string) string {
	v, ok := m[key]
	if !ok || v == nil {
		return ""
	}
	return fmt.Sprintf("%v", v)
}
