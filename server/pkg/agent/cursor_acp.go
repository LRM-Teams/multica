package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// ErrCursorACPTurnBusy is returned when a second Execute overlaps an in-flight
// resident ACP turn. The daemon must acquire the pool slot before claim.
var ErrCursorACPTurnBusy = errors.New("cursor ACP turn busy")

// ErrCursorACPAuthRequired marks fail-closed missing Cursor login. Daemon code
// may treat this as ProviderAuthRequiredMarker (terminal, no silent retry).
var ErrCursorACPAuthRequired = errors.New(ProviderAuthRequiredMarker + ": cursor ACP authentication required (run cursor-agent login, then authenticate cursor_login)")

// CursorACPBackend is the lifecycle surface for a long-lived cursor-agent acp
// child. Close is mandatory on eviction, config mismatch, and failed turns.
// #702 PR A: pkg-only; daemon wiring is a later PR onto the canonical
// agent×runtime pool (not a chatSession-keyed third pool).
type CursorACPBackend interface {
	Backend
	ResidentMessageInput
	ResidentIdleInboxNoticeInput
	ResidentPendingNoticeInput
	Close()
}

// cursorACPBackend keeps one `cursor-agent acp` child across compatible turns.
// Live CLI evidence (2026-07-27, cursor-agent 2026.06.24-*):
//   - entry: cursor-agent acp
//   - initialize → authMethods includes cursor_login
//   - session/new requires authenticate(methodId=cursor_login) after agent login
//   - mcpServers must be an array
//   - permission is server→client session/request_permission (acpClient)
type cursorACPBackend struct {
	cfg Config

	// process is published atomically the instant cmd.Start() succeeds (task
	// #62 follow-up), before the initialize/authenticate/session/new
	// handshake begins. This is what lets ForceKill() always find and kill
	// the process without ever contending with a lock a stuck handshake
	// could hold forever — a real, live-reproduced deadlock the previous
	// mutex-guarded design had (ensureProcess held that mutex across the
	// whole handshake, and ForceKill needed the same mutex just to read the
	// pointer). See disposeProcess for how double-dispose of an
	// early-published-but-still-handshaking process is made safe.
	process atomic.Pointer[cursorACPProcess]
	running atomic.Bool
	// forceKilled is set by ForceKill() (task #62) so the in-flight
	// Execute() goroutine can tell "interrupted on purpose" apart from a
	// genuine crash when it observes the resulting pipe/request failure.
	// Read-and-cleared by executeTurn's own error path, not by ForceKill —
	// ForceKill only signals; it does not touch anything Execute() owns.
	forceKilled atomic.Bool
	// afterResultPublishForTest runs after a terminal Result is published.
	// Tests use it to pin release-before-publish ordering without sleeps.
	afterResultPublishForTest func()
}

type cursorACPProcess struct {
	cmd        *exec.Cmd
	stdin      io.WriteCloser
	client     *acpClient
	readerDone chan struct{}
	stderrDone chan struct{}
	sessionID  string

	stateMu sync.Mutex
	message func(Message)

	// noticeMu fences terminal turn cleanup against a native follow-up write.
	// Cursor ACP accepts a second session/prompt while the first is active and
	// reports the original prompt as cancelled; keep the outer resident turn
	// alive until that native follow-up finishes.
	noticeMu   sync.Mutex
	noticeOpen bool
	noticeDone <-chan rpcResult

	// disposeOnce guards the actual Kill+Wait teardown (see disposeProcess).
	// Publishing the process before the handshake completes means Close(),
	// a failed handshake, and a failed in-flight turn can all independently
	// decide "this process needs disposing" for the same *cursorACPProcess;
	// disposeOnce ensures cmd.Wait() — which exec's docs require exactly one
	// caller for when using StdoutPipe/StdinPipe — only ever runs once no
	// matter how many of those paths race to call disposeProcess with it.
	disposeOnce sync.Once
}

func newCursorACPBackend(cfg Config) *cursorACPBackend {
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	return &cursorACPBackend{cfg: cfg}
}

// NewCursorACPBackend constructs a resident Cursor ACP backend for the daemon
// pool. Not registered in agent.New("cursor") — one-shot stream-json remains
// the default until PR B wires chat/issue gates.
func NewCursorACPBackend(cfg Config) CursorACPBackend { return newCursorACPBackend(cfg) }

func (b *cursorACPBackend) Close() {
	if p := b.process.Load(); p != nil {
		b.disposeProcess(p)
	}
}

// ForceKill implements agent.ResidentRuntimeForceKillable (task #62). Unlike
// Close(), this may be called while a turn is in flight (running.Load() ==
// true) — it must NOT itself call disposeProcess's cmd.Wait(), because the
// Go documentation for exec.Cmd with StdoutPipe/StdinPipe is explicit that
// only the goroutine reading the pipes may call Wait. Reading b.process is a
// plain atomic load: it is published the instant cmd.Start() succeeds (see
// ensureProcess), before the handshake, so ForceKill never needs to wait on
// anything a stuck handshake might be holding — that was the real deadlock
// task #62's live end-to-end verification found in the previous mutex-based
// design. Killing the process here unblocks whichever goroutine is
// currently waiting on it (executeTurn's in-flight request, or
// ensureProcess's handshake); that goroutine's own error path calls
// disposeProcess, so there remains exactly one caller of Wait(), not two.
func (b *cursorACPBackend) ForceKill() error {
	p := b.process.Load()
	if p == nil {
		return nil
	}
	b.forceKilled.Store(true)
	_ = p.stdin.Close()
	return forceKillProcess(p.cmd.Process)
}

func (b *cursorACPBackend) Execute(ctx context.Context, prompt string, opts ExecOptions) (*Session, error) {
	if !b.running.CompareAndSwap(false, true) {
		return nil, fmt.Errorf("%w: concurrent cursor ACP turn", ErrCursorACPTurnBusy)
	}
	msgCh := make(chan Message, 256)
	resCh := make(chan Result, 1)
	go func() {
		defer close(msgCh)
		defer close(resCh)
		var releaseOnce sync.Once
		releaseAdmission := func() {
			releaseOnce.Do(func() {
				b.running.Store(false)
			})
		}
		// Fallback if executeTurn panics or returns without an explicit release.
		defer releaseAdmission()
		started := time.Now()
		result := b.executeTurn(ctx, prompt, opts, msgCh, nil)
		result.DurationMs = time.Since(started).Milliseconds()
		// A terminal result is the caller's permission to begin the next turn.
		// Release admission before publishing it; otherwise the receiver can
		// observe completion while running is still true and get a false busy
		// error on an immediate follow-up (same race D6-1a fixed in pi_rpc).
		releaseAdmission()
		resCh <- result
		if b.afterResultPublishForTest != nil {
			b.afterResultPublishForTest()
		}
	}()
	return &Session{Messages: msgCh, Result: resCh, RuntimeAlive: b.runtimeAlive}, nil
}

// AcceptMessageBatch starts one Cursor ACP turn for canonical Message bodies
// while the resident runtime is idle. Acceptance is reported only after the
// session/prompt request has crossed Cursor's serialized native write boundary;
// process/session setup or goroutine scheduling alone is not sufficient.
func (b *cursorACPBackend) AcceptMessageBatch(ctx context.Context, messages []ResidentMessage) (ResidentMessageAcceptance, error) {
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

func (b *cursorACPBackend) AcceptIdleInboxNotice(ctx context.Context, notice ResidentPendingNotice) (ResidentMessageAcceptance, error) {
	prompt, err := formatResidentPendingNotice(notice)
	if err != nil {
		return ResidentMessageAcceptance{}, err
	}
	return b.acceptIdleInputPrompt(ctx, prompt)
}

func (b *cursorACPBackend) acceptIdleInputPrompt(ctx context.Context, prompt string) (ResidentMessageAcceptance, error) {
	if !b.running.CompareAndSwap(false, true) {
		return ResidentMessageAcceptance{}, fmt.Errorf("%w: idle input overlaps an active turn", ErrCursorACPTurnBusy)
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
func (b *cursorACPBackend) EnsureResidentProcess(ctx context.Context) error {
	_, err := b.ensureProcess(ctx, b.cfg.ResidentOptions)
	return err
}

func (b *cursorACPBackend) RuntimeAlive() (bool, bool) {
	return b.runtimeAlive()
}

// AcceptPendingNotice uses Cursor's native ACP follow-up path. Cursor's ACP
// v1 response is an end-of-turn receipt, so the successful serialized stdin
// write is the handoff receipt and executeTurn owns the eventual response.
func (b *cursorACPBackend) AcceptPendingNotice(ctx context.Context, notice ResidentPendingNotice) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !b.running.Load() {
		return errors.New("Cursor Pending Notice requires an active turn")
	}
	p := b.process.Load()
	if p == nil || strings.TrimSpace(p.sessionID) == "" {
		return errors.New("Cursor Pending Notice requires a live ACP session")
	}
	prompt, err := formatResidentPendingNotice(notice)
	if err != nil {
		return err
	}

	p.noticeMu.Lock()
	defer p.noticeMu.Unlock()
	if !p.noticeOpen {
		return errors.New("Cursor Pending Notice is waiting for the next active turn")
	}
	if p.noticeDone != nil {
		return errors.New("Cursor Pending Notice follow-up is already active")
	}
	_, done, err := p.client.beginRequest("session/prompt", map[string]any{
		"sessionId": p.sessionID,
		"prompt":    []map[string]any{{"type": "text", "text": prompt}},
	})
	if err != nil {
		return fmt.Errorf("Cursor Pending Notice: %w", err)
	}
	p.noticeDone = done
	return nil
}

func (b *cursorACPBackend) runtimeAlive() (bool, bool) {
	p := b.process.Load()
	if p == nil {
		return false, false
	}
	return processAlive(p.cmd.Process)
}

func (b *cursorACPBackend) executeTurn(ctx context.Context, prompt string, opts ExecOptions, msgCh chan<- Message, reportAcceptance func(error)) Result {
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
	p.noticeMu.Lock()
	p.noticeDone = nil
	p.client.resetToolCallFailure()
	primaryID, primaryDone, err := p.client.beginRequest("session/prompt", map[string]any{
		"sessionId": p.sessionID,
		"prompt":    []map[string]any{{"type": "text", "text": prompt}},
	})
	if err == nil {
		// The primary prompt is now on Cursor's serialized ACP wire. Only this
		// native admission proof opens the follow-up gate.
		p.noticeOpen = true
	}
	p.noticeMu.Unlock()
	if reportAcceptance != nil {
		reportAcceptance(err)
	}
	if err == nil {
		_, err = p.client.awaitRequest(ctx, primaryID, primaryDone)
	}
	p.noticeMu.Lock()
	p.noticeOpen = false
	noticeDone := p.noticeDone
	p.noticeMu.Unlock()
	if noticeDone != nil {
		select {
		case noticeResult := <-noticeDone:
			if noticeResult.err != nil {
				err = fmt.Errorf("Cursor Pending Notice: %w", noticeResult.err)
			}
		case <-ctx.Done():
			err = ctx.Err()
		}
		p.noticeMu.Lock()
		if p.noticeDone == noticeDone {
			p.noticeDone = nil
		}
		p.noticeMu.Unlock()
	}
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
		b.disposeProcess(p)
		if b.forceKilled.CompareAndSwap(true, false) {
			return Result{Status: "failed", Error: AgentForceKilledMarker + ": " + err.Error(), SessionID: p.sessionID}
		}
		status := "failed"
		if ctx.Err() == context.DeadlineExceeded {
			status = "timeout"
		} else if ctx.Err() == context.Canceled {
			status = "aborted"
		}
		return Result{Status: status, Error: fmt.Sprintf("cursor ACP session/prompt: %v", err), SessionID: p.sessionID}
	}
	return Result{Status: "completed", Output: output.String(), SessionID: p.sessionID}
}

func (b *cursorACPBackend) ensureProcess(ctx context.Context, opts ExecOptions) (*cursorACPProcess, error) {
	if p := b.process.Load(); p != nil {
		return p, nil
	}
	// Execute() serializes turns via running.CompareAndSwap, so at most one
	// goroutine ever reaches this point at a time — no concurrent creator to
	// race against.
	execPath := b.cfg.ExecutablePath
	if execPath == "" {
		execPath = "cursor-agent"
	}
	if _, err := exec.LookPath(execPath); err != nil {
		return nil, fmt.Errorf("cursor-agent executable not found at %q: %w", execPath, err)
	}

	// Live CLI: `cursor-agent acp` starts the ACP (Agent Client Protocol) server.
	cmd := exec.Command(execPath, "acp")
	hideAgentWindow(cmd)
	if opts.Cwd != "" {
		cmd.Dir = opts.Cwd
	}
	if b.cfg.Env != nil {
		cmd.Env = mergeEnv(os.Environ(), b.cfg.Env)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("cursor ACP stdout: %w", err)
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("cursor ACP stdin: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("cursor ACP stderr: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start cursor ACP: %w", err)
	}
	p := &cursorACPProcess{cmd: cmd, stdin: stdin, readerDone: make(chan struct{}), stderrDone: make(chan struct{})}
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
	// can now find and kill this process even if initialize/authenticate/
	// session/new below hangs forever.
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
		p.client.closeAllPending(fmt.Errorf("cursor ACP process exited"))
	}()
	go func() {
		defer close(p.stderrDone)
		_, _ = io.Copy(newLogWriter(b.cfg.Logger, "[cursor-acp:stderr] "), stderr)
	}()

	init, err := p.client.request(ctx, "initialize", map[string]any{
		"protocolVersion":    1,
		"clientInfo":         map[string]any{"name": "multica-agent-sdk", "version": "0.2.0"},
		"clientCapabilities": map[string]any{},
	})
	if err != nil {
		b.disposeProcess(p)
		return nil, fmt.Errorf("cursor ACP initialize: %w", err)
	}
	if err := cursorACPRequireAuthMethod(init, "cursor_login"); err != nil {
		b.disposeProcess(p)
		return nil, err
	}
	// Live CLI requires authenticate after agent login; methodId must be cursor_login.
	if _, err := p.client.request(ctx, "authenticate", map[string]any{"methodId": "cursor_login"}); err != nil {
		b.disposeProcess(p)
		if isCursorACPAuthError(err) {
			return nil, fmt.Errorf("%w: %v", ErrCursorACPAuthRequired, err)
		}
		return nil, fmt.Errorf("cursor ACP authenticate: %w", err)
	}

	mcp, err := buildACPMcpServers(opts.McpConfig, b.cfg.Logger)
	if err != nil {
		b.disposeProcess(p)
		return nil, fmt.Errorf("cursor ACP invalid mcp_config: %w", err)
	}
	// Live CLI rejects missing/non-array mcpServers; never omit the field.
	if mcp == nil {
		mcp = []any{}
	}
	mcp = filterACPMcpServersByCapability(mcp, extractACPMcpCapabilities(init), "cursor", b.cfg.Logger)
	cwd := opts.Cwd
	if cwd == "" {
		cwd = "."
	}
	var created json.RawMessage
	if opts.ResumeSessionID != "" {
		created, err = p.client.request(ctx, "session/load", map[string]any{
			"cwd": cwd, "sessionId": opts.ResumeSessionID, "mcpServers": mcp,
		})
		if err != nil {
			b.disposeProcess(p)
			if isCursorACPAuthError(err) {
				return nil, fmt.Errorf("%w: %v", ErrCursorACPAuthRequired, err)
			}
			return nil, fmt.Errorf("cursor ACP session/load: %w", err)
		}
		p.sessionID, _ = resolveResumedSessionID(opts.ResumeSessionID, created)
	} else {
		created, err = p.client.request(ctx, "session/new", map[string]any{"cwd": cwd, "mcpServers": mcp})
		if err != nil {
			b.disposeProcess(p)
			if isCursorACPAuthError(err) {
				return nil, fmt.Errorf("%w: %v", ErrCursorACPAuthRequired, err)
			}
			return nil, fmt.Errorf("cursor ACP session/new: %w", err)
		}
		p.sessionID = extractACPSessionID(created)
	}
	if p.sessionID == "" {
		b.disposeProcess(p)
		return nil, fmt.Errorf("cursor ACP session/new returned no session ID")
	}
	if opts.Model != "" {
		// session/new and session/load both echo the CLI's live model catalog
		// (models.availableModels) — Cursor validates set_model against this
		// same live list server-side, not against anything we ship, so our
		// own static catalog (models.go) can drift out of date at any time.
		// Cursor also uses different identifiers for its public CLI alias and
		// ACP catalog: `agent --model auto` is advertised to users as "auto",
		// while live ACP exposes the same choice as an account/version-specific
		// ID (currently `default[]`) named "Auto". Resolve that alias by display
		// name. A missing or rejected model is fatal: omitting set_model would
		// silently run Cursor's current model, which violates the configured
		// model contract.
		modelID := strings.TrimSpace(opts.Model)
		models := parseACPSessionNewModels(created)
		if models != nil {
			var found bool
			modelID, found = resolveCursorACPModelID(models, modelID)
			if !found {
				b.disposeProcess(p)
				return nil, fmt.Errorf("Cursor model %q is not available in the live model catalog", opts.Model)
			}
		}
		if _, err := p.client.request(ctx, "session/set_model", map[string]any{
			"sessionId": p.sessionID, "modelId": modelID,
		}); err != nil {
			b.disposeProcess(p)
			return nil, fmt.Errorf("Cursor could not apply configured model %q: %w", opts.Model, err)
		}
	}
	return p, nil
}

// disposeProcess tears down p and clears it from b.process if it is still
// the current one (a subsequent ensureProcess may already have replaced it
// with a newer process, in which case this must not clobber that). It is
// safe to call concurrently for the same p from multiple paths — Close(),
// a failed handshake in ensureProcess, and a failed in-flight turn in
// executeTurn can all reach here for the same early-published process — the
// actual Kill+Wait teardown happens at most once, guarded by p.disposeOnce.
func (b *cursorACPBackend) disposeProcess(p *cursorACPProcess) {
	b.process.CompareAndSwap(p, nil)
	p.disposeOnce.Do(func() {
		_ = p.stdin.Close()
		_ = p.cmd.Process.Kill()
		_ = p.cmd.Wait()
	})
}

func cursorACPRequireAuthMethod(init json.RawMessage, wantID string) error {
	var payload struct {
		AuthMethods []struct {
			ID string `json:"id"`
		} `json:"authMethods"`
	}
	if err := json.Unmarshal(init, &payload); err != nil {
		return fmt.Errorf("cursor ACP initialize: decode authMethods: %w", err)
	}
	for _, m := range payload.AuthMethods {
		if m.ID == wantID {
			return nil
		}
	}
	return fmt.Errorf("cursor ACP initialize: missing auth method %q", wantID)
}

func isCursorACPAuthError(err error) bool {
	if err == nil {
		return false
	}
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "authentication required") ||
		strings.Contains(s, "cursor_login") ||
		strings.Contains(s, "not logged in")
}

// acpModelListContains reports whether id matches a model in models by ID.
func acpModelListContains(models []Model, id string) bool {
	for _, m := range models {
		if m.ID == id {
			return true
		}
	}
	return false
}

// resolveCursorACPModelID translates Cursor's public "auto" model alias to
// the live ACP model whose display name is Auto. Cursor's ACP model IDs are
// parameterized and may change independently of the public CLI spelling, so
// the live name is the stable bridge. All concrete IDs still require an exact
// catalog match.
func resolveCursorACPModelID(models []Model, configured string) (string, bool) {
	configured = strings.TrimSpace(configured)
	if strings.EqualFold(configured, "auto") {
		for _, model := range models {
			if strings.EqualFold(strings.TrimSpace(model.Label), "auto") {
				return model.ID, true
			}
		}
	}
	if acpModelListContains(models, configured) {
		return configured, true
	}
	return "", false
}
