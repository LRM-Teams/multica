// Package agent provides a unified interface for executing prompts via
// coding agents (Claude Code, CodeBuddy, Codex, Copilot, OpenCode, OpenClaw,
// Hermes, Gemini, Pi, Cursor, Kimi, Kiro, Antigravity, Grok). It mirrors the happy-cli
// AgentBackend pattern, translated to idiomatic Go.
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"
)

// Backend is the unified interface for executing prompts via coding agents.
type Backend interface {
	// Execute runs a prompt and returns a Session for streaming results.
	// The caller should read from Session.Messages (optional) and wait on
	// Session.Result for the final outcome.
	Execute(ctx context.Context, prompt string, opts ExecOptions) (*Session, error)
}

// ProviderAuthRequiredMarker prefixes fail-closed auth preflight errors from
// provider CLIs. Daemon code may classify this as a terminal, user-visible
// provider-auth failure rather than retrying or launching an interactive login.
const ProviderAuthRequiredMarker = "provider_auth_required"

// AgentForceKilledMarker prefixes the Result.Error a resident backend
// produces when its in-flight turn was interrupted by ForceKill() (task
// #62), not by a genuine crash. Daemon code matches this the same way it
// matches ProviderAuthRequiredMarker (see reportTaskFailure) to set
// failure_reason="restarted_by_user" instead of running it through the
// generic taskfailure.Classify substring taxonomy — that taxonomy's 21
// categories are governed by an external SQL source of truth (MUL-1949) for
// genuine agent-side failures, and a deliberate user-initiated restart isn't
// one of those, so it must never fall into that classifier.
const AgentForceKilledMarker = "agent_force_killed_by_user"

// AuthPreflight is an optional contract for backends that can detect missing
// non-interactive credentials before spawning their provider CLI. Implementers
// must fail closed: return an error instead of entering the provider's
// interactive login flow.
type AuthPreflight interface {
	PreflightAuth(ctx context.Context) error
}

// ResidentRuntimeLivenessChecker is an optional contract for backends that
// keep a long-lived provider child process alive across turns (the canonical
// resident pool). Unlike Session.RuntimeAlive — which only exists while a
// turn is in flight — this can be polled at any time, including while the
// backend sits idle between turns, so a caller can detect a crashed resident
// process without waiting for the next dispatched task to fail against it
// (task #42). Implementers must fail open on an unknown answer: known=false
// is not proof the process died.
type ResidentRuntimeLivenessChecker interface {
	RuntimeAlive() (alive bool, known bool)
}

// ResidentRuntimeForceKillable is an optional contract for backends that keep
// a long-lived provider child process alive across turns (task #62). ForceKill
// terminates the underlying process immediately, even while a turn is
// in-flight against it. Implementers must be safe to call concurrently with
// an in-flight Execute() — the caller does not wait for Execute() to notice;
// Execute()'s own error handling is expected to observe the killed process
// (e.g. a pipe read failure) and release the turn itself, exactly as it
// already does for a genuine crash. ForceKill must NOT perform any step that
// only one goroutine may safely call (for example, exec.Cmd.Wait() while its
// stdout/stdin pipes may still be read/written by the in-flight Execute()
// goroutine) — it may only take the actions needed to make the process die;
// reaping/cleanup stays the responsibility of the goroutine that was already
// using the process.
type ResidentRuntimeForceKillable interface {
	ForceKill() error
}

// ResidentMessage is one canonical Message body handed to a resident runtime.
// It is agent-package owned so provider adapters do not depend on daemon wire
// envelopes. PartsJSON preserves structured Message parts without making the
// provider layer an alternate owner of their schema.
type ResidentMessage struct {
	ID        string
	Target    string
	Seq       int64
	Content   string
	PartsJSON json.RawMessage
}

// ResidentMessageInput is an optional capability for resident backends that
// can prove a concrete batch crossed their native input boundary while idle.
// Implementations must not report success merely because a goroutine or turn
// was scheduled; success means the provider-native input accepted the batch.
type ResidentMessageInput interface {
	AcceptMessageBatch(context.Context, []ResidentMessage) (ResidentMessageAcceptance, error)
}

// ResidentMessageAcceptance separates native input acceptance from the
// provider turn it may start. Done reports that the runtime is idle again;
// callers must keep turn admission closed until it resolves. Messages exposes
// optional provider-observed lifecycle events for Activity projection; it does
// not carry canonical Message bodies or alter Context Boundary semantics. A
// backend that supplies Messages must close it before resolving Done so a
// terminal idle/error observation cannot overtake buffered lifecycle events.
type ResidentMessageAcceptance struct {
	Done     <-chan error
	Messages <-chan Message
}

// ResidentPendingTarget is content-free metadata for one target represented by
// a Pending Notice. It deliberately carries no Message body, Parts, sender, or
// attachment data.
type ResidentPendingTarget struct {
	Target       string `json:"target"`
	PendingCount int    `json:"pending_count"`
}

// ResidentPendingNotice tells a busy runtime that concrete canonical Messages
// remain Pending without crossing their bodies into runtime context.
type ResidentPendingNotice struct {
	TotalPending   int                     `json:"total_pending"`
	ChangedTargets []ResidentPendingTarget `json:"changed_targets"`
}

// ResidentPendingNoticeInput is an optional resident-runtime capability. A nil
// error means the provider accepted the content-free Notice at a safe busy
// input boundary; it does not mean any Pending Message was consumed.
type ResidentPendingNoticeInput interface {
	AcceptPendingNotice(context.Context, ResidentPendingNotice) error
}

// ExecOptions configures a single execution.
type ExecOptions struct {
	Cwd   string
	Model string
	// SystemPrompt is consumed only by providers that can pass or safely inline
	// developer/system instructions. Hermes ACP intentionally ignores it and
	// relies on cwd-scoped context files such as AGENTS.md instead.
	SystemPrompt              string
	ThreadName                string
	MaxTurns                  int
	Timeout                   time.Duration
	SemanticInactivityTimeout time.Duration
	ResumeSessionID           string          // if non-empty, resume a previous agent session
	ExtraArgs                 []string        // daemon-wide default CLI arguments appended before CustomArgs; currently read by claude and codex backends only
	CustomArgs                []string        // per-agent CLI arguments appended after ExtraArgs
	McpConfig                 json.RawMessage // if non-nil, MCP server config to pass via --mcp-config
	// DisableTools requests a provider-enforced empty tool registry. Callers
	// must reject restricted profiles for backends that cannot enforce it;
	// silently running with tools would violate the profile boundary.
	DisableTools bool
	// EphemeralSession prevents the provider session transcript from becoming a
	// resumable runtime artifact. It is required for sidecar cognition runs.
	EphemeralSession bool
	// MaxOutputTokens caps the provider request output budget. Restricted
	// execution profiles set this explicitly and must use a backend that can
	// enforce it before the request is sent.
	MaxOutputTokens int
	// piOutputLimitExtension is populated internally by the Pi backend after it
	// creates the per-run control extension. Callers outside this package cannot
	// supply or override the trusted extension path.
	piOutputLimitExtension string
	// piMcpConfigPath is populated internally by the Pi backend after writing
	// agent.mcp_config to a temp file. Callers outside this package cannot
	// supply or override the path.
	piMcpConfigPath string
	// ThinkingLevel is the runtime-native reasoning/effort value (e.g.
	// Claude's "low|medium|high|xhigh|max", Codex's "none|minimal|low|
	// medium|high|xhigh", OpenCode's model variant names). Empty means
	// "use the runtime/model default" —
	// every backend that consumes this skips its --effort / reasoning_effort
	// injection so the upstream CLI's own default applies. Currently honoured
	// by the claude, codex, opencode, pi, and grok backends; other backends ignore the
	// field rather than fail (so MUL-2339 can grow runtime support
	// incrementally without breaking unrelated agents).
	ThinkingLevel string
	// TrustedExtensionPaths is a list of application-generated extension files
	// to load explicitly via --extension. Only accepted when DisableTools is
	// true (restricted execution profile). Each path must be an absolute,
	// regular file within TrustedExtensionRoot. Duplicates are silently
	// deduplicated. Relative paths, directories, and paths outside the root
	// are rejected at option-validation time.
	TrustedExtensionPaths []string
	// TrustedExtensionRoot is the directory that TrustedExtensionPaths must
	// reside within. Must be an absolute path. Ignored when
	// TrustedExtensionPaths is empty.
	TrustedExtensionRoot string
}

// runContext derives the execution context for an agent subprocess from the
// configured per-run timeout. A positive timeout imposes a hard wall-clock
// deadline; a zero (or negative) timeout imposes NO deadline, leaving liveness
// entirely to the daemon's inactivity watchdog so a session that keeps emitting
// events is never killed merely for running long (MUL-3064). The caller owns
// the returned CancelFunc and must call it to release resources.
func runContext(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if timeout > 0 {
		return context.WithTimeout(ctx, timeout)
	}
	return context.WithCancel(ctx)
}

// Session represents a running agent execution.
type Session struct {
	// Messages streams events as the agent works. The channel is closed
	// when the agent finishes (before Result is sent).
	Messages <-chan Message
	// Result receives exactly one value — the final outcome — then closes.
	Result <-chan Result
	// RuntimeAlive probes whether the provider child process that owns this
	// turn is still alive. It is optional for non-process backends. The daemon
	// uses it to distinguish a quiet-but-running provider from a dead runtime
	// before applying inactivity recovery.
	RuntimeAlive RuntimeLivenessProbe
}

// RuntimeLivenessProbe returns whether the runtime is alive and whether that
// answer is known. Unknown probe outcomes must fail open: they are not proof
// that a provider child died.
type RuntimeLivenessProbe func() (alive bool, known bool)

// MessageType identifies the kind of Message.
type MessageType string

const (
	MessageText       MessageType = "text"
	MessageThinking   MessageType = "thinking"
	MessageToolUse    MessageType = "tool-use"
	MessageToolResult MessageType = "tool-result"
	MessageStatus     MessageType = "status"
	MessageError      MessageType = "error"
	MessageLog        MessageType = "log"
	// MessageCompactionStarted and MessageCompactionFinished preserve the
	// provider's explicit context-compaction lifecycle. They are Activity
	// events, not generated assistant text.
	MessageCompactionStarted  MessageType = "compaction-started"
	MessageCompactionFinished MessageType = "compaction-finished"
)

// Message is a unified event emitted by an agent during execution.
type Message struct {
	Type      MessageType
	Content   string         // text content (Text, Error, Log)
	Tool      string         // tool name (ToolUse, ToolResult)
	CallID    string         // tool call ID (ToolUse, ToolResult)
	Input     map[string]any // tool input (ToolUse); also on ToolResult when completed backfills started-empty args (LRM-689)
	Output    string         // tool output (ToolResult)
	Status    string         // agent status string (Status)
	Level     string         // log level (Log)
	Lineage   string         // runtime subagent lineage (Thinking, Text)
	SessionID string         // backend session id (Status), for early resume-pointer pinning
}

// TokenUsage tracks token consumption for a single model.
type TokenUsage struct {
	InputTokens      int64
	OutputTokens     int64
	CacheReadTokens  int64
	CacheWriteTokens int64
}

// Result is the final outcome after an agent session completes.
type Result struct {
	Status       string // "completed", "failed", "aborted", "timeout", "cancelled"
	Output       string // accumulated text output
	Error        string // error message if failed
	DurationMs   int64
	SessionID    string
	Usage        map[string]TokenUsage // keyed by model name
	RuntimeStats *RuntimeTokenStats    // provider-native current-session telemetry when available
}

// RuntimeTokenStats is provider-native token/cost/context telemetry for the
// current persistent runtime session. Backends that cannot report context
// usage leave it nil.
type RuntimeTokenStats struct {
	Provider              string
	Model                 string
	InputTokens           int64
	OutputTokens          int64
	CacheReadTokens       int64
	CacheWriteTokens      int64
	TotalTokens           int64
	CostUSD               *float64
	ContextTokens         *int64
	ContextWindow         *int64
	ContextPercent        *float64
	AutoCompactionEnabled *bool
}

// Config configures a Backend instance.
type Config struct {
	ExecutablePath string            // path to CLI binary (claude, codebuddy, codex, copilot, opencode, openclaw, hermes, gemini, pi, cursor, kimi, kiro-cli, agy, grok)
	Env            map[string]string // extra environment variables
	Logger         *slog.Logger
	// ResidentOptions are the stable agent-scoped defaults used when a native
	// Message is the first input that starts a resident provider process. They
	// must not contain task, delivery, lease, or current-turn identity.
	ResidentOptions ExecOptions
}

// agentConstructors is the single source of truth for "what agent types does
// New() accept" — task #47's completeness check (TestProviderCapabilitiesCoverAllKnownTypes)
// reads this map's keys directly instead of a hand-maintained mirror list, so
// adding a case here without a matching providerCapabilities row fails that
// test instead of silently shipping a provider that fails closed on every
// capability. Do not reintroduce a parallel literal of these type strings.
var agentConstructors = map[string]func(Config) Backend{
	"claude":      func(cfg Config) Backend { return &claudeBackend{cfg: cfg} },
	"codebuddy":   func(cfg Config) Backend { return &codebuddyBackend{cfg: cfg} },
	"codex":       func(cfg Config) Backend { return &codexBackend{cfg: cfg} },
	"copilot":     func(cfg Config) Backend { return &copilotBackend{cfg: cfg} },
	"opencode":    func(cfg Config) Backend { return &opencodeBackend{cfg: cfg} },
	"openclaw":    func(cfg Config) Backend { return &openclawBackend{cfg: cfg} },
	"hermes":      func(cfg Config) Backend { return &hermesBackend{cfg: cfg} },
	"gemini":      func(cfg Config) Backend { return &geminiBackend{cfg: cfg} },
	"pi":          func(cfg Config) Backend { return &piBackend{cfg: cfg} },
	"cursor":      func(cfg Config) Backend { return &cursorBackend{cfg: cfg} },
	"kimi":        func(cfg Config) Backend { return &kimiBackend{cfg: cfg} },
	"kiro":        func(cfg Config) Backend { return &kiroBackend{cfg: cfg} },
	"antigravity": func(cfg Config) Backend { return &antigravityBackend{cfg: cfg} },
	"grok":        func(cfg Config) Backend { return &grokBackend{cfg: cfg} },
}

// KnownAgentTypes returns every agent type New() accepts (agentConstructors'
// keys). Order is unstable — sort in callers that need determinism.
func KnownAgentTypes() []string {
	out := make([]string, 0, len(agentConstructors))
	for name := range agentConstructors {
		out = append(out, name)
	}
	return out
}

// New creates a Backend for the given agent type.
// Supported types: see KnownAgentTypes().
func New(agentType string, cfg Config) (Backend, error) {
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}

	ctor, ok := agentConstructors[agentType]
	if !ok {
		return nil, fmt.Errorf("unknown agent type: %q (supported: claude, codebuddy, codex, copilot, opencode, openclaw, hermes, gemini, pi, cursor, kimi, kiro, antigravity, grok)", agentType)
	}
	return ctor(cfg), nil
}

// DetectVersion runs the agent CLI with --version and returns the output.
func DetectVersion(ctx context.Context, executablePath string) (string, error) {
	return detectCLIVersion(ctx, executablePath)
}

// launchHeaders maps each supported agent type to the user-visible skeleton
// that the daemon spawns before any custom_args are appended. This is
// intentionally minimal — only the command + subcommand (or a short mode
// label when there is no subcommand). Internal flags, transport values, and
// environment variables are deliberately omitted so the string is a hint
// about *what* users are extending, not a dump of the full command line.
var launchHeaders = map[string]string{
	"antigravity": "agy -p (print mode)",
	"claude":      "claude (stream-json)",
	"codebuddy":   "codebuddy (stream-json)",
	"codex":       "codex app-server",
	"copilot":     "copilot (json)",
	"cursor":      "cursor-agent (stream-json)",
	"gemini":      "gemini (stream-json)",
	"grok":        "grok (streaming-json)",
	"hermes":      "hermes acp",
	"kimi":        "kimi acp",
	"kiro":        "kiro-cli acp",
	"openclaw":    "openclaw agent (json)",
	"opencode":    "opencode run (json)",
	"pi":          "pi (json mode)",
}

// LaunchHeader returns the user-visible launch skeleton for agentType, or an
// empty string if the type is unknown. Callers render this as a preview so
// users understand which command their custom_args get appended to.
func LaunchHeader(agentType string) string {
	return launchHeaders[agentType]
}
