package daemon

import (
	"fmt"
	"log/slog"
	"strings"

	"github.com/multica-ai/multica/server/internal/daemon/execenv"
	"github.com/multica-ai/multica/server/pkg/agent"
)

// tryCanonicalChatBackend is the sole D6-1b production path for full Grok/Pi/
// Cursor chat wakes. Slot identity is agent×runtime. Context key is ChatSessionID
// (never task.ID): same key may reuse the resident process; key change
// disposes the process and forces a fresh provider session (no PriorSessionID).
// There is no generation==0 fallback to legacy ChatSession-keyed pools.
// #702 PR B: Cursor uses this same entry (no third ChatSession pool).
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
	case "grok", "pi", "cursor":
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

	// Option A: materialize only when a new provider process will be created.
	// Resident reuse re-reads neither AGENTS nor .agent_context (Pi proof); per-turn
	// facts travel in the Execute prompt. Startup-static digest is in the identity
	// fingerprint so slow field changes dispose + recreate.
	identity, err := newCanonicalAgentRuntimeIdentity(canonicalAgentRuntimeIdentityParams{
		AgentID:             agentID,
		RuntimeID:           task.RuntimeID,
		Provider:            provider,
		Executable:          entry.Path,
		Model:               execOpts.Model,
		Thinking:            execOpts.ThinkingLevel,
		WorkDir:             workDir,
		SystemPrompt:        execOpts.SystemPrompt,
		MCP:                 string(execOpts.McpConfig),
		CustomArgs:          append(append([]string(nil), execOpts.ExtraArgs...), execOpts.CustomArgs...),
		Environment:         turn.StableEnvironment,
		ContextKey:          task.ChatSessionID,
		WorkspaceID:         task.WorkspaceID,
		Directed:            taskCtx.Directed,
		ManagedRole:         taskCtx.ManagedRole,
		AgentInstructions:   taskCtx.AgentInstructions,
		WorkspaceContext:    task.WorkspaceContext,
		StartupStaticDigest: execenv.StartupStaticDigest(taskCtx),
	})
	if err != nil {
		return nil, nil, nil, false, fmt.Errorf("canonical identity: %w", err)
	}

	mode := mustCanonicalRuntimeMode(provider, profile)
	factory := d.canonicalChatFactory(provider, profile)
	// Pool clears CanonicalSessionID when ContextKey rotates (defense in depth
	// vs claim PriorSessionID from the wrong chat). Same-key hard-field drift
	// restarts the process but may keep PriorSessionID for chat continuity.
	// Materialize runs only inside BeforeCreate when factory will spawn a process
	// (Barry: acquire first / reuse path zero FS; create path materialize then factory).
	ledgerRoot := execenv.CanonicalTurnLedgerRoot(turn.Workspace.RootDir)
	taskCtxCopy := taskCtx
	lease, err := d.canonicalRuntimes.acquire(canonicalAgentRuntimeAcquireRequest{
		Identity:           identity,
		Mode:               mode,
		CanonicalSessionID: task.PriorSessionID,
		BackendConfig:      backendCfg,
		Factory:            factory,
		BeforeCreate: func() error {
			_, err := execenv.MaterializeCanonicalTurnContext(workDir, ledgerRoot, provider, taskCtxCopy)
			if err != nil {
				return fmt.Errorf("materialize canonical turn context: %w", err)
			}
			return nil
		},
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
