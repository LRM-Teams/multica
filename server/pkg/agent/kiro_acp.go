package agent

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// ErrKiroACPTurnBusy is returned when a second Execute overlaps an in-flight
// resident ACP turn. The daemon must acquire the pool slot before claim.
var ErrKiroACPTurnBusy = errors.New("kiro ACP turn busy")

// KiroACPBackend is the lifecycle surface for a long-lived kiro-cli acp child.
// Close is mandatory on eviction, config mismatch, and failed turns.
type KiroACPBackend interface {
	Backend
	ResidentMessageInput
	Close()
}

// kiroACPBackend keeps one `kiro-cli acp` child across compatible turns.
// Session resume uses session/load (not session/resume) — Kiro's observed API.
// Live probe (s144, 2.16.0): one process can session/new twice + session/load
// while staying alive (Barry 2026-08-03).
type kiroACPBackend struct {
	cfg Config

	process     atomic.Pointer[kiroACPProcess]
	running     atomic.Bool
	forceKilled atomic.Bool
}

type kiroACPProcess struct {
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

func newKiroACPBackend(cfg Config) *kiroACPBackend { return &kiroACPBackend{cfg: cfg} }

// NewKiroACPBackend returns the resident Kiro ACP backend for the canonical pool.
func NewKiroACPBackend(cfg Config) KiroACPBackend { return newKiroACPBackend(cfg) }

func (b *kiroACPBackend) Close() {
	if p := b.process.Load(); p != nil {
		b.disposeProcess(p)
	}
}

// ForceKill implements ResidentRuntimeForceKillable (task #62).
func (b *kiroACPBackend) ForceKill() error {
	p := b.process.Load()
	if p == nil {
		return nil
	}
	b.forceKilled.Store(true)
	_ = p.stdin.Close()
	return forceKillProcess(p.cmd.Process)
}

func (b *kiroACPBackend) Execute(ctx context.Context, prompt string, opts ExecOptions) (*Session, error) {
	if !b.running.CompareAndSwap(false, true) {
		return nil, fmt.Errorf("%w: concurrent kiro ACP turn", ErrKiroACPTurnBusy)
	}
	msgCh := make(chan Message, 256)
	resCh := make(chan Result, 1)
	go func() {
		defer b.running.Store(false)
		defer close(msgCh)
		defer close(resCh)
		started := time.Now()
		result := b.executeTurn(ctx, prompt, opts, msgCh, nil)
		result.DurationMs = time.Since(started).Milliseconds()
		resCh <- result
	}()
	return &Session{Messages: msgCh, Result: resCh, RuntimeAlive: b.runtimeAlive}, nil
}

// AcceptMessageBatch starts one Kiro ACP turn for canonical Message bodies
// while the resident runtime is idle. Acceptance is reported only after the
// session/prompt request crosses the serialized ACP write boundary.
func (b *kiroACPBackend) AcceptMessageBatch(ctx context.Context, messages []ResidentMessage) (ResidentMessageAcceptance, error) {
	if len(messages) == 0 {
		done := make(chan error)
		close(done)
		return ResidentMessageAcceptance{Done: done}, nil
	}
	prompt, err := formatResidentMessageBatch(messages)
	if err != nil {
		return ResidentMessageAcceptance{}, err
	}
	return b.acceptIdleInputPrompt(ctx, prompt)
}

func (b *kiroACPBackend) acceptIdleInputPrompt(ctx context.Context, prompt string) (ResidentMessageAcceptance, error) {
	if !b.running.CompareAndSwap(false, true) {
		return ResidentMessageAcceptance{}, fmt.Errorf("%w: idle input overlaps an active turn", ErrKiroACPTurnBusy)
	}

	accepted := make(chan error, 1)
	done := make(chan error, 1)
	msgCh := make(chan Message, 256)
	turnCtx, cancelTurn := context.WithCancel(context.Background())
	go func() {
		defer cancelTurn()
		result := b.executeTurn(turnCtx, prompt, b.cfg.ResidentOptions, msgCh, func(err error) {
			accepted <- err
		})
		close(msgCh)
		b.running.Store(false)
		done <- errorForResidentTurn(result)
		close(done)
	}()

	select {
	case err := <-accepted:
		if err != nil {
			return ResidentMessageAcceptance{}, err
		}
		return ResidentMessageAcceptance{Done: done, Messages: msgCh}, nil
	case <-ctx.Done():
		select {
		case err := <-accepted:
			if err != nil {
				return ResidentMessageAcceptance{}, err
			}
			return ResidentMessageAcceptance{Done: done, Messages: msgCh}, nil
		default:
			cancelTurn()
			return ResidentMessageAcceptance{}, ctx.Err()
		}
	}
}

func (b *kiroACPBackend) RuntimeAlive() (bool, bool) {
	return b.runtimeAlive()
}

func (b *kiroACPBackend) EnsureResidentProcess(ctx context.Context) error {
	_, err := b.ensureProcess(ctx, b.cfg.ResidentOptions)
	return err
}

func (b *kiroACPBackend) runtimeAlive() (bool, bool) {
	p := b.process.Load()
	if p == nil {
		return false, false
	}
	return processAlive(p.cmd.Process)
}

func (b *kiroACPBackend) executeTurn(ctx context.Context, prompt string, opts ExecOptions, msgCh chan<- Message, reportAcceptance func(error)) Result {
	p, err := b.ensureProcess(ctx, opts)
	if err != nil {
		if reportAcceptance != nil {
			reportAcceptance(err)
		}
		if b.forceKilled.CompareAndSwap(true, false) {
			return Result{Status: "failed", Error: AgentForceKilledMarker + ": " + err.Error()}
		}
		return Result{Status: "failed", Error: err.Error()}
	}

	var output strings.Builder
	p.stateMu.Lock()
	p.message = func(msg Message) {
		if msg.Type == MessageToolUse {
			msg.Tool = kiroToolNameFromTitle(msg.Tool)
		}
		if msg.Type == MessageText {
			output.WriteString(msg.Content)
		}
		trySend(msgCh, msg)
	}
	p.stateMu.Unlock()

	p.client.resetToolCallFailure()
	// Gate streaming notifications to this turn only (same as one-shot kiro).
	var streaming atomic.Bool
	streaming.Store(true)
	p.client.acceptNotification = func(string) bool { return streaming.Load() }

	promptBlocks := []map[string]any{{"type": "text", "text": prompt}}
	promptID, promptDone, err := p.client.beginRequest("session/prompt", map[string]any{
		"sessionId": p.sessionID,
		"content":   promptBlocks,
		"prompt":    promptBlocks,
	})
	if reportAcceptance != nil {
		reportAcceptance(err)
	}
	if err == nil {
		_, err = p.client.awaitRequest(ctx, promptID, promptDone)
	}
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
		// Dead session on resume: clear id so daemon stale-session fallback can retry fresh.
		if opts.ResumeSessionID != "" && isACPSessionNotFound(err) {
			b.cfg.Logger.Warn("resumed kiro session not found at prompt; clearing session id for fresh retry",
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
		return Result{Status: status, Error: fmt.Sprintf("kiro ACP session/prompt: %v", err), SessionID: sessionID}
	}
	return Result{Status: "completed", Output: output.String(), SessionID: p.sessionID}
}

func (b *kiroACPBackend) ensureProcess(ctx context.Context, opts ExecOptions) (*kiroACPProcess, error) {
	if p := b.process.Load(); p != nil {
		return p, nil
	}
	execPath := b.cfg.ExecutablePath
	if execPath == "" {
		execPath = "kiro-cli"
	}
	if _, err := exec.LookPath(execPath); err != nil {
		return nil, fmt.Errorf("kiro executable not found at %q: %w", execPath, err)
	}

	kiroArgs := append([]string{"acp", "--trust-all-tools"}, filterCustomArgs(opts.CustomArgs, kiroBlockedArgs, b.cfg.Logger)...)
	cmd := exec.Command(execPath, kiroArgs...)
	hideAgentWindow(cmd)
	if opts.Cwd != "" {
		cmd.Dir = opts.Cwd
	}
	cmd.Env = buildEnv(b.cfg.Env)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("kiro ACP stdout: %w", err)
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("kiro ACP stdin: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("kiro ACP stderr: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start kiro ACP: %w", err)
	}

	p := &kiroACPProcess{cmd: cmd, stdin: stdin, readerDone: make(chan struct{}), stderrDone: make(chan struct{})}
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
	b.cfg.Logger.Info("kiro ACP resident started", "pid", cmd.Process.Pid, "cwd", opts.Cwd)

	go func() {
		defer close(p.readerDone)
		scanner := bufio.NewScanner(stdout)
		scanner.Buffer(make([]byte, 0, 1024*1024), 10*1024*1024)
		for scanner.Scan() {
			if line := strings.TrimSpace(scanner.Text()); line != "" {
				p.client.handleLine(line)
			}
		}
		p.client.closeAllPending(fmt.Errorf("kiro ACP process exited"))
	}()
	go func() {
		defer close(p.stderrDone)
		_, _ = io.Copy(newLogWriter(b.cfg.Logger, "[kiro-acp:stderr] "), stderr)
	}()

	initResult, err := p.client.request(ctx, "initialize", map[string]any{
		"protocolVersion":    1,
		"clientInfo":         map[string]any{"name": "multica-agent-sdk", "version": "0.2.0"},
		"clientCapabilities": map[string]any{},
	})
	if err != nil {
		b.disposeProcess(p)
		return nil, fmt.Errorf("kiro ACP initialize: %w", err)
	}

	mcp, err := buildACPMcpServers(opts.McpConfig, b.cfg.Logger)
	if err != nil {
		b.disposeProcess(p)
		return nil, fmt.Errorf("kiro ACP invalid mcp_config: %w", err)
	}
	mcp = filterACPMcpServersByCapability(mcp, extractACPMcpCapabilities(initResult), "kiro", b.cfg.Logger)

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
			return nil, fmt.Errorf("kiro ACP session/load: %w", err)
		}
		p.sessionID, _ = resolveResumedSessionID(opts.ResumeSessionID, created)
	} else {
		created, err = p.client.request(ctx, "session/new", map[string]any{
			"cwd":        cwd,
			"mcpServers": mcp,
		})
		if err != nil {
			b.disposeProcess(p)
			return nil, fmt.Errorf("kiro ACP session/new: %w", err)
		}
		p.sessionID = extractACPSessionID(created)
	}
	if p.sessionID == "" {
		b.disposeProcess(p)
		return nil, fmt.Errorf("kiro ACP session returned no session ID")
	}
	p.client.sessionID = p.sessionID

	if opts.Model != "" {
		if _, err := p.client.request(ctx, "session/set_model", map[string]any{
			"sessionId": p.sessionID,
			"modelId":   opts.Model,
		}); err != nil {
			// Match one-shot kiro: fail the turn; clear id if dead resume.
			if opts.ResumeSessionID != "" && isACPSessionNotFound(err) {
				b.disposeProcess(p)
				return nil, fmt.Errorf("kiro could not switch to model %q on dead session: %w", opts.Model, err)
			}
			b.disposeProcess(p)
			return nil, fmt.Errorf("kiro could not switch to model %q: %w", opts.Model, err)
		}
	}
	return p, nil
}

func (b *kiroACPBackend) disposeProcess(p *kiroACPProcess) {
	b.process.CompareAndSwap(p, nil)
	p.disposeOnce.Do(func() {
		_ = p.stdin.Close()
		_ = p.cmd.Process.Kill()
		_ = p.cmd.Wait()
	})
}
