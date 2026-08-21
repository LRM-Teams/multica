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

// ErrPiRPCRunIdentityRequiresFreshSession means an existing resident cannot
// adopt the requested run identity without violating the one-session-per-run
// boundary. The daemon may replace the backend only while its pool slot is idle.
var ErrPiRPCRunIdentityRequiresFreshSession = errors.New("pi RPC run identity requires a fresh session")

// PiRPCBackend is the daemon-owned lifecycle surface for chat sessions. Close
// is required after a failed turn, identity mismatch, idle eviction, or daemon
// shutdown so no stale native context is retained.
type PiRPCBackend interface {
	Backend
	ResidentMessageInput
	ResidentMessagePreparation
	ResidentIdleInboxNoticeInput
	ResidentPendingNoticeInput
	BindRunIdentity(PiRunIdentity) (PiRunBinding, error)
	PrepareRun(context.Context, PiRunIdentity) (PiRunBinding, error)
	SettleRunTurn(PiRunIdentity) error
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

// PrepareMessageInput is a delivery-path hook only. Compaction runs after a
// completed turn, not before native Message acceptance.
func (b *piRPCBackend) PrepareMessageInput(ctx context.Context, emit func(Message)) error {
	_ = emit
	return ctx.Err()
}

func (b *piRPCBackend) maybeCompactAfterTurn(p *piRPCProcess, model string, msgCh chan<- Message) {
	if p == nil || !shouldProactivelyCompactAt(p.queryRuntimeStats(context.Background(), nil, model), &b.compact) {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), postTurnCompactionTimeout)
	defer cancel()
	trySend(msgCh, Message{Type: MessageCompactionStarted})
	runMemoryFlushBeforeCompaction(processWorkingDir(p.cmd))
	compacted, err := b.Compact(ctx, proactiveContextCompactionInstructions)
	b.compact.recordAttempt(err != nil, p.queryRuntimeStats(context.Background(), nil, model))
	if err != nil {
		if b.cfg.Logger != nil {
			b.cfg.Logger.Warn("post-turn runtime context compaction failed; leaving session as-is", "provider", "pi", "error", err)
		}
		return
	}
	trySend(msgCh, Message{Type: MessageCompactionFinished, Content: compacted.Summary})
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

	mu             sync.Mutex
	process        *piRPCProcess
	runBinding     *PiRunBinding
	boundarySerial uint64
	captureLogPath string
	captureExtPath string
	captureTurns   int64
	captureCalls   int64
	compact        compactionAttemptState
	running        atomic.Bool
	// forceKilled is set by ForceKill() (task #62); see cursorACPBackend's
	// field of the same name for the full explanation.
	forceKilled               atomic.Bool
	afterResultPublishForTest func()
}

type piRPCProcess struct {
	cmd           *exec.Cmd
	stdin         io.WriteCloser
	sessionID     string
	mcpConfigPath string // temp file from agent.mcp_config; removed on dispose

	writeMu   sync.Mutex
	stateMu   sync.Mutex
	turn      *piRPCTurn
	idleInput *piRPCIdleInput
	pending   map[string]chan piRPCResponse // request-ID keyed control responses
}

type piRPCIdleInput struct {
	done             chan piRPCCompletion
	turnDone         chan error
	messages         chan Message
	captures         chan ResidentTurnCapture
	stream           *piRPCTurn
	captureOffset    int64
	captureBinding   PiRunBinding
	captureTurn      int64
	captureFirstCall int64
}

// Capture extension generation, typed JSONL events, redaction, and trusted
// ResidentTurnCapture assembly live in pi_provider_capture.go.

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

// piRPCCompletionError preserves Pi's runtime error semantics when the RPC
// stream ends normally with agent_end. Pi reports provider failures inside the
// final assistant message (stopReason=error), so treating agent_end alone as a
// successful turn would hide failures such as certificate/connection errors.
func piRPCCompletionError(completion piRPCCompletion) error {
	if message := strings.TrimSpace(completion.err); message != "" {
		return errors.New(message)
	}
	for i := len(completion.messages) - 1; i >= 0; i-- {
		message := decodePiMessage(completion.messages[i])
		if message == nil || message.StopReason != "error" {
			continue
		}
		if detail := strings.TrimSpace(message.ErrorMessage); detail != "" {
			return errors.New(detail)
		}
		return errors.New("Pi runtime error")
	}
	return nil
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

func (b *piRPCBackend) BindRunIdentity(identity PiRunIdentity) (PiRunBinding, error) {
	if strings.TrimSpace(identity.RunID) == "" || strings.TrimSpace(identity.RunAgentID) == "" {
		return PiRunBinding{}, errors.New("Pi run identity requires run_id and run_agent_id")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.runBinding != nil {
		if b.runBinding.PiRunIdentity != identity {
			return PiRunBinding{}, fmt.Errorf("%w: Pi backend is already bound to another run agent", ErrPiRPCRunIdentityRequiresFreshSession)
		}
		return *b.runBinding, nil
	}
	if b.process != nil {
		return PiRunBinding{}, fmt.Errorf("%w: Pi run identity must be bound before the resident process starts", ErrPiRPCRunIdentityRequiresFreshSession)
	}
	sessionID := newPiSessionID()
	captureExtPath, captureLogPath, err := newPiCaptureExtension()
	if err != nil {
		return PiRunBinding{}, err
	}
	b.boundarySerial++
	binding := PiRunBinding{
		PiRunIdentity:   identity,
		SessionID:       sessionID,
		CaptureBoundary: fmt.Sprintf("%s:%d", sessionID, b.boundarySerial),
	}
	b.cfg.ResidentOptions.ResumeSessionID = sessionID
	b.runBinding = &binding
	b.captureExtPath = captureExtPath
	b.captureLogPath = captureLogPath
	b.captureTurns = 0
	b.captureCalls = 0
	return binding, nil
}

// PrepareRun binds a fresh run-specific native session and starts the Pi RPC
// process without submitting a prompt. A successful return proves executable,
// session-file, environment, and process startup readiness before any canonical
// conversation input can be persisted or delivered.
func (b *piRPCBackend) PrepareRun(ctx context.Context, identity PiRunIdentity) (PiRunBinding, error) {
	binding, err := b.BindRunIdentity(identity)
	if err != nil {
		return PiRunBinding{}, err
	}
	if _, err := b.ensureProcess(b.cfg.ResidentOptions); err != nil {
		b.mu.Lock()
		if b.process == nil && b.runBinding != nil && b.runBinding.PiRunIdentity == identity {
			b.runBinding = nil
			b.cfg.ResidentOptions.ResumeSessionID = ""
			b.removeCaptureArtifactsLocked()
		}
		b.mu.Unlock()
		return PiRunBinding{}, fmt.Errorf("prepare Pi run process: %w", err)
	}
	alive, known := b.runtimeAlive()
	if known && !alive {
		b.Close()
		return PiRunBinding{}, errors.New("prepare Pi run process: native process exited during startup")
	}
	return binding, nil
}

// SettleRunTurn closes only the current capture boundary. The resident Pi
// process and native session stay alive for later message batches in this run.
func (b *piRPCBackend) SettleRunTurn(identity PiRunIdentity) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.runBinding == nil || b.runBinding.PiRunIdentity != identity {
		return errors.New("Pi turn settlement identity mismatch")
	}
	if b.running.Load() {
		return ErrPiRPCTurnBusy
	}
	b.boundarySerial++
	b.runBinding.CaptureBoundary = fmt.Sprintf("%s:%d", b.runBinding.SessionID, b.boundarySerial)
	return nil
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

// AcceptMessageBatch crosses Pi's native RPC input boundary without waiting
// for the resulting agent turn to finish. Pi's success response means the
// prompt was accepted or queued; later provider failures remain native runtime
// events and do not revoke that input receipt.
func (b *piRPCBackend) AcceptMessageBatch(ctx context.Context, messages []ResidentMessage) (ResidentMessageAcceptance, error) {
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

func (b *piRPCBackend) AcceptIdleInboxNotice(ctx context.Context, notice ResidentPendingNotice) (ResidentMessageAcceptance, error) {
	prompt, err := formatResidentPendingNotice(notice)
	if err != nil {
		return ResidentMessageAcceptance{}, err
	}
	return b.acceptIdleInputPrompt(ctx, prompt)
}

func (b *piRPCBackend) acceptIdleInputPrompt(ctx context.Context, prompt string) (ResidentMessageAcceptance, error) {
	if !b.running.CompareAndSwap(false, true) {
		return ResidentMessageAcceptance{}, fmt.Errorf("%w: idle input overlaps an active Pi RPC turn", ErrPiRPCTurnBusy)
	}
	releaseAdmission := true
	defer func() {
		if releaseAdmission {
			b.running.Store(false)
		}
	}()

	p, err := b.ensureProcess(b.cfg.ResidentOptions)
	if err != nil {
		return ResidentMessageAcceptance{}, err
	}
	b.mu.Lock()
	binding := PiRunBinding{}
	if b.runBinding != nil {
		binding = *b.runBinding
	}
	captureTurn := b.captureTurns + 1
	captureFirstCall := b.captureCalls + 1
	captureOffset := int64(0)
	if b.captureLogPath != "" {
		if info, statErr := os.Stat(b.captureLogPath); statErr != nil {
			b.mu.Unlock()
			return ResidentMessageAcceptance{}, fmt.Errorf("Pi capture log stat: %w", statErr)
		} else {
			captureOffset = info.Size()
		}
	}
	b.mu.Unlock()
	idleInput := &piRPCIdleInput{
		done:             make(chan piRPCCompletion, 1),
		turnDone:         make(chan error, 1),
		messages:         make(chan Message, 256),
		captures:         make(chan ResidentTurnCapture, 1),
		captureOffset:    captureOffset,
		captureBinding:   binding,
		captureTurn:      captureTurn,
		captureFirstCall: captureFirstCall,
	}
	idleInput.stream = &piRPCTurn{
		done: idleInput.done,
		message: func(message Message) {
			message.SessionID = p.sessionID
			trySend(idleInput.messages, message)
		},
	}
	p.stateMu.Lock()
	if p.turn != nil || p.idleInput != nil {
		p.stateMu.Unlock()
		return ResidentMessageAcceptance{}, fmt.Errorf("%w: Pi RPC native input is active", ErrPiRPCTurnBusy)
	}
	p.idleInput = idleInput
	p.stateMu.Unlock()
	clearIdleInput := func() {
		p.stateMu.Lock()
		if p.idleInput == idleInput {
			p.idleInput = nil
		}
		p.stateMu.Unlock()
	}

	response, err := b.sendControlCommand(ctx, p, "multica-message-input", map[string]any{
		"type": "prompt", "message": prompt,
	})
	if err != nil {
		clearIdleInput()
		b.dispose(p)
		return ResidentMessageAcceptance{}, fmt.Errorf("Pi RPC idle Message input: %w", err)
	}
	if !response.Success {
		clearIdleInput()
		return ResidentMessageAcceptance{}, fmt.Errorf("Pi RPC idle Message input: %s", response.Error)
	}

	// Keep native turn admission closed until Pi reports agent_end. The daemon
	// has already obtained the provider-native input receipt and may persist its
	// Context Boundary; a concurrent canonical turn must not overlap the Pi turn.
	releaseAdmission = false
	go b.finishIdleMessageInput(p, idleInput)
	return ResidentMessageAcceptance{Done: idleInput.turnDone, Messages: idleInput.messages, Capture: idleInput.captures}, nil
}

// AcceptPendingNotice queues a content-free steering input while Pi is busy.
// Pi delivers a steer after the current tool call reaches its safe boundary and
// before the next model call. The native success response is the Notice write
// receipt; it never represents concrete Message context coverage.
func (b *piRPCBackend) AcceptPendingNotice(ctx context.Context, notice ResidentPendingNotice) error {
	if !b.running.Load() {
		return errors.New("Pi RPC Pending Notice requires an active turn")
	}
	p, err := b.getProcess()
	if err != nil {
		return err
	}
	prompt, err := formatResidentPendingNotice(notice)
	if err != nil {
		return err
	}
	response, err := b.sendControlCommand(ctx, p, "multica-message-notice", map[string]any{
		"type": "prompt", "message": prompt, "streamingBehavior": "steer",
	})
	if err != nil {
		return fmt.Errorf("Pi RPC Pending Notice: %w", err)
	}
	if !response.Success {
		return fmt.Errorf("Pi RPC Pending Notice: %s", response.Error)
	}
	return nil
}

func formatResidentPendingNotice(notice ResidentPendingNotice) (string, error) {
	if notice.TotalPending < 0 || notice.PendingAppItems < 0 || notice.TotalPending == 0 && notice.PendingAppItems == 0 {
		return "", errors.New("Pending Notice requires pending Messages or App items")
	}
	if notice.TotalPending > 0 && len(notice.ChangedTargets) == 0 {
		return "", errors.New("Pending Message Notice requires changed targets")
	}
	count := 0
	seen := make(map[string]struct{}, len(notice.ChangedTargets))
	for _, target := range notice.ChangedTargets {
		if strings.TrimSpace(target.Target) == "" || target.PendingCount <= 0 {
			return "", errors.New("Pending Notice target and positive count are required")
		}
		if _, duplicate := seen[target.Target]; duplicate {
			return "", errors.New("Pending Notice targets must be unique")
		}
		seen[target.Target] = struct{}{}
		count += target.PendingCount
	}
	if count > notice.TotalPending {
		return "", errors.New("Pending Notice target counts exceed total")
	}
	raw, err := json.Marshal(notice)
	if err != nil {
		return "", fmt.Errorf("marshal Pending Notice: %w", err)
	}
	return "Content-free Inbox Notice. Concrete Message and App item bodies remain local. Run `multica inbox check` at a natural breakpoint to inspect them:\n" + string(raw), nil
}

func (b *piRPCBackend) finishIdleMessageInput(p *piRPCProcess, idleInput *piRPCIdleInput) {
	completion := <-idleInput.done
	p.stateMu.Lock()
	if p.idleInput == idleInput {
		p.idleInput = nil
	}
	p.stateMu.Unlock()
	turnErr := piRPCCompletionError(completion)
	var capture *ResidentTurnCapture
	if idleInput.captureBinding.RunID != "" {
		identity := residentTurnCaptureIdentity(idleInput.captureBinding, idleInput.captureTurn)
		capture = &identity
	}
	if turnErr == nil && idleInput.captureBinding.RunID != "" {
		b.mu.Lock()
		captureLogPath := b.captureLogPath
		b.mu.Unlock()
		if captureLogPath != "" {
			records, captureErr := readPiCaptureRecords(captureLogPath, idleInput.captureOffset)
			if captureErr == nil {
				builtCapture, buildErr := buildResidentTurnCapture(idleInput.captureBinding, idleInput.captureTurn, idleInput.captureFirstCall, records)
				if buildErr == nil {
					b.mu.Lock()
					b.captureTurns = idleInput.captureTurn
					b.captureCalls += int64(len(builtCapture.ProviderCalls))
					b.mu.Unlock()
					captureCopy := builtCapture
					capture = &captureCopy
				} else {
					b.cfg.Logger.Warn("Pi resident capture rejected", "error", buildErr)
				}
			} else {
				b.cfg.Logger.Warn("Pi resident capture unavailable", "error", captureErr)
			}
		}
	}
	if capture != nil {
		idleInput.captures <- *capture
	}
	close(idleInput.captures)
	if turnErr != nil {
		b.dispose(p)
	}
	// Dispose a failed native turn before reopening admission so a new Execute
	// cannot obtain the process while it is being torn down.
	b.running.Store(false)
	close(idleInput.messages)
	idleInput.turnDone <- turnErr
	close(idleInput.turnDone)
}

func residentMessageReplyTarget(message ResidentMessage) string {
	if replyTarget := strings.TrimSpace(message.ReplyTarget); replyTarget != "" {
		return replyTarget
	}
	return strings.TrimSpace(message.Target)
}

func isStandaloneChatDeliveryTarget(target string) bool {
	return strings.HasPrefix(strings.TrimSpace(target), "chat:")
}

func formatResidentMessageBatch(messages []ResidentMessage) (string, error) {
	if len(messages) == 0 {
		return "", errors.New("resident Message batch is empty")
	}
	standaloneCount := 0
	for _, message := range messages {
		replyTarget := residentMessageReplyTarget(message)
		if strings.TrimSpace(message.ID) == "" || replyTarget == "" || message.Seq <= 0 {
			return "", errors.New("resident Message id, target, and positive seq are required")
		}
		if len(message.PartsJSON) > 0 && !json.Valid(message.PartsJSON) {
			return "", errors.New("resident Message parts are invalid JSON")
		}
		if isStandaloneChatDeliveryTarget(replyTarget) {
			standaloneCount++
		}
	}
	if standaloneCount > 0 && standaloneCount < len(messages) {
		return "", errors.New("resident Message batch mixes standalone chat: targets with channel/DM transport targets")
	}
	if standaloneCount == len(messages) {
		// engineering-principles.md §1.5: standalone bubbles are chat turns, not
		// channel Messages. Do not frame them as Canonical Message batches with
		// CLI --target — that contract caused Notes FAB agents to call
		// `message send --target chat:…` → 400 and leave the UI waiting.
		return formatStandaloneChatTurnPrompt(messages)
	}
	return formatChannelResidentMessageBatch(messages)
}

// formatStandaloneChatTurnPrompt is the idle-wake shape for FAB / Notes bubble
// chat_session turns. Final assistant output is auto-written back.
func formatStandaloneChatTurnPrompt(messages []ResidentMessage) (string, error) {
	var b strings.Builder
	b.WriteString("Standalone Agent Chat. This is a private bubble conversation with a human — not a channel or DM Message.\n")
	b.WriteString("Reply with final assistant output only. It is written back to the bubble automatically.\n")
	b.WriteString("Do not run `multica message send` or `multica message react`. There is no channel/DM --target for this turn.\n")
	b.WriteString("Do not run Issue commands unless the human asks for Issue or project work.\n")
	b.WriteString("If `<note_chat_context>` appears, follow selective note reads (`multica notes tree` / `multica notes get`) before inventing page bodies.\n\n")
	for i, message := range messages {
		if i > 0 {
			b.WriteString("\n---\n\n")
		}
		if rc := strings.TrimSpace(message.RuntimeContext); rc != "" {
			b.WriteString(rc)
			if !strings.HasSuffix(rc, "\n") {
				b.WriteString("\n")
			}
			b.WriteString("\n")
		}
		b.WriteString("Human:\n")
		b.WriteString(strings.TrimSpace(message.Content))
		b.WriteString("\n")
	}
	return b.String(), nil
}

func formatChannelResidentMessageBatch(messages []ResidentMessage) (string, error) {
	type residentMessageInput struct {
		ID             string          `json:"id"`
		Target         string          `json:"target"`
		Seq            int64           `json:"seq"`
		Content        string          `json:"content"`
		Parts          json.RawMessage `json:"parts,omitempty"`
		RuntimeContext string          `json:"runtime_context,omitempty"`
	}
	payload := make([]residentMessageInput, 0, len(messages))
	for _, message := range messages {
		replyTarget := residentMessageReplyTarget(message)
		payload = append(payload, residentMessageInput{
			ID: message.ID, Target: replyTarget, Seq: message.Seq, Content: message.Content, Parts: message.PartsJSON, RuntimeContext: message.RuntimeContext,
		})
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal resident Message batch: %w", err)
	}
	return "Canonical Messages received while the runtime was idle. Treat these as Message context in target sequence order. Each runtime_context is scoped only to its own Message; never carry a member's personal memory into another item in the batch. " +
		"Reply visibly with `multica message send --target <target>` using each message's explicit target, or use `multica message react --message-id <id> --emoji \"...\"` for a pure acknowledgement. " +
		"Final assistant output is not delivered. Do not run Issue commands unless a message asks for Issue or project work.\n" + string(raw), nil
}

// RuntimeAlive implements ResidentRuntimeLivenessChecker, letting a caller
// poll process liveness between turns, not just during an in-flight one.
func (b *piRPCBackend) RuntimeAlive() (bool, bool) {
	return b.runtimeAlive()
}

func (b *piRPCBackend) EnsureResidentProcess(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	_, err := b.ensureProcess(b.cfg.ResidentOptions)
	return err
}

func (b *piRPCBackend) ProviderSessionID() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.process == nil {
		return ""
	}
	return b.process.sessionID
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
		if completionErr := piRPCCompletionError(completed); completionErr != nil {
			b.dispose(p)
			return Result{Status: "failed", Error: completionErr.Error()}
		}
		usage := piRPCUsage(completed.messages, opts.Model)
		stats := p.queryRuntimeStats(ctx, turn, opts.Model)
		if strings.TrimSpace(output.String()) != "" {
			b.maybeCompactAfterTurn(p, opts.Model, msgCh)
			stats = p.queryRuntimeStats(context.Background(), nil, opts.Model)
		}
		return Result{Status: "completed", Output: output.String(), SessionID: p.sessionID, Usage: usage, RuntimeStats: stats}
	case <-ctx.Done():
		// A cancelled RPC turn has an unknown queue/agent state. Disposing is
		// safer than sending abort and guessing whether a later event belongs
		// to the next server task.
		b.dispose(p)
		return piRPCContextResult(ctx)
	}
}

func (b *piRPCBackend) hasProcess() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.process != nil
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
	opts.piCaptureExtension = b.captureExtPath
	execName := b.cfg.ExecutablePath
	if execName == "" {
		execName = "pi"
	}
	lookedUp, err := exec.LookPath(execName)
	if err != nil {
		return nil, fmt.Errorf("pi executable not found at %q: %w", execName, err)
	}
	sessionID := piRPCSessionID(opts)
	if sessionID == "" {
		sessionID = newPiSessionID()
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
	args := buildPiRPCArgs(sessionID, opts, b.cfg.Logger)
	argv0, cmdArgs := choosePiInvocation(execName, lookedUp, args, b.cfg.Logger)
	cmd := exec.Command(argv0, cmdArgs...)
	hideAgentWindow(cmd)
	if opts.Cwd != "" {
		cmd.Dir = opts.Cwd
	}
	cmd.Env = buildPiEnv(b.cfg.Env)
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
		sessionID:     sessionID,
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
		idleInput := p.idleInput
		p.stateMu.Unlock()
		if turn == nil && idleInput == nil {
			continue
		}
		if turn != nil {
			piRPCDispatchEvent(event, turn)
			continue
		}
		piRPCDispatchEvent(event, idleInput.stream)
	}
	p.stateMu.Lock()
	turn := p.turn
	idleInput := p.idleInput
	p.stateMu.Unlock()
	if turn != nil {
		exitErr := "Pi RPC process exited before agent_end"
		if b.forceKilled.CompareAndSwap(true, false) {
			exitErr = AgentForceKilledMarker + ": " + exitErr
		}
		trySendPiRPCCompletion(turn.done, piRPCCompletion{err: exitErr})
	}
	if idleInput != nil {
		exitErr := "Pi RPC process exited before idle Message input completed"
		if b.forceKilled.CompareAndSwap(true, false) {
			exitErr = AgentForceKilledMarker + ": " + exitErr
		}
		trySendPiRPCCompletion(idleInput.done, piRPCCompletion{err: exitErr})
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
			// readEvents delivers lines serially. If the matching ACK and a
			// terminal event were both buffered, the ACK was observed first on
			// stdout even though select may choose done. Preserve that ordering.
			// Put agent_end back so executeTurn's later <-turn.done still sees it.
			select {
			case response := <-turn.response:
				if response.ID == id {
					trySendPiRPCCompletion(turn.done, completion)
					return response, nil, true
				}
			default:
			}
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

func piRPCSessionID(opts ExecOptions) string { return opts.ResumeSessionID }

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
	b.removeCaptureArtifactsLocked()
}

func (b *piRPCBackend) removeCaptureArtifactsLocked() {
	if b.captureExtPath != "" {
		_ = os.Remove(b.captureExtPath)
		b.captureExtPath = ""
	}
	if b.captureLogPath != "" {
		_ = os.Remove(b.captureLogPath)
		b.captureLogPath = ""
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

func buildPiRPCArgs(sessionID string, opts ExecOptions, logger *slog.Logger) []string {
	args := appendPiSessionArgs([]string{"--mode", "rpc"}, sessionID, opts.Cwd)
	if opts.Model != "" {
		// Preserve provider-prefixed model IDs so Pi can hand them through to
		// provider-aware model routers unchanged.
		args = append(args, "--model", opts.Model)
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
	if opts.piCaptureExtension != "" {
		args = append(args, "--extension", opts.piCaptureExtension)
	}
	if opts.SystemPrompt != "" {
		args = append(args, "--append-system-prompt", opts.SystemPrompt)
	}
	if path := strings.TrimSpace(opts.piMcpConfigPath); path != "" {
		args = append(args, "--mcp-config", path)
	}
	return append(args, filterPiCustomArgs(opts, logger)...)
}
