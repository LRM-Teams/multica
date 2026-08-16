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
	ResidentMessageInput
	ResidentPendingNoticeInput
	Close()
}

// grokACPBackend is the native continued-session transport used by the
// daemon's persistent Grok pool. Unlike grokBackend (-p), it keeps one
// `grok agent stdio` child alive across compatible turns. It deliberately
// rejects a concurrent Execute rather than queueing a task behind a child.
// The daemon must acquire the corresponding session slot before it claims.
type grokACPBackend struct {
	cfg Config

	// process is published atomically the instant cmd.Start() succeeds (task
	// #62 follow-up), before the initialize/session/new handshake begins —
	// see cursorACPBackend.process's doc comment for the full deadlock this
	// fixes and why it applies identically here.
	process atomic.Pointer[grokACPProcess]
	running atomic.Bool
	// forceKilled is set by ForceKill() (task #62); see cursorACPBackend's
	// field of the same name for the full explanation.
	forceKilled atomic.Bool
}

type grokACPProcess struct {
	cmd        *exec.Cmd
	stdin      io.WriteCloser
	client     *acpClient
	readerDone chan struct{}
	stderrDone chan struct{}
	sessionID  string

	stateMu sync.Mutex
	message func(Message)

	// disposeOnce guards the actual Kill+Wait teardown — see
	// cursorACPProcess.disposeOnce's doc comment for why this is needed once
	// the process is published before its handshake completes.
	disposeOnce sync.Once
}

func newGrokACPBackend(cfg Config) *grokACPBackend { return &grokACPBackend{cfg: cfg} }

func NewGrokACPBackend(cfg Config) GrokACPBackend { return newGrokACPBackend(cfg) }

func (b *grokACPBackend) Close() {
	if p := b.process.Load(); p != nil {
		b.disposeProcess(p)
	}
}

// ForceKill implements agent.ResidentRuntimeForceKillable (task #62). Same
// shape and same reason as cursorACPBackend.ForceKill: a plain atomic load,
// published before the handshake, so ForceKill never contends with anything
// a stuck initialize/session/new could be holding, and never itself calls
// cmd.Wait() while a turn may still be reading this process's stdio — see
// that function's doc comment for the full explanation. Execute()'s own
// goroutine (or ensureProcess's own handshake failure path) remains the
// sole caller of Wait(), via disposeProcess.
func (b *grokACPBackend) ForceKill() error {
	p := b.process.Load()
	if p == nil {
		return nil
	}
	b.forceKilled.Store(true)
	_ = p.stdin.Close()
	return forceKillProcess(p.cmd.Process)
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
		result := b.executeTurn(ctx, prompt, opts, msgCh, nil)
		result.DurationMs = time.Since(started).Milliseconds()
		resCh <- result
	}()
	return &Session{Messages: msgCh, Result: resCh, RuntimeAlive: b.runtimeAlive}, nil
}

// AcceptMessageBatch starts one Grok ACP turn for canonical Message bodies
// while the resident runtime is idle. Acceptance is the successful
// session/prompt write, not the end-of-turn response.
func (b *grokACPBackend) AcceptMessageBatch(ctx context.Context, messages []ResidentMessage) (ResidentMessageAcceptance, error) {
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

func (b *grokACPBackend) acceptIdleInputPrompt(ctx context.Context, prompt string) (ResidentMessageAcceptance, error) {
	if !b.running.CompareAndSwap(false, true) {
		return ResidentMessageAcceptance{}, fmt.Errorf("%w: idle input overlaps an active turn", ErrGrokACPTurnBusy)
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

// RuntimeAlive implements ResidentRuntimeLivenessChecker, letting a caller
// poll process liveness between turns, not just during an in-flight one.
func (b *grokACPBackend) EnsureResidentProcess(ctx context.Context) error {
	_, err := b.ensureProcess(ctx, b.cfg.ResidentOptions)
	return err
}

func (b *grokACPBackend) RuntimeAlive() (bool, bool) {
	return b.runtimeAlive()
}

// AcceptPendingNotice uses Grok's native interjection request. The response is
// the provider receipt for the content-free write; it does not consume any
// Pending Message body or advance a Context Boundary.
func (b *grokACPBackend) AcceptPendingNotice(ctx context.Context, notice ResidentPendingNotice) error {
	if !b.running.Load() {
		return errors.New("Grok Pending Notice requires an active turn")
	}
	p := b.process.Load()
	if p == nil || strings.TrimSpace(p.sessionID) == "" {
		return errors.New("Grok Pending Notice requires a live session")
	}
	prompt, err := formatResidentPendingNotice(notice)
	if err != nil {
		return err
	}
	_, err = p.client.request(ctx, "_x.ai/interject", map[string]any{
		"sessionId":      p.sessionID,
		"text":           prompt,
		"interjectionId": fmt.Sprintf("multica-%d", time.Now().UnixNano()),
	})
	if err != nil {
		return fmt.Errorf("Grok Pending Notice: %w", err)
	}
	return nil
}

func (b *grokACPBackend) runtimeAlive() (bool, bool) {
	p := b.process.Load()
	if p == nil {
		return false, false
	}
	return processAlive(p.cmd.Process)
}

func (b *grokACPBackend) executeTurn(ctx context.Context, prompt string, opts ExecOptions, msgCh chan<- Message, reportAcceptance func(error)) Result {
	p, err := b.ensureProcess(ctx, opts)
	if err != nil {
		if reportAcceptance != nil {
			reportAcceptance(err)
		}
		// ForceKill() can now interrupt a process stuck in ensureProcess's own
		// handshake (task #62 follow-up), not just a turn already past it —
		// check forceKilled here too so a user-initiated restart during
		// handshake is reported as such, not misclassified as a generic crash.
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
	promptID, promptDone, err := p.client.beginRequest("session/prompt", map[string]any{
		"sessionId": p.sessionID,
		"prompt":    []map[string]any{{"type": "text", "text": prompt}},
	})
	if reportAcceptance != nil {
		reportAcceptance(err)
	}
	if err != nil {
		p.stateMu.Lock()
		p.message = nil
		p.stateMu.Unlock()
		b.disposeProcess(p)
		return Result{Status: "failed", Error: fmt.Sprintf("grok ACP session/prompt: %v", err)}
	}
	_, err = p.client.awaitRequest(ctx, promptID, promptDone)
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
	if p := b.process.Load(); p != nil {
		return p, nil
	}
	// Execute() serializes turns via running.CompareAndSwap, so at most one
	// goroutine ever reaches this point at a time — no concurrent creator to
	// race against.
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
	p.client = &acpClient{
		cfg: b.cfg, stdin: stdin, pending: make(map[int]*pendingRPC), pendingTools: make(map[string]*pendingToolCall),
		onMessage: func(msg Message) {
			p.stateMu.Lock()
			defer p.stateMu.Unlock()
			if p.message != nil {
				p.message(msg)
			}
		},
	}
	// Publish before the handshake, not after — this is the fix. ForceKill()
	// can now find and kill this process even if initialize/session/new
	// below hangs forever.
	b.process.Store(p)
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
		b.disposeProcess(p)
		return nil, fmt.Errorf("grok ACP initialize: %w", err)
	}
	mcp, err := buildACPMcpServers(opts.McpConfig, b.cfg.Logger)
	if err != nil {
		b.disposeProcess(p)
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
		if err != nil && isACPSessionNotFound(err) {
			if b.cfg.Logger != nil {
				b.cfg.Logger.Warn("resumed grok ACP session not found; starting a fresh session",
					"session_id", opts.ResumeSessionID,
					"cwd", cwd,
					"error", err,
				)
			}
			created, err = p.client.request(ctx, "session/new", map[string]any{"cwd": cwd, "mcpServers": mcp})
			if err != nil {
				b.disposeProcess(p)
				return nil, fmt.Errorf("grok ACP session/new after stale session/load: %w", err)
			}
			p.sessionID = extractACPSessionID(created)
		} else if err != nil {
			b.disposeProcess(p)
			return nil, fmt.Errorf("grok ACP session/load: %w", err)
		} else {
			p.sessionID, _ = resolveResumedSessionID(opts.ResumeSessionID, created)
		}
	} else {
		created, err = p.client.request(ctx, "session/new", map[string]any{"cwd": cwd, "mcpServers": mcp})
		if err != nil {
			b.disposeProcess(p)
			return nil, fmt.Errorf("grok ACP session/new: %w", err)
		}
		p.sessionID = extractACPSessionID(created)
	}
	if p.sessionID == "" {
		b.disposeProcess(p)
		return nil, fmt.Errorf("grok ACP session/new returned no session ID")
	}
	if opts.Model != "" {
		if _, err := p.client.request(ctx, "session/set_model", map[string]any{"sessionId": p.sessionID, "modelId": opts.Model}); err != nil {
			b.disposeProcess(p)
			return nil, fmt.Errorf("grok ACP set model: %w", err)
		}
	}
	return p, nil
}

// disposeProcess tears down p and clears it from b.process if it is still
// the current one. Safe to call concurrently for the same p from multiple
// paths (Close(), a failed handshake in ensureProcess, a failed in-flight
// turn in executeTurn) — see cursorACPBackend.disposeProcess for the full
// rationale; the shape here is identical.
func (b *grokACPBackend) disposeProcess(p *grokACPProcess) {
	b.process.CompareAndSwap(p, nil)
	p.disposeOnce.Do(func() {
		_ = p.stdin.Close()
		_ = p.cmd.Process.Kill()
		_ = p.cmd.Wait()
	})
}
