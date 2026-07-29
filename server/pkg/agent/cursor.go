package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

// cursorBackend implements Backend by spawning the Cursor Agent CLI
// (cursor-agent) with --output-format stream-json and parsing the JSONL
// event stream. The protocol is similar to Claude Code's stream-json
// format: events are newline-delimited JSON objects with a "type" field.
type cursorBackend struct {
	cfg Config
}

func (b *cursorBackend) Execute(ctx context.Context, prompt string, opts ExecOptions) (*Session, error) {
	execName := b.cfg.ExecutablePath
	if execName == "" {
		execName = "cursor-agent"
	}
	lookedUp, err := exec.LookPath(execName)
	if err != nil {
		return nil, fmt.Errorf("cursor-agent executable not found at %q: %w", execName, err)
	}

	timeout := opts.Timeout
	runCtx, cancel := runContext(ctx, timeout)

	// Materialise agent.mcp_config into `{cwd}/.cursor/mcp.json` before
	// spawning cursor-agent. Cursor has no --mcp-config flag; the project
	// file is the injection point (paired with --approve-mcps in argv).
	if err := ensureCursorMcpConfig(opts.Cwd, opts.McpConfig, b.cfg.Logger); err != nil {
		cancel()
		return nil, fmt.Errorf("apply cursor mcp_config: %w", err)
	}

	args := buildCursorArgs(prompt, opts, b.cfg.Logger)
	var promptFile string
	if shouldSpillCursorPrompt(prompt, args) {
		file, err := os.CreateTemp("", "multica-cursor-prompt-*.txt")
		if err != nil {
			cancel()
			return nil, fmt.Errorf("cursor prompt temp file: %w", err)
		}
		promptFile = file.Name()
		if _, err := file.WriteString(prompt); err != nil {
			_ = file.Close()
			_ = os.Remove(promptFile)
			cancel()
			return nil, fmt.Errorf("cursor prompt temp file write: %w", err)
		}
		if err := file.Close(); err != nil {
			_ = os.Remove(promptFile)
			cancel()
			return nil, fmt.Errorf("cursor prompt temp file close: %w", err)
		}
		args = buildCursorArgs("Read the full task JSON from this local file, then execute it exactly: "+promptFile, opts, b.cfg.Logger)
		b.cfg.Logger.Info("cursor prompt spilled to temp file to avoid argv overflow",
			"prompt_bytes", len(prompt), "argv_bytes", cursorArgsSize(args), "path", promptFile)
	}
	argv0, cmdArgs := chooseCursorInvocation(execName, lookedUp, args, b.cfg.Logger)

	cmd := exec.CommandContext(runCtx, argv0, cmdArgs...)
	hideAgentWindow(cmd)
	b.cfg.Logger.Info("agent command", "exec", argv0, "args", cmdArgs)
	cmd.WaitDelay = 500 * time.Millisecond
	if opts.Cwd != "" {
		cmd.Dir = opts.Cwd
	}
	cmd.Env = buildEnv(b.cfg.Env)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("cursor stdout pipe: %w", err)
	}
	cmd.Stderr = newLogWriter(b.cfg.Logger, "[cursor:stderr] ")

	if err := cmd.Start(); err != nil {
		cancel()
		return nil, fmt.Errorf("start cursor-agent: %w", err)
	}

	b.cfg.Logger.Info("cursor-agent started", "pid", cmd.Process.Pid, "cwd", opts.Cwd, "model", opts.Model)

	msgCh := make(chan Message, 256)
	resCh := make(chan Result, 1)

	go func() {
		defer cancel()
		defer close(msgCh)
		defer close(resCh)
		if promptFile != "" {
			defer os.Remove(promptFile)
		}

		// Close stdout when the context is cancelled so scanner.Scan() unblocks.
		go func() {
			<-runCtx.Done()
			_ = stdout.Close()
		}()

		startTime := time.Now()
		configuredModel := strings.TrimSpace(opts.Model)
		streamModel := ""
		var output strings.Builder
		var sessionID string
		finalStatus := "completed"
		var finalError string
		resultSeen := false
		// stepUsage accumulates per-step token counts from "step_finish" events.
		// resultUsage holds authoritative session totals from "result" events.
		// If the result event includes usage, we use resultUsage exclusively;
		// otherwise we fall back to stepUsage.
		stepUsage := make(map[string]TokenUsage)
		resultUsage := make(map[string]TokenUsage)
		hasResultUsage := false
		toolEvents := newRuntimeToolEventTracker(30*time.Minute, 1024)
		toolDiagnostics := newCursorToolEventDiagnostics()
		emitToolEvent := func(decoded cursorDecodedToolEvent) {
			if decoded.reason != "" {
				toolDiagnostics.dropped(decoded.reason)
				b.cfg.Logger.Warn(
					"cursor runtime tool event rejected",
					"schema", decoded.event.Schema,
					"event_id", decoded.event.EventID,
					"source", decoded.event.Source,
					"protocol_shape", decoded.event.ProtocolShape,
					"phase", decoded.event.Phase,
					"tool", decoded.event.Tool,
					"call_id", decoded.event.CallID,
					"reason", decoded.reason,
				)
				return
			}
			message, accepted, reason := toolEvents.accept(decoded.event)
			if !accepted {
				toolDiagnostics.dropped(reason)
				b.cfg.Logger.Warn(
					"cursor runtime tool event rejected",
					"schema", decoded.event.Schema,
					"event_id", decoded.event.EventID,
					"source", decoded.event.Source,
					"protocol_shape", decoded.event.ProtocolShape,
					"phase", decoded.event.Phase,
					"tool", decoded.event.Tool,
					"call_id", decoded.event.CallID,
					"reason", reason,
				)
				return
			}
			toolDiagnostics.accepted(decoded.event.ProtocolShape)
			trySend(msgCh, message)
		}

		scanner := bufio.NewScanner(stdout)
		scanner.Buffer(make([]byte, 0, 1024*1024), 10*1024*1024)

		for scanner.Scan() {
			raw := scanner.Text()
			line := normalizeCursorStreamLine(raw)
			if line == "" {
				continue
			}

			var evt cursorStreamEvent
			if err := json.Unmarshal([]byte(line), &evt); err != nil {
				continue
			}

			if sid := evt.readSessionID(); sid != "" {
				sessionID = sid
			}

			for _, decoded := range decodeCursorToolEvents(&evt, time.Now()) {
				emitToolEvent(decoded)
			}

			switch evt.Type {
			case "system":
				if evt.Subtype == "init" {
					if model := strings.TrimSpace(evt.Model); model != "" {
						streamModel = model
					}
					trySend(msgCh, Message{Type: MessageStatus, Status: "running"})
				}
				if evt.Subtype == "error" {
					errMsg := cursorErrorText(&evt)
					if errMsg != "" {
						trySend(msgCh, Message{Type: MessageError, Content: errMsg})
					}
				}

			case "assistant":
				b.handleCursorAssistant(&evt, msgCh, &output, emitToolEvent)

			case "result":
				resultSeen = true
				if evt.IsError || evt.Subtype == "error" {
					finalStatus = "failed"
					finalError = cursorErrorText(&evt)
				}
				if evt.ResultText != "" && output.Len() == 0 {
					output.WriteString(evt.ResultText)
				}
				b.accumulateResultUsage(resultUsage, &evt, streamModel, configuredModel)
				if evt.hasResultUsage() {
					hasResultUsage = true
				}
				// Current Cursor Agent versions can emit the terminal result
				// event but keep a worker process alive. Treat result as the
				// protocol boundary so the daemon can report completion.
				cancel()

			case "error":
				errMsg := cursorErrorText(&evt)
				if errMsg != "" {
					finalError = errMsg
				}
				trySend(msgCh, Message{Type: MessageError, Content: errMsg})

			case "text":
				if evt.Part != nil {
					var part cursorTextPart
					_ = json.Unmarshal(evt.Part, &part)
					if part.Text != "" {
						output.WriteString(part.Text)
						trySend(msgCh, Message{Type: MessageText, Content: part.Text})
					}
				}

			case "step_finish":
				if evt.Part != nil {
					var part cursorStepFinishPart
					_ = json.Unmarshal(evt.Part, &part)
					model := cursorUsageModel(evt.Model, streamModel, configuredModel)
					u := stepUsage[model]
					u.InputTokens += int64(part.Tokens.Input)
					u.OutputTokens += int64(part.Tokens.Output)
					u.CacheReadTokens += int64(part.Tokens.Cache.Read)
					stepUsage[model] = u
				}
			}
		}

		// Use result usage if available (session totals); otherwise fall back
		// to accumulated step_finish usage.
		if !hasResultUsage {
			resultUsage = stepUsage
		}
		missingCompletions, expiredIncomplete := toolEvents.finish()
		if missingCompletions > 0 {
			toolDiagnostics.droppedByReason["missing_completion"] += missingCompletions
		}
		if expiredIncomplete > 0 {
			toolDiagnostics.droppedByReason["expired_incomplete"] += expiredIncomplete
		}
		if len(toolDiagnostics.acceptedByShape) > 0 || len(toolDiagnostics.droppedByReason) > 0 {
			b.cfg.Logger.Info(
				"cursor runtime tool event summary",
				"schema", RuntimeToolEventSchemaV1,
				"source", cursorToolEventSource,
				"accepted_by_shape", toolDiagnostics.acceptedByShape,
				"dropped_by_reason", toolDiagnostics.droppedByReason,
			)
		}

		exitErr := cmd.Wait()
		duration := time.Since(startTime)

		if runCtx.Err() == context.DeadlineExceeded {
			finalStatus = "timeout"
			finalError = fmt.Sprintf("cursor-agent timed out after %s", timeout)
		} else if runCtx.Err() == context.Canceled && !resultSeen {
			finalStatus = "aborted"
			finalError = "execution cancelled"
		} else if exitErr != nil && finalStatus == "completed" && !resultSeen {
			finalStatus = "failed"
			finalError = fmt.Sprintf("cursor-agent exited with error: %v", exitErr)
		}

		b.cfg.Logger.Info("cursor-agent finished", "pid", cmd.Process.Pid, "status", finalStatus, "duration", duration.Round(time.Millisecond).String())

		resCh <- Result{
			Status:     finalStatus,
			Output:     output.String(),
			Error:      finalError,
			DurationMs: duration.Milliseconds(),
			SessionID:  sessionID,
			Usage:      resultUsage,
		}
	}()

	return &Session{Messages: msgCh, Result: resCh, RuntimeAlive: processLiveness(cmd.Process)}, nil
}

func (b *cursorBackend) handleCursorAssistant(evt *cursorStreamEvent, ch chan<- Message, output *strings.Builder, emitToolEvent func(cursorDecodedToolEvent)) {
	if evt.Message == nil {
		return
	}

	var content cursorAssistantMessage
	if err := json.Unmarshal(evt.Message, &content); err != nil {
		return
	}

	// Note: per-message usage in assistant events is intentionally ignored.
	// Token usage is taken exclusively from "result" events (session totals)
	// to avoid double-counting.

	for _, block := range content.Content {
		switch block.Type {
		case "output_text", "text":
			if block.Text != "" {
				output.WriteString(block.Text)
				trySend(ch, Message{Type: MessageText, Content: block.Text})
			}
		case "thinking":
			if block.Text != "" {
				trySend(ch, Message{Type: MessageThinking, Content: block.Text})
			}
		case "tool_use":
			if emitToolEvent != nil {
				emitToolEvent(decodeCursorAssistantToolBlock(evt.SessionID, block, time.Now()))
			}
		}
	}
}

func cursorUsageModel(models ...string) string {
	for _, candidate := range models {
		if model := strings.TrimSpace(candidate); model != "" {
			return model
		}
	}
	return "cursor"
}

func (b *cursorBackend) accumulateResultUsage(usage map[string]TokenUsage, evt *cursorStreamEvent, fallbackModels ...string) {
	if !evt.hasResultUsage() {
		return
	}
	model := cursorUsageModel(append([]string{evt.Model}, fallbackModels...)...)
	u := usage[model]
	nested := cursorUsage{}
	if evt.Usage != nil {
		nested = *evt.Usage
	}
	u.InputTokens += firstNonZeroInt64(evt.InputTokens, nested.InputTokens)
	u.OutputTokens += firstNonZeroInt64(evt.OutputTokens, nested.OutputTokens)
	u.CacheReadTokens += firstNonZeroInt64(evt.CacheReadTokens, nested.CacheReadInputTokens)
	u.CacheWriteTokens += firstNonZeroInt64(evt.CacheWriteTokens, nested.CacheWriteInputTokens)
	usage[model] = u
}

// ── Cursor stream-json types ──

type cursorStreamEvent struct {
	Type      string `json:"type"`
	Subtype   string `json:"subtype,omitempty"`
	SessionID string `json:"session_id,omitempty"`
	Model     string `json:"model,omitempty"`

	// assistant fields
	Message json.RawMessage `json:"message,omitempty"`

	// tool_use fields
	ToolName   string          `json:"tool_name,omitempty"`
	ToolID     string          `json:"tool_id,omitempty"`
	Parameters json.RawMessage `json:"parameters,omitempty"`

	// Current Cursor stream-json tool_call fields. The tool_call object has one
	// dynamic key such as readToolCall, writeToolCall, or shellToolCall.
	CallID   string          `json:"call_id,omitempty"`
	ToolCall json.RawMessage `json:"tool_call,omitempty"`

	// tool_result fields
	Output string `json:"output,omitempty"`

	// result fields
	ResultText       string       `json:"result,omitempty"`
	IsError          bool         `json:"is_error,omitempty"`
	InputTokens      int64        `json:"inputTokens,omitempty"`
	OutputTokens     int64        `json:"outputTokens,omitempty"`
	CacheReadTokens  int64        `json:"cacheReadTokens,omitempty"`
	CacheWriteTokens int64        `json:"cacheWriteTokens,omitempty"`
	Usage            *cursorUsage `json:"usage,omitempty"`
	TotalCost        float64      `json:"total_cost_usd,omitempty"`

	// error fields
	ErrorMsg string `json:"error,omitempty"`
	Detail   string `json:"detail,omitempty"`

	// legacy compat
	Part json.RawMessage `json:"part,omitempty"`
}

func (evt *cursorStreamEvent) readSessionID() string {
	if s := strings.TrimSpace(evt.SessionID); s != "" {
		return s
	}
	return ""
}

func (evt *cursorStreamEvent) hasResultUsage() bool {
	return evt.Usage != nil || evt.InputTokens != 0 || evt.OutputTokens != 0 || evt.CacheReadTokens != 0 || evt.CacheWriteTokens != 0
}

type cursorUsage struct {
	InputTokens           int64
	OutputTokens          int64
	CacheReadInputTokens  int64
	CacheWriteInputTokens int64
}

func (u *cursorUsage) UnmarshalJSON(data []byte) error {
	var raw struct {
		InputTokensSnake              int64 `json:"input_tokens"`
		InputTokensCamel              int64 `json:"inputTokens"`
		OutputTokensSnake             int64 `json:"output_tokens"`
		OutputTokensCamel             int64 `json:"outputTokens"`
		CachedInputTokensSnake        int64 `json:"cached_input_tokens"`
		CachedInputTokensCamel        int64 `json:"cachedInputTokens"`
		CacheReadTokensCamel          int64 `json:"cacheReadTokens"`
		CacheReadInputTokensSnake     int64 `json:"cache_read_input_tokens"`
		CacheReadInputTokensCamel     int64 `json:"cacheReadInputTokens"`
		CacheWriteTokensCamel         int64 `json:"cacheWriteTokens"`
		CacheCreationInputTokensSnake int64 `json:"cache_creation_input_tokens"`
		CacheCreationInputTokensCamel int64 `json:"cacheCreationInputTokens"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	u.InputTokens = firstNonZeroInt64(raw.InputTokensCamel, raw.InputTokensSnake)
	u.OutputTokens = firstNonZeroInt64(raw.OutputTokensCamel, raw.OutputTokensSnake)
	u.CacheReadInputTokens = firstNonZeroInt64(
		raw.CacheReadTokensCamel,
		raw.CachedInputTokensCamel,
		raw.CachedInputTokensSnake,
		raw.CacheReadInputTokensCamel,
		raw.CacheReadInputTokensSnake,
	)
	u.CacheWriteInputTokens = firstNonZeroInt64(
		raw.CacheWriteTokensCamel,
		raw.CacheCreationInputTokensCamel,
		raw.CacheCreationInputTokensSnake,
	)
	return nil
}

func firstNonZeroInt64(values ...int64) int64 {
	for _, value := range values {
		if value != 0 {
			return value
		}
	}
	return 0
}

type cursorAssistantMessage struct {
	Model   string               `json:"model"`
	Content []cursorContentBlock `json:"content"`
	Usage   *cursorUsage         `json:"usage,omitempty"`
}

type cursorContentBlock struct {
	Type  string          `json:"type"`
	Text  string          `json:"text,omitempty"`
	ID    string          `json:"id,omitempty"`
	Name  string          `json:"name,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`
}

type cursorToolCallPayload struct {
	Args   map[string]any  `json:"args,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
}

type cursorTextPart struct {
	Text string `json:"text"`
}

type cursorStepFinishPart struct {
	Tokens struct {
		Input  int `json:"input"`
		Output int `json:"output"`
		Cache  struct {
			Read int `json:"read"`
		} `json:"cache"`
	} `json:"tokens"`
	Cost float64 `json:"cost"`
}

// ── Helpers ──

// normalizeCursorStreamLine handles the stdout:/stderr: prefix that Cursor
// CLI may emit in stream-json mode. Returns the trimmed JSON line.
func normalizeCursorStreamLine(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	// Cursor CLI may prefix lines with "stdout:" or "stderr:" — strip it.
	if idx := cursorStreamPrefixRe.FindStringIndex(trimmed); idx != nil {
		return strings.TrimSpace(trimmed[idx[1]:])
	}
	return trimmed
}

var cursorStreamPrefixRe = regexp.MustCompile(`^(?i)(stdout|stderr)\s*[:=]?\s*`)

func cursorErrorText(evt *cursorStreamEvent) string {
	if evt.ErrorMsg != "" {
		return evt.ErrorMsg
	}
	if evt.Detail != "" {
		return evt.Detail
	}
	if evt.ResultText != "" {
		return evt.ResultText
	}
	return ""
}

func parseCursorToolCall(raw json.RawMessage) (string, map[string]any, json.RawMessage, bool) {
	if len(raw) == 0 {
		return "", nil, nil, false
	}

	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(raw, &envelope); err != nil || len(envelope) == 0 {
		return "", nil, nil, false
	}

	var matched *cursorToolCallPayload
	var matchedTool string
	for key, value := range envelope {
		if cursorToolCallMetadataKeys[key] {
			continue
		}
		tool := cursorToolCallName(key)
		if tool == "" {
			return "", nil, nil, false
		}
		if matched != nil {
			return "", nil, nil, false
		}
		var payload cursorToolCallPayload
		if err := json.Unmarshal(value, &payload); err != nil {
			return "", nil, nil, false
		}
		matched = &payload
		matchedTool = tool
	}
	if matched == nil {
		return "", nil, nil, false
	}
	return matchedTool, matched.Args, matched.Result, true
}

// Cursor 2026.07.17 adds lifecycle metadata beside the single dynamic
// *ToolCall payload. Keep this allowlist explicit: accepting arbitrary sibling
// keys would turn protocol drift into guessed Activity facts.
var cursorToolCallMetadataKeys = map[string]bool{
	"toolCallId":             true,
	"startedAtMs":            true,
	"completedAtMs":          true,
	"hookAdditionalContexts": true,
}

func cursorToolCallName(key string) string {
	base := strings.TrimSpace(key)
	if !strings.HasSuffix(base, "ToolCall") {
		return ""
	}
	base = strings.TrimSuffix(base, "ToolCall")
	base = strings.TrimSpace(base)
	if base == "" {
		return ""
	}

	var out strings.Builder
	for i, r := range base {
		if 'A' <= r && r <= 'Z' {
			if i > 0 {
				out.WriteByte('_')
			}
			out.WriteRune(r + ('a' - 'A'))
			continue
		}
		out.WriteRune(r)
	}

	switch out.String() {
	case "read":
		return "read_file"
	case "write":
		return "write_file"
	case "edit":
		return "edit_file"
	default:
		return out.String()
	}
}

func cursorToolCallResultText(result json.RawMessage) string {
	if len(result) == 0 || string(result) == "null" {
		return ""
	}
	return string(result)
}

// cursorBlockedArgs are flags hardcoded by the daemon that must not be
// overridden by user-configured custom_args. Overriding these would break
// the daemon↔cursor-agent communication protocol.
var cursorBlockedArgs = map[string]blockedArgMode{
	"-p":              blockedStandalone, // non-interactive print mode
	"--output-format": blockedWithValue,  // stream-json protocol
	"--yolo":          blockedStandalone, // auto-approval for autonomous operation
	"--approve-mcps":  blockedStandalone, // daemon owns MCP approval when mcp_config is set
}

// maxCursorArgvBytes is intentionally below common Linux ARG_MAX headroom once
// environment variables and the cursor-agent wrapper are included. Memory
// curation payloads with hundreds of evidence rows used to blow past this and
// fail with "argument list too long" before the process started.
const maxCursorArgvBytes = 64 * 1024

// maxCursorPromptBytes forces large task JSON onto a temp file even when the
// assembled argv looks "small enough". Curation prompts are mostly one -p
// argument, so measuring the prompt itself is the reliable signal.
const maxCursorPromptBytes = 48 * 1024

func shouldSpillCursorPrompt(prompt string, args []string) bool {
	return len(prompt) > maxCursorPromptBytes || cursorArgsSize(args) > maxCursorArgvBytes
}

func cursorArgsSize(args []string) int {
	size := 0
	for _, arg := range args {
		size += len(arg) + 1
	}
	return size
}

// buildCursorArgs assembles the argv for a one-shot cursor-agent invocation.
//
// Usage: cursor-agent -p <prompt> --output-format stream-json
//
//	--workspace <cwd> --yolo [--approve-mcps] [--model <m>] [--resume <id>]
func buildCursorArgs(prompt string, opts ExecOptions, logger *slog.Logger) []string {
	args := []string{
		"-p", prompt,
		"--output-format", "stream-json",
		"--yolo",
	}
	if opts.Cwd != "" {
		args = append(args, "--workspace", opts.Cwd)
	}
	// Headless daemon runs cannot click through MCP approval prompts.
	// When Multica owns mcp.json for this agent, auto-approve those servers.
	if hasManagedMcpConfig(opts.McpConfig) {
		args = append(args, "--approve-mcps")
	}
	if opts.Model != "" {
		args = append(args, "--model", opts.Model)
	}
	// NOTE: cursor-agent CLI does not support --system-prompt or --max-turns.
	// Instructions are injected via AGENTS.md and .cursor/skills/ files instead.
	if opts.ResumeSessionID != "" {
		args = append(args, "--resume", opts.ResumeSessionID)
	}
	args = append(args, filterCustomArgs(opts.CustomArgs, cursorBlockedArgs, logger)...)
	return args
}
