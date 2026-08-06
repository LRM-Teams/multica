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

// ErrPiRPCTurnBusy means a caller tried to overlap two turns on one native Pi
// session. The daemon owns admission; the backend must never queue unrelated
// server work behind an active conversation.
var ErrPiRPCTurnBusy = errors.New("pi RPC turn busy")

// PiRPCBackend is the daemon-owned lifecycle surface for chat sessions. Close
// is required after a failed turn, identity mismatch, idle eviction, or daemon
// shutdown so no stale native context is retained.
type PiRPCBackend interface {
	Backend
	Close()
	// Compact explicitly compacts the Pi session context with custom instructions.
	// Returns the compaction summary and before/after token counts.
	Compact(ctx context.Context, instructions string) (PiCompactionResult, error)
	// SetAutoCompaction enables or disables Pi's automatic context compaction.
	// Diagnosis sessions disable it so Multica controls compaction at segment
	// boundaries via Compact().
	SetAutoCompaction(ctx context.Context, enabled bool) error
	// RuntimeStats returns the current Pi process token/cost/context telemetry
	// outside an active prompt turn. Returns the same RuntimeTokenStats shape
	// that Execute attaches to Result, queryable between segment turns.
	RuntimeStats(ctx context.Context) (*RuntimeTokenStats, error)
}

// Compact explicitly compacts the Pi session context.
func (b *piRPCBackend) Compact(ctx context.Context, instructions string) (PiCompactionResult, error) {
	p, err := b.getProcess()
	if err != nil {
		return PiCompactionResult{}, err
	}
	resp, err := b.sendControlCommand(ctx, p, "multica-compact", map[string]any{
		"type":    "compact",
		"message": instructions,
	})
	if err != nil {
		return PiCompactionResult{}, err
	}
	if !resp.Success {
		return PiCompactionResult{}, fmt.Errorf("Pi RPC compact: %s", resp.Error)
	}
	var result PiCompactionResult
	if len(resp.Data) > 0 {
		var payload struct {
			Summary      string `json:"summary"`
			TokensBefore int    `json:"tokensBefore"`
			TokensAfter  int    `json:"tokensAfter"`
		}
		if json.Unmarshal(resp.Data, &payload) == nil {
			result = PiCompactionResult{
				Summary:      payload.Summary,
				TokensBefore: payload.TokensBefore,
				TokensAfter:  payload.TokensAfter,
			}
		}
	}
	return result, nil
}

// SetAutoCompaction enables or disables Pi's automatic compaction.
func (b *piRPCBackend) SetAutoCompaction(ctx context.Context, enabled bool) error {
	p, err := b.getProcess()
	if err != nil {
		return err
	}
	resp, err := b.sendControlCommand(ctx, p, "multica-autocompact", map[string]any{
		"type":    "set_auto_compaction",
		"enabled": enabled,
	})
	if err != nil {
		return err
	}
	if !resp.Success {
		return fmt.Errorf("Pi RPC set_auto_compaction: %s", resp.Error)
	}
	return nil
}

// RuntimeStats returns the current Pi process telemetry.
func (b *piRPCBackend) RuntimeStats(ctx context.Context) (*RuntimeTokenStats, error) {
	p, err := b.getProcess()
	if err != nil {
		return nil, err
	}
	return p.queryRuntimeStats(ctx, nil, ""), nil
}

func (b *piRPCBackend) getProcess() (*piRPCProcess, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.process == nil {
		return nil, fmt.Errorf("Pi RPC process not started")
	}
	return b.process, nil
}

// sendControlCommand writes a JSON-RPC-style command to Pi stdin and waits for
// the response by request ID. It is safe to call between or during turns;
// responses are routed through the pending map by request ID.
func (b *piRPCBackend) sendControlCommand(ctx context.Context, p *piRPCProcess, id string, command map[string]any) (piRPCResponse, error) {
	command["id"] = id
	ch := make(chan piRPCResponse, 1)
	p.stateMu.Lock()
	p.pending[id] = ch
	p.stateMu.Unlock()
	defer func() {
		p.stateMu.Lock()
		delete(p.pending, id)
		p.stateMu.Unlock()
	}()

	if err := p.writeCommand(command); err != nil {
		return piRPCResponse{}, fmt.Errorf("Pi RPC control write: %w", err)
	}
	select {
	case resp := <-ch:
		return resp, nil
	case <-ctx.Done():
		return piRPCResponse{}, ctx.Err()
	}
}

type piRPCBackend struct {
	cfg Config

	mu      sync.Mutex
	process *piRPCProcess
	running atomic.Bool
	// forceKilled is set by ForceKill() (task #62); see cursorACPBackend's
	// field of the same name for the full explanation.
	forceKilled               atomic.Bool
	afterResultPublishForTest func()
}

type piRPCProcess struct {
	cmd           *exec.Cmd
	stdin         io.WriteCloser
	sessionPath   string
	mcpConfigPath string // temp file from agent.mcp_config; removed on dispose

	writeMu sync.Mutex
	stateMu sync.Mutex
	turn    *piRPCTurn
	pending map[string]chan piRPCResponse // request-ID keyed control responses
}

type piRPCTurn struct {
	response chan piRPCResponse
	done     chan piRPCCompletion
	message  func(Message)
}

// PiCompactionResult is the outcome of an explicit compaction call.
type PiCompactionResult struct {
	Summary      string
	TokensBefore int
	TokensAfter  int
}

type piRPCResponse struct {
	ID      string          `json:"id"`
	Type    string          `json:"type"`
	Command string          `json:"command"`
	Success bool            `json:"success"`
	Error   string          `json:"error"`
	Data    json.RawMessage `json:"data"`
}

type piRPCCompletion struct {
	messages []json.RawMessage
	err      string
}

func newPiRPCBackend(cfg Config) *piRPCBackend {
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	return &piRPCBackend{cfg: cfg}
}

func NewPiRPCBackend(cfg Config) PiRPCBackend { return newPiRPCBackend(cfg) }

func (b *piRPCBackend) Close() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.process != nil {
		b.disposeLocked(b.process)
	}
}

// ForceKill implements agent.ResidentRuntimeForceKillable (task #62). Same
// shape and same reason as cursorACPBackend.ForceKill: must not call
// disposeLocked (or cmd.Wait() at all) while a turn may still be reading
// this process's stdio. Execute()'s own goroutine remains the sole
// reader/reaper, including the mcp config tempfile cleanup disposeLocked
// does — ForceKill only needs the process to die.
func (b *piRPCBackend) ForceKill() error {
	b.mu.Lock()
	p := b.process
	b.mu.Unlock()
	if p == nil {
		return nil
	}
	b.forceKilled.Store(true)
	_ = p.stdin.Close()
	return forceKillProcess(p.cmd.Process)
}

func (b *piRPCBackend) Execute(ctx context.Context, prompt string, opts ExecOptions) (*Session, error) {
	if !b.running.CompareAndSwap(false, true) {
		return nil, fmt.Errorf("%w: concurrent Pi RPC turn", ErrPiRPCTurnBusy)
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
		defer releaseAdmission()
		started := time.Now()
		result := b.executeTurn(ctx, prompt, opts, msgCh)
		result.DurationMs = time.Since(started).Milliseconds()
		// A terminal result is the caller's permission to begin the next turn.
		// Release admission before publishing it; otherwise the receiver can
		// observe completion while running is still true and get a false busy
		// error on an immediate follow-up.
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
func (b *piRPCBackend) RuntimeAlive() (bool, bool) {
	return b.runtimeAlive()
}

func (b *piRPCBackend) runtimeAlive() (bool, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.process == nil {
		return false, false
	}
	return processAlive(b.process.cmd.Process)
}

func (b *piRPCBackend) executeTurn(ctx context.Context, prompt string, opts ExecOptions, msgCh chan<- Message) Result {
	p, err := b.ensureProcess(opts)
	if err != nil {
		return Result{Status: "failed", Error: err.Error()}
	}

	var output strings.Builder
	turn := &piRPCTurn{
		response: make(chan piRPCResponse, 1),
		done:     make(chan piRPCCompletion, 1),
		message: func(msg Message) {
			if msg.Type == MessageText {
				output.WriteString(msg.Content)
			}
			trySend(msgCh, msg)
		},
	}
	p.stateMu.Lock()
	p.turn = turn
	p.stateMu.Unlock()
	defer func() {
		p.stateMu.Lock()
		if p.turn == turn {
			p.turn = nil
		}
		p.stateMu.Unlock()
	}()

	if err := p.writeCommand(map[string]any{"id": "multica-turn", "type": "prompt", "message": prompt}); err != nil {
		b.dispose(p)
		return Result{Status: "failed", Error: fmt.Sprintf("Pi RPC prompt write: %v", err)}
	}
	response, dead, ok := waitPiRPCResponse(ctx, turn, "multica-turn")
	if !ok {
		b.dispose(p)
		if dead != nil {
			// Task #65: the process died (or an out-of-order terminal event
			// arrived) before the initial prompt ack — surface readEvents'
			// actual completion (which already carries AgentForceKilledMarker
			// when applicable) instead of falling through to the generic
			// ctx-based result, which would misreport this as "aborted" even
			// though ctx was never cancelled.
			if dead.err != "" {
				return Result{Status: "failed", Error: dead.err}
			}
			return Result{Status: "failed", Error: "Pi RPC process ended before the prompt was acknowledged"}
		}
		return piRPCContextResult(ctx)
	}
	if !response.Success {
		b.dispose(p)
		return Result{Status: "failed", Error: fmt.Sprintf("Pi RPC prompt: %s", response.Error)}
	}

	select {
	case completed := <-turn.done:
		if completed.err != "" {
			b.dispose(p)
			return Result{Status: "failed", Error: completed.err}
		}
		usage := piRPCUsage(completed.messages, opts.Model)
		return Result{Status: "completed", Output: output.String(), SessionID: p.sessionPath, Usage: usage, RuntimeStats: p.queryRuntimeStats(ctx, turn, opts.Model)}
	case <-ctx.Done():
		// A cancelled RPC turn has an unknown queue/agent state. Disposing is
		// safer than sending abort and guessing whether a later event belongs
		// to the next server task.
		b.dispose(p)
		return piRPCContextResult(ctx)
	}
}

func piRPCContextResult(ctx context.Context) Result {
	if ctx.Err() == context.DeadlineExceeded {
		return Result{Status: "timeout", Error: "Pi RPC execution timed out"}
	}
	return Result{Status: "aborted", Error: "execution cancelled"}
}

func (b *piRPCBackend) ensureProcess(opts ExecOptions) (*piRPCProcess, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.process != nil {
		return b.process, nil
	}
	execName := b.cfg.ExecutablePath
	if execName == "" {
		execName = "pi"
	}
	lookedUp, err := exec.LookPath(execName)
	if err != nil {
		return nil, fmt.Errorf("pi executable not found at %q: %w", execName, err)
	}
	sessionPath := piRPCSessionPath(opts)
	if sessionPath == "" {
		var pathErr error
		sessionPath, pathErr = newPiSessionPath()
		if pathErr != nil {
			return nil, fmt.Errorf("Pi RPC session path: %w", pathErr)
		}
	}
	if err := ensurePiSessionFile(sessionPath); err != nil {
		return nil, fmt.Errorf("Pi RPC session file: %w", err)
	}
	var mcpConfigPath string
	if hasManagedMcpConfig(opts.McpConfig) {
		path, err := writeMcpConfigToTemp(opts.McpConfig)
		if err != nil {
			return nil, err
		}
		mcpConfigPath = path
		opts.piMcpConfigPath = path
	}
	args := buildPiRPCArgs(sessionPath, opts, b.cfg.Logger)
	argv0, cmdArgs := choosePiInvocation(execName, lookedUp, args, b.cfg.Logger)
	cmd := exec.Command(argv0, cmdArgs...)
	hideAgentWindow(cmd)
	if opts.Cwd != "" {
		cmd.Dir = opts.Cwd
	}
	cmd.Env = buildEnv(b.cfg.Env)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		if mcpConfigPath != "" {
			_ = os.Remove(mcpConfigPath)
		}
		return nil, fmt.Errorf("Pi RPC stdout: %w", err)
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		if mcpConfigPath != "" {
			_ = os.Remove(mcpConfigPath)
		}
		return nil, fmt.Errorf("Pi RPC stdin: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		if mcpConfigPath != "" {
			_ = os.Remove(mcpConfigPath)
		}
		return nil, fmt.Errorf("Pi RPC stderr: %w", err)
	}
	if err := cmd.Start(); err != nil {
		if mcpConfigPath != "" {
			_ = os.Remove(mcpConfigPath)
		}
		return nil, fmt.Errorf("start Pi RPC: %w", err)
	}
	p := &piRPCProcess{
		cmd:           cmd,
		stdin:         stdin,
		sessionPath:   sessionPath,
		mcpConfigPath: mcpConfigPath,
		pending:       make(map[string]chan piRPCResponse),
	}
	go b.readEvents(p, stdout)
	go func() { _, _ = io.Copy(newLogWriter(b.cfg.Logger, "[pi-rpc:stderr] "), stderr) }()
	b.cfg.Logger.Info("Pi RPC started", "pid", cmd.Process.Pid, "cwd", opts.Cwd, "model", opts.Model)
	b.process = p
	return p, nil
}

func (p *piRPCProcess) writeCommand(command map[string]any) error {
	p.writeMu.Lock()
	defer p.writeMu.Unlock()
	encoded, err := json.Marshal(command)
	if err != nil {
		return err
	}
	_, err = io.WriteString(p.stdin, string(encoded)+"\n")
	return err
}

func (b *piRPCBackend) readEvents(p *piRPCProcess, stdout io.Reader) {
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 1024*1024), 32*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var response piRPCResponse
		if json.Unmarshal([]byte(line), &response) == nil && response.Type == "response" {
			p.stateMu.Lock()
			// Route by request ID: control commands go to the pending map,
			// prompt-turn responses go to the active turn.
			if ch, ok := p.pending[response.ID]; ok {
				delete(p.pending, response.ID)
				p.stateMu.Unlock()
				trySendPiRPCResponse(ch, response)
				continue
			}
			turn := p.turn
			p.stateMu.Unlock()
			if turn != nil {
				trySendPiRPCResponse(turn.response, response)
			}
			continue
		}
		var event piRPCEvent
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			continue
		}
		p.stateMu.Lock()
		turn := p.turn
		p.stateMu.Unlock()
		if turn == nil {
			continue
		}
		piRPCDispatchEvent(event, turn)
	}
	p.stateMu.Lock()
	turn := p.turn
	p.stateMu.Unlock()
	if turn != nil {
		exitErr := "Pi RPC process exited before agent_end"
		if b.forceKilled.CompareAndSwap(true, false) {
			exitErr = AgentForceKilledMarker + ": " + exitErr
		}
		trySendPiRPCCompletion(turn.done, piRPCCompletion{err: exitErr})
	}
}

type piRPCEvent struct {
	piStreamEvent
	Messages []json.RawMessage `json:"messages,omitempty"`
}

func piRPCDispatchEvent(event piRPCEvent, turn *piRPCTurn) {
	switch event.Type {
	case "agent_start":
		turn.message(Message{Type: MessageStatus, Status: "running"})
	case "message_update":
		if event.AssistantMessageEvent == nil {
			return
		}
		switch event.AssistantMessageEvent.Type {
		case "text_delta":
			if event.AssistantMessageEvent.Delta != "" {
				turn.message(Message{Type: MessageText, Content: stripPiToolCallMarkup(event.AssistantMessageEvent.Delta)})
			}
		case "thinking_delta":
			turn.message(Message{Type: MessageThinking, Content: event.AssistantMessageEvent.Delta})
		}
	case "tool_execution_start":
		var params map[string]any
		_ = json.Unmarshal(event.Args, &params)
		turn.message(Message{Type: MessageToolUse, Tool: event.ToolName, CallID: event.ToolCallID, Input: params})
	case "tool_execution_end":
		turn.message(Message{Type: MessageToolResult, CallID: event.ToolCallID, Output: decodePiResult(event.Result)})
	case "error":
		trySendPiRPCCompletion(turn.done, piRPCCompletion{err: decodePiString(event.Message)})
	case "agent_end":
		trySendPiRPCCompletion(turn.done, piRPCCompletion{messages: event.Messages})
	}
}

// waitPiRPCResponse waits for the id-matching response, or bails out early if
// either ctx is done or turn.done fires first. Task #65: turn.done firing
// here means readEvents' terminal signal (process exit, or an "error"/
// "agent_end" event) landed before the id-matching response arrived — most
// notably, ForceKill() killing the process during this wait doesn't cancel
// ctx, so without this case the wait only unblocks via ctx.Done(), which may
// have no deadline at all. The returned *piRPCCompletion is non-nil exactly
// when turn.done was the reason for returning, so callers can surface its
// actual error (which already carries AgentForceKilledMarker when
// applicable) instead of guessing from ctx.Err().
func waitPiRPCResponse(ctx context.Context, turn *piRPCTurn, id string) (piRPCResponse, *piRPCCompletion, bool) {
	for {
		select {
		case response := <-turn.response:
			if response.ID == id {
				return response, nil, true
			}
		case completion := <-turn.done:
			return piRPCResponse{}, &completion, false
		case <-ctx.Done():
			return piRPCResponse{}, nil, false
		}
	}
}

const piRPCRuntimeStatsQueryTimeout = 300 * time.Millisecond

// queryPiRPCResponse waits for a single response by request ID from the pending
// map. It registers a channel, writes the command, waits for the response, then
// cleans up.
func (p *piRPCProcess) queryPiRPCResponse(ctx context.Context, id string, command map[string]any) (piRPCResponse, bool) {
	command["id"] = id
	ch := make(chan piRPCResponse, 1)
	p.stateMu.Lock()
	p.pending[id] = ch
	p.stateMu.Unlock()
	defer func() {
		p.stateMu.Lock()
		delete(p.pending, id)
		p.stateMu.Unlock()
	}()
	if err := p.writeCommand(command); err != nil {
		return piRPCResponse{}, false
	}
	select {
	case resp := <-ch:
		return resp, true
	case <-ctx.Done():
		return piRPCResponse{}, false
	}
}

func (p *piRPCProcess) queryRuntimeStats(ctx context.Context, turn *piRPCTurn, fallbackModel string) *RuntimeTokenStats {
	statsCtx, cancel := context.WithTimeout(ctx, piRPCRuntimeStatsQueryTimeout)
	defer cancel()

	// When called during a turn, use the turn's response channel for backward
	// compatibility. When called outside a turn (turn == nil), use the pending map.
	if turn != nil {
		if err := p.writeCommand(map[string]any{"id": "multica-stats", "type": "get_session_stats"}); err != nil {
			return nil
		}
		response, _, ok := waitPiRPCResponse(statsCtx, turn, "multica-stats")
		if !ok || !response.Success || len(response.Data) == 0 {
			return nil
		}
		stats := parsePiRPCSessionStats(response.Data, fallbackModel)
		if stats == nil {
			return nil
		}
		if err := p.writeCommand(map[string]any{"id": "multica-state", "type": "get_state"}); err != nil {
			return stats
		}
		stateResponse, _, ok := waitPiRPCResponse(statsCtx, turn, "multica-state")
		if ok && stateResponse.Success && len(stateResponse.Data) > 0 {
			applyPiRPCStateStats(stats, stateResponse.Data)
		}
		return stats
	}

	// Outside a turn: use the pending map.
	response, ok := p.queryPiRPCResponse(statsCtx, "multica-stats", map[string]any{"type": "get_session_stats"})
	if !ok || !response.Success || len(response.Data) == 0 {
		return nil
	}
	stats := parsePiRPCSessionStats(response.Data, fallbackModel)
	if stats == nil {
		return nil
	}
	stateResponse, ok := p.queryPiRPCResponse(statsCtx, "multica-state", map[string]any{"type": "get_state"})
	if ok && stateResponse.Success && len(stateResponse.Data) > 0 {
		applyPiRPCStateStats(stats, stateResponse.Data)
	}
	return stats
}

type piRPCContextUsage struct {
	Tokens        *int64   `json:"tokens"`
	ContextWindow *int64   `json:"contextWindow"`
	Percent       *float64 `json:"percent"`
}

func parsePiRPCSessionStats(raw json.RawMessage, fallbackModel string) *RuntimeTokenStats {
	var payload struct {
		Tokens struct {
			Input      int64 `json:"input"`
			Output     int64 `json:"output"`
			CacheRead  int64 `json:"cacheRead"`
			CacheWrite int64 `json:"cacheWrite"`
			Total      int64 `json:"total"`
		} `json:"tokens"`
		Cost         *float64           `json:"cost"`
		ContextUsage *piRPCContextUsage `json:"contextUsage"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil
	}
	if payload.Tokens.Total == 0 && payload.Tokens.Input == 0 && payload.Tokens.Output == 0 && payload.ContextUsage == nil && payload.Cost == nil {
		return nil
	}
	stats := &RuntimeTokenStats{
		Provider:         "pi",
		Model:            fallbackModel,
		InputTokens:      payload.Tokens.Input,
		OutputTokens:     payload.Tokens.Output,
		CacheReadTokens:  payload.Tokens.CacheRead,
		CacheWriteTokens: payload.Tokens.CacheWrite,
		TotalTokens:      payload.Tokens.Total,
		CostUSD:          payload.Cost,
	}
	if payload.ContextUsage != nil {
		stats.ContextTokens = payload.ContextUsage.Tokens
		stats.ContextWindow = payload.ContextUsage.ContextWindow
		stats.ContextPercent = payload.ContextUsage.Percent
	}
	return stats
}

func applyPiRPCStateStats(stats *RuntimeTokenStats, raw json.RawMessage) {
	var payload struct {
		AutoCompactionEnabled *bool `json:"autoCompactionEnabled"`
	}
	if json.Unmarshal(raw, &payload) == nil && payload.AutoCompactionEnabled != nil {
		stats.AutoCompactionEnabled = payload.AutoCompactionEnabled
	}
}

func piRPCUsage(messages []json.RawMessage, fallbackModel string) map[string]TokenUsage {
	usage := make(map[string]TokenUsage)
	for _, raw := range messages {
		message := decodePiMessage(raw)
		if message == nil || message.Usage == nil {
			continue
		}
		model := message.Model
		if model == "" {
			model = fallbackModel
		}
		if model == "" {
			model = "unknown"
		}
		current := usage[model]
		current.InputTokens += message.Usage.Input
		current.OutputTokens += message.Usage.Output
		current.CacheReadTokens += message.Usage.CacheRead
		current.CacheWriteTokens += message.Usage.CacheWrite
		usage[model] = current
	}
	return usage
}

func piRPCSessionPath(opts ExecOptions) string { return opts.ResumeSessionID }

func (b *piRPCBackend) dispose(p *piRPCProcess) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.disposeLocked(p)
}

func (b *piRPCBackend) disposeLocked(p *piRPCProcess) {
	if b.process == p {
		b.process = nil
	}
	_ = p.stdin.Close()
	if p.cmd.Process != nil {
		_ = p.cmd.Process.Kill()
	}
	_ = p.cmd.Wait()
	if p.mcpConfigPath != "" {
		_ = os.Remove(p.mcpConfigPath)
	}
}

func trySendPiRPCResponse(ch chan<- piRPCResponse, response piRPCResponse) {
	select {
	case ch <- response:
	default:
	}
}

func trySendPiRPCCompletion(ch chan<- piRPCCompletion, completion piRPCCompletion) {
	select {
	case ch <- completion:
	default:
	}
}

func buildPiRPCArgs(sessionPath string, opts ExecOptions, logger *slog.Logger) []string {
	args := []string{"--mode", "rpc", "--session", sessionPath}
	if opts.Model != "" {
		provider, model := splitPiModel(opts.Model)
		if provider != "" {
			args = append(args, "--provider", provider)
		}
		if model != "" {
			args = append(args, "--model", model)
		}
	}
	if opts.ThinkingLevel != "" {
		args = append(args, "--thinking", opts.ThinkingLevel)
	}
	if opts.DisableTools {
		args = append(args,
			"--no-extensions",
			"--no-skills",
			"--no-prompt-templates",
			"--no-context-files",
			"--no-approve",
			"--tools", "",
		)
		if len(opts.TrustedExtensionPaths) > 0 {
			for _, p := range opts.TrustedExtensionPaths {
				args = append(args, "--extension", p)
			}
		}
	}
	if opts.SystemPrompt != "" {
		args = append(args, "--append-system-prompt", opts.SystemPrompt)
	}
	if path := strings.TrimSpace(opts.piMcpConfigPath); path != "" {
		args = append(args, "--mcp-config", path)
	}
	return append(args, filterPiCustomArgs(opts, logger)...)
}
