package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/multica-ai/multica/server/internal/cli"
	"github.com/multica-ai/multica/server/internal/daemon"
)

func resolveProfile(cmd *cobra.Command) string {
	val, _ := cmd.Flags().GetString("profile")
	return val
}

func newAPIClient(cmd *cobra.Command) (*cli.APIClient, error) {
	serverURL := resolveServerURL(cmd)
	workspaceID := resolveWorkspaceID(cmd)
	token := resolveToken(cmd)

	if serverURL == "" {
		return nil, fmt.Errorf("server URL not set: use --server-url flag, MULTICA_SERVER_URL env, or 'multica config set server_url <url>'")
	}

	client := cli.NewAPIClient(serverURL, workspaceID, token)
	// When running inside a daemon task, attribute actions to the agent.
	if agentID := os.Getenv("MULTICA_AGENT_ID"); agentID != "" {
		client.AgentID = agentID
	}
	if taskID := os.Getenv("MULTICA_TASK_ID"); taskID != "" {
		client.TaskID = taskID
	}
	if inboxEventID := os.Getenv("MULTICA_AGENT_INBOX_EVENT_ID"); inboxEventID != "" {
		client.AgentInboxEventID = inboxEventID
	}
	if deliveryID := os.Getenv("MULTICA_AGENT_INBOX_DELIVERY_ID"); deliveryID != "" {
		client.AgentInboxDeliveryID = deliveryID
	}
	if leaseToken := os.Getenv("MULTICA_AGENT_INBOX_LEASE_TOKEN"); leaseToken != "" {
		client.AgentInboxLeaseToken = leaseToken
	}
	return client, nil
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
// inside a daemon-managed agent task (daemon sets MULTICA_AGENT_ID and
// MULTICA_TASK_ID in the agent env). In that context the workspace must be
// provided explicitly by the daemon — falling back to user-global
// ~/.multica/config.json would let the agent act on whatever workspace the
// user last configured, which is how cross-workspace contamination happens
// when multiple workspaces share a host.
func inAgentExecutionContext() bool {
	return os.Getenv("MULTICA_AGENT_ID") != "" || os.Getenv("MULTICA_TASK_ID") != ""
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
