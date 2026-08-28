package daemon

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mattn/go-shellwords"

	"github.com/multica-ai/multica/server/internal/agentworkspace"
	"github.com/multica-ai/multica/server/internal/cli"
	"github.com/multica-ai/multica/server/internal/computer"
	"github.com/multica-ai/multica/server/pkg/agent"
)

const (
	DefaultServerURL         = "ws://localhost:8080/ws"
	DefaultPollInterval      = 2 * time.Second
	DefaultHeartbeatInterval = 15 * time.Second
	// DefaultAgentTimeout is the optional absolute wall-clock cap on a single
	// agent run. 0 = no cap. Operators who want a
	// hard ceiling for cost/resource control can set MULTICA_AGENT_TIMEOUT.
	DefaultAgentTimeout                   = 0
	DefaultCodexSemanticInactivityTimeout = 10 * time.Minute
	// DefaultRuntimeProgressStale is the Raft-aligned resident runtime silence
	// threshold. Set MULTICA_RUNTIME_PROGRESS_STALE=0 to disable.
	DefaultRuntimeProgressStale     = 15 * time.Minute
	DefaultRuntimeName              = "Local Agent"
	DefaultWorkspaceSyncInterval    = 30 * time.Second
	DefaultHealthPort               = 19514
	DefaultSharedSkillsSyncInterval = 60 * time.Second
	DefaultMemoryCurationRunTimeout = 10 * time.Minute
	// DefaultProblemEvolutionBatchTimeout is the wall clock for one external
	// evolver invocation when the run's budget does not set its own.
	DefaultProblemEvolutionBatchTimeout = 30 * time.Minute
	// DefaultProblemEvolutionGracefulDrain is how long a stopped evolver has
	// to exit after SIGTERM before it is killed (spec §13.1).
	DefaultProblemEvolutionGracefulDrain = 60 * time.Second
	// DefaultProblemEvolutionClaimInterval is how often the daemon asks for a
	// queued run while none is executing locally.
	DefaultProblemEvolutionClaimInterval = 15 * time.Second

	// Graph memory reviewer (design: docs/superpowers/specs/2026-08-14-graph-memory-reviewer-design.zh-CN.md).
	MemoryTypeLegacy = "legacy"
	MemoryTypeGraph  = "graph"
	// DefaultMemoryType keeps the legacy memory pipeline active unless the
	// operator opts into the graph reviewer via MULTICA_MEMORY_TYPE=graph.
	DefaultMemoryType         = MemoryTypeLegacy
	DefaultGraphExploreAgents = 1
	// DefaultGraphExploreMaxRounds matches memorygraph.DefaultExploreConfig:
	// the merged /explore protocol counts one round per served node, so the
	// budget is larger than the legacy /view+/expand round count.
	DefaultGraphExploreMaxRounds     = 6
	DefaultGraphRewardTimeoutSeconds = 600
	// DefaultMemoryCurationL3ReviewTimeout is the per-invocation wall clock for
	// the curator agent (self-review / team curation / L3). 30s was too short
	// for Cursor/Codex team curation over multiple agents and evidence.
	DefaultMemoryCurationL3ReviewTimeout = 10 * time.Minute
	raftLoopbackNoProxy                  = "127.0.0.1,localhost"
	// DefaultInboundWatchdog: see inbound_watchdog.go (Raft-aligned 70s).

	// DefaultAgentWorkspaceQuotaBytes: per-agent cap on <workspace-id>/agents/<agent-id>.
	// 0 = unlimited (LRM-1047: prior 2GiB default was blocking real agents;
	// re-enable a cap via MULTICA_AGENT_WORKSPACE_QUOTA_BYTES=<positive>).
	DefaultAgentWorkspaceQuotaBytes int64 = 0
)

// Config holds all daemon configuration.
type Config struct {
	ServerBaseURL   string
	Environment     string // production or test; release channel is separate
	ReleaseChannel  string // latest or alpha
	DaemonID        string
	LegacyDaemonIDs []string // historical daemon_ids this machine may have registered under; reported at register time so the server can merge old runtime rows
	DeviceName      string
	// MachineID is the OS-level persistent machine fingerprint (e.g.
	// /etc/machine-id, IOPlatformUUID, MachineGuid). Unlike DaemonID it is
	// independent of ~/.multica, so it survives an identity rebuild and is the
	// authoritative same-machine proof for server-side convergence (LRM-1570).
	// Empty when the platform could not derive one.
	MachineID      string
	RuntimeName    string
	CLIVersion     string                // multica CLI version (e.g. "0.1.13")
	LaunchedBy     string                // "desktop" when spawned by the Electron app, empty for standalone
	Profile        string                // profile name (empty = default)
	WorkspaceID    string                // the one workspace this daemon registers for
	BindingsRoot   string                // machine-wide Computer Binding store; empty keeps legacy single-workspace test/config behavior
	Agents         map[string]AgentEntry // keyed by provider: claude, codex, opencode, pi, cursor, kiro, grok
	WorkspacesRoot string                // base path containing workspace directories (default: ~/.multica/workspaces)
	// BindingStateRoot isolates durable workspace-execution coordinator state
	// for one WorkspaceDaemon. Empty keeps the historical single-process paths.
	BindingStateRoot string
	HealthPort       int // local HTTP port for health checks (default: 19514)
	// LocalControlToken authenticates owner-only loopback mutation requests.
	// It is generated by the CLI launcher in the per-profile private directory,
	// never from an environment variable or the unauthenticated health surface.
	LocalControlToken             string
	AgentWorkspaceQuotaBytes      int64         // per-agent cap on <workspace-id>/agents/<agent-id> total size, checked at turn-start; 0 = unlimited (default)
	SharedSkillsDir               string        // optional global override; when empty each provider uses its own shared root
	SharedSkillsSyncInterval      time.Duration // how often to scan and sync SharedSkillsDir
	MemoryCurationL3ReviewEnabled bool          // run the local Pi L3 reviewer during daemon-side curation
	MemoryCurationL3ReviewTimeout time.Duration // per-agent L3 reviewer timeout
	MemoryCurationRunTimeout      time.Duration // wall-clock timeout for one daemon-claimed curation run
	// ProblemEvolutionEvolverPath is the external evolution program the daemon
	// launches for problem-evolution runs. It is daemon-owned configuration on
	// purpose: the server must not be able to name an arbitrary executable.
	// Empty disables the capability on this machine.
	ProblemEvolutionEvolverPath string
	// ProblemEvolutionEvolverArgs are extra leading arguments passed before
	// --input / --workdir (e.g. a python module invocation).
	ProblemEvolutionEvolverArgs []string
	// ProblemEvolutionBatchTimeout bounds one evolver invocation.
	ProblemEvolutionBatchTimeout time.Duration
	// ProblemEvolutionGracefulDrain is the SIGTERM → SIGKILL window.
	ProblemEvolutionGracefulDrain time.Duration
	// ProblemEvolutionClaimInterval throttles claim polling.
	ProblemEvolutionClaimInterval time.Duration
	// MemoryType selects the memory reviewer pipeline: "legacy" (default)
	// or "graph" (design §1 memory_type switch). Any other value is a
	// configuration error and fails LoadConfig.
	MemoryType string
	// GraphEmbed* configure the OpenAI-compatible embedding endpoint used by
	// the hybrid retriever's vector channel (design §5.2). Empty BaseURL/Model
	// silently disables embeddings and retrieval runs BM25-only.
	GraphEmbedBaseURL string
	GraphEmbedAPIKey  string
	GraphEmbedModel   string
	// GraphExploreAgents is the TTT K parallel explore trajectories (1 =
	// non-TTT single trajectory, design Q17).
	GraphExploreAgents int
	// GraphExploreMaxRounds is the exploration-round budget per trajectory
	// (design Q15).
	GraphExploreMaxRounds int
	// GraphRewardTimeoutSeconds is the delayed-reward sweep timeout (design
	// Q28): pending explore traces whose judge result never arrives are
	// resolved with the miss penalty after this long.
	GraphRewardTimeoutSeconds int
	PollInterval              time.Duration
	HeartbeatInterval         time.Duration
	// InboundWatchdog is the daemon-ws silence threshold for probe→terminate
	// reconnect (default 70s). 0 disables. Override: MULTICA_DAEMON_INBOUND_WATCHDOG.
	InboundWatchdog                time.Duration
	AgentTimeout                   time.Duration
	CodexSemanticInactivityTimeout time.Duration
	RuntimeProgressStale           time.Duration // resident Message stalled threshold (0 = disabled)
	ClaudeArgs                     []string
	CodexArgs                      []string
}

// Overrides allows CLI flags to override environment variables and defaults.
// Zero values are ignored and the env/default value is used instead.
type Overrides struct {
	ServerURL         string
	WorkspacesRoot    string
	PollInterval      time.Duration
	HeartbeatInterval time.Duration
	// AgentTimeout is a pointer so an explicit `--agent-timeout 0` (no cap) is
	// distinguishable from "flag not passed". nil = use env/default.
	AgentTimeout                   *time.Duration
	CodexSemanticInactivityTimeout time.Duration
	DaemonID                       string
	DeviceName                     string
	RuntimeName                    string
	Profile                        string // profile name (empty = default)
	HealthPort                     int    // health check port (0 = use default)
}

// LoadConfig builds the daemon configuration from environment variables
// and optional CLI flag overrides.
func LoadConfig(overrides Overrides) (Config, error) {
	// Server URL: override > env > default
	rawServerURL := envOrDefault("MULTICA_SERVER_URL", DefaultServerURL)
	if overrides.ServerURL != "" {
		rawServerURL = overrides.ServerURL
	}
	serverBaseURL, err := NormalizeServerBaseURL(rawServerURL)
	if err != nil {
		return Config{}, err
	}

	// CLI config is optional: a missing or malformed ~/.multica/config.json
	// must not prevent daemon startup. Proxy and workspace_id come from it
	// when present; everything else still resolves from env / defaults.
	workspaceID := ""
	releaseChannel := string(cli.ReleaseChannelLatest)
	if cliCfg, err := cli.LoadCLIConfigForProfile(overrides.Profile); err != nil {
		slog.Warn("could not load CLI config for daemon overrides; proceeding without",
			"profile", overrides.Profile, "err", err)
		applyProxyConfig(nil)
	} else {
		applyProxyConfig(cliCfg.Proxy)
		workspaceID = strings.TrimSpace(cliCfg.WorkspaceID)
		releaseChannel = string(cli.ReleaseChannelForEnvironment(cli.ServiceEnvironment(cliCfg.Environment)))
	}

	// Probe available agent CLIs. exec.LookPath is the primary path, but on
	// macOS/Linux a GUI-launched daemon (Electron, Launchpad) does not
	// inherit the user's interactive shell PATH — fnm/nvm/volta multishells,
	// the Anthropic native installer prefix, and per-user npm prefixes all
	// live in dirs that only get added to PATH by ~/.zshrc or ~/.bashrc.
	// shellResolvedAgents asks the user's login shell, lazily on first miss,
	// to resolve every standard agent name to its canonical absolute path,
	// so we can find binaries the bare daemon process can't see. See
	// resolveAgentsViaLoginShell for the details and constraints.
	//
	// Laziness matters: the happy path (every agent on the daemon's PATH or
	// pinned to an explicit MULTICA_*_PATH) must not pay the cost of
	// spawning the user's login shell — that touches their rc files and
	// adds startup latency that scales with whatever they put in there. We
	// only fork a shell when a bare command name actually missed LookPath.
	var (
		shellResolveOnce sync.Once
		shellResolved    map[string]string
	)
	getShellResolved := func() map[string]string {
		shellResolveOnce.Do(func() {
			shellResolved = resolveAgentsViaLoginShell(defaultAgentCommandNames)
		})
		return shellResolved
	}
	probe := func(envVar, defaultCmd, modelEnv string) (AgentEntry, bool) {
		cmd := envOrDefault(envVar, defaultCmd)
		if _, err := exec.LookPath(cmd); err == nil {
			return AgentEntry{
				Path:  cmd,
				Model: strings.TrimSpace(os.Getenv(modelEnv)),
			}, true
		}
		// The shell fallback only rescues bare command names. An operator
		// who pinned MULTICA_*_PATH to an absolute or relative path that
		// doesn't exist should hard-miss, not silently get a different
		// binary.
		if strings.ContainsAny(cmd, "/\\") {
			return AgentEntry{}, false
		}
		if path, ok := getShellResolved()[cmd]; ok {
			return AgentEntry{
				Path:  path,
				Model: strings.TrimSpace(os.Getenv(modelEnv)),
			}, true
		}
		if defaultCmd == agent.ProviderCodex && cmd == defaultCmd {
			// Codex Desktop bundles its CLI inside the macOS app instead of
			// installing it onto PATH.
			for _, p := range codexDesktopAppBundlePaths() {
				if _, err := os.Stat(p); err == nil {
					return AgentEntry{
						Path:  p,
						Model: strings.TrimSpace(os.Getenv(modelEnv)),
					}, true
				}
			}
		}
		return AgentEntry{}, false
	}

	agents := map[string]AgentEntry{}
	if e, ok := probe("MULTICA_CLAUDE_PATH", agent.ProviderClaude, "MULTICA_CLAUDE_MODEL"); ok {
		agents[agent.ProviderClaude] = e
	}
	if e, ok := probe("MULTICA_CODEX_PATH", agent.ProviderCodex, "MULTICA_CODEX_MODEL"); ok {
		agents[agent.ProviderCodex] = e
	}
	if e, ok := probe("MULTICA_OPENCODE_PATH", agent.ProviderOpenCode, "MULTICA_OPENCODE_MODEL"); ok {
		agents[agent.ProviderOpenCode] = e
	}
	if e, ok := probe("MULTICA_PI_PATH", agent.ProviderPi, "MULTICA_PI_MODEL"); ok {
		agents[agent.ProviderPi] = e
	}
	if e, ok := probe("MULTICA_CURSOR_PATH", "agent", "MULTICA_CURSOR_MODEL"); ok {
		agents[agent.ProviderCursor] = e
	} else if e, ok := probe("MULTICA_CURSOR_PATH", "cursor-agent", "MULTICA_CURSOR_MODEL"); ok {
		agents[agent.ProviderCursor] = e
	}
	if e, ok := probe("MULTICA_KIRO_PATH", "kiro-cli", "MULTICA_KIRO_MODEL"); ok {
		agents[agent.ProviderKiro] = e
	}
	if e, ok := probe("MULTICA_GROK_PATH", agent.ProviderGrok, "MULTICA_GROK_MODEL"); ok {
		agents[agent.ProviderGrok] = e
	}
	// Zero detected agent CLIs is a valid Computer. Setup and WorkspaceDaemon
	// connectivity are proven by the Workspace connection, not by runtime count.

	claudeArgs, err := shellArgsFromEnv("MULTICA_CLAUDE_ARGS")
	if err != nil {
		return Config{}, err
	}
	codexArgs, err := shellArgsFromEnv("MULTICA_CODEX_ARGS")
	if err != nil {
		return Config{}, err
	}
	// Machine hostname
	host, err := os.Hostname()
	if err != nil || strings.TrimSpace(host) == "" {
		host = "local-machine"
	}

	// Durations: override > env > default
	pollInterval, err := durationFromEnv("MULTICA_DAEMON_POLL_INTERVAL", DefaultPollInterval)
	if err != nil {
		return Config{}, err
	}
	if overrides.PollInterval > 0 {
		pollInterval = overrides.PollInterval
	}

	heartbeatInterval, err := durationFromEnv("MULTICA_DAEMON_HEARTBEAT_INTERVAL", DefaultHeartbeatInterval)
	if err != nil {
		return Config{}, err
	}
	if overrides.HeartbeatInterval > 0 {
		heartbeatInterval = overrides.HeartbeatInterval
	}

	// MULTICA_DAEMON_INBOUND_WATCHDOG=0 disables the WS inbound probe/terminate
	// path; any positive duration overrides DefaultInboundWatchdog (70s).
	inboundWatchdog, err := durationFromEnv("MULTICA_DAEMON_INBOUND_WATCHDOG", DefaultInboundWatchdog)
	if err != nil {
		return Config{}, err
	}

	agentTimeout, err := durationFromEnv("MULTICA_AGENT_TIMEOUT", DefaultAgentTimeout)
	if err != nil {
		return Config{}, err
	}
	if overrides.AgentTimeout != nil {
		agentTimeout = *overrides.AgentTimeout
	}

	codexSemanticInactivityTimeout, err := durationFromEnv("MULTICA_CODEX_SEMANTIC_INACTIVITY_TIMEOUT", DefaultCodexSemanticInactivityTimeout)
	if err != nil {
		return Config{}, err
	}
	if overrides.CodexSemanticInactivityTimeout > 0 {
		codexSemanticInactivityTimeout = overrides.CodexSemanticInactivityTimeout
	}

	// MULTICA_RUNTIME_PROGRESS_STALE=0 disables the resident Message stalled watchdog. We
	// route 0 through durationFromEnv so the operator can opt out without
	// patching the binary; any positive duration overrides DefaultRuntimeProgressStale.
	runtimeProgressStale, err := durationFromEnv("MULTICA_RUNTIME_PROGRESS_STALE", DefaultRuntimeProgressStale)
	if err != nil {
		return Config{}, err
	}

	// Profile
	profile := overrides.Profile

	// daemon_id resolution: override > env > persistent UUID on disk.
	// The persistent UUID is written once to `<profile-dir>/daemon.id` and
	// then reused forever so hostname drift (.local suffix, system rename,
	// mDNS state, profile switch) no longer mints a new runtime identity.
	// Callers may still pin a specific id via MULTICA_DAEMON_ID or the
	// override field (e.g. for tests, sandbox create, or embedded environments).
	daemonID := strings.TrimSpace(os.Getenv("MULTICA_DAEMON_ID"))
	if overrides.DaemonID != "" {
		daemonID = overrides.DaemonID
	}
	pinnedDaemonID := daemonID != ""
	if daemonID == "" {
		persisted, err := EnsureDaemonID(profile)
		if err != nil {
			return Config{}, fmt.Errorf("ensure daemon id: %w", err)
		}
		daemonID = persisted
	}
	// Historical daemon_ids derived from the current hostname/profile. The
	// server uses these at register time to merge any pre-UUID runtime rows
	// for this machine into the new UUID-keyed row and delete the stale ones.
	legacyDaemonIDs := LegacyDaemonIDs(host, profile)
	// Pre-change (#1220) daemon identity was stored per profile, which means
	// the same machine could end up with multiple leftover daemon.id files
	// — e.g. ~/.multica/daemon.id (default) plus ~/.multica/profiles/<x>/
	// daemon.id. Surface those UUIDs so the server can merge their runtime
	// rows into the canonical machine UUID. Fatal-free: a broken profiles
	// dir shouldn't block startup.
	//
	// Skip when the caller pinned MULTICA_DAEMON_ID / --daemon-id: sandbox
	// snapshot templates freeze foreign profile daemon.id files from the
	// source instance, and merging those UUIDs would steal the source
	// sandbox's agent_runtime row.
	if !pinnedDaemonID {
		if uuids, err := LegacyDaemonUUIDs(); err == nil {
			legacyDaemonIDs = append(legacyDaemonIDs, uuids...)
		}
	}
	// Strip anything that collides with the resolved daemon_id (e.g. when
	// the user explicitly pins MULTICA_DAEMON_ID=<hostname>, or when the
	// canonical id was itself promoted from a pre-change profile file).
	legacyDaemonIDs = filterLegacyIDs(legacyDaemonIDs, daemonID)

	deviceName := envOrDefault("MULTICA_DAEMON_DEVICE_NAME", host)
	if overrides.DeviceName != "" {
		deviceName = overrides.DeviceName
	}

	// Machine fingerprint is an OS attribute, never user-provided: it must
	// survive identity rebuilds and cannot be spoofed via env to fake a
	// different machine in convergence decisions.
	machineID := computer.MachineID()

	runtimeName := envOrDefault("MULTICA_AGENT_RUNTIME_NAME", DefaultRuntimeName)
	if overrides.RuntimeName != "" {
		runtimeName = overrides.RuntimeName
	}

	// Workspaces root: override > env > default (~/.multica/workspaces).
	workspacesRoot, err := ResolveWorkspacesRoot(overrides.WorkspacesRoot)
	if err != nil {
		return Config{}, err
	}

	// Health port: override > default
	healthPort := DefaultHealthPort
	if overrides.HealthPort > 0 {
		healthPort = overrides.HealthPort
	}

	agentWorkspaceQuotaBytes := DefaultAgentWorkspaceQuotaBytes
	if v := os.Getenv("MULTICA_AGENT_WORKSPACE_QUOTA_BYTES"); v != "" {
		parsed, parseErr := strconv.ParseInt(v, 10, 64)
		if parseErr != nil || parsed < 0 {
			return Config{}, fmt.Errorf("invalid MULTICA_AGENT_WORKSPACE_QUOTA_BYTES %q: must be a non-negative integer (0 = unlimited)", v)
		}
		agentWorkspaceQuotaBytes = parsed
	}

	// Empty means "resolve per provider" in shared_skills.go (pi → ~/.pi/share/skills).
	sharedSkillsDir := strings.TrimSpace(os.Getenv("MULTICA_SHARED_SKILLS_DIR"))
	sharedSkillsInterval, err := durationFromEnv("MULTICA_SHARED_SKILLS_SYNC_INTERVAL", DefaultSharedSkillsSyncInterval)
	if err != nil {
		return Config{}, err
	}
	memoryCurationL3ReviewEnabled := true
	if v := strings.TrimSpace(os.Getenv("MULTICA_DAEMON_MEMORY_CURATION_L3_REVIEW_ENABLED")); v != "" {
		switch strings.ToLower(v) {
		case "false", "0", "no", "off":
			memoryCurationL3ReviewEnabled = false
		case "true", "1", "yes", "on":
			memoryCurationL3ReviewEnabled = true
		default:
			return Config{}, fmt.Errorf("MULTICA_DAEMON_MEMORY_CURATION_L3_REVIEW_ENABLED: invalid boolean %q", v)
		}
	}
	memoryCurationL3ReviewTimeout := DefaultMemoryCurationL3ReviewTimeout
	if v := strings.TrimSpace(os.Getenv("MEMORY_CURATION_L3_REVIEW_TIMEOUT_SECONDS")); v != "" {
		seconds, parseErr := strconv.Atoi(v)
		if parseErr != nil || seconds <= 0 {
			return Config{}, fmt.Errorf("MEMORY_CURATION_L3_REVIEW_TIMEOUT_SECONDS: invalid positive seconds %q", v)
		}
		memoryCurationL3ReviewTimeout = time.Duration(seconds) * time.Second
	}
	memoryCurationRunTimeout, err := durationFromEnv("MULTICA_DAEMON_MEMORY_CURATION_RUN_TIMEOUT", DefaultMemoryCurationRunTimeout)
	if err != nil {
		return Config{}, err
	}
	if memoryCurationRunTimeout <= 0 {
		return Config{}, fmt.Errorf("MULTICA_DAEMON_MEMORY_CURATION_RUN_TIMEOUT: must be positive")
	}
	problemEvolutionBatchTimeout, err := durationFromEnv("MULTICA_DAEMON_PROBLEM_EVOLUTION_BATCH_TIMEOUT", DefaultProblemEvolutionBatchTimeout)
	if err != nil {
		return Config{}, err
	}
	if problemEvolutionBatchTimeout <= 0 {
		return Config{}, fmt.Errorf("MULTICA_DAEMON_PROBLEM_EVOLUTION_BATCH_TIMEOUT: must be positive")
	}
	problemEvolutionGracefulDrain, err := durationFromEnv("MULTICA_DAEMON_PROBLEM_EVOLUTION_GRACEFUL_DRAIN", DefaultProblemEvolutionGracefulDrain)
	if err != nil {
		return Config{}, err
	}
	if problemEvolutionGracefulDrain <= 0 {
		return Config{}, fmt.Errorf("MULTICA_DAEMON_PROBLEM_EVOLUTION_GRACEFUL_DRAIN: must be positive")
	}
	problemEvolutionClaimInterval, err := durationFromEnv("MULTICA_DAEMON_PROBLEM_EVOLUTION_CLAIM_INTERVAL", DefaultProblemEvolutionClaimInterval)
	if err != nil {
		return Config{}, err
	}
	if problemEvolutionClaimInterval <= 0 {
		return Config{}, fmt.Errorf("MULTICA_DAEMON_PROBLEM_EVOLUTION_CLAIM_INTERVAL: must be positive")
	}
	// Graph memory reviewer (design §1/§6). memory_type fails loud on any
	// value outside legacy|graph: a typo must not silently pin the daemon to
	// the wrong memory pipeline.
	memoryType := strings.ToLower(strings.TrimSpace(os.Getenv("MULTICA_MEMORY_TYPE")))
	if memoryType == "" {
		memoryType = DefaultMemoryType
	}
	switch memoryType {
	case MemoryTypeLegacy, MemoryTypeGraph:
	default:
		return Config{}, fmt.Errorf("MULTICA_MEMORY_TYPE: invalid memory type %q (want %q or %q)", memoryType, MemoryTypeLegacy, MemoryTypeGraph)
	}
	graphExploreAgents, err := positiveIntFromEnv("MULTICA_GRAPH_EXPLORE_AGENTS", DefaultGraphExploreAgents)
	if err != nil {
		return Config{}, err
	}
	graphExploreMaxRounds, err := positiveIntFromEnv("MULTICA_GRAPH_EXPLORE_MAX_ROUNDS", DefaultGraphExploreMaxRounds)
	if err != nil {
		return Config{}, err
	}
	graphRewardTimeoutSeconds, err := positiveIntFromEnv("MULTICA_GRAPH_REWARD_TIMEOUT_SECONDS", DefaultGraphRewardTimeoutSeconds)
	if err != nil {
		return Config{}, err
	}

	return Config{
		ServerBaseURL:                  serverBaseURL,
		ReleaseChannel:                 releaseChannel,
		DaemonID:                       daemonID,
		LegacyDaemonIDs:                legacyDaemonIDs,
		DeviceName:                     deviceName,
		MachineID:                      machineID,
		RuntimeName:                    runtimeName,
		Profile:                        profile,
		WorkspaceID:                    workspaceID,
		Agents:                         agents,
		WorkspacesRoot:                 workspacesRoot,
		AgentWorkspaceQuotaBytes:       agentWorkspaceQuotaBytes,
		SharedSkillsDir:                sharedSkillsDir,
		SharedSkillsSyncInterval:       sharedSkillsInterval,
		MemoryCurationL3ReviewEnabled:  memoryCurationL3ReviewEnabled,
		MemoryCurationL3ReviewTimeout:  memoryCurationL3ReviewTimeout,
		MemoryCurationRunTimeout:       memoryCurationRunTimeout,
		ProblemEvolutionEvolverPath:    strings.TrimSpace(os.Getenv("MULTICA_DAEMON_PROBLEM_EVOLUTION_EVOLVER")),
		ProblemEvolutionEvolverArgs:    splitEvolverArgs(os.Getenv("MULTICA_DAEMON_PROBLEM_EVOLUTION_EVOLVER_ARGS")),
		ProblemEvolutionBatchTimeout:   problemEvolutionBatchTimeout,
		ProblemEvolutionGracefulDrain:  problemEvolutionGracefulDrain,
		ProblemEvolutionClaimInterval:  problemEvolutionClaimInterval,
		MemoryType:                     memoryType,
		GraphEmbedBaseURL:              strings.TrimSpace(os.Getenv("MULTICA_GRAPH_EMBED_BASE_URL")),
		GraphEmbedAPIKey:               strings.TrimSpace(os.Getenv("MULTICA_GRAPH_EMBED_API_KEY")),
		GraphEmbedModel:                strings.TrimSpace(os.Getenv("MULTICA_GRAPH_EMBED_MODEL")),
		GraphExploreAgents:             graphExploreAgents,
		GraphExploreMaxRounds:          graphExploreMaxRounds,
		GraphRewardTimeoutSeconds:      graphRewardTimeoutSeconds,
		HealthPort:                     healthPort,
		PollInterval:                   pollInterval,
		HeartbeatInterval:              heartbeatInterval,
		InboundWatchdog:                inboundWatchdog,
		AgentTimeout:                   agentTimeout,
		CodexSemanticInactivityTimeout: codexSemanticInactivityTimeout,
		RuntimeProgressStale:           runtimeProgressStale,
		ClaudeArgs:                     claudeArgs,
		CodexArgs:                      codexArgs,
	}, nil
}

// positiveIntFromEnv parses a positive-integer env var, returning def when
// the var is unset. Non-numeric or non-positive values are configuration
// errors (fail loud, same contract as the curation loaders above).
func positiveIntFromEnv(name string, def int) (int, error) {
	v := strings.TrimSpace(os.Getenv(name))
	if v == "" {
		return def, nil
	}
	parsed, err := strconv.Atoi(v)
	if err != nil || parsed <= 0 {
		return 0, fmt.Errorf("%s: invalid positive integer %q", name, v)
	}
	return parsed, nil
}

// officialCloudHost remains a small URL-normalization helper for compatibility
// tests and any caller that needs to identify the hosted API origin. It no
// longer selects an update policy: upgrades are explicit-only everywhere.
const officialCloudHost = cli.OfficialCloudAPIHost

// isOfficialCloudServer reports whether the resolved server base URL points
// at Multica's hosted cloud. Matching is host-only and case-insensitive — port
// and path are ignored.
func isOfficialCloudServer(baseURL string) bool {
	u, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil {
		return false
	}
	return cli.IsOfficialCloudHost(u.Hostname())
}

// NormalizeServerBaseURL converts a WebSocket or HTTP URL to a base HTTP URL.
func NormalizeServerBaseURL(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return "", fmt.Errorf("invalid MULTICA_SERVER_URL: %w", err)
	}
	switch u.Scheme {
	case "ws":
		u.Scheme = "http"
	case "wss":
		u.Scheme = "https"
	case "http", "https":
	default:
		return "", fmt.Errorf("MULTICA_SERVER_URL must use ws, wss, http, or https")
	}
	if u.Path == "/ws" {
		u.Path = ""
	}
	u.RawPath = ""
	u.RawQuery = ""
	u.Fragment = ""
	return cli.CanonicalizeOfficialCloudAPIURL(strings.TrimRight(u.String(), "/")), nil
}

// ResolveWorkspacesRoot returns the absolute path that the daemon and CLI
// should treat as the workspaces root. Resolution order: explicit override >
// MULTICA_WORKSPACES_ROOT env > default ($HOME/.multica/workspaces).
func ResolveWorkspacesRoot(override string) (string, error) {
	root := strings.TrimSpace(os.Getenv("MULTICA_WORKSPACES_ROOT"))
	if override != "" {
		root = override
	}
	if root == "" {
		home, err := userHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve home directory: %w (set MULTICA_WORKSPACES_ROOT to override)", err)
		}
		root = agentworkspace.DefaultWorkspacesRoot(home)
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("resolve absolute workspaces root: %w", err)
	}
	return abs, nil
}

func shellArgsFromEnv(name string) ([]string, error) {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return nil, nil
	}
	args, err := shellwords.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("invalid %s: %w", name, err)
	}
	return args, nil
}

// defaultAgentCommandNames lists the command names the agent probe loop tries
// before any MULTICA_*_PATH override is applied. Kept in sync with the
// `probe(...)` calls in LoadConfig — the shell-fallback resolver uses this
// list to pre-fetch canonical paths for every known agent in a single shell
// invocation, instead of paying the cost-per-miss.
var defaultAgentCommandNames = []string{
	"claude", "codex", "opencode", "pi", "agent", "cursor-agent", "kiro-cli", "grok",
}

var codexDesktopAppBundlePaths = func() []string {
	paths := []string{
		"/Applications/Codex.app/Contents/Resources/codex",
	}
	if home, err := userHomeDir(); err == nil {
		paths = append(paths, filepath.Join(home, "Applications", "Codex.app", "Contents", "Resources", "codex"))
	}
	return paths
}

// loginShellResolveTimeout caps how long the daemon will wait for the user's
// login shell to print canonical agent paths. A broken rc file should not
// block startup — if the shell takes longer than this, we proceed without
// shell-resolved fallbacks and the daemon falls back to the same behaviour
// it had before this code was added.
const loginShellResolveTimeout = 3 * time.Second

// loginShellResolveWaitDelay is the hard cap that runs *after*
// loginShellResolveTimeout has elapsed and `CommandContext` has signalled the
// shell to exit. The context kills the shell process itself, but rc files in
// the wild routinely background things that inherit stdout (`nvm` shims,
// `direnv hook`, `eval $(starship init)`, plain `&`). Those survivors keep
// the stdout pipe open and `cmd.Output()` will block on EOF for as long as
// they live. Cmd.WaitDelay (Go 1.20+) forcibly closes the pipes and returns
// once this delay elapses, so the total daemon-startup penalty caused by a
// pathological rc file is bounded by `timeout + waitDelay`, not by however
// long the user's background processes happen to run.
const loginShellResolveWaitDelay = 2 * time.Second

// supportedLoginShells limits which interpreters we will invoke via
// `<shell> -ilc <script>`. Sticking to POSIX-compatible shells means the
// resolver script below works unchanged. Notably absent: fish (uses
// `command -s` and a different syntax for command substitution).
var supportedLoginShells = map[string]struct{}{
	"bash": {},
	"zsh":  {},
	"sh":   {},
	"dash": {},
	"ksh":  {},
}

// resolveAgentsViaLoginShell asks the user's login shell to print the canonical
// (symlink-resolved) absolute path to each name in `names`. It returns a map
// of name → path for whatever the shell could find, and an empty map if the
// shell is unavailable / unsupported / times out / produces no usable output.
//
// Why we need this:
//
// Daemon-style processes on macOS/Linux do not inherit the user's interactive
// PATH. `claude --version` working in Terminal.app is no guarantee that
// exec.LookPath("claude") will work from a binary spawned by Launchpad, the
// Electron app, or `launchctl`. The most common offenders are fnm/nvm/volta
// "multishell" prefix dirs (per-shell, ephemeral) and the Anthropic native
// installer (`~/.claude/local/`) — both leave their binaries on a path that
// only `.zshrc` knows about.
//
// Implementation notes:
//
//   - We invoke `$SHELL -ilc <script>` with both -i (interactive) and -l
//     (login) so we pick up PATH set in either ~/.zshrc / ~/.bashrc OR
//     ~/.zprofile / ~/.bash_profile. Real users put it in both places.
//   - The script resolves symlinks via `cd "$dirname" && pwd -P` while the
//     spawned shell is still alive. fnm/nvm "multishell" directories vanish
//     on shell exit, so the canonical path must be captured before stdout is
//     returned to Go — by then the original path is already gone.
//   - We only trust outputs that look like an absolute path AND still pass a
//     fresh exec.LookPath check from the daemon's vantage point. That filters
//     out aliases (`command -v` prints the alias definition for those, not a
//     path) and per-shell paths the shell happened not to fully canonicalise.
//   - Agent names are restricted to the bare set in defaultAgentCommandNames
//     (`[A-Za-z0-9._-]` only); we inline them into the script unquoted to
//     keep the script readable. Custom MULTICA_*_PATH values never reach this
//     resolver — those go through exec.LookPath directly.
func resolveAgentsViaLoginShell(names []string) map[string]string {
	out := map[string]string{}
	if len(names) == 0 {
		return out
	}
	shell := strings.TrimSpace(os.Getenv("SHELL"))
	if shell == "" {
		return out
	}
	if _, ok := supportedLoginShells[filepath.Base(shell)]; !ok {
		return out
	}

	safe := make([]string, 0, len(names))
	for _, n := range names {
		if isSafeAgentName(n) {
			safe = append(safe, n)
		}
	}
	if len(safe) == 0 {
		return out
	}

	ctx, cancel := context.WithTimeout(context.Background(), loginShellResolveTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, shell, "-ilc", buildLoginShellResolveScript(safe))
	cmd.WaitDelay = loginShellResolveWaitDelay
	raw, err := cmd.Output()
	if err != nil {
		return out
	}

	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		parts := strings.SplitN(line, "\t", 2)
		if len(parts) != 2 {
			continue
		}
		name, path := parts[0], strings.TrimSpace(parts[1])
		if !filepath.IsAbs(path) {
			continue
		}
		// Final reality check: the path the shell gave us must still be
		// executable from the daemon's perspective right now. fnm
		// multishells are the motivating example — pwd -P inside the
		// helper shell can fail to break out of the per-session bin dir,
		// and we'd rather report "not found" than hand back a path that
		// vanishes between detection and execution.
		if _, err := exec.LookPath(path); err != nil {
			continue
		}
		out[name] = path
	}
	return out
}

// buildLoginShellResolveScript returns the shell script that resolveAgentsViaLoginShell
// runs inside `$SHELL -ilc`. The script:
//
//  1. iterates the provided command names,
//  2. strips any locally-defined alias and shell function with that name so
//     `command -v` reaches through to a real binary on PATH (see below),
//  3. uses POSIX `command -v` to find each one on the interactive PATH,
//  4. rejects results that are not absolute paths (defence in depth — if the
//     unalias/unset -f pair somehow didn't take effect, `command -v` would
//     still print the alias/function definition, and we'd rather drop it
//     than hand back garbage),
//  5. canonicalises the directory via `cd ... && pwd -P` so symlinked prefix
//     dirs (fnm/nvm/volta) collapse to stable paths,
//  6. prints `<name>\t<canonical_path>` one entry per line for the caller.
//
// Why steps 2 is important — and why this PR's first revision missed #2512:
// the motivating case has `alias claude=...` in ~/.zshrc *and* fnm's real
// claude binary further down on PATH. With `-i` set, the alias loads, and
// `command -v claude` returns `claude: aliased to ...` (zsh) or `alias
// claude='...'` (bash) — neither starts with `/`, so step 4 drops them, and
// the loop never looks at PATH again. Unaliasing inside the same shell makes
// `command -v` fall back to the PATH search the daemon actually wants.
// Shell functions exhibit the same shadowing in bash/zsh, hence `unset -f`.
// Both calls are wrapped in `2>/dev/null` so the harmless "no such alias"
// error never reaches stderr.
//
// All input names are vetted by isSafeAgentName before they reach this
// function, so inlining them unquoted into the for-loop word list is safe.
func buildLoginShellResolveScript(names []string) string {
	var b strings.Builder
	b.WriteString("for n in")
	for _, n := range names {
		b.WriteByte(' ')
		b.WriteString(n)
	}
	b.WriteString("; do\n")
	b.WriteString("  unalias \"$n\" 2>/dev/null\n")
	b.WriteString("  unset -f \"$n\" 2>/dev/null\n")
	b.WriteString("  p=$(command -v \"$n\" 2>/dev/null) || continue\n")
	b.WriteString("  [ -n \"$p\" ] || continue\n")
	b.WriteString("  case \"$p\" in /*) ;; *) continue ;; esac\n")
	b.WriteString("  d=$(dirname \"$p\") && f=$(basename \"$p\") && c=$(cd \"$d\" 2>/dev/null && pwd -P) || continue\n")
	b.WriteString("  printf '%s\\t%s\\n' \"$n\" \"$c/$f\"\n")
	b.WriteString("done\n")
	return b.String()
}

// isSafeAgentName checks that `s` is a bare command name composed only of
// characters that are safe to inline into a shell script (ASCII letters,
// digits, dot, dash, underscore). The agent names this daemon ships with all
// satisfy the predicate; it exists to guard against future drift, not to
// constrain operator-supplied paths (those never reach the shell resolver).
func isSafeAgentName(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case r == '-' || r == '_' || r == '.':
		default:
			return false
		}
	}
	return true
}

// applyProxyConfig canonicalizes the standard proxy environment once, before
// the daemon creates HTTP/WebSocket clients or launches child processes.
//
// Existing environment values win over config-file values. Uppercase wins
// when both cases are present, matching net/http's standard precedence. The
// selected value is exported in both cases because downstream tools differ in
// which spelling they honor. NO_PROXY is a union rather than a precedence
// choice: Raft preserves both inherited variants and always adds
// 127.0.0.1,localhost so local runtime control traffic never leaves the host.
func applyProxyConfig(proxy *cli.ProxyConfig) {
	httpProxy := firstNonEmptyEnv("HTTP_PROXY", "http_proxy")
	if httpProxy == "" && proxy != nil {
		httpProxy = strings.TrimSpace(proxy.HTTP)
	}
	setProxyEnvPair("HTTP_PROXY", "http_proxy", httpProxy)

	httpsProxy := firstNonEmptyEnv("HTTPS_PROXY", "https_proxy")
	if httpsProxy == "" && proxy != nil {
		httpsProxy = strings.TrimSpace(proxy.HTTPS)
	}
	setProxyEnvPair("HTTPS_PROXY", "https_proxy", httpsProxy)

	noProxyValues := []string{
		raftLoopbackNoProxy,
		os.Getenv("NO_PROXY"),
		os.Getenv("no_proxy"),
	}
	if proxy != nil {
		noProxyValues = append(noProxyValues, proxy.NoProxy)
	}
	noProxy := mergeNoProxy(noProxyValues...)
	_ = os.Setenv("NO_PROXY", noProxy)
	_ = os.Setenv("no_proxy", noProxy)
}

func firstNonEmptyEnv(names ...string) string {
	for _, name := range names {
		if value := strings.TrimSpace(os.Getenv(name)); value != "" {
			return value
		}
	}
	return ""
}

func setProxyEnvPair(upper, lower, value string) {
	if value == "" {
		return
	}
	_ = os.Setenv(upper, value)
	_ = os.Setenv(lower, value)
}

func mergeNoProxy(values ...string) string {
	seen := make(map[string]struct{})
	var merged []string
	for _, value := range values {
		for _, entry := range strings.Split(value, ",") {
			entry = strings.TrimSpace(entry)
			if entry == "" {
				continue
			}
			key := strings.ToLower(entry)
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			merged = append(merged, entry)
		}
	}
	return strings.Join(merged, ",")
}
