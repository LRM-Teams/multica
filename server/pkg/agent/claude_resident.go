package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

var ErrClaudeResidentTurnBusy = errors.New("claude stream-json turn busy")

// ClaudeStreamJSONBackend is the canonical Claude chat transport. It keeps the
// raw Claude stream-json process alive across turns and gates busy Notice input
// on provider-observed boundaries, matching Raft's Claude lifecycle without
// depending on the claude-agent-acp adapter.
type ClaudeStreamJSONBackend interface {
	Backend
	ResidentMessageInput
	ResidentReminderInputReceiver
	ResidentPendingNoticeInput
	ResidentRuntimeLivenessChecker
	ResidentRuntimeForceKillable
	Close()
}

type claudeStreamJSONBackend struct {
	cfg Config

	startMu     sync.Mutex
	process     atomic.Pointer[claudeStreamJSONProcess]
	running     atomic.Bool
	forceKilled atomic.Bool
}

type claudeStreamJSONProcess struct {
	cmd        *exec.Cmd
	stdin      io.WriteCloser
	stdout     io.ReadCloser
	stderr     *stderrTail
	readerDone chan struct{}

	writeMu sync.Mutex
	stateMu sync.Mutex
	turn    *claudeStreamJSONTurn

	sessionID       string
	outstandingTool map[string]struct{}
	noticeReady     bool
	disposeOnce     sync.Once
}

type claudeStreamJSONTurn struct {
	started   time.Time
	opts      ExecOptions
	msgCh     chan Message
	resCh     chan Result
	done      chan error
	completed chan struct{}
	output    strings.Builder
	usage     map[string]TokenUsage
}

func newClaudeStreamJSONBackend(cfg Config) *claudeStreamJSONBackend {
	return &claudeStreamJSONBackend{cfg: cfg}
}

func NewClaudeStreamJSONBackend(cfg Config) ClaudeStreamJSONBackend {
	return newClaudeStreamJSONBackend(cfg)
}

func (b *claudeStreamJSONBackend) Close() {
	if p := b.process.Load(); p != nil {
		b.disposeProcess(p)
		<-p.readerDone
	}
}

func (b *claudeStreamJSONBackend) ForceKill() error {
	p := b.process.Load()
	if p == nil {
		return nil
	}
	b.forceKilled.Store(true)
	b.disposeProcess(p)
	return nil
}

func (b *claudeStreamJSONBackend) EnsureResidentProcess(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	_, err := b.ensureProcess(b.cfg.ResidentOptions)
	return err
}

func (b *claudeStreamJSONBackend) RuntimeAlive() (bool, bool) {
	p := b.process.Load()
	if p == nil {
		return false, false
	}
	return processAlive(p.cmd.Process)
}

func (b *claudeStreamJSONBackend) Execute(ctx context.Context, prompt string, opts ExecOptions) (*Session, error) {
	turn := &claudeStreamJSONTurn{
		started: time.Now(), opts: opts, msgCh: make(chan Message, 256),
		resCh: make(chan Result, 1), completed: make(chan struct{}), usage: make(map[string]TokenUsage),
	}
	if err := b.startTurn(ctx, prompt, opts, turn); err != nil {
		return nil, err
	}
	return &Session{Messages: turn.msgCh, Result: turn.resCh, RuntimeAlive: b.RuntimeAlive}, nil
}

func (b *claudeStreamJSONBackend) AcceptMessageBatch(ctx context.Context, messages []ResidentMessage) (ResidentMessageAcceptance, error) {
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

func (b *claudeStreamJSONBackend) AcceptReminderInput(ctx context.Context, input ResidentReminderInput) (ResidentMessageAcceptance, error) {
	prompt, err := formatResidentReminderInput(input)
	if err != nil {
		return ResidentMessageAcceptance{}, err
	}
	return b.acceptIdleInputPrompt(ctx, prompt)
}

func (b *claudeStreamJSONBackend) acceptIdleInputPrompt(ctx context.Context, prompt string) (ResidentMessageAcceptance, error) {
	done := make(chan error, 1)
	msgCh := make(chan Message, 256)
	turn := &claudeStreamJSONTurn{started: time.Now(), done: done, msgCh: msgCh, completed: make(chan struct{}), usage: make(map[string]TokenUsage)}
	if err := b.startTurn(context.Background(), prompt, b.cfg.ResidentOptions, turn); err != nil {
		return ResidentMessageAcceptance{}, err
	}
	return ResidentMessageAcceptance{Done: done, Messages: msgCh}, nil
}

// AcceptPendingNotice writes only at a provider-observed safe boundary. A
// successful stdin write is the Notice receipt; Message bodies remain Pending.
func (b *claudeStreamJSONBackend) AcceptPendingNotice(ctx context.Context, notice ResidentPendingNotice) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !b.running.Load() {
		return errors.New("Claude Pending Notice requires an active turn")
	}
	p := b.process.Load()
	if p == nil {
		return errors.New("Claude Pending Notice requires a live stream-json process")
	}
	prompt, err := formatResidentPendingNotice(notice)
	if err != nil {
		return err
	}
	p.stateMu.Lock()
	if p.turn == nil || !p.noticeReady || len(p.outstandingTool) != 0 {
		p.stateMu.Unlock()
		return errors.New("Claude Pending Notice is waiting for a safe runtime boundary")
	}
	// One boundary admits at most one Notice. A failed write tears down the
	// process; the coordinator retains debt and replays after recovery. Keep
	// stateMu through the write so a terminal result cannot clear this turn and
	// reinterpret the Notice as the first input of a newer turn.
	p.noticeReady = false
	err = p.writeUserInput(prompt)
	p.stateMu.Unlock()
	if err != nil {
		b.disposeProcess(p)
		return fmt.Errorf("Claude Pending Notice: %w", err)
	}
	return nil
}

func (b *claudeStreamJSONBackend) startTurn(ctx context.Context, prompt string, opts ExecOptions, turn *claudeStreamJSONTurn) error {
	if !b.running.CompareAndSwap(false, true) {
		return fmt.Errorf("%w: overlapping canonical turn", ErrClaudeResidentTurnBusy)
	}
	release := true
	defer func() {
		if release {
			b.running.Store(false)
		}
	}()
	p, err := b.ensureProcess(opts)
	if err != nil {
		return err
	}
	p.stateMu.Lock()
	if p.turn != nil {
		p.stateMu.Unlock()
		return fmt.Errorf("%w: native turn is active", ErrClaudeResidentTurnBusy)
	}
	p.turn = turn
	p.noticeReady = false
	p.outstandingTool = make(map[string]struct{})
	p.stateMu.Unlock()
	if err := p.writeUserInput(prompt); err != nil {
		p.stateMu.Lock()
		if p.turn == turn {
			p.turn = nil
		}
		p.stateMu.Unlock()
		b.disposeProcess(p)
		return fmt.Errorf("write Claude stream-json input: %w", err)
	}
	release = false
	if ctx != nil {
		go b.watchTurnContext(ctx, opts.Timeout, p, turn)
	}
	return nil
}

func (b *claudeStreamJSONBackend) watchTurnContext(parent context.Context, timeout time.Duration, p *claudeStreamJSONProcess, turn *claudeStreamJSONTurn) {
	ctx := parent
	cancel := func() {}
	if timeout > 0 {
		ctx, cancel = context.WithTimeout(parent, timeout)
	}
	defer cancel()
	select {
	case <-ctx.Done():
		p.stateMu.Lock()
		active := p.turn == turn
		p.stateMu.Unlock()
		if active {
			b.disposeProcess(p)
		}
	case <-turn.completed:
	case <-p.readerDone:
	}
}

func (b *claudeStreamJSONBackend) ensureProcess(opts ExecOptions) (*claudeStreamJSONProcess, error) {
	if p := b.process.Load(); p != nil {
		if alive, known := processAlive(p.cmd.Process); !known || alive {
			return p, nil
		}
	}
	b.startMu.Lock()
	defer b.startMu.Unlock()
	if p := b.process.Load(); p != nil {
		if alive, known := processAlive(p.cmd.Process); !known || alive {
			return p, nil
		}
	}
	execPath := b.cfg.ExecutablePath
	if execPath == "" {
		execPath = "claude"
	}
	if _, err := exec.LookPath(execPath); err != nil {
		return nil, fmt.Errorf("claude executable not found at %q: %w", execPath, err)
	}
	args := buildClaudeArgs(opts, b.cfg.Logger)
	var mcpPath string
	if len(opts.McpConfig) > 0 {
		path, err := writeMcpConfigToTemp(opts.McpConfig)
		if err != nil {
			return nil, err
		}
		mcpPath = path
		args = append(args, "--mcp-config", path)
	}
	cmd := exec.Command(execPath, args...)
	hideAgentWindow(cmd)
	cmd.WaitDelay = 10 * time.Second
	if opts.Cwd != "" {
		cmd.Dir = opts.Cwd
	}
	cmd.Env = buildEnv(b.cfg.Env)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("claude resident stdout: %w", err)
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("claude resident stdin: %w", err)
	}
	stderr := newStderrTail(newLogWriter(b.cfg.Logger, "[claude-resident:stderr] "), agentStderrTailBytes)
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		if mcpPath != "" {
			_ = os.Remove(mcpPath)
		}
		return nil, fmt.Errorf("start claude resident: %w", err)
	}
	p := &claudeStreamJSONProcess{
		cmd: cmd, stdin: stdin, stdout: stdout, stderr: stderr, readerDone: make(chan struct{}),
		outstandingTool: make(map[string]struct{}),
	}
	b.process.Store(p)
	go b.readProcess(p, mcpPath)
	return p, nil
}

func (p *claudeStreamJSONProcess) writeUserInput(prompt string) error {
	data, err := buildClaudeInput(prompt)
	if err != nil {
		return err
	}
	p.writeMu.Lock()
	defer p.writeMu.Unlock()
	_, err = p.stdin.Write(data)
	return err
}

func (p *claudeStreamJSONProcess) writeControlResponse(data []byte) error {
	p.writeMu.Lock()
	defer p.writeMu.Unlock()
	_, err := p.stdin.Write(data)
	return err
}

func (b *claudeStreamJSONBackend) readProcess(p *claudeStreamJSONProcess, mcpPath string) {
	defer close(p.readerDone)
	if mcpPath != "" {
		defer os.Remove(mcpPath)
	}
	scanner := bufio.NewScanner(p.stdout)
	scanner.Buffer(make([]byte, 0, 1024*1024), 10*1024*1024)
	parser := &claudeBackend{cfg: b.cfg}
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var msg claudeSDKMessage
		if err := json.Unmarshal([]byte(line), &msg); err != nil {
			continue
		}
		b.handleProcessMessage(p, parser, msg)
	}
	exitErr := p.cmd.Wait()
	if b.process.CompareAndSwap(p, nil) {
		b.finishProcessFailure(p, exitErr)
	}
}

func (b *claudeStreamJSONBackend) handleProcessMessage(p *claudeStreamJSONProcess, parser *claudeBackend, msg claudeSDKMessage) {
	p.stateMu.Lock()
	turn := p.turn
	if msg.SessionID != "" {
		p.sessionID = msg.SessionID
	}
	switch msg.Type {
	case "assistant":
		p.noticeReady = false
		for _, id := range claudeToolUseIDs(msg) {
			p.outstandingTool[id] = struct{}{}
		}
	case "user":
		for _, id := range claudeToolResultIDs(msg) {
			delete(p.outstandingTool, id)
		}
		if turn != nil && len(p.outstandingTool) == 0 {
			p.noticeReady = true
		}
	case "system":
		if msg.Subtype == "status" && msg.Status == "compacting" {
			p.noticeReady = false
		} else if msg.Subtype == "compact_boundary" && turn != nil && len(p.outstandingTool) == 0 {
			p.noticeReady = true
		}
	case "result":
		p.noticeReady = false
		p.outstandingTool = make(map[string]struct{})
		p.turn = nil
	}
	p.stateMu.Unlock()
	if turn == nil {
		return
	}
	switch msg.Type {
	case "assistant":
		if turn.msgCh != nil {
			parser.handleAssistant(msg, turn.msgCh, &turn.output, turn.usage)
		}
	case "user":
		if turn.msgCh != nil {
			parser.handleUser(msg, turn.msgCh)
		}
	case "system":
		if turn.msgCh != nil {
			if compaction, ok := claudeCompactionMessage(msg); ok {
				trySend(turn.msgCh, compaction)
			}
			trySend(turn.msgCh, Message{Type: MessageStatus, Status: "running", SessionID: msg.SessionID})
		}
	case "control_request":
		b.handleResidentControlRequest(p, msg)
	case "result":
		b.finishTurn(p, turn, msg)
	}
}

func (b *claudeStreamJSONBackend) handleResidentControlRequest(p *claudeStreamJSONProcess, msg claudeSDKMessage) {
	var req claudeControlRequestPayload
	if err := json.Unmarshal(msg.Request, &req); err != nil {
		return
	}
	var input map[string]any
	_ = json.Unmarshal(req.Input, &input)
	if input == nil {
		input = map[string]any{}
	}
	response := map[string]any{"type": "control_response", "response": map[string]any{
		"subtype": "success", "request_id": msg.RequestID,
		"response": map[string]any{"behavior": "allow", "updatedInput": input},
	}}
	data, err := json.Marshal(response)
	if err == nil {
		data = append(data, '\n')
		err = p.writeControlResponse(data)
	}
	if err != nil {
		b.disposeProcess(p)
	}
}

func (b *claudeStreamJSONBackend) finishTurn(p *claudeStreamJSONProcess, turn *claudeStreamJSONTurn, msg claudeSDKMessage) {
	status := "completed"
	errText := ""
	if msg.IsError {
		status = "failed"
		errText = msg.ResultText
	}
	if msg.ResultText != "" {
		turn.output.Reset()
		turn.output.WriteString(msg.ResultText)
	}
	if usage := claudeResultUsage(msg, turn.opts.Model); len(usage) > 0 {
		turn.usage = usage
	}
	result := Result{
		Status: status, Output: turn.output.String(), Error: errText,
		DurationMs: time.Since(turn.started).Milliseconds(), SessionID: p.sessionID, Usage: turn.usage,
	}
	b.running.Store(false)
	close(turn.completed)
	if turn.msgCh != nil {
		close(turn.msgCh)
	}
	if turn.resCh != nil {
		turn.resCh <- result
		close(turn.resCh)
	}
	if turn.done != nil {
		turn.done <- errorForResidentTurn(result)
		close(turn.done)
	}
}

func errorForResidentTurn(result Result) error {
	if result.Status == "completed" {
		return nil
	}
	if result.Error != "" {
		return errors.New(result.Error)
	}
	return fmt.Errorf("resident turn ended with status %s", result.Status)
}

func (b *claudeStreamJSONBackend) finishProcessFailure(p *claudeStreamJSONProcess, exitErr error) {
	p.stateMu.Lock()
	turn := p.turn
	p.turn = nil
	p.stateMu.Unlock()
	if turn == nil {
		return
	}
	status := "failed"
	errText := fmt.Sprintf("Claude resident process exited: %v", exitErr)
	if b.forceKilled.CompareAndSwap(true, false) {
		errText = AgentForceKilledMarker + ": " + errText
	}
	errText = withAgentStderr(errText, "claude", p.stderr.Tail())
	b.running.Store(false)
	close(turn.completed)
	result := Result{Status: status, Error: errText, DurationMs: time.Since(turn.started).Milliseconds(), SessionID: p.sessionID}
	if turn.msgCh != nil {
		close(turn.msgCh)
	}
	if turn.resCh != nil {
		turn.resCh <- result
		close(turn.resCh)
	}
	if turn.done != nil {
		turn.done <- errors.New(errText)
		close(turn.done)
	}
}

func (b *claudeStreamJSONBackend) disposeProcess(p *claudeStreamJSONProcess) {
	p.disposeOnce.Do(func() {
		_ = p.stdin.Close()
		_ = forceKillProcess(p.cmd.Process)
	})
}

func claudeToolUseIDs(msg claudeSDKMessage) []string {
	var content claudeMessageContent
	if json.Unmarshal(msg.Message, &content) != nil {
		return nil
	}
	ids := make([]string, 0)
	for _, block := range content.Content {
		if block.Type == "tool_use" && block.ID != "" {
			ids = append(ids, block.ID)
		}
	}
	return ids
}

func claudeToolResultIDs(msg claudeSDKMessage) []string {
	var content claudeMessageContent
	if json.Unmarshal(msg.Message, &content) != nil {
		return nil
	}
	ids := make([]string, 0)
	for _, block := range content.Content {
		if block.Type == "tool_result" && block.ToolUseID != "" {
			ids = append(ids, block.ToolUseID)
		}
	}
	return ids
}
