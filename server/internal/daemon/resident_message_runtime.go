package daemon

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/internal/daemon/execenv"
	"github.com/multica-ai/multica/server/pkg/agent"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// residentPiRunIdentity converts an optional run-scoped identity pair into the
// Pi binding input. Both fields empty means a plain resident session; exactly
// one set is a protocol violation.
func residentPiRunIdentity(runID, runAgentID string) (*agent.PiRunIdentity, error) {
	runID = strings.TrimSpace(runID)
	runAgentID = strings.TrimSpace(runAgentID)
	if runID == "" && runAgentID == "" {
		return nil, nil
	}
	if runID == "" || runAgentID == "" {
		return nil, errors.New("resident Pi run identity requires both run_id and run_agent_id")
	}
	return &agent.PiRunIdentity{RunID: runID, RunAgentID: runAgentID}, nil
}

// ensureResidentMessageRuntime creates the single Agent×runtime provider
// process needed by the MessageCoordinator. Its input is deliberately only
// stable Agent placement/configuration; Message delivery never constructs a
// Task or a current-turn transport envelope.
func (d *Daemon) ensureResidentMessageRuntime(ctx context.Context, agentID, runtimeID string, runIdentity *agent.PiRunIdentity) error {
	if d == nil || d.canonicalRuntimes == nil {
		return errors.New("resident Message runtime is not configured")
	}
	if d.canonicalRuntimes.hasResidentBackend(agentID, runtimeID) {
		if runIdentity != nil {
			if _, err := d.canonicalRuntimes.bindResidentPiRunIdentity(ctx, agentID, runtimeID, *runIdentity); err == nil {
				return d.ensureResidentProviderProcess(ctx, agentID, runtimeID)
			} else if !errors.Is(err, agent.ErrPiRPCRunIdentityRequiresFreshSession) {
				return fmt.Errorf("bind resident Pi run identity: %w", err)
			}
			// A run-scoped Pi session cannot inherit an unbound or differently
			// bound resident. Replace it only through the pool's idle invalidation
			// boundary; an active prior turn remains busy and is never torn down.
			if err := d.canonicalRuntimes.invalidateSession(agentID, runtimeID); err != nil {
				return fmt.Errorf("rotate resident Pi run identity: %w", err)
			}
		} else {
			return d.ensureResidentProviderProcess(ctx, agentID, runtimeID)
		}
	}
	if d.client == nil {
		return errors.New("resident Message runtime is not configured")
	}
	d.mu.Lock()
	runtime, ok := d.runtimeIndex[runtimeID]
	d.mu.Unlock()
	if !ok || strings.TrimSpace(runtime.Provider) == "" {
		return errors.New("runtime is not available for resident Message delivery")
	}
	entry, ok := d.cfg.Agents[runtime.Provider]
	if !ok {
		return fmt.Errorf("no agent configured for provider %q", runtime.Provider)
	}
	config, err := d.client.GetResidentAgentRuntimeConfig(ctx, runtimeID, agentID)
	if err != nil {
		return fmt.Errorf("load resident Agent configuration: %w", err)
	}
	if config.Agent == nil || config.Agent.ID != agentID || config.RuntimeID != runtimeID || config.WorkspaceID != runtime.WorkspaceID {
		return errors.New("invalid resident Agent configuration")
	}
	if !isCanonicalResidentProvider(runtime.Provider) {
		return fmt.Errorf("provider %q has no resident Message runtime", runtime.Provider)
	}

	if _, err := d.ensureResidentAgentCredential(ctx, config.WorkspaceID, config.RuntimeID, config.Agent.ID); err != nil {
		return fmt.Errorf("ensure durable Agent credential: %w", err)
	}
	workspace, err := execenv.ProvisionAgentWorkspace(d.cfg.WorkspacesRoot, config.WorkspaceID, config.Agent.ID, d.logger)
	if err != nil {
		return fmt.Errorf("provision resident Agent workspace: %w", err)
	}

	taskCtx := execenv.TaskContextForEnv{
		MessageDelivery:   true,
		AgentID:           config.Agent.ID,
		AgentName:         config.Agent.Name,
		ManagedRole:       config.Agent.ManagedRole,
		AgentInstructions: config.Agent.Instructions,
		AgentRoot:         workspace.AgentRoot,
		AgentSkills:       convertSkillsForEnv(config.Agent.Skills),
		WorkspaceContext:  config.WorkspaceContext,
	}
	env := execenv.Reuse(execenv.ReuseParams{
		AgentRoot:    workspace.AgentRoot,
		Provider:     runtime.Provider,
		CodexVersion: d.agentVersion(agent.ProviderCodex),
		McpConfig:    config.Agent.McpConfig,
		Task:         taskCtx,
	}, d.logger)
	if env == nil {
		return errors.New("prepare resident Agent environment")
	}

	model := strings.TrimSpace(config.Agent.Model)
	if model == "" {
		model = entry.Model
	}
	thinking := strings.TrimSpace(config.Agent.ThinkingLevel)
	if thinking != "" {
		valid, err := agent.ValidateThinkingLevel(ctx, runtime.Provider, entry.Path, model, thinking)
		if err != nil {
			if d.logger != nil {
				d.logger.Warn("resident Message runtime: thinking-level catalog lookup failed; passing through", "provider", runtime.Provider, "error", err)
			}
		} else if !valid {
			if d.logger != nil {
				d.logger.Warn("resident Message runtime: invalid thinking level; skipping", "provider", runtime.Provider, "model", model, "thinking_level", thinking)
			}
			thinking = ""
		}
	}
	selfBin, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve multica executable: %w", err)
	}
	agentEnv := map[string]string{
		"MULTICA_DAEMON_PORT":  fmt.Sprintf("%d", d.cfg.HealthPort),
		"MULTICA_WORKSPACE_ID": config.WorkspaceID,
		"MULTICA_AGENT_NAME":   config.Agent.Name,
		"MULTICA_AGENT_ID":     config.Agent.ID,
		"MULTICA_AGENT_ROOT":   env.AgentRoot,
		"PATH":                 filepath.Dir(selfBin) + string(os.PathListSeparator) + os.Getenv("PATH"),
	}
	// Resident Message processes accept only agent-scoped custom environment;
	// channel and project scoped secrets belong to product Task execution.
	injectAgentCustomEnv(agentEnv, config.Agent, d.logger)
	addMulticaAgentEnv(agentEnv, d.cfg, config.WorkspaceID, config.Agent.ID)
	if runtime.Provider == agent.ProviderPi {
		addPiMemoryFastModeEnv(agentEnv)
	}
	residentLaunchID := "resident-" + uuid.NewString()
	identity, err := newCanonicalAgentRuntimeIdentity(canonicalAgentRuntimeIdentityParams{
		AgentID:             config.Agent.ID,
		RuntimeID:           config.RuntimeID,
		Provider:            runtime.Provider,
		Executable:          entry.Path,
		Model:               model,
		Thinking:            thinking,
		WorkDir:             env.AgentRoot,
		MCP:                 string(config.Agent.McpConfig),
		CustomArgs:          append([]string(nil), config.Agent.CustomArgs...),
		Environment:         agentEnv,
		WorkspaceID:         config.WorkspaceID,
		AgentInstructions:   config.Agent.Instructions,
		WorkspaceContext:    config.WorkspaceContext,
		StartupStaticDigest: execenv.StartupStaticDigest(runtime.Provider, taskCtx),
	})
	if err != nil {
		return fmt.Errorf("resident Message runtime identity: %w", err)
	}

	// Resume only the id last applied by agent:start for this DaemonCore.
	// Do not invent a disk-backed pointer; a new process starts empty until
	// the next start payload, same as Raft idleRestartSnapshots.
	resumeSessionID := ""
	if d.agentRuntimeSessions != nil {
		if stored, err := d.agentRuntimeSessions.Get(agentID, runtimeID); err == nil {
			resumeSessionID = stored
		}
	}
	lease, err := d.canonicalRuntimes.acquire(canonicalAgentRuntimeAcquireRequest{
		Identity:           identity,
		CanonicalSessionID: resumeSessionID,
		BackendConfig: agent.Config{
			ExecutablePath: entry.Path,
			Env:            agentEnv,
			Logger:         d.logger,
			ResidentOptions: agent.ExecOptions{
				Cwd:             env.AgentRoot,
				Model:           model,
				CustomArgs:      append([]string(nil), config.Agent.CustomArgs...),
				McpConfig:       append([]byte(nil), config.Agent.McpConfig...),
				ThinkingLevel:   thinking,
				ResumeSessionID: resumeSessionID,
			},
		},
		Factory: d.canonicalResidentMessageFactory(runtime.Provider),
		BeforeCreate: func() error {
			ledgerRoot := execenv.CanonicalTurnLedgerRoot(workspace.AgentRoot)
			_, _, err := execenv.MaterializeCanonicalTurnContextB(env.AgentRoot, ledgerRoot, runtime.Provider, taskCtx)
			return err
		},
		PrepareLaunchEnvironment: func(environment map[string]string) (func(), error) {
			transport, err := d.prepareAgentProxyCLITransport(
				InboxKey{WorkspaceID: config.WorkspaceID, AgentID: config.Agent.ID},
				config.RuntimeID,
				residentLaunchID,
				selfBin,
			)
			if err != nil {
				return nil, err
			}
			environment[AgentProxyCLIWrapperEnv] = transport.wrapperPath
			environment["PATH"] = filepath.Dir(transport.wrapperPath) + string(os.PathListSeparator) + environment["PATH"]
			return func() { _ = transport.Close() }, nil
		},
		Context: ctx,
	})
	if err != nil {
		return fmt.Errorf("acquire resident Message runtime: %w", err)
	}
	lease.release(true)
	if runIdentity != nil {
		if _, err := d.canonicalRuntimes.bindResidentPiRunIdentity(ctx, agentID, runtimeID, *runIdentity); err != nil {
			_ = d.canonicalRuntimes.invalidateSession(agentID, runtimeID)
			return fmt.Errorf("bind resident Pi run identity: %w", err)
		}
	}
	return d.ensureResidentProviderProcess(ctx, agentID, runtimeID)
}

// ensureResidentProviderProcess keeps Raft's failed-start cleanup invariant:
// a provider that did not start cannot remain registered as resident. Idle
// backends detach immediately. If an older turn is still draining, force the
// provider process to exit and let that turn's owner detach the fenced backend.
func (d *Daemon) ensureResidentProviderProcess(ctx context.Context, agentID, runtimeID string) error {
	if err := d.canonicalRuntimes.ensureResidentProcess(ctx, agentID, runtimeID); err != nil {
		startErr := fmt.Errorf("start resident provider process: %w", err)
		cleanupErr := d.canonicalRuntimes.invalidateSession(agentID, runtimeID)
		if errors.Is(cleanupErr, ErrCanonicalAgentRuntimeBusy) {
			cleanupErr = d.canonicalRuntimes.forceInvalidateSession(agentID, runtimeID)
		}
		if cleanupErr != nil {
			return errors.Join(startErr, fmt.Errorf("retire failed resident provider process: %w", cleanupErr))
		}
		return startErr
	}
	d.recordProviderSession(agentID, runtimeID, d.canonicalRuntimes.residentProviderSession(agentID, runtimeID))
	return nil
}

// ensureResidentAgentCredential is the durable Agent identity for Message
// delivery. It never creates a task-shaped execution record.
func (d *Daemon) ensureResidentAgentCredential(ctx context.Context, workspaceID, runtimeID, agentID string) (string, error) {
	return d.ensureAgentCredential(ctx, workspaceID, runtimeID, agentID, nil)
}

func (d *Daemon) canonicalResidentMessageFactory(provider string) canonicalRuntimeBackendFactory {
	if d != nil && d.canonicalResidentFactoryOverride != nil {
		return d.canonicalResidentFactoryOverride
	}
	return defaultCanonicalRuntimeFactory(provider)
}

func newCanonicalGrokResidentBackend(cfg agent.Config) (agent.Backend, func(), error) {
	backend := agent.NewGrokACPBackend(cfg)
	return backend, backend.Close, nil
}

func newCanonicalPiResidentBackend(cfg agent.Config) (agent.Backend, func(), error) {
	backend := agent.NewPiRPCBackend(cfg)
	return backend, backend.Close, nil
}

func (d *Daemon) prepareResidentPiRun(ctx context.Context, agentID, runtimeID string, identity agent.PiRunIdentity) (agent.PiRunBinding, error) {
	if err := d.ensureResidentMessageRuntime(ctx, agentID, runtimeID, &identity); err != nil {
		return agent.PiRunBinding{}, err
	}
	return d.canonicalRuntimes.bindResidentPiRunIdentity(ctx, agentID, runtimeID, identity)
}

func (d *Daemon) revokeResidentPiRun(agentID, runtimeID string, identity agent.PiRunIdentity) error {
	if d == nil || d.canonicalRuntimes == nil {
		return nil
	}
	return d.canonicalRuntimes.revokeResidentPiRunIdentity(agentID, runtimeID, identity)
}

func (d *Daemon) handlePreparePiRunRequest(ctx context.Context, req protocol.PreparePiRunRequestPayload, writes chan<- []byte) {
	response := protocol.PreparePiRunResponsePayload{RequestID: req.RequestID}
	binding, err := d.prepareResidentPiRun(ctx, req.AgentID, req.RuntimeID, agent.PiRunIdentity{RunID: req.RunID, RunAgentID: req.RunAgentID})
	if err != nil {
		response.Error = err.Error()
	} else {
		response.SessionID = binding.SessionID
		response.CaptureBoundary = binding.CaptureBoundary
	}
	d.sendDaemonFrame(protocol.EventDaemonPreparePiRunResponse, response, req.RequestID, writes)
}

func (d *Daemon) handleRevokePiRunRequest(req protocol.RevokePiRunRequestPayload, writes chan<- []byte) {
	response := protocol.RevokePiRunResponsePayload{RequestID: req.RequestID}
	if err := d.revokeResidentPiRun(req.AgentID, req.RuntimeID, agent.PiRunIdentity{RunID: req.RunID, RunAgentID: req.RunAgentID}); err != nil {
		response.Error = err.Error()
	}
	d.sendDaemonFrame(protocol.EventDaemonRevokePiRunResponse, response, req.RequestID, writes)
}
