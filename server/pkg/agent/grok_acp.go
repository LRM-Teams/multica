package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

var ErrGrokACPTurnBusy = errors.New("grok ACP turn busy")

// GrokACPBackend is the lifecycle surface owned by the daemon's chat-session
// pool. Close is mandatory on eviction, configuration mismatch, and a failed
// turn; leaving a child alive after those boundaries would leak chat context.
type GrokACPBackend interface {
	Backend
	Close()
}

// grokACPBackend is the native continued-session transport used by the
// daemon's persistent Grok pool. Unlike grokBackend (-p), it keeps one
// `grok agent stdio` child alive across compatible turns. It deliberately
// rejects a concurrent Execute rather than queueing a task behind a child.
// The daemon must acquire the corresponding session slot before it claims.
type grokACPBackend struct {
	cfg Config

	mu      sync.Mutex
	process *grokACPProcess
	running atomic.Bool
	// forceKilled is set by ForceKill() (task #62); see cursorACPBackend's
	// field of the same name for the full explanation.
	forceKilled atomic.Bool
}

type grokACPProcess struct {
	cmd        *exec.Cmd
	stdin      io.WriteCloser
	client     *hermesClient
	readerDone chan struct{}
	stderrDone chan struct{}
	sessionID  string

	stateMu sync.Mutex
	message func(Message)
}

func newGrokACPBackend(cfg Config) *grokACPBackend { return &grokACPBackend{cfg: cfg} }

func NewGrokACPBackend(cfg Config) GrokACPBackend { return newGrokACPBackend(cfg) }

func (b *grokACPBackend) Close() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.process != nil {
		b.disposeProcessLocked(b.process)
	}
}

// ForceKill implements agent.ResidentRuntimeForceKillable (task #62). Same
// shape and same reason as cursorACPBackend.ForceKill: must not call
// disposeProcessLocked (or cmd.Wait() at all) while a turn may still be
// reading this process's stdio — see that function's doc comment for the
// full explanation. Execute()'s own goroutine remains the sole reader/reaper.
func (b *grokACPBackend) ForceKill() error {
	b.mu.Lock()
	p := b.process
	b.mu.Unlock()
	if p == nil {
		return nil
	}
	b.forceKilled.Store(true)
	_ = p.stdin.Close()
	return p.cmd.Process.Kill()
}

func (b *grokACPBackend) Execute(ctx context.Context, prompt string, opts ExecOptions) (*Session, error) {
	if !b.running.CompareAndSwap(false, true) {
		return nil, fmt.Errorf("%w: concurrent grok ACP turn", ErrGrokACPTurnBusy)
	}
	msgCh := make(chan Message, 256)
	resCh := make(chan Result, 1)
	go func() {
		defer b.running.Store(false)
		defer close(msgCh)
		defer close(resCh)
		started := time.Now()
		result := b.executeTurn(ctx, prompt, opts, msgCh)
		result.DurationMs = time.Since(started).Milliseconds()
		resCh <- result
	}()
	return &Session{Messages: msgCh, Result: resCh, RuntimeAlive: b.runtimeAlive}, nil
}

// RuntimeAlive implements ResidentRuntimeLivenessChecker, letting a caller
// poll process liveness between turns, not just during an in-flight one.
func (b *grokACPBackend) RuntimeAlive() (bool, bool) {
	return b.runtimeAlive()
}

func (b *grokACPBackend) runtimeAlive() (bool, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.process == nil {
		return false, false
	}
	return processAlive(b.process.cmd.Process)
}

func (b *grokACPBackend) executeTurn(ctx context.Context, prompt string, opts ExecOptions, msgCh chan<- Message) Result {
	p, err := b.ensureProcess(ctx, opts)
	if err != nil {
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
	_, err = p.client.request(ctx, "session/prompt", map[string]any{
		"sessionId": p.sessionID,
		"prompt":    []map[string]any{{"type": "text", "text": prompt}},
	})
	p.stateMu.Lock()
	p.message = nil
	p.stateMu.Unlock()
	if toolFailure := p.client.takeToolCallFailure(); toolFailure != nil {
		// Grok may return a successful session/prompt response after a failed
		// tool frame. The native turn is not reusable: preserve the provider's
		// original error and force both the child and the daemon pool slot out.
		b.disposeProcess(p)
		return Result{
			Status:    "failed",
			Output:    output.String(),
			Error:     toolFailure.Error(),
			SessionID: p.sessionID,
		}
	}
	if err != nil {
		// A cancelled/failed request leaves the native turn state unknown. Do
		// not reuse it; the persistent-pool lease will replace this backend.
		b.disposeProcess(p)
		if b.forceKilled.CompareAndSwap(true, false) {
			return Result{Status: "failed", Error: AgentForceKilledMarker + ": " + err.Error()}
		}
		status := "failed"
		if ctx.Err() == context.DeadlineExceeded {
			status = "timeout"
		} else if ctx.Err() == context.Canceled {
			status = "aborted"
		}
		return Result{Status: status, Error: fmt.Sprintf("grok ACP session/prompt: %v", err)}
	}
	return Result{Status: "completed", Output: output.String(), SessionID: p.sessionID}
}

func (b *grokACPBackend) ensureProcess(ctx context.Context, opts ExecOptions) (*grokACPProcess, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.process != nil {
		return b.process, nil
	}
	execPath := b.cfg.ExecutablePath
	if execPath == "" {
		execPath = "grok"
	}
	if _, err := exec.LookPath(execPath); err != nil {
		return nil, fmt.Errorf("grok executable not found at %q: %w", execPath, err)
	}
	grokHome, err := ensureGrokRuntimeHome(b.cfg.Env, b.cfg.Logger)
	if err != nil {
		return nil, fmt.Errorf("grok runtime home: %w", err)
	}
	if err := validateGrokAuth(grokHome, b.cfg.Env); err != nil {
		return nil, err
	}

	// Keep the daemon-owned ACP process isolated from Grok's optional shared
	// leader and suppress interactive permission requests. A shared leader can
	// retain an older tool/permission schema across daemon sessions, while this
	// backend owns the complete lifecycle of its child.
	cmd := exec.Command(execPath, "agent", "--no-leader", "--always-approve", "stdio")
	hideAgentWindow(cmd)
	if opts.Cwd != "" {
		cmd.Dir = opts.Cwd
	}
	cmd.Env = buildGrokEnv(b.cfg.Env, grokHome)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("grok ACP stdout: %w", err)
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("grok ACP stdin: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("grok ACP stderr: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start grok ACP: %w", err)
	}
	p := &grokACPProcess{cmd: cmd, stdin: stdin, readerDone: make(chan struct{}), stderrDone: make(chan struct{})}
	p.client = &hermesClient{
		cfg: b.cfg, stdin: stdin, pending: make(map[int]*pendingRPC), pendingTools: make(map[string]*pendingToolCall),
		onMessage: func(msg Message) {
			p.stateMu.Lock()
			defer p.stateMu.Unlock()
			if p.message != nil {
				p.message(msg)
			}
		},
	}
	go func() {
		defer close(p.readerDone)
		scanner := bufio.NewScanner(stdout)
		scanner.Buffer(make([]byte, 0, 1024*1024), 10*1024*1024)
		for scanner.Scan() {
			if line := strings.TrimSpace(scanner.Text()); line != "" {
				p.client.handleLine(line)
			}
		}
		p.client.closeAllPending(fmt.Errorf("grok ACP process exited"))
	}()
	go func() {
		defer close(p.stderrDone)
		_, _ = io.Copy(newLogWriter(b.cfg.Logger, "[grok-acp:stderr] "), stderr)
	}()

	init, err := p.client.request(ctx, "initialize", map[string]any{
		"protocolVersion":    1,
		"clientInfo":         map[string]any{"name": "multica-agent-sdk", "version": "0.2.0"},
		"clientCapabilities": map[string]any{},
	})
	if err != nil {
		b.disposeProcessLocked(p)
		return nil, fmt.Errorf("grok ACP initialize: %w", err)
	}
	mcp, err := buildACPMcpServers(opts.McpConfig, b.cfg.Logger)
	if err != nil {
		b.disposeProcessLocked(p)
		return nil, fmt.Errorf("grok ACP invalid mcp_config: %w", err)
	}
	mcp = filterACPMcpServersByCapability(mcp, extractACPMcpCapabilities(init), "grok", b.cfg.Logger)
	cwd := opts.Cwd
	if cwd == "" {
		cwd = "."
	}
	var created json.RawMessage
	if opts.ResumeSessionID != "" {
		created, err = p.client.request(ctx, "session/load", map[string]any{"cwd": cwd, "sessionId": opts.ResumeSessionID, "mcpServers": mcp})
		if err != nil {
			b.disposeProcessLocked(p)
			return nil, fmt.Errorf("grok ACP session/load: %w", err)
		}
		p.sessionID, _ = resolveResumedSessionID(opts.ResumeSessionID, created)
	} else {
		created, err = p.client.request(ctx, "session/new", map[string]any{"cwd": cwd, "mcpServers": mcp})
		if err != nil {
			b.disposeProcessLocked(p)
			return nil, fmt.Errorf("grok ACP session/new: %w", err)
		}
		p.sessionID = extractACPSessionID(created)
	}
	if p.sessionID == "" {
		b.disposeProcessLocked(p)
		return nil, fmt.Errorf("grok ACP session/new returned no session ID")
	}
	if opts.Model != "" {
		if _, err := p.client.request(ctx, "session/set_model", map[string]any{"sessionId": p.sessionID, "modelId": opts.Model}); err != nil {
			b.disposeProcessLocked(p)
			return nil, fmt.Errorf("grok ACP set model: %w", err)
		}
	}
	b.process = p
	return p, nil
}

func (b *grokACPBackend) disposeProcess(p *grokACPProcess) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.disposeProcessLocked(p)
}

func (b *grokACPBackend) disposeProcessLocked(p *grokACPProcess) {
	if b.process == p {
		b.process = nil
	}
	_ = p.stdin.Close()
	_ = p.cmd.Process.Kill()
	_ = p.cmd.Wait()
}
