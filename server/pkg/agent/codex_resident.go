package agent

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// ErrCodexResidentTurnBusy is returned when a second Execute overlaps an
// in-flight resident app-server turn. The daemon must acquire the pool slot
// before claim.
var ErrCodexResidentTurnBusy = errors.New("codex app-server turn busy")

// CodexAppServerBackend is the lifecycle surface for a long-lived
// `codex app-server --listen stdio://` child. Close is mandatory on eviction,
// config mismatch, and unhealthy release.
type CodexAppServerBackend interface {
	Backend
	ResidentMessageInput
	ResidentPendingNoticeInput
	Close()
}

// codexAppServerBackend keeps one app-server process across compatible chat
// turns. Protocol (verified against codex-cli 0.144+ one-shot path):
//
//	initialize → thread/start|thread/resume → turn/start (repeat) → Close
//
// Issue tasks continue to use the one-shot codexBackend via agent.New.
type codexAppServerBackend struct {
	cfg Config

	process     atomic.Pointer[codexAppServerProcess]
	running     atomic.Bool
	forceKilled atomic.Bool
}

type codexAppServerProcess struct {
	cmd        *exec.Cmd
	stdin      io.WriteCloser
	client     *codexClient
	readerDone chan struct{}
	stderrDone chan struct{}
	threadID   string
	execPath   string

	stateMu     sync.Mutex
	message     func(Message)
	disposeOnce sync.Once
}

func newCodexAppServerBackend(cfg Config) *codexAppServerBackend {
	return &codexAppServerBackend{cfg: cfg}
}

// NewCodexAppServerBackend returns the resident Codex app-server backend for
// the canonical agent×runtime pool. Not registered in agent.New("codex") —
// one-shot codexBackend remains the default for issue/one-shot paths.
func NewCodexAppServerBackend(cfg Config) CodexAppServerBackend {
	return newCodexAppServerBackend(cfg)
}

func (b *codexAppServerBackend) Close() {
	if p := b.process.Load(); p != nil {
		b.disposeProcess(p)
	}
}

// ForceKill implements ResidentRuntimeForceKillable (task #62).
func (b *codexAppServerBackend) ForceKill() error {
	p := b.process.Load()
	if p == nil {
		return nil
	}
	b.forceKilled.Store(true)
	_ = p.stdin.Close()
	return forceKillProcess(p.cmd.Process)
}

func (b *codexAppServerBackend) Execute(ctx context.Context, prompt string, opts ExecOptions) (*Session, error) {
	if !b.running.CompareAndSwap(false, true) {
		return nil, fmt.Errorf("%w: concurrent codex app-server turn", ErrCodexResidentTurnBusy)
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
		result := b.executeTurn(ctx, prompt, opts, msgCh, nil)
		result.DurationMs = time.Since(started).Milliseconds()
		// Release before publish so an immediate follow-up turn does not race
		// a still-true running flag (same ordering as cursor/pi residents).
		releaseAdmission()
		resCh <- result
	}()
	return &Session{Messages: msgCh, Result: resCh, RuntimeAlive: b.runtimeAlive}, nil
}

// AcceptMessageBatch starts one native Codex app-server turn for canonical
// Message bodies while the resident runtime is idle. The acceptance receipt is
// published only after turn/start succeeds; scheduling the goroutine alone is
// not a Context Boundary receipt.
func (b *codexAppServerBackend) AcceptMessageBatch(ctx context.Context, messages []ResidentMessage) (ResidentMessageAcceptance, error) {
	if len(messages) == 0 {
		done := make(chan error)
		close(done)
		return ResidentMessageAcceptance{Done: done}, nil
	}
	prompt, err := formatResidentMessageBatch(messages)
	if err != nil {
		return ResidentMessageAcceptance{}, err
	}
	if !b.running.CompareAndSwap(false, true) {
		return ResidentMessageAcceptance{}, fmt.Errorf("%w: canonical Message handoff overlaps an active turn", ErrCodexResidentTurnBusy)
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
		if result.Status == "completed" {
			done <- nil
		} else if result.Error != "" {
			done <- errors.New(result.Error)
		} else {
			done <- fmt.Errorf("Codex canonical Message turn ended with status %q", result.Status)
		}
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

func (b *codexAppServerBackend) RuntimeAlive() (bool, bool) {
	return b.runtimeAlive()
}

func (b *codexAppServerBackend) runtimeAlive() (bool, bool) {
	p := b.process.Load()
	if p == nil {
		return false, false
	}
	return processAlive(p.cmd.Process)
}

func (b *codexAppServerBackend) executeTurn(ctx context.Context, prompt string, opts ExecOptions, msgCh chan<- Message, reportAcceptance func(error)) Result {
	hadResidentProcess := b.process.Load() != nil
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
	if hadResidentProcess && shouldProactivelyCompact(p.client.currentRuntimeStats()) {
		if err := b.compactRuntime(ctx, p, msgCh); err != nil {
			b.cfg.Logger.Warn("proactive runtime context compaction failed; continuing turn", "provider", "codex", "error", err)
		}
	}

	var outputMu sync.Mutex
	var output strings.Builder
	p.stateMu.Lock()
	p.message = func(msg Message) {
		if msg.Type == MessageText {
			outputMu.Lock()
			output.WriteString(msg.Content)
			outputMu.Unlock()
		}
		trySend(msgCh, msg)
	}
	p.stateMu.Unlock()

	// Per-turn client state: the same process/client spans many turns.
	p.client.clearTurnError()
	p.client.usageMu.Lock()
	p.client.usage = TokenUsage{}
	p.client.usageMu.Unlock()
	p.client.setActiveTurn(false, "")
	p.client.completedTurnIDs = make(map[string]bool)

	semanticActivityCh := make(chan string, 256)
	turnDone := make(chan bool, 1)
	p.client.onSemanticActivity = func(description string) {
		b.cfg.Logger.Debug("codex semantic activity observed", "activity", description)
		trySendString(semanticActivityCh, description)
	}
	p.client.onTurnDone = func(aborted bool) {
		select {
		case turnDone <- aborted:
		default:
		}
	}
	p.client.onMessage = func(msg Message) {
		logCodexAgentMessage(b.cfg.Logger, msg)
		p.stateMu.Lock()
		cb := p.message
		p.stateMu.Unlock()
		if cb != nil {
			cb(msg)
		}
		trySendString(semanticActivityCh, describeCodexSemanticActivity(msg))
	}

	threadID := p.threadID
	turnParams := map[string]any{
		"threadId": threadID,
		"input": []map[string]any{
			{"type": "text", "text": prompt},
		},
	}
	applyCodexReasoningEffort(turnParams, opts.ThinkingLevel)
	_, err = p.client.request(ctx, "turn/start", turnParams)
	if err != nil {
		if reportAcceptance != nil {
			reportAcceptance(err)
		}
		p.stateMu.Lock()
		p.message = nil
		p.stateMu.Unlock()
		sessionID := threadID
		b.disposeProcess(p)
		if b.forceKilled.CompareAndSwap(true, false) {
			return Result{Status: "failed", Error: AgentForceKilledMarker + ": " + err.Error(), SessionID: sessionID}
		}
		return Result{
			Status:    "failed",
			Error:     fmt.Sprintf("codex turn/start failed: %v", err),
			SessionID: sessionID,
		}
	}
	if reportAcceptance != nil {
		reportAcceptance(nil)
	}

	timeout := opts.Timeout
	semanticInactivityTimeout := opts.SemanticInactivityTimeout
	if semanticInactivityTimeout == 0 {
		semanticInactivityTimeout = defaultCodexSemanticInactivityTimeout
	}
	runCtx := ctx
	var cancel context.CancelFunc
	if timeout > 0 {
		runCtx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	lastSemanticActivityDescription := "turn/start"
	semanticTimer := time.NewTimer(semanticInactivityTimeout)
	defer semanticTimer.Stop()

	firstTurnNoProgressTimeout := codexFirstTurnNoProgressTimeout(semanticInactivityTimeout)
	var firstTurnNoProgressTimer *time.Timer
	var firstTurnNoProgressTimerC <-chan time.Time
	firstTurnStarted := false
	firstTurnProgressObserved := false
	stopFirstTurnNoProgressTimer := func() {
		if firstTurnNoProgressTimer == nil {
			return
		}
		stopTimer(firstTurnNoProgressTimer)
		firstTurnNoProgressTimerC = nil
	}
	defer stopFirstTurnNoProgressTimer()

	finalStatus := "completed"
	var finalError string
	var timeoutDiagnostic codexTimeoutDiagnostic
	waitingForTurn := true
	for waitingForTurn {
		select {
		case aborted := <-turnDone:
			waitingForTurn = false
			switch {
			case aborted:
				finalStatus = "aborted"
				finalError = "turn was aborted"
			default:
				if errMsg := p.client.getTurnError(); errMsg != "" {
					finalStatus = "failed"
					finalError = errMsg
				}
			}
		case activity := <-semanticActivityCh:
			lastSemanticActivityDescription = activity
			if activity == "status:running" && !firstTurnStarted {
				firstTurnStarted = true
				firstTurnNoProgressTimer = time.NewTimer(firstTurnNoProgressTimeout)
				firstTurnNoProgressTimerC = firstTurnNoProgressTimer.C
			} else if firstTurnStarted && !firstTurnProgressObserved && isCodexFirstTurnProgressActivity(activity) {
				firstTurnProgressObserved = true
				stopFirstTurnNoProgressTimer()
			}
			resetTimer(semanticTimer, semanticInactivityTimeout)
		case <-firstTurnNoProgressTimerC:
			waitingForTurn = false
			finalStatus = "timeout"
			timeoutDiagnostic = codexTimeoutDiagnostic{
				Kind:         codexTimeoutFirstTurnNoProgress,
				Timeout:      firstTurnNoProgressTimeout,
				LastActivity: lastSemanticActivityDescription,
				ThreadID:     threadID,
				TurnID:       p.client.activeTurnID(),
				Model:        opts.Model,
			}
			b.cfg.Logger.Warn(CodexFirstTurnNoProgressMarker,
				"pid", p.cmd.Process.Pid,
				"thread_id", threadID,
				"turn_id", p.client.activeTurnID(),
				"timeout", firstTurnNoProgressTimeout.String(),
				"last_activity", lastSemanticActivityDescription,
			)
		case <-semanticTimer.C:
			waitingForTurn = false
			finalStatus = "timeout"
			timeoutKind := codexTimeoutSemanticInactivity
			timeoutMarker := CodexSemanticInactivityMarker
			if firstTurnStarted && !firstTurnProgressObserved {
				timeoutKind = codexTimeoutFirstTurnNoProgress
				timeoutMarker = CodexFirstTurnNoProgressMarker
			}
			timeoutDiagnostic = codexTimeoutDiagnostic{
				Kind:         timeoutKind,
				Timeout:      semanticInactivityTimeout,
				LastActivity: lastSemanticActivityDescription,
				ThreadID:     threadID,
				TurnID:       p.client.activeTurnID(),
				Model:        opts.Model,
			}
			b.cfg.Logger.Warn(timeoutMarker,
				"pid", p.cmd.Process.Pid,
				"thread_id", threadID,
				"turn_id", p.client.activeTurnID(),
				"timeout", semanticInactivityTimeout.String(),
				"last_activity", lastSemanticActivityDescription,
			)
		case <-runCtx.Done():
			waitingForTurn = false
			if runCtx.Err() == context.DeadlineExceeded {
				finalStatus = "timeout"
				finalError = fmt.Sprintf("codex timed out after %s", timeout)
			} else {
				finalStatus = "aborted"
				finalError = "execution cancelled"
			}
		}
	}

	p.stateMu.Lock()
	p.message = nil
	p.stateMu.Unlock()
	p.client.onMessage = nil
	p.client.onSemanticActivity = nil
	p.client.onTurnDone = nil

	// Unhealthy / aborted turns dispose the resident process so the next
	// Execute rebuilds app-server + thread (resume via ResumeSessionID).
	// Successful turns keep the process alive for the next chat turn.
	if finalStatus != "completed" {
		if timeoutDiagnostic.Kind != codexTimeoutNone {
			timeoutDiagnostic.CodexVersion = detectCodexVersionForDiagnostics(
				context.Background(), p.execPath, p.cmd.Env, b.cfg.Logger,
			)
			finalError = buildCodexTimeoutDiagnosticError(timeoutDiagnostic, "")
		}
		sessionID := threadID
		b.disposeProcess(p)
		if b.forceKilled.CompareAndSwap(true, false) {
			return Result{Status: "failed", Error: AgentForceKilledMarker + ": " + finalError, SessionID: sessionID}
		}
		outputMu.Lock()
		out := output.String()
		outputMu.Unlock()
		return Result{Status: finalStatus, Output: out, Error: finalError, SessionID: sessionID}
	}

	outputMu.Lock()
	finalOutput := output.String()
	outputMu.Unlock()

	var usageMap map[string]TokenUsage
	p.client.usageMu.Lock()
	u := p.client.usage
	p.client.usageMu.Unlock()
	if u.InputTokens == 0 && u.OutputTokens == 0 {
		if scanned := scanCodexSessionUsage(time.Now().Add(-time.Hour), threadID); scanned != nil {
			u = scanned.usage
			if scanned.model != "" && opts.Model == "" {
				opts.Model = scanned.model
			}
		}
	}
	if u.InputTokens > 0 || u.OutputTokens > 0 || u.CacheReadTokens > 0 || u.CacheWriteTokens > 0 {
		model := opts.Model
		if model == "" {
			model = "unknown"
		}
		usageMap = map[string]TokenUsage{model: u}
	}

	return Result{
		Status:    "completed",
		Output:    finalOutput,
		SessionID: threadID,
		Usage:     usageMap,
	}
}

func (b *codexAppServerBackend) compactRuntime(ctx context.Context, p *codexAppServerProcess, msgCh chan<- Message) error {
	finished := make(chan struct{}, 1)
	p.client.onMessage = func(msg Message) {
		if msg.Type != MessageCompactionStarted && msg.Type != MessageCompactionFinished {
			return
		}
		trySend(msgCh, msg)
		if msg.Type == MessageCompactionFinished {
			select {
			case finished <- struct{}{}:
			default:
			}
		}
	}
	defer func() { p.client.onMessage = nil }()

	if _, err := p.client.request(ctx, "thread/compact/start", map[string]any{"threadId": p.threadID}); err != nil {
		return fmt.Errorf("codex thread/compact/start: %w", err)
	}
	select {
	case <-finished:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// AcceptPendingNotice steers a content-free Notice into the active Codex turn.
// expectedTurnId fences a late write from landing in a newer turn after the
// daemon observed the busy slot.
func (b *codexAppServerBackend) AcceptPendingNotice(ctx context.Context, notice ResidentPendingNotice) error {
	if !b.running.Load() {
		return errors.New("Codex Pending Notice requires an active turn")
	}
	p := b.process.Load()
	if p == nil {
		return errors.New("Codex Pending Notice requires a live app-server")
	}
	turnID := p.client.activeTurnID()
	if turnID == "" {
		return errors.New("Codex Pending Notice requires an accepted active turn")
	}
	prompt, err := formatResidentPendingNotice(notice)
	if err != nil {
		return err
	}
	_, err = p.client.request(ctx, "turn/steer", map[string]any{
		"threadId":       p.threadID,
		"expectedTurnId": turnID,
		"input":          []map[string]any{{"type": "text", "text": prompt}},
	})
	if err != nil {
		return fmt.Errorf("Codex Pending Notice: %w", err)
	}
	return nil
}

func (b *codexAppServerBackend) ensureProcess(ctx context.Context, opts ExecOptions) (*codexAppServerProcess, error) {
	if p := b.process.Load(); p != nil {
		if alive, known := processAlive(p.cmd.Process); known && alive {
			return p, nil
		}
		// Stale pointer to a dead child — drop and recreate.
		b.disposeProcess(p)
	}

	execPath := b.cfg.ExecutablePath
	if execPath == "" {
		execPath = "codex"
	}
	if _, err := exec.LookPath(execPath); err != nil {
		return nil, fmt.Errorf("codex executable not found at %q: %w", execPath, err)
	}

	// Same MCP materialisation as one-shot codexBackend: secrets stay in
	// $CODEX_HOME/config.toml (0o600), never argv.
	if codexHome := strings.TrimSpace(b.cfg.Env["CODEX_HOME"]); codexHome != "" {
		if err := ensureCodexMcpConfig(filepath.Join(codexHome, "config.toml"), opts.McpConfig, b.cfg.Logger); err != nil {
			return nil, fmt.Errorf("apply codex mcp_config: %w", err)
		}
	} else if hasManagedMcpConfig(opts.McpConfig) {
		return nil, fmt.Errorf("codex: mcp_config is set but CODEX_HOME env var is not configured; cannot apply managed MCP")
	}

	codexArgs := buildCodexArgs(opts, b.cfg.Logger)
	cmd := exec.Command(execPath, codexArgs...)
	hideAgentWindow(cmd)
	if opts.Cwd != "" {
		cmd.Dir = opts.Cwd
	}
	cmd.Env = buildEnv(b.cfg.Env)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("codex app-server stdout: %w", err)
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("codex app-server stdin: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("codex app-server stderr: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start codex app-server: %w", err)
	}

	p := &codexAppServerProcess{
		cmd:        cmd,
		stdin:      stdin,
		readerDone: make(chan struct{}),
		stderrDone: make(chan struct{}),
		execPath:   execPath,
	}
	p.client = &codexClient{
		cfg:                  b.cfg,
		stdin:                stdin,
		pending:              make(map[int]*pendingRPC),
		completedTurnIDs:     make(map[string]bool),
		notificationProtocol: "unknown",
		onMessage: func(msg Message) {
			p.stateMu.Lock()
			defer p.stateMu.Unlock()
			if p.message != nil {
				p.message(msg)
			}
		},
	}
	// Publish before handshake so ForceKill can interrupt a hung initialize.
	b.process.Store(p)
	b.cfg.Logger.Info("codex app-server resident started", "pid", cmd.Process.Pid, "cwd", opts.Cwd)

	go func() {
		defer close(p.readerDone)
		scanner := bufio.NewScanner(stdout)
		scanner.Buffer(make([]byte, 0, 1024*1024), 10*1024*1024)
		for scanner.Scan() {
			if line := strings.TrimSpace(scanner.Text()); line != "" {
				p.client.handleLine(line)
			}
		}
		p.client.closeAllPending(fmt.Errorf("codex app-server process exited"))
	}()
	go func() {
		defer close(p.stderrDone)
		_, _ = io.Copy(newLogWriter(b.cfg.Logger, "[codex-app-server:stderr] "), stderr)
	}()

	_, err = p.client.request(ctx, "initialize", map[string]any{
		"clientInfo": map[string]any{
			"name":    "multica-agent-sdk",
			"title":   "Multica Agent SDK",
			"version": "0.2.0",
		},
		"capabilities": map[string]any{
			"experimentalApi": true,
		},
	})
	if err != nil {
		b.disposeProcess(p)
		return nil, fmt.Errorf("codex initialize failed: %w", err)
	}
	p.client.notify("initialized")

	threadID, resumed, err := p.client.startOrResumeThread(ctx, opts, b.cfg.Logger)
	if err != nil {
		b.disposeProcess(p)
		return nil, err
	}
	p.threadID = threadID
	p.client.threadID = threadID
	if resumed {
		b.cfg.Logger.Info("codex resident thread resumed", "thread_id", threadID)
	} else {
		b.cfg.Logger.Info("codex resident thread started", "thread_id", threadID)
	}
	return p, nil
}

func (b *codexAppServerBackend) disposeProcess(p *codexAppServerProcess) {
	b.process.CompareAndSwap(p, nil)
	p.disposeOnce.Do(func() {
		_ = p.stdin.Close()
		// Prefer graceful exit (OTEL flush) then force-kill.
		select {
		case <-p.readerDone:
		case <-time.After(codexGracefulShutdownTimeout):
			if p.cmd.Process != nil {
				_ = forceKillProcess(p.cmd.Process)
			}
			<-p.readerDone
		}
		_ = p.cmd.Wait()
		<-p.stderrDone
	})
}
