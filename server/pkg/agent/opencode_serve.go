package agent

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// ErrOpenCodeServeTurnBusy is returned when a second Execute overlaps an
// in-flight resident opencode-serve turn. The daemon must acquire the pool
// slot before claim, same invariant as ErrCursorACPTurnBusy.
var ErrOpenCodeServeTurnBusy = errors.New("opencode serve turn busy")

// OpenCodeServeBackend is the lifecycle surface for a long-lived
// `opencode serve` child. Close is mandatory on eviction, config mismatch,
// and failed turns — same contract as CursorACPBackend/GrokACPBackend.
type OpenCodeServeBackend interface {
	Backend
	ResidentMessageInput
	ResidentReminderInputReceiver
	ResidentPendingNoticeInput
	Close()
}

// opencodeServeBackend keeps one `opencode serve` child across compatible
// turns. Unlike Cursor/Grok/Pi (resident over stdio ACP/RPC), OpenCode's
// long-running mode is a headless HTTP+SSE server
// (opencode.ai/docs/server): POST /session, POST /session/:id/message,
// GET /event. The pool/lease pattern is identical; only the wire protocol
// differs.
type opencodeServeBackend struct {
	cfg Config

	// noticeMu serializes primary-turn admission, queued Notice admission,
	// and terminal release. A successful queue receipt therefore belongs to
	// the exact resident turn whose busy state the coordinator observed.
	noticeMu    sync.Mutex
	noticeReady bool
	mu          sync.Mutex
	server      *opencodeServeProcess
	running     bool
	// forceKilled is set by ForceKill() (task #62); see cursorACPBackend's
	// field of the same name for the full explanation. Plain bool guarded by
	// b.mu, matching this backend's existing convention for `running`.
	forceKilled bool
}

type opencodeServeProcess struct {
	cmd       *exec.Cmd
	baseURL   string
	password  string
	username  string
	client    *opencodeServeClient
	stderrBuf *stderrTail

	sessionMu sync.Mutex
	sessionID string
}

func newOpenCodeServeBackend(cfg Config) *opencodeServeBackend {
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	return &opencodeServeBackend{cfg: cfg}
}

// NewOpenCodeServeBackend constructs a resident OpenCode backend for the
// daemon's canonical agent×runtime pool.
func NewOpenCodeServeBackend(cfg Config) OpenCodeServeBackend { return newOpenCodeServeBackend(cfg) }

func (b *opencodeServeBackend) Close() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.server != nil {
		b.disposeServerLocked()
	}
}

// ForceKill implements agent.ResidentRuntimeForceKillable (task #62). Same
// shape and same reason as cursorACPBackend.ForceKill, applied uniformly
// across all four canonical-resident backends even though this one
// communicates over HTTP rather than manually-read stdio pipes (so the
// specific StdoutPipe/StdinPipe + cmd.Wait() hazard Nash caught for cursor
// may not strictly apply here): ForceKill never calls cmd.Wait(), full stop,
// so the "no ForceKill implementation calls Wait()" static contract (see
// cmd_force_kill_no_wait_test.go) holds without a backend-specific
// exception to remember.
func (b *opencodeServeBackend) ForceKill() error {
	b.mu.Lock()
	p := b.server
	b.forceKilled = true
	b.mu.Unlock()
	if p == nil {
		return nil
	}
	p.client.close()
	return forceKillProcess(p.cmd.Process)
}

// takeForceKilled reports and clears whether ForceKill() was the cause of
// the turn currently failing, mirroring the atomic CompareAndSwap pattern
// the other three backends use for the same purpose.
func (b *opencodeServeBackend) takeForceKilled() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	was := b.forceKilled
	b.forceKilled = false
	return was
}

func (b *opencodeServeBackend) Execute(ctx context.Context, prompt string, opts ExecOptions) (*Session, error) {
	return b.startTurn(ctx, prompt, opts, nil)
}

func (b *opencodeServeBackend) startTurn(ctx context.Context, prompt string, opts ExecOptions, onAdmitted func()) (*Session, error) {
	b.noticeMu.Lock()
	b.mu.Lock()
	if b.running {
		b.mu.Unlock()
		b.noticeMu.Unlock()
		return nil, fmt.Errorf("%w: concurrent opencode serve turn", ErrOpenCodeServeTurnBusy)
	}
	b.running = true
	b.noticeReady = false
	b.mu.Unlock()
	b.noticeMu.Unlock()

	msgCh := make(chan Message, 256)
	resCh := make(chan Result, 1)
	go func() {
		defer close(msgCh)
		defer close(resCh)
		var releaseOnce sync.Once
		release := func() {
			releaseOnce.Do(func() {
				b.releaseTurnAdmission()
			})
		}
		defer release()
		started := time.Now()
		result := b.executeTurn(ctx, prompt, opts, msgCh, onAdmitted)
		result.DurationMs = time.Since(started).Milliseconds()
		// Release admission before publishing the terminal result — same
		// ordering cursorACPBackend.Execute uses, so an immediate follow-up
		// turn never sees a false "busy" against a completed turn.
		release()
		resCh <- result
	}()
	return &Session{Messages: msgCh, Result: resCh, RuntimeAlive: b.runtimeAlive}, nil
}

// AcceptMessageBatch starts an idle OpenCode turn and reports acceptance only
// after POST /session/:id/message succeeds. Process startup and session
// creation alone are not native Message acceptance.
func (b *opencodeServeBackend) AcceptMessageBatch(ctx context.Context, messages []ResidentMessage) (ResidentMessageAcceptance, error) {
	prompt, err := formatResidentMessageBatch(messages)
	if err != nil {
		return ResidentMessageAcceptance{}, err
	}
	return b.acceptIdleInputPrompt(ctx, prompt)
}

func (b *opencodeServeBackend) AcceptReminderInput(ctx context.Context, input ResidentReminderInput) (ResidentMessageAcceptance, error) {
	prompt, err := formatResidentReminderInput(input)
	if err != nil {
		return ResidentMessageAcceptance{}, err
	}
	return b.acceptIdleInputPrompt(ctx, prompt)
}

func (b *opencodeServeBackend) acceptIdleInputPrompt(ctx context.Context, prompt string) (ResidentMessageAcceptance, error) {
	admitted := make(chan struct{})
	var admittedOnce sync.Once
	session, err := b.startTurn(ctx, prompt, b.cfg.ResidentOptions, func() {
		admittedOnce.Do(func() { close(admitted) })
	})
	if err != nil {
		return ResidentMessageAcceptance{}, err
	}
	completion := func(result Result) ResidentMessageAcceptance {
		done := make(chan error, 1)
		done <- errorForResidentTurn(result)
		close(done)
		return ResidentMessageAcceptance{Done: done, Messages: session.Messages}
	}
	select {
	case <-admitted:
		done := make(chan error, 1)
		go func() {
			result := <-session.Result
			done <- errorForResidentTurn(result)
			close(done)
		}()
		return ResidentMessageAcceptance{Done: done, Messages: session.Messages}, nil
	case result := <-session.Result:
		select {
		case <-admitted:
			return completion(result), nil
		default:
			go func() {
				for range session.Messages {
				}
			}()
			if err := errorForResidentTurn(result); err != nil {
				return ResidentMessageAcceptance{}, err
			}
			return ResidentMessageAcceptance{}, errors.New("OpenCode canonical Message turn ended before native input acceptance")
		}
	case <-ctx.Done():
		go func() {
			for range session.Messages {
			}
		}()
		return ResidentMessageAcceptance{}, ctx.Err()
	}
}

func (b *opencodeServeBackend) releaseTurnAdmission() {
	b.noticeMu.Lock()
	defer b.noticeMu.Unlock()
	b.noticeReady = false
	b.mu.Lock()
	b.running = false
	b.mu.Unlock()
}

// RuntimeAlive implements ResidentRuntimeLivenessChecker, letting a caller
// poll process liveness between turns, not just during an in-flight one.
func (b *opencodeServeBackend) RuntimeAlive() (bool, bool) {
	return b.runtimeAlive()
}

func (b *opencodeServeBackend) EnsureResidentProcess(ctx context.Context) error {
	_, err := b.ensureServer(ctx, b.cfg.ResidentOptions)
	return err
}

// AcceptPendingNotice durably queues a content-free follow-up through
// OpenCode's v2 Session inbox. Queue delivery is the provider's safe-boundary
// contract: it is promoted only when the current drain would otherwise idle.
func (b *opencodeServeBackend) AcceptPendingNotice(ctx context.Context, notice ResidentPendingNotice) error {
	prompt, err := formatResidentPendingNotice(notice)
	if err != nil {
		return err
	}
	b.noticeMu.Lock()
	defer b.noticeMu.Unlock()
	b.mu.Lock()
	if !b.running {
		b.mu.Unlock()
		return errors.New("OpenCode Pending Notice requires an active turn")
	}
	p := b.server
	b.mu.Unlock()
	if !b.noticeReady {
		return errors.New("OpenCode Pending Notice is waiting for primary prompt admission")
	}
	if p == nil {
		return errors.New("OpenCode Pending Notice requires a live server")
	}
	p.sessionMu.Lock()
	sessionID := p.sessionID
	p.sessionMu.Unlock()
	if strings.TrimSpace(sessionID) == "" {
		return errors.New("OpenCode Pending Notice requires a live session")
	}
	if err := p.client.queuePendingNotice(ctx, sessionID, prompt); err != nil {
		return fmt.Errorf("OpenCode Pending Notice: %w", err)
	}
	return nil
}

func (b *opencodeServeBackend) runtimeAlive() (bool, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.server == nil {
		return false, false
	}
	return processAlive(b.server.cmd.Process)
}

func (b *opencodeServeBackend) executeTurn(ctx context.Context, prompt string, opts ExecOptions, msgCh chan<- Message, onAdmitted func()) Result {
	p, err := b.ensureServer(ctx, opts)
	if err != nil {
		return Result{Status: "failed", Error: err.Error()}
	}

	p.sessionMu.Lock()
	sessionID := p.sessionID
	p.sessionMu.Unlock()
	if sessionID == "" {
		parentID := ""
		if opts.ResumeSessionID != "" {
			parentID = opts.ResumeSessionID
		}
		created, err := p.client.createSession(ctx, parentID)
		if err != nil {
			tail := p.stderrBuf.Tail()
			b.disposeServer(p)
			return Result{Status: "failed", Error: withAgentStderr(fmt.Sprintf("opencode serve create session: %v", err), "opencode-serve", tail)}
		}
		sessionID = created
		p.sessionMu.Lock()
		p.sessionID = sessionID
		p.sessionMu.Unlock()
	}

	var output strings.Builder
	turn, err := p.client.runTurnWithAdmission(ctx, sessionID, prompt, opts, func() {
		b.noticeMu.Lock()
		b.noticeReady = true
		b.noticeMu.Unlock()
		if onAdmitted != nil {
			onAdmitted()
		}
	}, func(msg Message) {
		if msg.Type == MessageText {
			output.WriteString(msg.Content)
		}
		trySend(msgCh, msg)
	})
	if err != nil {
		tail := p.stderrBuf.Tail()
		b.disposeServer(p)
		if b.takeForceKilled() {
			return Result{Status: "failed", Output: output.String(), Error: AgentForceKilledMarker + ": " + err.Error(), SessionID: sessionID}
		}
		status := "failed"
		errMsg := err.Error()
		if ctx.Err() == context.DeadlineExceeded || errors.Is(err, errOpenCodeServeTurnTimeout) {
			status = "timeout"
		} else if ctx.Err() == context.Canceled {
			status = "aborted"
		} else {
			errMsg = withAgentStderr(errMsg, "opencode-serve", tail)
		}
		return Result{Status: status, Output: output.String(), Error: errMsg, SessionID: sessionID}
	}
	if turn.errMsg != "" {
		return Result{
			Status:    "failed",
			Output:    output.String(),
			Error:     withAgentStderr(turn.errMsg, "opencode-serve", p.stderrBuf.Tail()),
			SessionID: sessionID,
			Usage:     turn.usage(opts.Model),
		}
	}
	// turn.output is the reconciled GET /session/:id/message read-back, not
	// the SSE-accumulated `output` — per the design contract, it is
	// authoritative for the final text even when message.part.delta dropped,
	// duplicated, or arrived incomplete (the same class of upstream
	// delivery bug this adapter exists to be resilient to).
	return Result{
		Status:    "completed",
		Output:    turn.output,
		SessionID: sessionID,
		Usage:     turn.usage(opts.Model),
	}
}

func (b *opencodeServeBackend) ensureServer(ctx context.Context, opts ExecOptions) (*opencodeServeProcess, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.server != nil {
		return b.server, nil
	}

	execPath := b.cfg.ExecutablePath
	if execPath == "" {
		execPath = "opencode"
	}
	resolved, err := exec.LookPath(execPath)
	if err != nil {
		return nil, fmt.Errorf("opencode executable not found at %q: %w", execPath, err)
	}
	if runtime.GOOS == "windows" {
		if native := resolveOpenCodeNativeFromShim(resolved, os.Stat); native != "" {
			resolved = native
		}
	}

	port, err := reserveLocalPort()
	if err != nil {
		return nil, fmt.Errorf("opencode serve: reserve port: %w", err)
	}
	password, err := randomOpenCodeServePassword()
	if err != nil {
		return nil, fmt.Errorf("opencode serve: generate password: %w", err)
	}
	const username = "opencode"

	args := []string{"serve", "--port", strconv.Itoa(port), "--hostname", "127.0.0.1"}
	cmd := exec.CommandContext(context.Background(), resolved, args...)
	hideAgentWindow(cmd)
	if opts.Cwd != "" {
		cmd.Dir = opts.Cwd
	}

	env := buildEnv(b.cfg.Env)
	// Same project-discovery anchoring opencode.go's one-shot Execute uses:
	// OpenCode reads PWD before falling back to process.cwd() when it walks
	// for AGENTS.md / .opencode/skills.
	if opts.Cwd != "" {
		env = append(env, "PWD="+opts.Cwd)
	}
	mcpContent, err := buildOpenCodeMCPConfigContent(opts.McpConfig)
	if err != nil {
		return nil, err
	}
	if mcpContent != "" {
		env = append(env, "OPENCODE_CONFIG_CONTENT="+mcpContent)
	}
	env = append(env, "OPENCODE_SERVER_PASSWORD="+password, "OPENCODE_SERVER_USERNAME="+username)
	cmd.Env = env
	// Diagnostic-only capture: opencode prints its own "listening on..."
	// readiness signal to stdout (per its docs), not stderr — before this,
	// a failed waitReady gave zero insight into what the subprocess itself
	// was doing, because stdout was silently discarded.
	cmd.Stdout = newLogWriter(b.cfg.Logger, "[opencode-serve:stdout] ")
	// Buffered (not just logged) so a mid-turn crash can attach the
	// subprocess's own error text to the surfaced Result.Error instead of a
	// bare exit code — see stderrBuf.Tail() usage below and in executeTurn.
	stderrBuf := newStderrTail(newLogWriter(b.cfg.Logger, "[opencode-serve:stderr] "), agentStderrTailBytes)
	cmd.Stderr = stderrBuf

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start opencode serve: %w", err)
	}
	b.cfg.Logger.Info("opencode serve started", "pid", cmd.Process.Pid, "port", port)

	baseURL := "http://127.0.0.1:" + strconv.Itoa(port)
	client := newOpenCodeServeClient(baseURL, username, password, b.cfg.Logger)

	readyCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if err := client.waitReady(readyCtx); err != nil {
		// Diagnostic-only: distinguish "the process already exited" from
		// "the process is alive but never accepted a connection" — these
		// are different bugs (crash vs. hang) needing different fixes, and
		// without this the timeout error alone can't tell them apart.
		alive, known := processAlive(cmd.Process)
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		b.cfg.Logger.Warn("opencode serve readiness timeout",
			"pid", cmd.Process.Pid, "port", port, "process_alive_at_timeout", alive, "liveness_check_known", known)
		return nil, errors.New(withAgentStderr(fmt.Sprintf("opencode serve did not become ready: %v", err), "opencode-serve", stderrBuf.Tail()))
	}

	go client.runEventLoop(func(err error) {
		b.cfg.Logger.Warn("opencode serve event stream ended", "error", err)
	})

	// task #49: waitReady above only proves the HTTP server is up — it says
	// nothing about whether anything is subscribed to /event yet. Block
	// here until runEventLoop's SSE handshake actually completes, so a turn
	// can never be sent (and opencode's response events silently dropped)
	// before a listener exists to receive them. The server has already
	// answered /global/health by this point, so the SSE handshake itself
	// should be near-instant; a short, separate timeout is enough and keeps
	// a genuine hang here distinguishable from the readiness timeout above.
	connectCtx, connectCancel := context.WithTimeout(ctx, 5*time.Second)
	defer connectCancel()
	select {
	case <-client.connectedCh:
		if client.connectErr != nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
			return nil, errors.New(withAgentStderr(fmt.Sprintf("opencode serve event stream failed to connect: %v", client.connectErr), "opencode-serve", stderrBuf.Tail()))
		}
	case <-connectCtx.Done():
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return nil, errors.New(withAgentStderr("opencode serve event stream did not connect in time", "opencode-serve", stderrBuf.Tail()))
	}

	p := &opencodeServeProcess{cmd: cmd, baseURL: baseURL, password: password, username: username, client: client, stderrBuf: stderrBuf}
	b.server = p
	return p, nil
}

func (b *opencodeServeBackend) disposeServer(p *opencodeServeProcess) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.server == p {
		b.disposeServerLocked()
	}
}

func (b *opencodeServeBackend) disposeServerLocked() {
	p := b.server
	b.server = nil
	if p == nil {
		return
	}
	p.client.close()
	_ = p.cmd.Process.Kill()
	_ = p.cmd.Wait()
}

// reserveLocalPort finds a free localhost TCP port by binding then
// releasing it. There is an inherent (and accepted) race between release
// and the child binding it — this process owns the host and the window is
// a single scheduler tick, same tradeoff every "find a free port for a
// child process" helper makes.
func reserveLocalPort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}

func randomOpenCodeServePassword() (string, error) {
	buf := make([]byte, 24)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// ── HTTP+SSE client ──

var errOpenCodeServeTurnTimeout = errors.New("opencode serve turn timed out")

// opencodeServeClient talks to one `opencode serve` process. Turn
// completion is driven by the reliably-delivered `session.idle` bus event,
// never by `message.updated`/`message.part.updated` — those two event
// types silently stop being delivered over /event on OpenCode 1.14.42+
// (github.com/anomalyco/opencode#27966: DB writes succeed but the
// in-memory event bus never republishes them to SSE subscribers).
// `session.idle`, `session.diff`, `session.status`, and
// `message.part.delta` are confirmed unaffected. On session.idle this
// client polls GET /session/:id/message for the authoritative final
// content rather than trusting whatever streamed through SSE — SSE is
// the live-typing UX layer only, never the source of truth.
type opencodeServeClient struct {
	baseURL  string
	username string
	password string
	logger   *slog.Logger
	http     *http.Client

	mu       sync.Mutex
	waiters  map[string]*opencodeServeWaiter
	closed   bool
	closeCh  chan struct{}

	// connectedCh closes once runEventLoop's GET /event has received a
	// response (headers back — the SSE handshake completed and opencode has
	// registered this connection as a subscriber), or once runEventLoop
	// fails to even establish the request. task #49: waitReady only checks
	// /global/health, which says nothing about whether anyone is listening
	// on the event stream yet — opencode can process a turn and emit its
	// SSE events before this goroutine's http.Do(req) below returns,
	// silently dropping them for a reader that was never there. The
	// constructor blocks on this channel before treating the server as
	// usable, so a turn is never sent before something is actually
	// subscribed to receive its events.
	connectedCh chan struct{}
	connectErr  error
}

// opencodeServeWaiter is the per-session registration runTurn holds for the
// life of one turn: a signal channel for the terminal event (session.idle /
// session.error) and a callback for incremental message.part.delta text as
// it streams in.
type opencodeServeWaiter struct {
	signal    chan opencodeServeSessionSignal
	onMessage func(Message)
	// sawText is set by deliverText the first time this turn receives a
	// non-empty SSE text delta. reconcileFinalMessage runs unconditionally
	// after every turn (not only when SSE delivery fails), so it checks
	// this before reporting its own polled text via onMessage — otherwise
	// a turn where SSE worked normally would have its assistant reply
	// reported twice (once streamed, once again from the reconcile poll).
	sawText atomic.Bool
}

// opencodeServeSessionSignal is delivered to executeTurn's waiter when the
// session this turn owns reaches a terminal SSE signal.
type opencodeServeSessionSignal struct {
	idle    bool
	errText string
}

func newOpenCodeServeClient(baseURL, username, password string, logger *slog.Logger) *opencodeServeClient {
	// This client only ever talks to an opencode serve process we just
	// spawned on 127.0.0.1 — a same-machine control channel, never a
	// destination reachable through a proxy. Clone http.DefaultTransport
	// (keeping its tuned connection-pool/timeout defaults intact) and only
	// override Proxy, rather than relying on a host's HTTP_PROXY/NO_PROXY
	// config correctly exempting 127.0.0.1, or constructing a bare
	// &http.Transport{} that would silently drop those defaults. If this
	// disable-proxy pattern gets copied to a different client (higher
	// concurrency, non-localhost), re-check the cloned defaults still fit —
	// this Clone() is scoped to this low-traffic localhost-only use case.
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	return &opencodeServeClient{
		baseURL:     baseURL,
		username:    username,
		password:    password,
		logger:      logger,
		http:        &http.Client{Transport: transport},
		waiters:     make(map[string]*opencodeServeWaiter),
		closeCh:     make(chan struct{}),
		connectedCh: make(chan struct{}),
	}
}

func (c *opencodeServeClient) close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return
	}
	c.closed = true
	close(c.closeCh)
}

func (c *opencodeServeClient) newRequest(ctx context.Context, method, path string, body io.Reader) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, body)
	if err != nil {
		return nil, err
	}
	req.SetBasicAuth(c.username, c.password)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return req, nil
}

// waitReadyProbeTimeout bounds a single readiness probe. c.http has no
// client-wide Timeout (long-lived requests like the SSE event stream must
// not be cut off), so without a per-probe deadline here, a connection that
// succeeds but never gets a response header blocks c.http.Do for however
// long ctx has left — consuming the *entire* remaining readiness budget on
// one attempt instead of the intended 100ms retry cadence. Kept as a
// defense-in-depth bound even after fixing the actual root cause below.
const waitReadyProbeTimeout = 2 * time.Second

// waitReady polls /global/health — opencode's documented lightweight
// health-check endpoint — until the server responds or ctx is done.
//
// This used to probe /doc, which looks like a natural "is anything there"
// check but is actually the full OpenAPI 3.1 spec renderer: authenticated,
// it took ~1.9s to respond even with zero other load on the machine (vs.
// ~4ms for /global/health), confirmed via manual curl. Unauthenticated it
// fails fast (~45ms, auth middleware short-circuits before ever reaching the
// spec generator), which is why an earlier no-auth-only investigation missed
// this — this client always sends real Basic Auth, so it always paid the
// slow path. Under production load (a dozen+ other resident agent processes
// competing for CPU on the same machine), that ~1.9s stretched past our 15s
// readiness deadline and opencode chat failed with "did not become ready"
// even though the process and port were completely healthy the whole time.
func (c *opencodeServeClient) waitReady(ctx context.Context) error {
	for {
		probeCtx, cancel := context.WithTimeout(ctx, waitReadyProbeTimeout)
		req, err := c.newRequest(probeCtx, http.MethodGet, "/global/health", nil)
		if err == nil {
			resp, err := c.http.Do(req)
			if err == nil {
				resp.Body.Close()
				cancel()
				return nil
			}
		}
		cancel()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func (c *opencodeServeClient) createSession(ctx context.Context, parentID string) (string, error) {
	body := map[string]any{}
	if parentID != "" {
		body["parentID"] = parentID
	}
	payload, _ := json.Marshal(body)
	req, err := c.newRequest(ctx, http.MethodPost, "/session", strings.NewReader(string(payload)))
	if err != nil {
		return "", err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		data, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("create session: status %d: %s", resp.StatusCode, string(data))
	}
	var session struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&session); err != nil {
		return "", fmt.Errorf("create session: decode response: %w", err)
	}
	if session.ID == "" {
		return "", errors.New("create session: response had no id")
	}
	return session.ID, nil
}

type opencodeServeTurnResult struct {
	errMsg    string
	usageInfo *TokenUsage
	// output is the reconciled final text, read back from GET
	// /session/:id/message after session.idle — the authoritative source for
	// Result.Output, per the design's "never trust SSE alone" contract. Only
	// populated on the success path (reconcileFinalMessage is never called
	// when the turn ended via session.error).
	output string
}

func (r opencodeServeTurnResult) usage(model string) map[string]TokenUsage {
	if r.usageInfo == nil {
		return nil
	}
	u := *r.usageInfo
	if u.InputTokens == 0 && u.OutputTokens == 0 && u.CacheReadTokens == 0 && u.CacheWriteTokens == 0 {
		return nil
	}
	if model == "" {
		model = "unknown"
	}
	return map[string]TokenUsage{model: u}
}

// runTurn sends the prompt, waits for this session's terminal SSE signal
// (session.idle or session.error), then reconciles the final message
// content via a direct GET rather than trusting the SSE stream alone.
func (c *opencodeServeClient) runTurn(ctx context.Context, sessionID, prompt string, opts ExecOptions, onMessage func(Message)) (opencodeServeTurnResult, error) {
	return c.runTurnWithAdmission(ctx, sessionID, prompt, opts, nil, onMessage)
}

func (c *opencodeServeClient) runTurnWithAdmission(ctx context.Context, sessionID, prompt string, opts ExecOptions, onAdmitted func(), onMessage func(Message)) (opencodeServeTurnResult, error) {
	signalCh := make(chan opencodeServeSessionSignal, 1)
	c.registerWaiter(sessionID, &opencodeServeWaiter{signal: signalCh, onMessage: onMessage})
	defer c.unregisterWaiter(sessionID)

	if err := c.sendMessage(ctx, sessionID, prompt, opts); err != nil {
		return opencodeServeTurnResult{}, err
	}
	if onAdmitted != nil {
		onAdmitted()
	}

	select {
	case <-ctx.Done():
		return opencodeServeTurnResult{}, ctx.Err()
	case <-c.closeCh:
		return opencodeServeTurnResult{}, errors.New("opencode serve connection closed")
	case sig := <-signalCh:
		if sig.errText != "" {
			return opencodeServeTurnResult{errMsg: sig.errText}, nil
		}
	case <-time.After(turnWatchdogTimeout(opts)):
		return opencodeServeTurnResult{}, errOpenCodeServeTurnTimeout
	}

	return c.reconcileFinalMessage(ctx, sessionID, onMessage)
}

func turnWatchdogTimeout(opts ExecOptions) time.Duration {
	if opts.Timeout > 0 {
		return opts.Timeout
	}
	return 10 * time.Minute
}

func (c *opencodeServeClient) sendMessage(ctx context.Context, sessionID, prompt string, opts ExecOptions) error {
	body := map[string]any{
		"parts": []map[string]any{{"type": "text", "text": prompt}},
	}
	if opts.Model != "" {
		if providerID, modelID, ok := strings.Cut(opts.Model, "/"); ok {
			body["model"] = map[string]string{"providerID": providerID, "modelID": modelID}
		}
	}
	payload, _ := json.Marshal(body)
	req, err := c.newRequest(ctx, http.MethodPost, "/session/"+sessionID+"/message", strings.NewReader(string(payload)))
	if err != nil {
		return err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("send message: %w", err)
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)
	if resp.StatusCode >= 300 {
		return fmt.Errorf("send message: status %d", resp.StatusCode)
	}
	return nil
}

func (c *opencodeServeClient) queuePendingNotice(ctx context.Context, sessionID, prompt string) error {
	body := map[string]any{
		"prompt":   map[string]string{"text": prompt},
		"delivery": "queue",
		"resume":   true,
	}
	payload, _ := json.Marshal(body)
	req, err := c.newRequest(ctx, http.MethodPost, "/api/session/"+sessionID+"/prompt", strings.NewReader(string(payload)))
	if err != nil {
		return err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("queue Notice: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		data, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("queue Notice: status %d: %s", resp.StatusCode, strings.TrimSpace(string(data)))
	}
	var receipt struct {
		Data struct {
			ID        string `json:"id"`
			SessionID string `json:"sessionID"`
			Delivery  string `json:"delivery"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&receipt); err != nil {
		return fmt.Errorf("queue Notice receipt: %w", err)
	}
	if receipt.Data.ID == "" || receipt.Data.SessionID != sessionID || receipt.Data.Delivery != "queue" {
		return fmt.Errorf("queue Notice returned invalid admission receipt")
	}
	return nil
}

func (c *opencodeServeClient) reconcileFinalMessage(ctx context.Context, sessionID string, onMessage func(Message)) (opencodeServeTurnResult, error) {
	req, err := c.newRequest(ctx, http.MethodGet, "/session/"+sessionID+"/message", nil)
	if err != nil {
		return opencodeServeTurnResult{}, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return opencodeServeTurnResult{}, fmt.Errorf("reconcile: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		data, _ := io.ReadAll(resp.Body)
		return opencodeServeTurnResult{}, fmt.Errorf("reconcile: status %d: %s", resp.StatusCode, string(data))
	}
	var messages []opencodeServeMessage
	if err := json.NewDecoder(resp.Body).Decode(&messages); err != nil {
		return opencodeServeTurnResult{}, fmt.Errorf("reconcile: decode: %w", err)
	}
	if len(messages) == 0 {
		return opencodeServeTurnResult{}, nil
	}
	c.mu.Lock()
	waiter := c.waiters[sessionID]
	c.mu.Unlock()

	last := messages[len(messages)-1]
	result := opencodeServeTurnResult{}
	var text strings.Builder
	for _, part := range last.Parts {
		switch part.Type {
		case "text":
			text.WriteString(part.Text)
		case "tool":
			if part.State != nil {
				var input map[string]any
				if len(part.State.Input) > 0 {
					_ = json.Unmarshal(part.State.Input, &input)
				}
				onMessage(Message{Type: MessageToolUse, Tool: part.Tool, CallID: part.CallID, Input: input})
				if part.State.Status == "completed" {
					onMessage(Message{Type: MessageToolResult, Tool: part.Tool, CallID: part.CallID, Output: extractToolOutput(part.State.Output)})
				}
			}
		}
		if part.Tokens != nil {
			if result.usageInfo == nil {
				result.usageInfo = &TokenUsage{}
			}
			result.usageInfo.InputTokens += part.Tokens.Input
			result.usageInfo.OutputTokens += part.Tokens.Output
			if part.Tokens.Cache != nil {
				result.usageInfo.CacheReadTokens += part.Tokens.Cache.Read
				result.usageInfo.CacheWriteTokens += part.Tokens.Cache.Write
			}
		}
	}
	result.output = text.String()
	// The tool parts above are forwarded via onMessage (so the daemon's
	// message-reporting drain loop persists them as a chat_message), but
	// text was only ever accumulated into the local builder above — never
	// forwarded. reconcileFinalMessage runs unconditionally after every
	// turn (not only when SSE delivery fails), so report the reconciled
	// text via onMessage ONLY when this turn's SSE stream never delivered
	// any text delta (waiter.sawText false) — otherwise a turn where SSE
	// worked normally would have its assistant reply persisted twice.
	//
	// Known gap, deliberately not handled here (see task #49): if SSE
	// delivers a PARTIAL reply and then silently stops mid-turn, sawText
	// is already true, so this fallback does not fire — the user sees a
	// truncated reply with no indication anything was cut off. Full SSE
	// loss (the incident this fix addresses) and full SSE success are the
	// dominant cases and both are handled correctly; partial loss needs a
	// content/length comparison to fix properly and is out of scope for a
	// same-night fix on the core message-reporting path.
	if result.output != "" && (waiter == nil || !waiter.sawText.Load()) {
		onMessage(Message{Type: MessageText, Content: result.output})
	}
	return result, nil
}

func (c *opencodeServeClient) registerWaiter(sessionID string, waiter *opencodeServeWaiter) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.waiters[sessionID] = waiter
}

func (c *opencodeServeClient) unregisterWaiter(sessionID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.waiters, sessionID)
}

func (c *opencodeServeClient) deliver(sessionID string, sig opencodeServeSessionSignal) {
	c.mu.Lock()
	waiter, ok := c.waiters[sessionID]
	c.mu.Unlock()
	if ok {
		trySendOpenCodeServeSignal(waiter.signal, sig)
	}
}

// deliverText routes an incremental message.part.delta chunk to the turn
// currently waiting on sessionID, if any. Silently dropped when no turn is
// active for that session (e.g. a stray event after the turn's context was
// already cancelled).
func (c *opencodeServeClient) deliverText(sessionID, text string) {
	c.mu.Lock()
	waiter, ok := c.waiters[sessionID]
	c.mu.Unlock()
	if ok && waiter.onMessage != nil {
		if text != "" {
			waiter.sawText.Store(true)
		}
		waiter.onMessage(Message{Type: MessageText, Content: text})
	}
}

func trySendOpenCodeServeSignal(ch chan opencodeServeSessionSignal, sig opencodeServeSessionSignal) {
	select {
	case ch <- sig:
	default:
	}
}

// runEventLoop opens GET /event and dispatches session.idle / session.error
// events to whichever turn is waiting on that sessionID. onDone is called
// once the stream ends (process exit, network error, or Close()).
func (c *opencodeServeClient) runEventLoop(onDone func(error)) {
	req, err := c.newRequest(context.Background(), http.MethodGet, "/event", nil)
	if err != nil {
		c.connectErr = err
		close(c.connectedCh)
		onDone(err)
		return
	}
	resp, err := c.http.Do(req)
	if err != nil {
		c.connectErr = err
		close(c.connectedCh)
		onDone(err)
		return
	}
	// A non-2xx response (e.g. an auth or routing failure) is not a live SSE
	// subscription even though http.Do returned no transport error — treat
	// it as a failed connection rather than closing connectedCh as if a
	// listener were now registered.
	if resp.StatusCode >= 300 {
		resp.Body.Close()
		c.connectErr = fmt.Errorf("event stream: unexpected status %d", resp.StatusCode)
		close(c.connectedCh)
		onDone(c.connectErr)
		return
	}
	defer resp.Body.Close()

	// The SSE handshake completed — opencode now has this connection
	// registered as a subscriber, so any event it emits from this point
	// forward will reach the scanner loop below. This is the earliest
	// point at which sending a turn is safe (see connectedCh's doc comment
	// on why waitReady alone does not guarantee this).
	close(c.connectedCh)

	go func() {
		<-c.closeCh
		resp.Body.Close()
	}()

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "" {
			continue
		}
		c.handleEventLine(data)
	}
	onDone(scanner.Err())
}

func (c *opencodeServeClient) handleEventLine(data string) {
	var envelope struct {
		Type       string          `json:"type"`
		Properties json.RawMessage `json:"properties"`
	}
	if err := json.Unmarshal([]byte(data), &envelope); err != nil {
		return
	}
	switch envelope.Type {
	case "session.idle":
		var props struct {
			SessionID string `json:"sessionID"`
		}
		_ = json.Unmarshal(envelope.Properties, &props)
		if props.SessionID != "" {
			c.deliver(props.SessionID, opencodeServeSessionSignal{idle: true})
		}
	case "session.error":
		var props struct {
			SessionID string `json:"sessionID"`
			Error     struct {
				Data struct {
					Message string `json:"message"`
				} `json:"data"`
				Name string `json:"name"`
			} `json:"error"`
		}
		_ = json.Unmarshal(envelope.Properties, &props)
		msg := props.Error.Data.Message
		if msg == "" {
			msg = props.Error.Name
		}
		if msg == "" {
			msg = "unknown opencode error"
		}
		if props.SessionID != "" {
			c.deliver(props.SessionID, opencodeServeSessionSignal{errText: msg})
		}
	case "message.part.delta":
		var props struct {
			SessionID string `json:"sessionID"`
			Part      struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"part"`
		}
		_ = json.Unmarshal(envelope.Properties, &props)
		// Incremental live-typing UX only; final content always comes from
		// the post-idle reconcile poll, never accumulated from deltas.
		if props.SessionID != "" && props.Part.Type == "text" && props.Part.Text != "" {
			c.deliverText(props.SessionID, props.Part.Text)
		}
	}
}

// ── JSON types for the reconcile-poll response ──

type opencodeServeMessage struct {
	ID    string                     `json:"id"`
	Parts []opencodeServeMessagePart `json:"parts"`
}

type opencodeServeMessagePart struct {
	Type   string                  `json:"type"`
	Text   string                  `json:"text,omitempty"`
	Tool   string                  `json:"tool,omitempty"`
	CallID string                  `json:"callID,omitempty"`
	State  *opencodeServeToolState `json:"state,omitempty"`
	Tokens *opencodeServeTokens    `json:"tokens,omitempty"`
}

type opencodeServeToolState struct {
	Status string          `json:"status,omitempty"`
	Input  json.RawMessage `json:"input,omitempty"`
	Output any             `json:"output,omitempty"`
}

type opencodeServeTokens struct {
	Input  int64                     `json:"input"`
	Output int64                     `json:"output"`
	Cache  *opencodeServeCacheTokens `json:"cache,omitempty"`
}

type opencodeServeCacheTokens struct {
	Read  int64 `json:"read"`
	Write int64 `json:"write"`
}
