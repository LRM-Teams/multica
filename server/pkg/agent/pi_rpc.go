package agent

import (
	"bufio"
	"context"
	"crypto/sha256"
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

	"github.com/google/uuid"
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
	ResidentReminderInputReceiver
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

// PrepareMessageInput runs proactive compaction before the daemon starts its
// native Message-acceptance timeout. A failed compaction remains a failed gate;
// callers must not silently inject the Message into an overfull context.
func (b *piRPCBackend) PrepareMessageInput(ctx context.Context, emit func(Message)) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !b.hasProcess() {
		return nil
	}
	p, err := b.getProcess()
	if err != nil || !shouldProactivelyCompact(p.queryRuntimeStats(ctx, nil, b.cfg.ResidentOptions.Model)) {
		return err
	}
	if emit != nil {
		emit(Message{Type: MessageCompactionStarted})
	}
	compacted, err := b.Compact(ctx, proactiveContextCompactionInstructions)
	if err != nil {
		return err
	}
	if emit != nil {
		emit(Message{Type: MessageCompactionFinished, Content: compacted.Summary})
	}
	return nil
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

type PiRunIdentity struct {
	RunID      string
	RunAgentID string
}

type PiRunBinding struct {
	PiRunIdentity
	SessionID       string
	CaptureBoundary string
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
	running        atomic.Bool
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

// piCaptureRecord is emitted by the daemon-created Pi extension. Its payload
// is intentionally parsed and redacted by Go before it can leave the daemon.
type piCaptureRecord struct {
	Kind    string          `json:"kind"`
	At      string          `json:"at"`
	Payload json.RawMessage `json:"payload,omitempty"`
	Message json.RawMessage `json:"message,omitempty"`
}

func buildResidentTurnCapture(binding PiRunBinding, turnOrdinal, firstCallOrdinal int64, records []piCaptureRecord) (ResidentTurnCapture, error) {
	var requests []piCaptureRecord
	var finalMessages []piCaptureRecord
	for _, record := range records {
		switch record.Kind {
		case "provider_request":
			requests = append(requests, record)
		case "turn_end":
			finalMessages = append(finalMessages, record)
		}
	}
	if len(requests) == 0 || len(requests) != len(finalMessages) {
		return ResidentTurnCapture{}, fmt.Errorf("incomplete Pi capture: provider_requests=%d final_messages=%d", len(requests), len(finalMessages))
	}
	if turnOrdinal <= 0 || firstCallOrdinal <= 0 {
		return ResidentTurnCapture{}, errors.New("Pi capture requires positive turn and call ordinals")
	}
	capture := residentTurnCaptureIdentity(binding, turnOrdinal)
	turnID := capture.TurnID
	for index, requestRecord := range requests {
		request, err := redactPiCaptureJSON(requestRecord.Payload)
		if err != nil {
			return ResidentTurnCapture{}, fmt.Errorf("redact provider request %d: %w", index, err)
		}
		finalMessage, err := redactPiCaptureJSON(finalMessages[index].Message)
		if err != nil {
			return ResidentTurnCapture{}, fmt.Errorf("redact final assistant message %d: %w", index, err)
		}
		var metadata struct {
			Provider string `json:"provider"`
			Model    string `json:"model"`
		}
		_ = json.Unmarshal(request, &metadata)
		var final struct {
			StopReason string `json:"stopReason"`
		}
		_ = json.Unmarshal(finalMessage, &final)
		startedAt, err := time.Parse(time.RFC3339Nano, requestRecord.At)
		if err != nil {
			return ResidentTurnCapture{}, fmt.Errorf("parse provider request timestamp %d: %w", index, err)
		}
		completedAt, err := time.Parse(time.RFC3339Nano, finalMessages[index].At)
		if err != nil {
			return ResidentTurnCapture{}, fmt.Errorf("parse final assistant timestamp %d: %w", index, err)
		}
		if capture.StartedAt.IsZero() || startedAt.Before(capture.StartedAt) {
			capture.StartedAt = startedAt
		}
		if completedAt.After(capture.CompletedAt) {
			capture.CompletedAt = completedAt
		}
		callOrdinal := firstCallOrdinal + int64(index)
		callID := uuid.NewSHA1(uuid.NameSpaceURL, []byte(turnID+":call:"+fmt.Sprint(callOrdinal))).String()
		capture.ProviderCalls = append(capture.ProviderCalls, ResidentProviderCallCapture{
			CallID:                callID,
			CallOrdinal:           callOrdinal,
			Provider:              metadata.Provider,
			Model:                 metadata.Model,
			APIKind:               "messages",
			RawProviderRequest:    request,
			FinalAssistantMessage: finalMessage,
			Status:                "completed",
			StopReason:            final.StopReason,
			ResponseComplete:      true,
			RequestHash:           sha256JSON(request),
			ResponseHash:          sha256JSON(finalMessage),
			StartedAt:             startedAt,
			CompletedAt:           completedAt,
		})
	}
	integrity, err := json.Marshal(struct {
		RunID           string                        `json:"run_id"`
		RunAgentID      string                        `json:"run_agent_id"`
		SessionID       string                        `json:"pi_session_id"`
		CaptureBoundary string                        `json:"capture_boundary"`
		TurnID          string                        `json:"turn_id"`
		TurnOrdinal     int64                         `json:"turn_ordinal"`
		ProviderCalls   []ResidentProviderCallCapture `json:"provider_calls"`
	}{capture.RunID, capture.RunAgentID, capture.PiSessionID, capture.CaptureBoundary, capture.TurnID, capture.TurnOrdinal, capture.ProviderCalls})
	if err != nil {
		return ResidentTurnCapture{}, fmt.Errorf("canonicalize Pi capture: %w", err)
	}
	capture.PayloadHash = sha256JSON(integrity)
	capture.Complete = true
	return capture, nil
}

func residentTurnCaptureIdentity(binding PiRunBinding, turnOrdinal int64) ResidentTurnCapture {
	turnID := uuid.NewSHA1(uuid.NameSpaceURL, []byte(binding.RunAgentID+":turn:"+fmt.Sprint(turnOrdinal))).String()
	return ResidentTurnCapture{
		RunID:           binding.RunID,
		RunAgentID:      binding.RunAgentID,
		PiSessionID:     binding.SessionID,
		CaptureBoundary: binding.CaptureBoundary,
		TurnID:          turnID,
		CaptureBatchID:  uuid.NewSHA1(uuid.NameSpaceURL, []byte("capture:"+turnID)).String(),
		TurnOrdinal:     turnOrdinal,
	}
}

func sha256JSON(raw []byte) string {
	sum := sha256.Sum256(raw)
	return fmt.Sprintf("sha256:%x", sum[:])
}

func redactPiCaptureJSON(raw json.RawMessage) (json.RawMessage, error) {
	if !json.Valid(raw) {
		return nil, errors.New("invalid JSON")
	}
	var payload any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, err
	}
	redactPiCaptureValue(payload)
	encoded, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	return encoded, nil
}

func redactPiCaptureValue(value any) {
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			normalized := strings.ToLower(strings.ReplaceAll(key, "-", "_"))
			switch normalized {
			case "authorization", "api_key", "apikey", "access_token", "token", "password", "secret", "x_api_key":
				delete(typed, key)
			default:
				redactPiCaptureValue(child)
			}
		}
	case []any:
		for _, child := range typed {
			redactPiCaptureValue(child)
		}
	}
}

func newPiCaptureExtension() (extensionPath string, captureLogPath string, err error) {
	logFile, err := os.CreateTemp("", "multica-pi-capture-*.jsonl")
	if err != nil {
		return "", "", fmt.Errorf("create Pi capture log: %w", err)
	}
	captureLogPath = logFile.Name()
	if err := logFile.Close(); err != nil {
		_ = os.Remove(captureLogPath)
		return "", "", fmt.Errorf("close Pi capture log: %w", err)
	}
	if err := os.Chmod(captureLogPath, 0o600); err != nil {
		_ = os.Remove(captureLogPath)
		return "", "", fmt.Errorf("secure Pi capture log: %w", err)
	}
	extension, err := os.CreateTemp("", "multica-pi-capture-extension-*.mjs")
	if err != nil {
		_ = os.Remove(captureLogPath)
		return "", "", fmt.Errorf("create Pi capture extension: %w", err)
	}
	extensionPath = extension.Name()
	encodedPath, err := json.Marshal(captureLogPath)
	if err != nil {
		_ = extension.Close()
		_ = os.Remove(extensionPath)
		_ = os.Remove(captureLogPath)
		return "", "", err
	}
	source := fmt.Sprintf(`import { appendFileSync } from "node:fs";
const capturePath = %s;
const record = (kind, fields) => {
  try { appendFileSync(capturePath, JSON.stringify({ kind, at: new Date().toISOString(), ...fields }) + "\\n", { encoding: "utf8", mode: 0o600 }); } catch (_) {}
};
export default function (pi) {
  pi.on("before_provider_request", (event) => { record("provider_request", { payload: event.payload }); });
  pi.on("turn_end", (event) => { record("turn_end", { message: event.message }); });
}
`, string(encodedPath))
	if _, err := io.WriteString(extension, source); err != nil {
		_ = extension.Close()
		_ = os.Remove(extensionPath)
		_ = os.Remove(captureLogPath)
		return "", "", fmt.Errorf("write Pi capture extension: %w", err)
	}
	if err := extension.Close(); err != nil {
		_ = os.Remove(extensionPath)
		_ = os.Remove(captureLogPath)
		return "", "", fmt.Errorf("close Pi capture extension: %w", err)
	}
	if err := os.Chmod(extensionPath, 0o600); err != nil {
		_ = os.Remove(extensionPath)
		_ = os.Remove(captureLogPath)
		return "", "", fmt.Errorf("secure Pi capture extension: %w", err)
	}
	return extensionPath, captureLogPath, nil
}

func readPiCaptureRecords(path string, offset int64) ([]piCaptureRecord, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	if _, err := file.Seek(offset, io.SeekStart); err != nil {
		return nil, err
	}
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 1024), 32*1024*1024)
	var records []piCaptureRecord
	for scanner.Scan() {
		var record piCaptureRecord
		if err := json.Unmarshal(scanner.Bytes(), &record); err != nil {
			return nil, fmt.Errorf("decode Pi capture record: %w", err)
		}
		records = append(records, record)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return records, nil
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
	sessionPath, err := newPiSessionPath()
	if err != nil {
		return PiRunBinding{}, fmt.Errorf("allocate Pi run session: %w", err)
	}
	captureExtPath, captureLogPath, err := newPiCaptureExtension()
	if err != nil {
		return PiRunBinding{}, err
	}
	b.boundarySerial++
	binding := PiRunBinding{
		PiRunIdentity:   identity,
		SessionID:       sessionPath,
		CaptureBoundary: fmt.Sprintf("%s:%d", sessionPath, b.boundarySerial),
	}
	b.cfg.ResidentOptions.ResumeSessionID = sessionPath
	b.runBinding = &binding
	b.captureExtPath = captureExtPath
	b.captureLogPath = captureLogPath
	b.captureTurns = 0
	b.captureCalls = 0
	return binding, nil
}

// SettleRunTurn closes only the current capture boundary. The resident Pi

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
		_ = os.Remove(binding.SessionID)
		return PiRunBinding{}, fmt.Errorf("prepare Pi run process: %w", err)
	}
	alive, known := b.runtimeAlive()
	if known && !alive {
		b.Close()
		_ = os.Remove(binding.SessionID)
		return PiRunBinding{}, errors.New("prepare Pi run process: native process exited during startup")
	}
	return binding, nil
}

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

func (b *piRPCBackend) AcceptReminderInput(ctx context.Context, input ResidentReminderInput) (ResidentMessageAcceptance, error) {
	prompt, err := formatResidentReminderInput(input)
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
	if notice.TotalPending <= 0 || len(notice.ChangedTargets) == 0 {
		return "", errors.New("Pending Notice requires a positive total and changed targets")
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
	return "Content-free Message Notice. Concrete bodies remain Pending. Run `multica message check` at a natural breakpoint to inspect them:\n" + string(raw), nil
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

func formatResidentMessageBatch(messages []ResidentMessage) (string, error) {
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
		replyTarget := strings.TrimSpace(message.ReplyTarget)
		if replyTarget == "" {
			replyTarget = strings.TrimSpace(message.Target)
		}
		if strings.TrimSpace(message.ID) == "" || replyTarget == "" || message.Seq <= 0 {
			return "", errors.New("resident Message id, target, and positive seq are required")
		}
		if len(message.PartsJSON) > 0 && !json.Valid(message.PartsJSON) {
			return "", errors.New("resident Message parts are invalid JSON")
		}
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

func (b *piRPCBackend) runtimeAlive() (bool, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.process == nil {
		return false, false
	}
	return processAlive(b.process.cmd.Process)
}

func (b *piRPCBackend) executeTurn(ctx context.Context, prompt string, opts ExecOptions, msgCh chan<- Message) Result {
	hadResidentProcess := b.hasProcess()
	p, err := b.ensureProcess(opts)
	if err != nil {
		return Result{Status: "failed", Error: err.Error()}
	}
	if hadResidentProcess && shouldProactivelyCompact(p.queryRuntimeStats(ctx, nil, opts.Model)) {
		trySend(msgCh, Message{Type: MessageCompactionStarted})
		if compacted, err := b.Compact(ctx, proactiveContextCompactionInstructions); err != nil {
			b.cfg.Logger.Warn("proactive runtime context compaction failed; continuing turn", "provider", "pi", "error", err)
		} else {
			trySend(msgCh, Message{Type: MessageCompactionFinished, Content: compacted.Summary})
		}
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
		return Result{Status: "completed", Output: output.String(), SessionID: p.sessionPath, Usage: usage, RuntimeStats: p.queryRuntimeStats(ctx, turn, opts.Model)}
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
