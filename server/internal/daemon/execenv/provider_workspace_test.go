package execenv

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"

	"github.com/multica-ai/multica/server/internal/agentworkspace"
)

type testPrepareParams struct {
	WorkspacesRoot string
	WorkspaceID    string
	AgentID        string
	Provider       string
	CodexVersion   string
	McpConfig      json.RawMessage
	Task           TaskContextForEnv
}

func prepareTestEnvironment(params testPrepareParams, logger *slog.Logger) (*Environment, error) {
	if params.WorkspacesRoot == "" || params.WorkspaceID == "" || params.AgentID == "" {
		return nil, fmt.Errorf("execenv test prepare: root, workspace ID, and agent ID are required")
	}
	agentRoot := agentworkspace.Root(params.WorkspacesRoot, params.WorkspaceID, params.AgentID)
	if err := os.RemoveAll(agentRoot); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(agentRoot, 0o755); err != nil {
		return nil, err
	}
	env := Reuse(ReuseParams{
		AgentRoot:    agentRoot,
		Provider:     params.Provider,
		CodexVersion: params.CodexVersion,
		McpConfig:    params.McpConfig,
		Task:         params.Task,
	}, logger)
	if env == nil {
		return nil, fmt.Errorf("prepare %s environment failed", params.Provider)
	}
	return env, nil
}

func cleanupTestEnvironment(env *Environment) {
	if env != nil {
		_ = os.RemoveAll(env.AgentRoot)
	}
}
