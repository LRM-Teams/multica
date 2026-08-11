package daemon

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/multica-ai/multica/server/internal/daemon/execenv"
	"github.com/multica-ai/multica/server/pkg/agent"
)

// ensureResidentMessageRuntime creates the single Agent×runtime provider
// process needed by the MessageCoordinator. Its input is deliberately only
// stable Agent placement/configuration; Message delivery never constructs a
// Task or a current-turn transport envelope.
func (d *Daemon) ensureResidentMessageRuntime(ctx context.Context, agentID, runtimeID string) error {
	if d == nil || d.canonicalRuntimes == nil {
		return errors.New("resident Message runtime is not configured")
	}
	if d.canonicalRuntimes.hasResidentBackend(agentID, runtimeID) {
		return nil
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
	if config.Agent == nil || config.Agent.ID != agentID || config.RuntimeID != runtimeID || config.WorkspaceID != runtime.WorkspaceID || config.RuntimeStateGeneration <= 0 {
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
	openclawBin := ""
	if runtime.Provider == "openclaw" {
		openclawBin = entry.Path
	}
	env := execenv.Reuse(execenv.ReuseParams{
		AgentRoot:    workspace.AgentRoot,
		Provider:     runtime.Provider,
		CodexVersion: d.agentVersion("codex"),
		OpenclawBin:  openclawBin,
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
	if runtime.Provider == "pi" {
		addPiMemoryFastModeEnv(agentEnv)
	}
	if env.CodexHome != "" {
		agentEnv["CODEX_HOME"] = env.CodexHome
	}
	if env.OpenclawConfigPath != "" {
		agentEnv["OPENCLAW_CONFIG_PATH"] = env.OpenclawConfigPath
	}
	if roots, ok := composeOpenclawIncludeRoots(env.OpenclawIncludeRoot, os.Getenv("OPENCLAW_INCLUDE_ROOTS")); ok {
		agentEnv["OPENCLAW_INCLUDE_ROOTS"] = roots
	}

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

	lease, err := d.canonicalRuntimes.acquire(canonicalAgentRuntimeAcquireRequest{
		Identity: identity,
		Mode:     canonicalRuntimeResident,
		BackendConfig: agent.Config{
			ExecutablePath: entry.Path,
			Env:            agentEnv,
			Logger:         d.logger,
			ResidentOptions: agent.ExecOptions{
				Cwd:           env.AgentRoot,
				Model:         model,
				CustomArgs:    append([]string(nil), config.Agent.CustomArgs...),
				McpConfig:     append([]byte(nil), config.Agent.McpConfig...),
				ThinkingLevel: thinking,
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
				selfBin,
			)
			if err != nil {
				return nil, err
			}
			environment["PATH"] = filepath.Dir(transport.wrapperPath) + string(os.PathListSeparator) + environment["PATH"]
			return func() { _ = transport.Close() }, nil
		},
		Context: ctx,
	})
	if err != nil {
		return fmt.Errorf("acquire resident Message runtime: %w", err)
	}
	lease.release(true)
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
	return defaultCanonicalRuntimeFactory(provider, canonicalRuntimeResident)
}

func newCanonicalGrokResidentBackend(cfg agent.Config) (agent.Backend, func(), error) {
	backend := agent.NewGrokACPBackend(cfg)
	return backend, backend.Close, nil
}

func newCanonicalPiResidentBackend(cfg agent.Config) (agent.Backend, func(), error) {
	backend := agent.NewPiRPCBackend(cfg)
	return backend, backend.Close, nil
}
