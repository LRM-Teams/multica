package agent

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// ErrClaudeACPTurnBusy is returned when a second Execute overlaps an in-flight
// resident ACP turn. The daemon must acquire the pool slot before claim.
var ErrClaudeACPTurnBusy = errors.New("claude ACP turn busy")

// ClaudeACPBackend is the lifecycle surface for a long-lived
// `claude-agent-acp` (Agent Client Protocol adapter) child. Close is mandatory
// on eviction, config mismatch, and failed turns.
//
// Product note (2026-08-03): Claude Code's main CLI has no `acp` subcommand.
// The official ACP entry is the Zed/ACP adapter package
// `@agentclientprotocol/claude-agent-acp` (bin: claude-agent-acp), which speaks
// stdio JSON-RPC ACP and uses the Claude Agent SDK under the hood.
type ClaudeACPBackend interface {
	Backend
	Close()
}

// claudeACPBackend keeps one claude-agent-acp process across compatible turns.
type claudeACPBackend struct {
	cfg Config

	process     atomic.Pointer[claudeACPProcess]
	running     atomic.Bool
	forceKilled atomic.Bool
	compact     compactionAttemptState
}

type claudeACPProcess struct {
	cmd        *exec.Cmd
	stdin      io.WriteCloser
	client     *acpClient
	readerDone chan struct{}
	stderrDone chan struct{}
	sessionID  string

	stateMu     sync.Mutex
	message     func(Message)
	disposeOnce sync.Once
}

func newClaudeACPBackend(cfg Config) *claudeACPBackend { return &claudeACPBackend{cfg: cfg} }

// NewClaudeACPBackend returns the resident Claude ACP backend for the canonical pool.
// Not registered in agent.New("claude") — one-shot stream-json remains the default
// for issue/one-shot paths until chat uses the canonical pool.
func NewClaudeACPBackend(cfg Config) ClaudeACPBackend { return newClaudeACPBackend(cfg) }

func (b *claudeACPBackend) Close() {
	if p := b.process.Load(); p != nil {
		b.disposeProcess(p)
	}
}

// ForceKill implements ResidentRuntimeForceKillable (task #62).
func (b *claudeACPBackend) ForceKill() error {
	p := b.process.Load()
	if p == nil {
		return nil
	}
	b.forceKilled.Store(true)
	_ = p.stdin.Close()
	return forceKillProcess(p.cmd.Process)
}

func (b *claudeACPBackend) Execute(ctx context.Context, prompt string, opts ExecOptions) (*Session, error) {
	if !b.running.CompareAndSwap(false, true) {
		return nil, fmt.Errorf("%w: concurrent claude ACP turn", ErrClaudeACPTurnBusy)
	}
	msgCh := make(chan Message, 256)
	resCh := make(chan Result, 1)
	go func() {
		var releaseOnce sync.Once
		releaseAdmission := func() {
			releaseOnce.Do(func() { b.running.Store(false) })
		}
		defer releaseAdmission()
		defer close(msgCh)
		defer close(resCh)
		started := time.Now()
		result := b.executeTurn(ctx, prompt, opts, msgCh)
		result.DurationMs = time.Since(started).Milliseconds()
		releaseAdmission()
		resCh <- result
	}()
	return &Session{Messages: msgCh, Result: resCh, RuntimeAlive: b.runtimeAlive}, nil
}

func (b *claudeACPBackend) RuntimeAlive() (bool, bool) {
	return b.runtimeAlive()
}

func (b *claudeACPBackend) EnsureResidentProcess(ctx context.Context) error {
	_, err := b.ensureProcess(ctx, b.cfg.ResidentOptions)
	return err
}

func (b *claudeACPBackend) runtimeAlive() (bool, bool) {
	p := b.process.Load()
	if p == nil {
		return false, false
	}
	return processAlive(p.cmd.Process)
}

func (b *claudeACPBackend) executeTurn(ctx context.Context, prompt string, opts ExecOptions, msgCh chan<- Message) Result {
	p, err := b.ensureProcess(ctx, opts)
	if err != nil {
		if b.forceKilled.CompareAndSwap(true, false) {
			return Result{Status: "failed", Error: AgentForceKilledMarker + ": " + err.Error()}
		}
		return Result{Status: "failed", Error: err.Error()}
	}

	var output strings.Builder
	p.stateMu.Lock()
	p.message = func(msg Message) {
		if msg.Type == MessageText {
			output.WriteString(msg.Content)
		}
		trySend(msgCh, msg)
	}
	p.stateMu.Unlock()

	p.client.resetToolCallFailure()
	var streaming atomic.Bool
	streaming.Store(true)
	p.client.acceptNotification = func(string) bool { return streaming.Load() }

	_, err = p.client.request(ctx, "session/prompt", map[string]any{
		"sessionId": p.sessionID,
		"prompt":    []map[string]any{{"type": "text", "text": prompt}},
	})
	streaming.Store(false)
	p.stateMu.Lock()
	p.message = nil
	p.stateMu.Unlock()

	if toolFailure := p.client.takeToolCallFailure(); toolFailure != nil {
		b.disposeProcess(p)
		return Result{
			Status:    "failed",
			Output:    output.String(),
			Error:     toolFailure.Error(),
			SessionID: p.sessionID,
		}
	}
	if err != nil {
		sessionID := p.sessionID
		if opts.ResumeSessionID != "" && isACPSessionNotFound(err) {
			b.cfg.Logger.Warn("resumed claude ACP session not found at prompt; clearing session id for fresh retry",
				"session_id", sessionID,
			)
			sessionID = ""
		}
		b.disposeProcess(p)
		if b.forceKilled.CompareAndSwap(true, false) {
			return Result{Status: "failed", Error: AgentForceKilledMarker + ": " + err.Error(), SessionID: sessionID}
		}
		status := "failed"
		if ctx.Err() == context.DeadlineExceeded {
			status = "timeout"
		} else if ctx.Err() == context.Canceled {
			status = "aborted"
		}
		return Result{Status: status, Error: fmt.Sprintf("claude ACP session/prompt: %v", err), SessionID: sessionID}
	}
	if strings.TrimSpace(output.String()) != "" {
		b.maybeCompactAfterTurn(p, msgCh)
	}
	return Result{Status: "completed", Output: output.String(), SessionID: p.sessionID}
}

func (b *claudeACPBackend) maybeCompactAfterTurn(p *claudeACPProcess, msgCh chan<- Message) {
	if p == nil || !shouldProactivelyCompactAt(p.client.currentRuntimeStats(), &b.compact) {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), postTurnCompactionTimeout)
	defer cancel()
	trySend(msgCh, Message{Type: MessageCompactionStarted})
	err := b.compactRuntime(ctx, p)
	b.compact.recordAttempt(err != nil, p.client.currentRuntimeStats())
	if err != nil {
		if b.cfg.Logger != nil {
			b.cfg.Logger.Warn("post-turn runtime context compaction failed; leaving session as-is", "provider", "claude", "error", err)
		}
		return
	}
	trySend(msgCh, Message{Type: MessageCompactionFinished})
}

func (b *claudeACPBackend) compactRuntime(ctx context.Context, p *claudeACPProcess) error {
	p.client.acceptNotification = func(string) bool { return true }
	defer func() { p.client.acceptNotification = nil }()
	_, err := p.client.request(ctx, "session/prompt", map[string]any{
		"sessionId": p.sessionID,
		"prompt": []map[string]any{{
			"type": "text",
			"text": "/compact " + proactiveContextCompactionInstructions,
		}},
	})
	if err != nil {
		return fmt.Errorf("claude /compact: %w", err)
	}
	return nil
}

func (b *claudeACPBackend) ensureProcess(ctx context.Context, opts ExecOptions) (*claudeACPProcess, error) {
	if p := b.process.Load(); p != nil {
		if alive, known := processAlive(p.cmd.Process); known && alive {
			return p, nil
		}
		b.disposeProcess(p)
	}

	execPath, err := resolveClaudeACPExecutable(b.cfg)
	if err != nil {
		return nil, err
	}

	// Adapter is the entrypoint (stdio ACP). Do not pass "acp" — that is a
	// kiro-cli/cursor-agent subcommand shape, not claude-agent-acp.
	cmd := exec.Command(execPath)
	hideAgentWindow(cmd)
	if opts.Cwd != "" {
		cmd.Dir = opts.Cwd
	}
	cmd.Env = buildEnv(b.cfg.Env)
	// Prefer subscription over ambient API key when both exist (adapter docs).
	cmd.Env = stripEnvKeys(cmd.Env, "ANTHROPIC_API_KEY")

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("claude ACP stdout: %w", err)
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("claude ACP stdin: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("claude ACP stderr: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start claude ACP: %w", err)
	}

	p := &claudeACPProcess{cmd: cmd, stdin: stdin, readerDone: make(chan struct{}), stderrDone: make(chan struct{})}
	p.client = &acpClient{
		cfg:          b.cfg,
		stdin:        stdin,
		pending:      make(map[int]*pendingRPC),
		pendingTools: make(map[string]*pendingToolCall),
		onMessage: func(msg Message) {
			p.stateMu.Lock()
			defer p.stateMu.Unlock()
			if p.message != nil {
				p.message(msg)
			}
		},
	}
	b.process.Store(p)
	b.cfg.Logger.Info("claude ACP resident started", "pid", cmd.Process.Pid, "cwd", opts.Cwd, "exec", execPath)

	go func() {
		defer close(p.readerDone)
		scanner := bufio.NewScanner(stdout)
		scanner.Buffer(make([]byte, 0, 1024*1024), 10*1024*1024)
		for scanner.Scan() {
			if line := strings.TrimSpace(scanner.Text()); line != "" {
				p.client.handleLine(line)
			}
		}
		p.client.closeAllPending(fmt.Errorf("claude ACP process exited"))
	}()
	go func() {
		defer close(p.stderrDone)
		_, _ = io.Copy(newLogWriter(b.cfg.Logger, "[claude-acp:stderr] "), stderr)
	}()

	_, err = p.client.request(ctx, "initialize", map[string]any{
		"protocolVersion": 1,
		"clientInfo":      map[string]any{"name": "multica-agent-sdk", "version": "0.2.0"},
		"clientCapabilities": map[string]any{
			"fs": map[string]any{"readTextFile": false, "writeTextFile": false},
		},
	})
	if err != nil {
		b.disposeProcess(p)
		return nil, fmt.Errorf("claude ACP initialize: %w", err)
	}

	mcp, err := buildACPMcpServers(opts.McpConfig, b.cfg.Logger)
	if err != nil {
		b.disposeProcess(p)
		return nil, fmt.Errorf("claude ACP invalid mcp_config: %w", err)
	}

	cwd := opts.Cwd
	if cwd == "" {
		cwd = "."
	}

	var created []byte
	if opts.ResumeSessionID != "" {
		created, err = p.client.request(ctx, "session/load", map[string]any{
			"cwd":        cwd,
			"sessionId":  opts.ResumeSessionID,
			"mcpServers": mcp,
		})
		if err != nil {
			b.disposeProcess(p)
			return nil, fmt.Errorf("claude ACP session/load: %w", err)
		}
		p.sessionID, _ = resolveResumedSessionID(opts.ResumeSessionID, created)
	} else {
		created, err = p.client.request(ctx, "session/new", map[string]any{
			"cwd":        cwd,
			"mcpServers": mcp,
		})
		if err != nil {
			b.disposeProcess(p)
			return nil, fmt.Errorf("claude ACP session/new: %w", err)
		}
		p.sessionID = extractACPSessionID(created)
	}
	if p.sessionID == "" {
		b.disposeProcess(p)
		return nil, fmt.Errorf("claude ACP session returned no session ID")
	}
	p.client.sessionID = p.sessionID

	// Best-effort model switch when the adapter supports it.
	if opts.Model != "" {
		if _, err := p.client.request(ctx, "session/set_model", map[string]any{
			"sessionId": p.sessionID,
			"modelId":   opts.Model,
		}); err != nil {
			b.cfg.Logger.Warn("claude ACP session/set_model failed; continuing with adapter default",
				"model", opts.Model, "error", err)
		}
	}
	return p, nil
}

func (b *claudeACPBackend) disposeProcess(p *claudeACPProcess) {
	b.process.CompareAndSwap(p, nil)
	p.disposeOnce.Do(func() {
		_ = p.stdin.Close()
		if p.cmd.Process != nil {
			_ = forceKillProcess(p.cmd.Process)
		}
		_ = p.cmd.Wait()
	})
}

// resolveClaudeACPExecutable picks the ACP adapter binary. The main `claude`
// CLI is intentionally rejected — it has no ACP subcommand.
func resolveClaudeACPExecutable(cfg Config) (string, error) {
	candidates := make([]string, 0, 4)
	if p := strings.TrimSpace(cfg.Env["MULTICA_CLAUDE_ACP_EXECUTABLE"]); p != "" {
		candidates = append(candidates, p)
	}
	if p := strings.TrimSpace(cfg.Env["CLAUDE_AGENT_ACP"]); p != "" {
		candidates = append(candidates, p)
	}
	if p := strings.TrimSpace(cfg.ExecutablePath); p != "" && looksLikeClaudeACPBinary(p) {
		candidates = append(candidates, p)
	}
	candidates = append(candidates, "claude-agent-acp", "claude-code-acp")

	var tried []string
	for _, c := range candidates {
		if c == "" {
			continue
		}
		tried = append(tried, c)
		if path, err := exec.LookPath(c); err == nil {
			return path, nil
		}
		// Absolute/relative path that exists.
		if filepath.IsAbs(c) || strings.Contains(c, string(os.PathSeparator)) {
			if st, err := os.Stat(c); err == nil && !st.IsDir() {
				return c, nil
			}
		}
	}
	return "", fmt.Errorf("claude ACP adapter not found (tried %s); install @agentclientprotocol/claude-agent-acp (bin: claude-agent-acp) or set MULTICA_CLAUDE_ACP_EXECUTABLE — the main `claude` CLI has no acp subcommand", strings.Join(tried, ", "))
}

func looksLikeClaudeACPBinary(path string) bool {
	base := strings.ToLower(filepath.Base(path))
	return strings.Contains(base, "claude-agent-acp") ||
		strings.Contains(base, "claude-code-acp") ||
		base == "claude-agent-acp" ||
		base == "claude-code-acp"
}

func stripEnvKeys(env []string, keys ...string) []string {
	drop := make(map[string]struct{}, len(keys))
	for _, k := range keys {
		drop[k] = struct{}{}
	}
	out := make([]string, 0, len(env))
	for _, e := range env {
		if i := strings.IndexByte(e, '='); i > 0 {
			if _, ok := drop[e[:i]]; ok {
				continue
			}
		}
		out = append(out, e)
	}
	return out
}
