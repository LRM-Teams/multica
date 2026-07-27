package daemon

import (
	"fmt"
	"log/slog"
	"strings"

	"github.com/multica-ai/multica/server/internal/daemon/execenv"
	"github.com/multica-ai/multica/server/pkg/agent"
)

// tryCanonicalChatBackend is the sole D6-1b production path for full Grok/Pi
// chat wakes. Slot identity is agent×runtime. Context key is ChatSessionID
// (never task.ID): same key may reuse the resident process; key change
// disposes the process and forces a fresh provider session (no PriorSessionID).
// There is no generation==0 fallback to legacy ChatSession-keyed pools.
//
// Resident process cwd/identity WorkDir is always the stable agent workspace
// from Begin (turn.WorkDir), never the per-task cloud env workdir.
// Materialize refreshes disk context before Execute; process restart (fingerprint
// / context rotate) is what makes Pi/Grok re-read it.
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
	execOpts *agent.ExecOptions,
	taskCtx execenv.TaskContextForEnv,
	taskLog *slog.Logger,
) (backend agent.Backend, release func(bool), turn *agentRuntimeTurn, used bool, err error) {
	if d == nil || d.agentRuntimeTurns == nil || d.canonicalRuntimes == nil {
		return nil, nil, nil, false, fmt.Errorf("canonical chat runtime is not configured")
	}
	if strings.TrimSpace(task.ChatSessionID) == "" || isRestrictedExecutionProfile(profile) {
		return nil, nil, nil, false, nil
	}
	switch provider {
	case "grok", "pi":
	default:
		return nil, nil, nil, false, nil
	}
	if task.RuntimeStateGeneration <= 0 {
		// Fail closed: do not re-enter ChatSession-keyed pools. Served D6-1a
		// must attach generation; soft-fail/empty claim is a pairing bug.
		return nil, nil, nil, false, fmt.Errorf(
			"canonical chat requires runtime_state_generation>0 from D6-1a claim (got %d); refusing legacy ChatSession pool",
			task.RuntimeStateGeneration,
		)
	}
	if strings.TrimSpace(agentToken) == "" || strings.TrimSpace(selfBin) == "" {
		return nil, nil, nil, false, nil
	}
	if strings.TrimSpace(agentID) == "" || strings.TrimSpace(task.RuntimeID) == "" {
		return nil, nil, nil, false, nil
	}
	if execOpts == nil {
		return nil, nil, nil, false, fmt.Errorf("canonical chat requires exec options")
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

	// Stable agent workspace only — not per-task {ws}/{shortTask}/workdir.
	workDir := strings.TrimSpace(turn.WorkDir)
	if workDir == "" {
		return nil, nil, nil, false, fmt.Errorf("canonical turn workdir is empty")
	}
	execOpts.Cwd = workDir

	// Materialize Multica runtime brief + task context + skills into the
	// stable cwd. Per-task env.WorkDir still got an inject earlier for
	// non-resident paths; resident Grok/Pi only see turn.WorkDir.
	if _, err := execenv.MaterializeCanonicalTurnContext(workDir, provider, taskCtx); err != nil {
		return nil, nil, nil, false, fmt.Errorf("materialize canonical turn context: %w", err)
	}

	identity, err := newCanonicalAgentRuntimeIdentity(canonicalAgentRuntimeIdentityParams{
		AgentID:           agentID,
		RuntimeID:         task.RuntimeID,
		Provider:          provider,
		Executable:        entry.Path,
		Model:             execOpts.Model,
		Thinking:          execOpts.ThinkingLevel,
		WorkDir:           workDir,
		SystemPrompt:      execOpts.SystemPrompt,
		MCP:               string(execOpts.McpConfig),
		CustomArgs:        append(append([]string(nil), execOpts.ExtraArgs...), execOpts.CustomArgs...),
		Environment:       turn.StableEnvironment,
		ContextKey:        task.ChatSessionID,
		WorkspaceID:       task.WorkspaceID,
		Directed:          taskCtx.Directed,
		ManagedRole:       taskCtx.ManagedRole,
		AgentInstructions: taskCtx.AgentInstructions,
		WorkspaceContext:  task.WorkspaceContext,
	})
	if err != nil {
		return nil, nil, nil, false, fmt.Errorf("canonical identity: %w", err)
	}

	factory := d.canonicalChatFactory(provider, profile)
	// Pool clears CanonicalSessionID when ContextKey rotates (defense in depth
	// vs claim PriorSessionID from the wrong chat). Same-key hard-field drift
	// restarts the process but may keep PriorSessionID for chat continuity.
	lease, err := d.canonicalRuntimes.acquire(canonicalAgentRuntimeAcquireRequest{
		Identity:           identity,
		Mode:               mustCanonicalRuntimeMode(provider, profile),
		CanonicalSessionID: task.PriorSessionID,
		BackendConfig:      backendCfg,
		Factory:            factory,
	})
	if err != nil {
		return nil, nil, nil, false, fmt.Errorf("acquire canonical runtime: %w", err)
	}

	releaseTurn = false
	if taskLog != nil {
		resumeID := ""
		if wrapped, ok := lease.backend.(*canonicalSessionBackend); ok {
			resumeID = wrapped.canonicalSessionID
		}
		taskLog.Info("canonical chat runtime acquired",
			"provider", provider,
			"generation", task.RuntimeStateGeneration,
			"context_key", task.ChatSessionID,
			"prior_session_claim", task.PriorSessionID != "",
			"resume_session", resumeID != "",
			"slot_agent", agentID,
			"slot_runtime", task.RuntimeID,
			"work_dir", workDir,
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

func (d *Daemon) canonicalChatFactory(provider, profile string) canonicalRuntimeBackendFactory {
	if d != nil && d.canonicalChatFactoryOverride != nil {
		return d.canonicalChatFactoryOverride
	}
	mode, err := canonicalRuntimeModeFor(provider, profile)
	if err != nil {
		return func(agent.Config) (agent.Backend, func(), error) {
			return nil, nil, err
		}
	}
	return defaultCanonicalRuntimeFactory(provider, mode)
}

func mustCanonicalRuntimeMode(provider, profile string) canonicalRuntimeMode {
	mode, err := canonicalRuntimeModeFor(provider, profile)
	if err != nil {
		return canonicalRuntimeOneShot
	}
	return mode
}
