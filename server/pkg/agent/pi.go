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
	"os/user"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
)

// ErrPiCLIResidentUnsupported is returned when a caller asks the one-shot Pi
// CLI backend for mixed-run resident APIs. Use NewPiRPCBackend / AsPiRPCBackend
// instead of agent.New("pi") for persistent run identity, AcceptMessageBatch,
// and non-terminal SettleRunTurn.
var ErrPiCLIResidentUnsupported = errors.New("pi CLI backend does not support resident mixed-run APIs")

// piBackend implements Backend by spawning the Pi CLI in non-interactive
// JSON mode (`pi -p --mode json`) and parsing its event
// stream on stdout.
//
// Interface segregation: piBackend is intentionally one-shot only. It does
// not implement PiRPCBackend, ResidentMessageInput, or related resident
// surfaces. Mixed-run callers must obtain a PiRPCBackend via NewPiRPCBackend
// (or AsPiRPCBackend) so PrepareRun / AcceptMessageBatch / SettleRunTurn cannot
// be mistaken for CLI Execute semantics.
type piBackend struct {
	cfg Config
}

// Compile-time: one-shot CLI remains a Backend. Resident APIs stay on
// PiRPCBackend so type assertions fail closed for mixed runs.
var _ Backend = (*piBackend)(nil)

// AsPiRPCBackend returns the resident Pi surface. One-shot CLI backends from
// agent.New("pi") yield ErrPiCLIResidentUnsupported.
func AsPiRPCBackend(b Backend) (PiRPCBackend, error) {
	if rpc, ok := b.(PiRPCBackend); ok {
		return rpc, nil
	}
	return nil, fmt.Errorf("%w: use NewPiRPCBackend for persistent run identity and resident turns", ErrPiCLIResidentUnsupported)
}

var (
	piControlTokenRE = regexp.MustCompile(`<\|[A-Za-z0-9_-]+>[A-Za-z0-9_-]*|<[A-Za-z0-9_-]+\|>`)
)

const (
	piCodingAgentDirEnvKey = "PI_CODING_AGENT_DIR"
	piPackageDirEnvKey     = "PI_PACKAGE_DIR"
)

var piInheritedEnvBlocklist = map[string]struct{}{
	// buildPiEnv resolves this value once so custom, inherited, and default
	// sources cannot leave duplicate entries in the child environment.
	piCodingAgentDirEnvKey: {},
	// Raft's SEA launcher uses this name for its own resource root. Pi treats it
	// as an override for Pi's package root, so a host value makes Pi search the
	// Raft stub for its bundled themes instead of Pi's installed package.
	piPackageDirEnvKey: {},
}

// buildPiEnv applies Pi's provider-specific inherited-environment policy at
// the shared child-environment boundary. A runtime custom_env value is
// deliberate Pi configuration and must still win.
func buildPiEnv(extra map[string]string) []string {
	agentDir, explicitlySet := extra[piCodingAgentDirEnvKey]
	agentDir = strings.TrimSpace(agentDir)
	if !explicitlySet {
		agentDir = strings.TrimSpace(os.Getenv(piCodingAgentDirEnvKey))
	}
	if agentDir == "" {
		home := ""
		if currentUser, err := user.Current(); err == nil {
			home = strings.TrimSpace(currentUser.HomeDir)
		}
		if home == "" {
			home, _ = os.UserHomeDir()
		}
		if home != "" {
			agentDir = filepath.Join(home, ".pi", "agent")
		}
	}

	piEnv := make(map[string]string, len(extra)+1)
	for key, value := range extra {
		piEnv[key] = value
	}
	piEnv[piCodingAgentDirEnvKey] = agentDir
	return buildProviderEnv(piEnv, piInheritedEnvBlocklist)
}

func stripPiToolCallMarkup(s string) string {
	s = stripPiStructuredToolMarkup(s)
	return piControlTokenRE.ReplaceAllString(s, "")
}

func drainPiTextBuffer(buf *strings.Builder, delta string) string {
	buf.WriteString(delta)
	emit, pending := drainPiSanitizedText(buf.String())
	buf.Reset()
	buf.WriteString(pending)
	return emit
}

func flushPiTextBuffer(buf *strings.Builder) string {
	s := buf.String()
	buf.Reset()
	emit, pending := drainPiSanitizedText(s)
	emit += piControlTokenRE.ReplaceAllString(pending, "")
	return emit
}

func drainPiSanitizedText(s string) (string, string) {
	var out strings.Builder
	for i := 0; i < len(s); {
		start, prefixLen := nextPiToolMarkupPrefix(s, i)
		if start == -1 {
			safeLen := safePiTextEmitLen(s[i:])
			out.WriteString(s[i : i+safeLen])
			return piControlTokenRE.ReplaceAllString(out.String(), ""), s[i+safeLen:]
		}
		out.WriteString(s[i:start])
		end, ok := scanPiToolMarkupEnd(s, start+prefixLen)
		if !ok {
			return piControlTokenRE.ReplaceAllString(out.String(), ""), s[start:]
		}
		i = end
	}
	return piControlTokenRE.ReplaceAllString(out.String(), ""), ""
}

func stripPiStructuredToolMarkup(s string) string {
	var out strings.Builder
	for i := 0; i < len(s); {
		start, prefixLen := nextPiToolMarkupPrefix(s, i)
		if start == -1 {
			out.WriteString(s[i:])
			break
		}
		out.WriteString(s[i:start])
		end, ok := scanPiToolMarkupEnd(s, start+prefixLen)
		if !ok {
			out.WriteString(s[start:])
			break
		}
		i = end
	}
	return out.String()
}

func safePiTextEmitLen(s string) int {
	hold := 0
	for _, prefix := range []string{"call:", "response:"} {
		for n := 1; n < len(prefix) && n <= len(s); n++ {
			if strings.HasSuffix(s, prefix[:n]) && n > hold {
				hold = n
			}
		}
	}
	if i := strings.LastIndexByte(s, '<'); i >= 0 && looksLikePiControlTokenPrefix(s[i:]) {
		if len(s)-i > hold {
			hold = len(s) - i
		}
	}
	return len(s) - hold
}

func looksLikePiControlTokenPrefix(s string) bool {
	if len(s) == 0 || s[0] != '<' || len(s) > 64 {
		return false
	}
	for i := 1; i < len(s); i++ {
		b := s[i]
		if (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9') || b == '_' || b == '-' || b == '|' || b == '>' {
			continue
		}
		return false
	}
	return true
}

func nextPiToolMarkupPrefix(s string, from int) (int, int) {
	best := -1
	bestLen := 0
	for _, prefix := range []string{"call:", "response:"} {
		if i := strings.Index(s[from:], prefix); i >= 0 {
			abs := from + i
			if best == -1 || abs < best {
				best = abs
				bestLen = len(prefix)
			}
		}
	}
	return best, bestLen
}

func scanPiToolMarkupEnd(s string, i int) (int, bool) {
	nameStart := i
	for i < len(s) && isPiToolNameByte(s[i]) {
		i++
	}
	if i == nameStart || i >= len(s) || s[i] != '{' {
		return 0, false
	}

	const quoteMarker = `<|"|>`
	depth := 0
	inQuote := false
	for i < len(s) {
		if strings.HasPrefix(s[i:], quoteMarker) {
			inQuote = !inQuote
			i += len(quoteMarker)
			continue
		}

		if !inQuote {
			switch s[i] {
			case '{':
				depth++
			case '}':
				depth--
				if depth == 0 {
					i++
					if strings.HasPrefix(s[i:], "<tool_call|>") {
						i += len("<tool_call|>")
					}
					return i, true
				}
			}
		}
		i++
	}
	return 0, false
}

func isPiToolNameByte(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9') || b == '_' || b == '-'
}

func (b *piBackend) Execute(ctx context.Context, prompt string, opts ExecOptions) (*Session, error) {
	execName := b.cfg.ExecutablePath
	if execName == "" {
		execName = "pi"
	}
	lookedUp, err := exec.LookPath(execName)
	if err != nil {
		return nil, fmt.Errorf("pi executable not found at %q: %w", execName, err)
	}

	timeout := opts.Timeout
	outputLimitExtensionPath := ""
	outputLimitExtensionOwned := false
	if opts.MaxOutputTokens > 0 {
		outputLimitExtensionPath, err = newPiOutputLimitExtension(opts.MaxOutputTokens)
		if err != nil {
			return nil, err
		}
		opts.piOutputLimitExtension = outputLimitExtensionPath
		outputLimitExtensionOwned = true
	}
	defer func() {
		if outputLimitExtensionOwned && outputLimitExtensionPath != "" {
			_ = os.Remove(outputLimitExtensionPath)
		}
	}()

	// Early-complete: when the final assistant turn ends (turn_end with a
	// terminal stop reason), the model's answer and usage are already known.
	// Emitting the Result at turn_end lets the daemon mark the task done and
	// release the user-facing reply immediately. The daemon's normal cleanup
	// cancels the Pi process after this result, so expensive exit-time memory
	// work is intentionally skipped for Multica-managed runs. Synchronous memory
	// tool calls made during the turn have already completed. Opt out with
	// PI_EARLY_COMPLETE=0.
	earlyCompleteEnabled := getenvDefault("PI_EARLY_COMPLETE", "1") != "0"
	earlyCompleted := false

	// Durable Pi sessions use an opaque provider session ID. Pi resolves that
	// ID within the Agent's cwd-scoped .pi-sessions directory and creates a
	// same-ID session when no prior transcript exists.
	sessionID := opts.ResumeSessionID
	ephemeralSession := false
	if opts.EphemeralSession {
		if sessionID != "" {
			return nil, fmt.Errorf("Pi ephemeral session cannot resume %q", sessionID)
		}
		f, err := os.CreateTemp("", "multica-pi-ephemeral-*.jsonl")
		if err != nil {
			return nil, fmt.Errorf("Pi ephemeral session path: %w", err)
		}
		sessionID = f.Name()
		ephemeralSession = true
		if err := f.Close(); err != nil {
			_ = os.Remove(sessionID)
			return nil, fmt.Errorf("Pi ephemeral session file: %w", err)
		}
	} else if sessionID == "" {
		sessionID = newPiSessionID()
	}
	runCtx, cancel := runContext(ctx, timeout)

	var mcpConfigPath string
	var mcpFileCleanup func()
	if hasManagedMcpConfig(opts.McpConfig) {
		path, err := writeMcpConfigToTemp(opts.McpConfig)
		if err != nil {
			cancel()
			if ephemeralSession {
				_ = os.Remove(sessionID)
			}
			return nil, err
		}
		mcpConfigPath = path
		opts.piMcpConfigPath = path
		mcpFileCleanup = func() { os.Remove(mcpConfigPath) }
	}
	defer func() {
		if mcpFileCleanup != nil {
			mcpFileCleanup()
		}
	}()

	var args []string
	var stdinPrompt string
	if ephemeralSession {
		args, stdinPrompt = buildPiArgsForEphemeralExecution(prompt, sessionID, opts, b.cfg.Logger)
	} else {
		args, stdinPrompt = buildPiArgsForExecution(prompt, sessionID, opts, b.cfg.Logger)
	}
	argv0, cmdArgs := choosePiInvocation(execName, lookedUp, args, b.cfg.Logger)

	cmd := exec.CommandContext(runCtx, argv0, cmdArgs...)
	hideAgentWindow(cmd)
	b.cfg.Logger.Info("agent command", "exec", argv0, "args", cmdArgs)
	cmd.WaitDelay = 10 * time.Second
	if opts.Cwd != "" {
		cmd.Dir = opts.Cwd
	}
	cmd.Env = buildPiEnv(b.cfg.Env)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		if ephemeralSession {
			_ = os.Remove(sessionID)
		}
		return nil, fmt.Errorf("pi stdout pipe: %w", err)
	}
	// Attach an explicit stdin pipe and write the prompt through it on every
	// platform. Linux also caps each argv entry (MAX_ARG_STRLEN, commonly
	// 128 KiB), so a large but otherwise valid channel context can fail at
	// fork/exec with E2BIG before Pi starts. Pi accepts piped stdin as the
	// initial non-interactive prompt, keeping user content out of argv while
	// still delivering a clean EOF under systemd (#2188).
	stdin, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		if ephemeralSession {
			_ = os.Remove(sessionID)
		}
		return nil, fmt.Errorf("pi stdin pipe: %w", err)
	}
	stderrBuf := newStderrTail(newLogWriter(b.cfg.Logger, "[pi:stderr] "), agentStderrTailBytes)
	cmd.Stderr = stderrBuf

	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		cancel()
		if ephemeralSession {
			_ = os.Remove(sessionID)
		}
		return nil, fmt.Errorf("start pi: %w", err)
	}
	// Transfer temp MCP file ownership to the run goroutine (cleaned on exit).
	mcpFileCleanup = nil
	stdinWriteResult := make(chan error, 1)
	go func() {
		var writeErr error
		if stdinPrompt != "" {
			n, err := io.WriteString(stdin, stdinPrompt)
			if err != nil {
				writeErr = err
			} else if n != len(stdinPrompt) {
				writeErr = io.ErrShortWrite
			}
		}
		if err := stdin.Close(); writeErr == nil && err != nil {
			writeErr = err
		}
		if writeErr != nil {
			b.cfg.Logger.Warn("pi stdin prompt write failed", "error", writeErr)
		}
		stdinWriteResult <- writeErr
	}()

	b.cfg.Logger.Info("pi started", "pid", cmd.Process.Pid, "cwd", opts.Cwd, "model", opts.Model)

	msgCh := make(chan Message, 256)
	resCh := make(chan Result, 1)

	// Close stdout when the context is cancelled so scanner.Scan() unblocks.
	go func() {
		<-runCtx.Done()
		_ = stdout.Close()
	}()

	go func() {
		defer cancel()
		defer close(msgCh)
		defer close(resCh)
		if mcpConfigPath != "" {
			defer func() { _ = os.Remove(mcpConfigPath) }()
		}
		if outputLimitExtensionPath != "" {
			defer func() {
				if err := os.Remove(outputLimitExtensionPath); err != nil && !os.IsNotExist(err) {
					b.cfg.Logger.Warn("Pi output-limit extension cleanup failed", "path", outputLimitExtensionPath, "error", err)
				}
			}()
		}
		if ephemeralSession {
			defer func() {
				if err := os.Remove(sessionID); err != nil && !os.IsNotExist(err) {
					b.cfg.Logger.Warn("Pi ephemeral session cleanup failed", "path", sessionID, "error", err)
				}
			}()
		}

		startTime := time.Now()
		var output strings.Builder
		finalStatus := "completed"
		var finalError string
		usage := make(map[string]TokenUsage)
		sessionEstablished := false
		stdinWriteChecked := false
		var stdinWriteErr error
		awaitStdinWrite := func() error {
			if !stdinWriteChecked {
				stdinWriteErr = <-stdinWriteResult
				stdinWriteChecked = true
			}
			return stdinWriteErr
		}

		scanner := bufio.NewScanner(stdout)
		// Pi message_update events can be large (they embed the full message
		// partial on each delta), so give the scanner generous headroom.
		scanner.Buffer(make([]byte, 0, 1024*1024), 32*1024*1024)
		var textBuffer strings.Builder

		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line == "" {
				continue
			}
			var evt piStreamEvent
			if err := json.Unmarshal([]byte(line), &evt); err != nil {
				continue
			}

			switch evt.Type {
			case "agent_start":
				sessionEstablished = true
				trySend(msgCh, Message{Type: MessageStatus, Status: "running"})

			case "message_end":
				if msg := decodePiMessage(evt.Message); msg != nil {
					if msg.StopReason == "error" && finalStatus == "completed" {
						finalStatus = "failed"
						finalError = msg.ErrorMessage
						if finalError == "" {
							finalError = "pi assistant turn ended with stopReason=error"
						}
						trySend(msgCh, Message{Type: MessageError, Content: finalError})
					}
				}

			case "message_update":
				if evt.AssistantMessageEvent == nil {
					continue
				}
				switch evt.AssistantMessageEvent.Type {
				case "text_delta":
					if d := drainPiTextBuffer(&textBuffer, evt.AssistantMessageEvent.Delta); d != "" {
						output.WriteString(d)
						trySend(msgCh, Message{Type: MessageText, Content: d})
					}
				case "thinking_delta":
					if d := evt.AssistantMessageEvent.Delta; d != "" {
						trySend(msgCh, Message{Type: MessageThinking, Content: d})
					}
				}

			case "tool_execution_start":
				var params map[string]any
				if len(evt.Args) > 0 {
					_ = json.Unmarshal(evt.Args, &params)
				}
				trySend(msgCh, Message{
					Type:   MessageToolUse,
					Tool:   evt.ToolName,
					CallID: evt.ToolCallID,
					Input:  params,
				})

			case "tool_execution_end":
				trySend(msgCh, Message{
					Type:   MessageToolResult,
					CallID: evt.ToolCallID,
					Output: decodePiResult(evt.Result),
				})

			case "turn_end":
				turnStopReason := ""
				if msg := decodePiMessage(evt.Message); msg != nil {
					turnStopReason = msg.StopReason
					if msg.Usage != nil {
						model := msg.Model
						if model == "" {
							model = opts.Model
						}
						if model == "" {
							model = "unknown"
						}
						u := usage[model]
						u.InputTokens += msg.Usage.Input
						u.OutputTokens += msg.Usage.Output
						u.CacheReadTokens += msg.Usage.CacheRead
						u.CacheWriteTokens += msg.Usage.CacheWrite
						usage[model] = u
					}
					if msg.StopReason == "error" && finalStatus == "completed" {
						finalStatus = "failed"
						finalError = msg.ErrorMessage
						if finalError == "" {
							finalError = "pi assistant turn ended with stopReason=error"
						}
					}
				}

				// Early-complete: a terminal turn_end means the model finished
				// answering and usage is accumulated above. Flush any pending
				// text and emit the Result now so the daemon can mark the task
				// done without waiting for Pi's exit-time cleanup. Errors
				// (stopReason=error) still flip finalStatus above and are
				// reported. We emit exactly once.
				//
				// A turn that ended to call a tool (stopReason=toolUse) is NOT
				// terminal: the model still owes a follow-up turn once the tool
				// result comes back (e.g. the `multica send` bash call that
				// actually delivers a chat reply). Early-completing here makes
				// the daemon kill Pi mid-loop, truncating the run before the
				// model answers — this is the "先查后答" silent-DM bug where a
				// message that first triggers web_search/help lookup never gets
				// a visible reply. Gate on a strict terminal allowlist so only a
				// genuinely finished turn (stopReason=stop) early-completes; any
				// tool-use/unseen reason falls through and the loop continues.
				if earlyCompleteEnabled && !earlyCompleted && finalStatus != "failed" && piStopReasonAllowsEarlyComplete(turnStopReason) {
					if err := awaitStdinWrite(); err != nil {
						finalStatus = "failed"
						finalError = fmt.Sprintf("pi stdin prompt delivery failed: %v", err)
						trySend(msgCh, Message{Type: MessageError, Content: finalError})
						continue
					}
					if d := flushPiTextBuffer(&textBuffer); d != "" {
						output.WriteString(d)
						trySend(msgCh, Message{Type: MessageText, Content: d})
					}
					earlyCompleted = true
					b.cfg.Logger.Info("pi early-complete", "pid", cmd.Process.Pid, "duration", time.Since(startTime).Round(time.Millisecond).String())
					finalStatus, finalError = enforcePiOutputTokenLimit(finalStatus, finalError, usage, opts.MaxOutputTokens)
					resCh <- Result{
						Status:     finalStatus,
						Output:     output.String(),
						Error:      finalError,
						DurationMs: time.Since(startTime).Milliseconds(),
						SessionID:  piReportedSessionID(sessionID, ephemeralSession),
						Usage:      usage,
					}
				}

			case "error":
				errText := decodePiString(evt.Message)
				trySend(msgCh, Message{Type: MessageError, Content: errText})
				if finalStatus == "completed" {
					finalStatus = "failed"
					finalError = errText
				}

			case "auto_retry_end":
				if !evt.Success && finalStatus == "completed" {
					finalStatus = "failed"
					if evt.FinalError != "" {
						finalError = evt.FinalError
					} else {
						finalError = "pi exhausted automatic retries"
					}
				}
			}
		}
		if d := flushPiTextBuffer(&textBuffer); d != "" {
			output.WriteString(d)
			trySend(msgCh, Message{Type: MessageText, Content: d})
		}

		waitErr := cmd.Wait()
		duration := time.Since(startTime)

		if earlyCompleted {
			// We already emitted the Result at turn_end. The daemon may cancel
			// the process immediately after receiving that result; either way,
			// do not overwrite the already-sent user-facing success.
			b.cfg.Logger.Info("pi finished (after early-complete)", "pid", cmd.Process.Pid, "waitErr", waitErr, "duration", duration.Round(time.Millisecond).String())
			return
		}

		if runCtx.Err() == context.DeadlineExceeded {
			finalStatus = "timeout"
			finalError = fmt.Sprintf("pi timed out after %s", timeout)
		} else if runCtx.Err() == context.Canceled {
			finalStatus = "aborted"
			finalError = "execution cancelled"
		} else if waitErr != nil && finalStatus == "completed" {
			finalStatus = "failed"
			finalError = withAgentStderr(fmt.Sprintf("pi exited with error: %v", waitErr), "pi", stderrBuf.Tail())
		} else if finalStatus == "failed" {
			finalError = withAgentStderr(finalError, "pi", stderrBuf.Tail())
		}
		if err := awaitStdinWrite(); err != nil && finalStatus == "completed" {
			finalStatus = "failed"
			finalError = fmt.Sprintf("pi stdin prompt delivery failed: %v", err)
		}

		b.cfg.Logger.Info("pi finished", "pid", cmd.Process.Pid, "status", finalStatus, "duration", duration.Round(time.Millisecond).String())

		reportedSessionID := piReportedSessionID(sessionID, ephemeralSession)
		if finalStatus == "failed" && !sessionEstablished {
			// A resume failure before Pi announces agent_start means no usable
			// session was established. Returning the requested ID here makes
			// the daemon believe resume succeeded and suppresses its one-shot
			// fresh-session retry.
			reportedSessionID = ""
		}

		finalStatus, finalError = enforcePiOutputTokenLimit(finalStatus, finalError, usage, opts.MaxOutputTokens)
		resCh <- Result{
			Status:     finalStatus,
			Output:     output.String(),
			Error:      finalError,
			DurationMs: duration.Milliseconds(),
			SessionID:  reportedSessionID,
			Usage:      usage,
		}
	}()

	outputLimitExtensionOwned = false
	return &Session{Messages: msgCh, Result: resCh, RuntimeAlive: processLiveness(cmd.Process)}, nil
}

// ── Pi event types ──

// piStreamEvent is the union of fields we consume from Pi's JSON event
// stream. Fields that can be either string or object across event types
// (e.g. `message`, `result`) are held as json.RawMessage and decoded on
// demand by the switch arms.
type piStreamEvent struct {
	Type string `json:"type"`

	// message_update
	AssistantMessageEvent *piAssistantMessageEvent `json:"assistantMessageEvent,omitempty"`

	// tool_execution_start / tool_execution_end
	ToolCallID string          `json:"toolCallId,omitempty"`
	ToolName   string          `json:"toolName,omitempty"`
	Args       json.RawMessage `json:"args,omitempty"`
	Result     json.RawMessage `json:"result,omitempty"`
	IsError    bool            `json:"isError,omitempty"`

	// error: Message is a string. turn_end: Message is an object.
	Message json.RawMessage `json:"message,omitempty"`

	// auto_retry_end
	Success    bool   `json:"success,omitempty"`
	FinalError string `json:"finalError,omitempty"`
}

type piAssistantMessageEvent struct {
	Type  string `json:"type"`
	Delta string `json:"delta,omitempty"`
}

type piMessage struct {
	Role         string   `json:"role,omitempty"`
	Model        string   `json:"model,omitempty"`
	StopReason   string   `json:"stopReason,omitempty"`
	ErrorMessage string   `json:"errorMessage,omitempty"`
	Usage        *piUsage `json:"usage,omitempty"`
}

type piUsage struct {
	Input       int64 `json:"input"`
	Output      int64 `json:"output"`
	CacheRead   int64 `json:"cacheRead"`
	CacheWrite  int64 `json:"cacheWrite"`
	TotalTokens int64 `json:"totalTokens"`
}

func decodePiMessage(raw json.RawMessage) *piMessage {
	if len(raw) == 0 {
		return nil
	}
	var m piMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil
	}
	return &m
}

func decodePiString(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	return strings.Trim(string(raw), `"`)
}

func decodePiResult(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	return string(raw)
}

// ── Arg builder ──

// piBlockedArgs are flags hardcoded by the daemon that must not be
// overridden by user-configured custom_args. Overriding these would
// break the daemon↔Pi communication protocol.
var piBlockedArgs = map[string]blockedArgMode{
	"-p":            blockedStandalone, // non-interactive mode
	"--print":       blockedStandalone, // alias for -p
	"--mode":        blockedWithValue,  // "json" event stream protocol
	"--session":     blockedWithValue,  // daemon manages the session path
	"--session-id":  blockedWithValue,  // daemon manages the opaque session ID
	"--session-dir": blockedWithValue,  // daemon scopes sessions to AgentRoot
	"--thinking":    blockedWithValue,  // daemon owns per-agent thinking level
	"--mcp-config":  blockedWithValue,  // daemon owns MCP from agent.mcp_config
}

// buildPiArgs assembles the argv for a one-shot Pi invocation.
//
// Flags:
//
//	-p                          non-interactive mode (prompt is positional)
//	--mode json                 emit one JSON event per line on stdout
//	--session-id <id>           exact provider session ID (created when missing)
//	--session-dir <cwd>         Agent-local session storage and lookup
//	--model <id>                model identifier, including an optional provider/id prefix
//	--append-system-prompt <s>  extra system instructions
//
// Custom args are passed on argv; the user prompt is returned separately for
// stdin so platform command-line limits never constrain prompt size.
func buildPiArgsForExecution(prompt, sessionID string, opts ExecOptions, logger *slog.Logger) ([]string, string) {
	return buildPiArgs("", sessionID, opts, logger), prompt
}

func buildPiArgsForEphemeralExecution(prompt, sessionPath string, opts ExecOptions, logger *slog.Logger) ([]string, string) {
	args := buildPiArgs("", "", opts, logger)
	return append(args, "--session", sessionPath), prompt
}

func buildPiArgs(prompt, sessionID string, opts ExecOptions, logger *slog.Logger) []string {
	args := []string{
		"-p",
		"--mode", "json",
	}
	args = appendPiSessionArgs(args, sessionID, opts.Cwd)
	if opts.Model != "" {
		// Pi resolves provider/model IDs itself. Splitting the prefix here
		// loses it before the request reaches provider-aware gateways.
		args = append(args, "--model", opts.Model)
	}
	if opts.ThinkingLevel != "" {
		args = append(args, "--thinking", opts.ThinkingLevel)
	}
	// Normal runs intentionally omit --tools so Pi can use its full registry,
	// including extension tools (#2379). Restricted execution profiles pass an
	// explicit empty allowlist; omission would silently re-enable every tool.
	if opts.DisableTools {
		args = append(args,
			"--no-extensions",
			"--no-skills",
			"--no-prompt-templates",
			"--no-context-files",
			"--no-approve",
			"--tools", "",
		)
		// Load explicitly trusted extensions (application-generated only).
		if len(opts.TrustedExtensionPaths) > 0 {
			for _, p := range opts.TrustedExtensionPaths {
				args = append(args, "--extension", p)
			}
		}
	}
	if opts.piOutputLimitExtension != "" {
		// --no-extensions disables discovery only; Pi still loads an explicit
		// trusted -e/--extension path. This per-run extension lowers the active
		// model's provider request budget before the first LLM call.
		args = append(args, "--extension", opts.piOutputLimitExtension)
	}
	if opts.SystemPrompt != "" {
		args = append(args, "--append-system-prompt", opts.SystemPrompt)
	}
	if path := strings.TrimSpace(opts.piMcpConfigPath); path != "" {
		args = append(args, "--mcp-config", path)
	}
	args = append(args, filterPiCustomArgs(opts, logger)...)
	if prompt != "" {
		args = append(args, prompt)
	}
	return args
}

func filterPiCustomArgs(opts ExecOptions, logger *slog.Logger) []string {
	blocked := piBlockedArgs
	if opts.DisableTools || opts.MaxOutputTokens > 0 || opts.piCaptureExtension != "" {
		blocked = make(map[string]blockedArgMode, len(piBlockedArgs)+16)
		for name, mode := range piBlockedArgs {
			blocked[name] = mode
		}
	}
	if opts.DisableTools || opts.MaxOutputTokens > 0 {
		blocked["--tools"] = blockedWithValue
		blocked["-t"] = blockedWithValue
		blocked["--no-tools"] = blockedStandalone
		blocked["-nt"] = blockedStandalone
		blocked["--extension"] = blockedWithValue
		blocked["-e"] = blockedWithValue
		blocked["--no-extensions"] = blockedStandalone
		blocked["-ne"] = blockedStandalone
		blocked["--skill"] = blockedWithValue
		blocked["--no-skills"] = blockedStandalone
		blocked["-ns"] = blockedStandalone
		blocked["--prompt-template"] = blockedWithValue
		blocked["--no-prompt-templates"] = blockedStandalone
		blocked["-np"] = blockedStandalone
		blocked["--no-context-files"] = blockedStandalone
		blocked["-nc"] = blockedStandalone
		blocked["--approve"] = blockedStandalone
		blocked["-a"] = blockedStandalone
	}
	// Application-generated capture extension is the only allowed --extension
	// for mixed-run resident sessions; reject user overrides while it is loaded.
	if opts.piCaptureExtension != "" {
		blocked["--extension"] = blockedWithValue
		blocked["-e"] = blockedWithValue
		blocked["--no-extensions"] = blockedStandalone
		blocked["-ne"] = blockedStandalone
	}
	return filterCustomArgs(opts.CustomArgs, blocked, logger)
}

func newPiOutputLimitExtension(limit int) (string, error) {
	if limit <= 0 {
		return "", fmt.Errorf("Pi output token limit must be positive")
	}
	f, err := os.CreateTemp("", "multica-pi-output-limit-*.mjs")
	if err != nil {
		return "", fmt.Errorf("create Pi output-limit extension: %w", err)
	}
	path := f.Name()
	source := fmt.Sprintf(`export default function (pi) {
  const limit = %d;
  const enforce = (model, ctx) => {
    if (!model || typeof model.maxTokens !== "number" || !Number.isFinite(model.maxTokens)) {
      if (ctx) ctx.abort();
      throw new Error("restricted execution requires a model with a finite maxTokens contract");
    }
    model.maxTokens = Math.min(model.maxTokens, limit);
    if (model.maxTokens > limit) {
      if (ctx) ctx.abort();
      throw new Error("failed to enforce restricted output token limit");
    }
  };
  pi.on("model_select", (event, ctx) => enforce(event.model, ctx));
  pi.on("before_agent_start", (_event, ctx) => enforce(ctx.model, ctx));
}
`, limit)
	if _, err := io.WriteString(f, source); err != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return "", fmt.Errorf("write Pi output-limit extension: %w", err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(path)
		return "", fmt.Errorf("close Pi output-limit extension: %w", err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		_ = os.Remove(path)
		return "", fmt.Errorf("secure Pi output-limit extension: %w", err)
	}
	return path, nil
}

func enforcePiOutputTokenLimit(status, errText string, usage map[string]TokenUsage, limit int) (string, string) {
	if limit <= 0 {
		return status, errText
	}
	var outputTokens int64
	for _, modelUsage := range usage {
		outputTokens += modelUsage.OutputTokens
	}
	if outputTokens <= int64(limit) {
		return status, errText
	}
	return "failed", fmt.Sprintf("Pi output used %d tokens; restricted limit is %d", outputTokens, limit)
}

func piReportedSessionID(sessionID string, ephemeral bool) string {
	if ephemeral {
		return ""
	}
	return sessionID
}

// piStopReasonAllowsEarlyComplete reports whether a turn_end stop reason
// marks a genuinely terminal turn that is safe to early-complete on.
//
// This is a strict allowlist, not a blocklist, on purpose. Across the Pi
// session corpus the only stop reasons observed are "stop" (terminal),
// "toolUse" (the model owes a follow-up turn once the tool result returns),
// and "error" (handled separately as a failure). Only "stop" is terminal.
// Any other/unseen value must NOT early-complete: the failure direction we
// want is "slow" (degrade to waiting for Pi's exit-time cleanup) rather than
// "mute" (kill Pi mid-loop and truncate the reply). Early-completing on a
// tool-use turn is exactly what truncated "先查后答" chat runs into silent
// no-reply. PI_EARLY_COMPLETE=0 remains the global escape valve.
func piStopReasonAllowsEarlyComplete(stopReason string) bool {
	return strings.ToLower(strings.TrimSpace(stopReason)) == "stop"
}

// getenvDefault returns the env var value or default when unset/empty.
func getenvDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// ── Session identity ──

func newPiSessionID() string { return uuid.NewString() }

func piSessionDir(cwd string) string { return filepath.Join(cwd, ".pi-sessions") }

func appendPiSessionArgs(args []string, sessionID, cwd string) []string {
	if sessionID == "" {
		return args
	}
	return append(args, "--session-id", sessionID, "--session-dir", piSessionDir(cwd))
}

// validateTrustedExtensionPaths checks that every path in TrustedExtensionPaths
// is an absolute regular file within the trusted root. DisableTools must be
// true. Duplicates are silently deduplicated. Returns cleaned absolute paths.
func validateTrustedExtensionPaths(paths []string, root string, disableTools bool) ([]string, error) {
	if len(paths) == 0 {
		return nil, nil
	}
	if !disableTools {
		return nil, fmt.Errorf("TrustedExtensionPaths is only accepted when DisableTools is true")
	}
	root = filepath.Clean(root)
	// Resolve symlinks in root too, matching the resolution done on each path
	// below. On macOS, t.TempDir() (and /tmp generally) returns a path under
	// /var/folders, itself a symlink to /private/var/folders; comparing a
	// resolved path against an unresolved root spuriously fails the
	// trusted-root containment check.
	if resolvedRoot, err := filepath.EvalSymlinks(root); err == nil {
		root = resolvedRoot
	}
	seen := make(map[string]struct{}, len(paths))
	cleaned := make([]string, 0, len(paths))
	for _, p := range paths {
		if !filepath.IsAbs(p) {
			return nil, fmt.Errorf("trusted extension path %q must be absolute", p)
		}
		resolved, err := filepath.EvalSymlinks(p)
		if err != nil {
			return nil, fmt.Errorf("trusted extension path %q: %w", p, err)
		}
		info, err := os.Stat(resolved)
		if err != nil {
			return nil, fmt.Errorf("trusted extension path %q: %w", p, err)
		}
		if !info.Mode().IsRegular() {
			return nil, fmt.Errorf("trusted extension path %q is not a regular file", p)
		}
		rel, err := filepath.Rel(root, resolved)
		if err != nil || strings.HasPrefix(rel, "..") {
			return nil, fmt.Errorf("trusted extension path %q is outside trusted root %q", p, root)
		}
		if _, ok := seen[resolved]; ok {
			continue
		}
		seen[resolved] = struct{}{}
		cleaned = append(cleaned, resolved)
	}
	return cleaned, nil
}
