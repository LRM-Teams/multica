package daemon

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/multica-ai/multica/server/internal/daemon/execenv"
	"github.com/multica-ai/multica/server/pkg/agent"
)

// tryCanonicalChatBackend is the sole D6-1b production path for full Grok/Pi/
// Cursor chat wakes. Slot identity is agent×runtime. One long-lived resident
// process spans channel/DM/thread surfaces (no ChatSessionID force-fresh).
// There is no generation==0 fallback to legacy ChatSession-keyed pools.
//
// Resident process cwd is always the stable agent workspace from Begin
// (turn.WorkDir). Create-only AGENTS write runs in BeforeCreate; reuse does
// zero disk I/O. Per-turn surface/initiator/issue facts travel in the Execute
// prompt.
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
	if !isCanonicalResidentProvider(provider) {
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

	// Caller-boundary clean cut: legacy runTask still stuffs MULTICA_TOKEN_FILE
	// into agentEnv for the CLI wrapper. Provider process identity must not see
	// raw credential keys — Bind(request.Token) is the only secret channel.
	// SplitEnvironment remains fail-closed if anything still leaks past this strip.
	turn, err = d.agentRuntimeTurns.Begin(agentRuntimeTurnRequest{
		WorkspaceID:            task.WorkspaceID,
		AgentID:                agentID,
		RuntimeID:              task.RuntimeID,
		TurnID:                 task.ID,
		PriorSessionID:         task.PriorSessionID,
		RuntimeStateGeneration: task.RuntimeStateGeneration,
		MulticaBinary:          selfBin,
		Token:                  agentToken,
		Environment:            stripProviderCredentialTransport(agentEnv),
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

	// Create-only AGENTS: digest in fingerprint so slow field changes dispose
	// + recreate; reuse path has zero FS I/O.
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
		WorkspaceID:         task.WorkspaceID,
		AgentInstructions:   taskCtx.AgentInstructions,
		WorkspaceContext:    task.WorkspaceContext,
		StartupStaticDigest: execenv.StartupStaticDigest(provider, taskCtx),
	})
	if err != nil {
		return nil, nil, nil, false, fmt.Errorf("canonical identity: %w", err)
	}

	mode := mustCanonicalRuntimeMode(provider, profile)
	factory := d.canonicalChatFactory(provider, profile)
	// No ledger: slim materialize writes AGENTS only (no skill tree / cleanup).
	taskCtxCopy := taskCtx
	lease, err := d.canonicalRuntimes.acquire(canonicalAgentRuntimeAcquireRequest{
		Identity:           identity,
		Mode:               mode,
		CanonicalSessionID: task.PriorSessionID,
		BackendConfig:      backendCfg,
		Factory:            factory,
		BeforeCreate: func() error {
			// ledgerRoot unused; pass agent root sibling for API compat only.
			ledgerRoot := execenv.CanonicalTurnLedgerRoot(turn.Workspace.AgentRoot)
			brief, receipt, err := execenv.MaterializeCanonicalTurnContextB(workDir, ledgerRoot, provider, taskCtxCopy)
			if err != nil {
				return fmt.Errorf("materialize canonical AGENTS: %w", err)
			}
			if taskLog != nil {
				taskLog.Info("canonical startup AGENTS written",
					"managed_input_digest", receipt.ManagedInputDigest,
					"agents_final_sha256", receipt.AgentsFinalSHA256,
					"brief_bytes", len(brief),
				)
			}
			return nil
		},
	})
	if err != nil {
		return nil, nil, nil, false, fmt.Errorf("acquire canonical runtime: %w", err)
	}
	if mode == canonicalRuntimeResident {
		created, err := d.ensureIdleMessageCoordinator(agentID, task.RuntimeID, turn.Workspace.AgentRoot)
		if err != nil {
			lease.release(false)
			return nil, nil, nil, false, fmt.Errorf("register idle Message coordinator: %w", err)
		}
		if created {
			d.beginAgentMessageRecovery(agentID, nil)
		}
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
			"chat_session_id", task.ChatSessionID,
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
		if healthy {
			if err := d.flushIdleAgentDelivery(context.Background(), agentID); err != nil && taskLog != nil {
				taskLog.Debug("idle Message flush deferred", "error", err)
			}
		}
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
