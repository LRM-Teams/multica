package daemon

import (
	"fmt"
	"log/slog"
	"strings"

	"github.com/multica-ai/multica/server/pkg/agent"
)

// tryCanonicalChatBackend activates the D6-1b production path when claim has
// populated RuntimeStateGeneration (D6-1a) and the provider has a resident
// adapter. It returns used=false so callers fall back to the legacy Grok/Pi
// ChatSession-keyed pools when generation is absent (old server / soft-fail).
//
// Slot identity is agent×runtime via the canonical pool (no ChatSessionID).
// PriorSessionID still comes from legacy claim sources until D6-2.
func (d *Daemon) tryCanonicalChatBackend(
	task Task,
	provider string,
	profile string,
	agentID string,
	agentToken string,
	selfBin string,
	agentEnv map[string]string,
	entry AgentEntry,
	backendCfg agent.Config,
	execOpts agent.ExecOptions,
	taskLog *slog.Logger,
) (backend agent.Backend, release func(bool), turn *agentRuntimeTurn, used bool, err error) {
	if d == nil || d.agentRuntimeTurns == nil || d.canonicalRuntimes == nil {
		return nil, nil, nil, false, nil
	}
	if task.RuntimeStateGeneration <= 0 {
		return nil, nil, nil, false, nil
	}
	if strings.TrimSpace(task.ChatSessionID) == "" || isRestrictedExecutionProfile(profile) {
		return nil, nil, nil, false, nil
	}
	switch provider {
	case "grok", "pi":
	default:
		return nil, nil, nil, false, nil
	}
	if strings.TrimSpace(agentToken) == "" || strings.TrimSpace(selfBin) == "" {
		return nil, nil, nil, false, nil
	}
	if strings.TrimSpace(agentID) == "" || strings.TrimSpace(task.RuntimeID) == "" {
		return nil, nil, nil, false, nil
	}

	turn, err = d.agentRuntimeTurns.Begin(agentRuntimeTurnRequest{
		WorkspaceID:            task.WorkspaceID,
		AgentID:                agentID,
		RuntimeID:              task.RuntimeID,
		TurnID:                 task.ID,
		PriorSessionID:         task.PriorSessionID,
		RuntimeStateGeneration: task.RuntimeStateGeneration,
		MulticaBinary:          selfBin,
		Token:                  agentToken,
		Environment:            cloneEnvironment(agentEnv),
	})
	if err != nil {
		return nil, nil, nil, false, fmt.Errorf("canonical turn begin: %w", err)
	}
	releaseTurn := true
	defer func() {
		if releaseTurn && turn != nil {
			_ = turn.Close()
		}
	}()

	// Prefer the task execution cwd for the fingerprint when already prepared;
	// fall back to the agent-scoped workspace from Begin. Slot key remains
	// agent×runtime; workdir drift restarts the resident backend in-place.
	workDir := strings.TrimSpace(execOpts.Cwd)
	if workDir == "" {
		workDir = turn.WorkDir
	}

	identity, err := newCanonicalAgentRuntimeIdentity(canonicalAgentRuntimeIdentityParams{
		AgentID:      agentID,
		RuntimeID:    task.RuntimeID,
		Provider:     provider,
		Executable:   entry.Path,
		Model:        execOpts.Model,
		Thinking:     execOpts.ThinkingLevel,
		WorkDir:      workDir,
		SystemPrompt: execOpts.SystemPrompt,
		MCP:          string(execOpts.McpConfig),
		CustomArgs:   append(append([]string(nil), execOpts.ExtraArgs...), execOpts.CustomArgs...),
		Environment:  turn.StableEnvironment,
	})
	if err != nil {
		return nil, nil, nil, false, fmt.Errorf("canonical identity: %w", err)
	}

	lease, err := d.canonicalRuntimes.acquireCanonicalAgentRuntime(
		identity,
		task.PriorSessionID,
		profile,
		backendCfg,
	)
	if err != nil {
		return nil, nil, nil, false, fmt.Errorf("acquire canonical runtime: %w", err)
	}

	releaseTurn = false
	if taskLog != nil {
		taskLog.Info("canonical chat runtime acquired",
			"provider", provider,
			"generation", task.RuntimeStateGeneration,
			"prior_session", task.PriorSessionID != "",
			"slot_agent", agentID,
			"slot_runtime", task.RuntimeID,
		)
	}
	return lease.backend, func(healthy bool) {
		if healthy {
			lease.releaseForResult("completed", nil)
		} else {
			lease.release(false)
		}
		_ = turn.Close()
	}, turn, true, nil
}
