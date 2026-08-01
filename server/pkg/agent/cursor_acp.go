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
	Close()
}

// cursorACPBackend keeps one `cursor-agent acp` child across compatible turns.
// Live CLI evidence (2026-07-27, cursor-agent 2026.06.24-*):
//   - entry: cursor-agent acp
//   - initialize → authMethods includes cursor_login
//   - session/new requires authenticate(methodId=cursor_login) after agent login
//   - mcpServers must be an array
//   - permission is server→client session/request_permission (hermesClient)
type cursorACPBackend struct {
	cfg Config

	mu      sync.Mutex
	process *cursorACPProcess
	running atomic.Bool
	// afterResultPublishForTest runs after a terminal Result is published.
	// Tests use it to pin release-before-publish ordering without sleeps.
	afterResultPublishForTest func()
}

type cursorACPProcess struct {
	cmd        *exec.Cmd
	stdin      io.WriteCloser
	client     *hermesClient
	readerDone chan struct{}
	stderrDone chan struct{}
	sessionID  string

	stateMu sync.Mutex
	message func(Message)
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
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.process != nil {
		b.disposeProcessLocked(b.process)
	}
}

// ForceKill implements agent.ResidentRuntimeForceKillable (task #62). Unlike
// Close(), this may be called while a turn is in flight (running.Load() ==
// true) — it must NOT call disposeProcessLocked, because that ends in
// cmd.Wait(), and the Go documentation for exec.Cmd with StdoutPipe/
// StdinPipe is explicit that only the goroutine reading the pipes may call
// Wait. Execute()'s own goroutine is that reader; it already reaps the
// process once it observes the pipe read failing (the same path a genuine
// crash takes today). ForceKill only needs to make the process die — it
// deliberately leaves reaping to that goroutine so there is exactly one
// caller of Wait(), not two.
func (b *cursorACPBackend) ForceKill() error {
	b.mu.Lock()
	p := b.process
	b.mu.Unlock()
	if p == nil {
		return nil
	}
	_ = p.stdin.Close()
	return p.cmd.Process.Kill()
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
		result := b.executeTurn(ctx, prompt, opts, msgCh)
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

// RuntimeAlive implements ResidentRuntimeLivenessChecker, letting a caller
// poll process liveness between turns, not just during an in-flight one.
func (b *cursorACPBackend) RuntimeAlive() (bool, bool) {
	return b.runtimeAlive()
}

func (b *cursorACPBackend) runtimeAlive() (bool, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.process == nil {
		return false, false
	}
	return processAlive(b.process.cmd.Process)
}

func (b *cursorACPBackend) executeTurn(ctx context.Context, prompt string, opts ExecOptions, msgCh chan<- Message) Result {
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
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.process != nil {
		return b.process, nil
	}
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
		b.disposeProcessLocked(p)
		return nil, fmt.Errorf("cursor ACP initialize: %w", err)
	}
	if err := cursorACPRequireAuthMethod(init, "cursor_login"); err != nil {
		b.disposeProcessLocked(p)
		return nil, err
	}
	// Live CLI requires authenticate after agent login; methodId must be cursor_login.
	if _, err := p.client.request(ctx, "authenticate", map[string]any{"methodId": "cursor_login"}); err != nil {
		b.disposeProcessLocked(p)
		if isCursorACPAuthError(err) {
			return nil, fmt.Errorf("%w: %v", ErrCursorACPAuthRequired, err)
		}
		return nil, fmt.Errorf("cursor ACP authenticate: %w", err)
	}

	mcp, err := buildACPMcpServers(opts.McpConfig, b.cfg.Logger)
	if err != nil {
		b.disposeProcessLocked(p)
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
			b.disposeProcessLocked(p)
			if isCursorACPAuthError(err) {
				return nil, fmt.Errorf("%w: %v", ErrCursorACPAuthRequired, err)
			}
			return nil, fmt.Errorf("cursor ACP session/load: %w", err)
		}
		p.sessionID, _ = resolveResumedSessionID(opts.ResumeSessionID, created)
	} else {
		created, err = p.client.request(ctx, "session/new", map[string]any{"cwd": cwd, "mcpServers": mcp})
		if err != nil {
			b.disposeProcessLocked(p)
			if isCursorACPAuthError(err) {
				return nil, fmt.Errorf("%w: %v", ErrCursorACPAuthRequired, err)
			}
			return nil, fmt.Errorf("cursor ACP session/new: %w", err)
		}
		p.sessionID = extractACPSessionID(created)
	}
	if p.sessionID == "" {
		b.disposeProcessLocked(p)
		return nil, fmt.Errorf("cursor ACP session/new returned no session ID")
	}
	if opts.Model != "" {
		// session/new and session/load both echo the CLI's live model catalog
		// (models.availableModels) — Cursor validates set_model against this
		// same live list server-side, not against anything we ship, so our
		// own static catalog (models.go) can drift out of date at any time
		// (new models, account/plan changes, Cursor renaming an ID). Skip the
		// call entirely when our target isn't in the live list: better to run
		// with the CLI's own default model than to fail the whole session
		// over a config our own list produced, and the "always send unless we
		// try to be clever" behavior this replaces is exactly the confirmed
		// bug — an agent whose stored model is a name Cursor doesn't currently
		// recognize (e.g. "auto", after Cursor's own catalog moved past it)
		// could never start at all.
		if models := parseACPSessionNewModels(created); models != nil && !acpModelListContains(models, opts.Model) {
			b.cfg.Logger.Warn("cursor ACP: configured model not in live catalog, leaving CLI default",
				"configured_model", opts.Model)
		} else if _, err := p.client.request(ctx, "session/set_model", map[string]any{
			"sessionId": p.sessionID, "modelId": opts.Model,
		}); err != nil && !isACPMethodNotFound(err) && !isACPInvalidParams(err) {
			// Method-not-found (CLI doesn't implement set_model) and
			// invalid-params (CLI rejects this specific value) both mean "we
			// couldn't apply the requested model," not "this session is
			// broken" — every other error (auth, transport, crash) still
			// fails the session, since those really are broken.
			b.disposeProcessLocked(p)
			return nil, fmt.Errorf("cursor ACP set model: %w", err)
		} else if err != nil {
			b.cfg.Logger.Warn("cursor ACP: CLI rejected configured model, leaving CLI default",
				"configured_model", opts.Model, "error", err)
		}
	}
	b.process = p
	return p, nil
}

func (b *cursorACPBackend) disposeProcess(p *cursorACPProcess) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.disposeProcessLocked(p)
}

func (b *cursorACPBackend) disposeProcessLocked(p *cursorACPProcess) {
	if b.process == p {
		b.process = nil
	}
	_ = p.stdin.Close()
	_ = p.cmd.Process.Kill()
	_ = p.cmd.Wait()
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

func isACPMethodNotFound(err error) bool {
	if err == nil {
		return false
	}
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "method not found") || strings.Contains(s, "-32601")
}

// isACPInvalidParams reports whether err is a JSON-RPC "Invalid params"
// (-32602) response. For session/set_model specifically, the only param is
// modelId, so this code means "the CLI doesn't recognize this model value
// right now" — confirmed against cursor-agent's real validation logic, which
// checks modelId against a live, account/plan-specific catalog rather than a
// fixed enum (see the caller's comment).
func isACPInvalidParams(err error) bool {
	var rpcErr *acpRPCError
	if errors.As(err, &rpcErr) {
		return rpcErr.Code == -32602
	}
	return strings.Contains(strings.ToLower(err.Error()), "-32602")
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
