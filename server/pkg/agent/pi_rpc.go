package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
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
}

type piRPCBackend struct {
	cfg Config

	mu      sync.Mutex
	process *piRPCProcess
	running atomic.Bool
}

type piRPCProcess struct {
	cmd         *exec.Cmd
	stdin       io.WriteCloser
	sessionPath string

	writeMu sync.Mutex
	stateMu sync.Mutex
	turn    *piRPCTurn
}

type piRPCTurn struct {
	response chan piRPCResponse
	done     chan piRPCCompletion
	message  func(Message)
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

func (b *piRPCBackend) Execute(ctx context.Context, prompt string, opts ExecOptions) (*Session, error) {
	if !b.running.CompareAndSwap(false, true) {
		return nil, fmt.Errorf("%w: concurrent Pi RPC turn", ErrPiRPCTurnBusy)
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
	return &Session{Messages: msgCh, Result: resCh}, nil
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
	response, ok := waitPiRPCResponse(ctx, turn, "multica-turn")
	if !ok {
		b.dispose(p)
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
		return nil, fmt.Errorf("Pi RPC stdout: %w", err)
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("Pi RPC stdin: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("Pi RPC stderr: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start Pi RPC: %w", err)
	}
	p := &piRPCProcess{cmd: cmd, stdin: stdin, sessionPath: sessionPath}
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
		trySendPiRPCCompletion(turn.done, piRPCCompletion{err: "Pi RPC process exited before agent_end"})
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

func waitPiRPCResponse(ctx context.Context, turn *piRPCTurn, id string) (piRPCResponse, bool) {
	for {
		select {
		case response := <-turn.response:
			if response.ID == id {
				return response, true
			}
		case <-ctx.Done():
			return piRPCResponse{}, false
		}
	}
}

const piRPCRuntimeStatsQueryTimeout = 300 * time.Millisecond

func (p *piRPCProcess) queryRuntimeStats(ctx context.Context, turn *piRPCTurn, fallbackModel string) *RuntimeTokenStats {
	statsCtx, cancel := context.WithTimeout(ctx, piRPCRuntimeStatsQueryTimeout)
	defer cancel()

	if err := p.writeCommand(map[string]any{"id": "multica-stats", "type": "get_session_stats"}); err != nil {
		return nil
	}
	response, ok := waitPiRPCResponse(statsCtx, turn, "multica-stats")
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
	stateResponse, ok := waitPiRPCResponse(statsCtx, turn, "multica-state")
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
	if opts.SystemPrompt != "" {
		args = append(args, "--append-system-prompt", opts.SystemPrompt)
	}
	return append(args, filterCustomArgs(opts.CustomArgs, piBlockedArgs, logger)...)
}
