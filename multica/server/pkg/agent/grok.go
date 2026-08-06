package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
)

const defaultGrokFirstStreamEventTimeout = 30 * time.Second

// GrokFirstStreamEventTimeoutMarker identifies the failure mode where the
// Grok CLI process starts but never accepts the prompt far enough to emit its
// first streaming-json event. This is distinct from a normal long-running turn:
// no turn has started yet, so the daemon must not leave the run silently alive.
const GrokFirstStreamEventTimeoutMarker = "grok first stream event timeout"
const GrokNoStreamingJSONEventsMarker = "grok exited without emitting any streaming-json events"

// grokBackend implements Backend by spawning the Grok CLI (https://grok.com)
// in headless mode with --output-format streaming-json.
//
// Protocol (verified against grok 0.2.93), aligned with Pi's Multica UX:
//
//   - stdout NDJSON: thought / text / end / error
//   - live tool cards: tail session sidecar updates.jsonl
//     (tool_call / tool_call_update → MessageToolUse / MessageToolResult)
//   - session resume: --session-id <uuid> first run; --resume <id> later
//   - early SessionID pin on MessageStatus (daemon resume pointer)
//   - isolated GROK_HOME under ~/.multica/grok-runtime (auth linked, no user
//     MCP pollution — chrome-devtools etc. must not attach to daemon tasks)
//   - --always-approve (shell tools hang without it)
//   - --no-memory (task isolation; Multica owns cross-task context via resume)
type grokBackend struct {
	cfg Config
}

func (b *grokBackend) Execute(ctx context.Context, prompt string, opts ExecOptions) (*Session, error) {
	execPath := b.cfg.ExecutablePath
	if execPath == "" {
		execPath = "grok"
	}
	if _, err := exec.LookPath(execPath); err != nil {
		return nil, fmt.Errorf("grok executable not found at %q: %w", execPath, err)
	}
	if err := b.PreflightAuth(ctx); err != nil {
		return nil, err
	}

	// Session id is known up-front so we can pin resume early and locate the
	// tool-event sidecar while the process is still running (Pi parity).
	sessionID := strings.TrimSpace(opts.ResumeSessionID)
	newSession := sessionID == ""
	if newSession {
		sessionID = uuid.NewString()
	}

	grokHome, err := ensureGrokRuntimeHome(b.cfg.Env, b.cfg.Logger)
	if err != nil {
		return nil, fmt.Errorf("grok runtime home: %w", err)
	}

	timeout := opts.Timeout
	runCtx, cancel := runContext(ctx, timeout)

	args := buildGrokArgs(prompt, sessionID, newSession, opts, b.cfg.Logger)

	cmd := exec.CommandContext(runCtx, execPath, args...)
	hideAgentWindow(cmd)
	b.cfg.Logger.Info("agent command", "exec", execPath, "args", args, "grok_home", grokHome)
	cmd.WaitDelay = 10 * time.Second
	if opts.Cwd != "" {
		cmd.Dir = opts.Cwd
	}
	cmd.Env = buildGrokEnv(b.cfg.Env, grokHome)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("grok stdout pipe: %w", err)
	}
	stderrBuf := newStderrTail(newLogWriter(b.cfg.Logger, "[grok:stderr] "), agentStderrTailBytes)
	cmd.Stderr = stderrBuf

	if err := cmd.Start(); err != nil {
		cancel()
		return nil, fmt.Errorf("start grok: %w", err)
	}

	b.cfg.Logger.Info("grok started", "pid", cmd.Process.Pid, "cwd", opts.Cwd, "model", opts.Model, "session_id", sessionID)

	msgCh := make(chan Message, 256)
	resCh := make(chan Result, 1)

	go func() {
		<-runCtx.Done()
		_ = stdout.Close()
	}()

	go func() {
		defer cancel()
		defer close(msgCh)
		defer close(resCh)

		startTime := time.Now()
		var output strings.Builder
		finalStatus := "completed"
		var finalError string
		// Usage intentionally empty: streaming-json has no input/output token
		// split (unlike Pi's turn_end usage). Do not invent billing numbers.
		usage := make(map[string]TokenUsage)

		// Early pin so the daemon can resume even if the process is killed
		// before the end event arrives.
		trySend(msgCh, Message{Type: MessageStatus, Status: "running", SessionID: sessionID})

		firstStreamEventTimeout := grokFirstStreamEventTimeout(opts.SemanticInactivityTimeout)
		var firstStreamEventObserved atomic.Bool
		var firstStreamEventTimeoutFired atomic.Bool
		firstStreamEventTimer := time.NewTimer(firstStreamEventTimeout)
		defer stopTimer(firstStreamEventTimer)
		firstStreamEventSessionID := sessionID
		go func() {
			select {
			case <-firstStreamEventTimer.C:
				if firstStreamEventObserved.CompareAndSwap(false, true) {
					firstStreamEventTimeoutFired.Store(true)
					b.cfg.Logger.Warn(GrokFirstStreamEventTimeoutMarker,
						"pid", cmd.Process.Pid,
						"session_id", firstStreamEventSessionID,
						"timeout", firstStreamEventTimeout.String(),
						"cwd", opts.Cwd,
						"model", opts.Model,
					)
					cancel()
				}
			case <-runCtx.Done():
			}
		}()
		markFirstStreamEvent := func() {
			if firstStreamEventObserved.CompareAndSwap(false, true) {
				stopTimer(firstStreamEventTimer)
			}
		}

		var toolWG sync.WaitGroup
		toolWG.Add(1)
		go func() {
			defer toolWG.Done()
			tailGrokSessionTools(runCtx, grokHome, opts.Cwd, sessionID, msgCh, b.cfg.Logger)
		}()

		scanner := bufio.NewScanner(stdout)
		scanner.Buffer(make([]byte, 0, 1024*1024), 10*1024*1024)

		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line == "" {
				continue
			}

			var evt grokStreamEvent
			if err := json.Unmarshal([]byte(line), &evt); err != nil {
				continue
			}
			if evt.Type != "" {
				markFirstStreamEvent()
			}

			switch evt.Type {
			case "thought":
				if evt.Data != "" {
					trySend(msgCh, Message{Type: MessageThinking, Content: evt.Data})
				}

			case "text":
				if evt.Data != "" {
					output.WriteString(evt.Data)
					trySend(msgCh, Message{Type: MessageText, Content: evt.Data})
				}

			case "end":
				if evt.SessionID != "" && evt.SessionID != sessionID {
					sessionID = evt.SessionID
					trySend(msgCh, Message{Type: MessageStatus, Status: "running", SessionID: sessionID})
				}
				switch strings.ToLower(evt.StopReason) {
				case "endturn", "end_turn", "":
					// success
				case "cancelled", "canceled", "aborted":
					if finalStatus == "completed" {
						finalStatus = "aborted"
						finalError = "grok stopped: " + evt.StopReason
					}
				case "max_turns", "maxturns", "max_turns_reached":
					if finalStatus == "completed" {
						finalStatus = "failed"
						finalError = "grok reached max turns"
					}
				default:
					if output.Len() == 0 && finalStatus == "completed" {
						finalStatus = "failed"
						finalError = "grok stopped: " + evt.StopReason
					}
				}

			case "error":
				msg := evt.Message
				if msg == "" {
					msg = evt.Data
				}
				if msg == "" {
					msg = "grok error"
				}
				trySend(msgCh, Message{Type: MessageError, Content: msg})
				if finalStatus == "completed" {
					finalStatus = "failed"
					finalError = msg
				}
				if evt.SessionID != "" {
					sessionID = evt.SessionID
				}

			case "max_turns_reached":
				if finalStatus == "completed" {
					finalStatus = "failed"
					finalError = "grok reached max turns"
				}
			}
		}

		deadlineExceeded := runCtx.Err() == context.DeadlineExceeded
		parentCancelled := ctx.Err() == context.Canceled

		waitErr := cmd.Wait()
		cancel()
		toolWG.Wait()

		duration := time.Since(startTime)

		if firstStreamEventTimeoutFired.Load() {
			finalStatus = "timeout"
			finalError = fmt.Sprintf("%s after %s: process started but emitted no streaming-json event before first turn progress (session_id=%s model=%q)",
				GrokFirstStreamEventTimeoutMarker,
				firstStreamEventTimeout,
				sessionID,
				opts.Model,
			)
		} else if deadlineExceeded {
			finalStatus = "timeout"
			finalError = fmt.Sprintf("grok timed out after %s", timeout)
		} else if parentCancelled && finalStatus == "completed" {
			finalStatus = "aborted"
			finalError = "execution cancelled"
		} else if !firstStreamEventObserved.Load() {
			finalStatus = "failed"
			finalError = GrokNoStreamingJSONEventsMarker
		} else if waitErr != nil && finalStatus == "completed" {
			finalStatus = "failed"
			finalError = withAgentStderr(fmt.Sprintf("grok exited with error: %v", waitErr), "grok", stderrBuf.Tail())
		} else if finalStatus == "failed" && finalError != "" {
			finalError = withAgentStderr(finalError, "grok", stderrBuf.Tail())
		}

		b.cfg.Logger.Info("grok finished", "pid", cmd.Process.Pid, "status", finalStatus, "duration", duration.Round(time.Millisecond).String(), "session_id", sessionID)

		resCh <- Result{
			Status:     finalStatus,
			Output:     output.String(),
			Error:      finalError,
			DurationMs: duration.Milliseconds(),
			SessionID:  sessionID,
			Usage:      usage,
		}
	}()

	return &Session{Messages: msgCh, Result: resCh, RuntimeAlive: processLiveness(cmd.Process)}, nil
}

func (b *grokBackend) PreflightAuth(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	grokHome, err := ensureGrokRuntimeHome(b.cfg.Env, b.cfg.Logger)
	if err != nil {
		return fmt.Errorf("grok runtime home: %w", err)
	}
	return validateGrokAuth(grokHome, b.cfg.Env)
}

func grokFirstStreamEventTimeout(semanticInactivityTimeout time.Duration) time.Duration {
	if semanticInactivityTimeout <= 0 || semanticInactivityTimeout > defaultGrokFirstStreamEventTimeout {
		return defaultGrokFirstStreamEventTimeout
	}
	scaled := semanticInactivityTimeout * 4 / 5
	if scaled <= 0 {
		return semanticInactivityTimeout
	}
	return scaled
}

// grokStreamEvent is one NDJSON line from `grok --output-format streaming-json`.
type grokStreamEvent struct {
	Type       string `json:"type"`
	Data       string `json:"data,omitempty"`
	Message    string `json:"message,omitempty"`
	StopReason string `json:"stopReason,omitempty"`
	SessionID  string `json:"sessionId,omitempty"`
	RequestID  string `json:"requestId,omitempty"`
}

// ── Isolated runtime home ──

// ensureGrokRuntimeHome prepares a Multica-managed GROK_HOME that:
//   - reuses the user's auth (symlink/copy of auth.json)
//   - does NOT load the user's interactive MCP servers (empty config.toml)
//   - keeps sessions under a stable path so --resume works across tasks
//
// Callers may override via cfg.Env["GROK_HOME"] (tests / operators).
func ensureGrokRuntimeHome(cfgEnv map[string]string, logger *slog.Logger) (string, error) {
	if v := strings.TrimSpace(cfgEnv["GROK_HOME"]); v != "" {
		if err := os.MkdirAll(v, 0o755); err != nil {
			return "", err
		}
		return v, nil
	}
	// Escape hatch: MULTICA_GROK_USE_USER_HOME=1 uses the real ~/.grok
	// (or existing GROK_HOME) for operators who want full user config.
	if os.Getenv("MULTICA_GROK_USE_USER_HOME") == "1" {
		return userGrokHome(), nil
	}

	userHome, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("user home: %w", err)
	}
	runtimeHome := filepath.Join(userHome, ".multica", "grok-runtime")
	if err := os.MkdirAll(runtimeHome, 0o755); err != nil {
		return "", err
	}

	srcHome := userGrokHome()
	// Auth: required for API calls. Prefer symlink so token refresh lands in
	// the real home; fall back to copy on platforms that block symlinks.
	if err := linkOrCopyFile(filepath.Join(srcHome, "auth.json"), filepath.Join(runtimeHome, "auth.json")); err != nil {
		// Auth may be missing on a fresh machine — Grok will fail at runtime
		// with a clear error; don't block spawn here.
		if logger != nil {
			logger.Debug("grok runtime home: auth.json not linked", "error", err, "src", srcHome)
		}
	}

	// Skills: optional symlink so user-level skills remain discoverable
	// (project skills still come from workdir/.grok/skills via execenv).
	_ = linkOrCopyDir(filepath.Join(srcHome, "skills"), filepath.Join(runtimeHome, "skills"))

	// Minimal config: no mcp_servers, so interactive chrome-devtools / user
	// MCP never attaches to Multica daemon tasks.
	configPath := filepath.Join(runtimeHome, "config.toml")
	const managedConfig = `# Managed by Multica daemon — do not edit.
# Interactive MCP servers from ~/.grok are intentionally NOT loaded here
# so Multica agent runs stay isolated (Pi-style task isolation).
# Set MULTICA_GROK_USE_USER_HOME=1 to use your full ~/.grok instead.

[hints]
project_picker_disabled = true
`
	if err := os.WriteFile(configPath, []byte(managedConfig), 0o644); err != nil {
		return "", fmt.Errorf("write config.toml: %w", err)
	}

	return runtimeHome, nil
}

func validateGrokAuth(grokHome string, cfgEnv map[string]string) error {
	if strings.TrimSpace(cfgEnv["XAI_API_KEY"]) != "" || strings.TrimSpace(os.Getenv("XAI_API_KEY")) != "" {
		return nil
	}
	authPath := filepath.Join(grokHome, "auth.json")
	st, err := os.Stat(authPath)
	if err != nil {
		return fmt.Errorf("%s: grok not logged in: auth.json is missing and XAI_API_KEY is not set; configure Grok auth before retrying", ProviderAuthRequiredMarker)
	}
	if st.IsDir() || st.Size() == 0 {
		return fmt.Errorf("%s: grok not logged in: auth.json is empty or invalid and XAI_API_KEY is not set; configure Grok auth before retrying", ProviderAuthRequiredMarker)
	}
	return nil
}

func userGrokHome() string {
	if v := strings.TrimSpace(os.Getenv("GROK_HOME")); v != "" {
		return v
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".", ".grok")
	}
	return filepath.Join(home, ".grok")
}

func buildGrokEnv(extra map[string]string, grokHome string) []string {
	merged := make(map[string]string, len(extra)+2)
	for k, v := range extra {
		merged[k] = v
	}
	// Caller-supplied GROK_HOME in extra wins (tests).
	if _, ok := merged["GROK_HOME"]; !ok {
		merged["GROK_HOME"] = grokHome
	}
	// Quiet auto-updater noise in daemon logs when supported by env.
	if _, ok := merged["GROK_DISABLE_AUTOUPDATER"]; !ok {
		merged["GROK_DISABLE_AUTOUPDATER"] = "1"
	}
	return buildEnv(merged)
}

func linkOrCopyFile(src, dst string) error {
	if _, err := os.Stat(src); err != nil {
		return err
	}
	_ = os.Remove(dst)
	if err := os.Symlink(src, dst); err == nil {
		return nil
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}

func linkOrCopyDir(src, dst string) error {
	if st, err := os.Stat(src); err != nil || !st.IsDir() {
		return err
	}
	_ = os.RemoveAll(dst)
	if err := os.Symlink(src, dst); err == nil {
		return nil
	}
	// Best-effort: leave missing rather than deep-copying large skill trees.
	return nil
}

// ── Session sidecar tool tail (Pi-equivalent live tool cards) ──

func tailGrokSessionTools(ctx context.Context, grokHome, cwd, sessionID string, msgCh chan<- Message, logger *slog.Logger) {
	if sessionID == "" {
		return
	}
	path, err := waitForGrokUpdatesFile(ctx, grokHome, cwd, sessionID)
	if err != nil {
		if ctx.Err() == nil && logger != nil {
			logger.Debug("grok tool tail: no session sidecar", "session_id", sessionID, "error", err)
		}
		return
	}
	if logger != nil {
		logger.Debug("grok tool tail: attached", "path", path, "session_id", sessionID)
	}

	var offset int64
	seenToolUse := map[string]bool{}
	seenToolResult := map[string]bool{}
	ticker := time.NewTicker(80 * time.Millisecond)
	defer ticker.Stop()

	offset = drainGrokUpdates(path, offset, seenToolUse, seenToolResult, msgCh)

	for {
		select {
		case <-ctx.Done():
			_ = drainGrokUpdates(path, offset, seenToolUse, seenToolResult, msgCh)
			return
		case <-ticker.C:
			offset = drainGrokUpdates(path, offset, seenToolUse, seenToolResult, msgCh)
		}
	}
}

func waitForGrokUpdatesFile(ctx context.Context, grokHome, cwd, sessionID string) (string, error) {
	deadline := time.Now().Add(30 * time.Second)
	for {
		if path := findGrokUpdatesFile(grokHome, cwd, sessionID); path != "" {
			return path, nil
		}
		if time.Now().After(deadline) {
			return "", fmt.Errorf("session updates.jsonl not found for %s", sessionID)
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func findGrokUpdatesFile(grokHome, cwd, sessionID string) string {
	if grokHome == "" {
		grokHome = userGrokHome()
	}
	sessionsRoot := filepath.Join(grokHome, "sessions")
	for _, encoded := range grokCwdEncodings(cwd) {
		p := filepath.Join(sessionsRoot, encoded, sessionID, "updates.jsonl")
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p
		}
	}
	entries, err := os.ReadDir(sessionsRoot)
	if err != nil {
		return ""
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		p := filepath.Join(sessionsRoot, e.Name(), sessionID, "updates.jsonl")
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p
		}
	}
	return ""
}

// grokCwdEncodings returns the URL-encoded cwd keys Grok may use as the
// session parent directory name. On macOS /tmp is typically a symlink to
// /private/tmp, and Grok stores sessions under the realpath.
func grokCwdEncodings(cwd string) []string {
	var paths []string
	add := func(p string) {
		p = strings.TrimSpace(p)
		if p == "" {
			return
		}
		for _, existing := range paths {
			if existing == p {
				return
			}
		}
		paths = append(paths, p)
	}
	add(cwd)
	if cwd != "" {
		if abs, err := filepath.Abs(cwd); err == nil {
			add(abs)
			if real, err := filepath.EvalSymlinks(abs); err == nil {
				add(real)
			}
		}
	}
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		// Percent-encode the absolute path with `/` → `%2F` (url.QueryEscape).
		out = append(out, url.QueryEscape(p))
	}
	return out
}

func drainGrokUpdates(path string, offset int64, seenUse, seenResult map[string]bool, msgCh chan<- Message) int64 {
	f, err := os.Open(path)
	if err != nil {
		return offset
	}
	defer f.Close()

	if offset > 0 {
		if _, err := f.Seek(offset, io.SeekStart); err != nil {
			return offset
		}
	}

	reader := bufio.NewReader(f)
	for {
		line, err := reader.ReadString('\n')
		if len(line) > 0 {
			offset += int64(len(line))
			handleGrokUpdateLine(strings.TrimSpace(line), seenUse, seenResult, msgCh)
		}
		if err != nil {
			break
		}
	}
	return offset
}

type grokUpdateEnvelope struct {
	Method string `json:"method"`
	Params struct {
		Update grokSessionUpdate `json:"update"`
	} `json:"params"`
}

type grokSessionUpdate struct {
	SessionUpdate string          `json:"sessionUpdate"`
	ToolCallID    string          `json:"toolCallId,omitempty"`
	Title         string          `json:"title,omitempty"`
	Status        string          `json:"status,omitempty"`
	RawInput      json.RawMessage `json:"rawInput,omitempty"`
	RawOutput     json.RawMessage `json:"rawOutput,omitempty"`
	Content       json.RawMessage `json:"content,omitempty"`
	Meta          json.RawMessage `json:"_meta,omitempty"`
}

func handleGrokUpdateLine(line string, seenUse, seenResult map[string]bool, msgCh chan<- Message) {
	if line == "" {
		return
	}
	var env grokUpdateEnvelope
	if err := json.Unmarshal([]byte(line), &env); err != nil {
		return
	}
	su := env.Params.Update
	switch su.SessionUpdate {
	case "tool_call":
		id := su.ToolCallID
		if id == "" || seenUse[id] {
			return
		}
		seenUse[id] = true
		name := grokToolName(su)
		var input map[string]any
		if len(su.RawInput) > 0 {
			_ = json.Unmarshal(su.RawInput, &input)
		}
		trySend(msgCh, Message{
			Type:   MessageToolUse,
			Tool:   name,
			CallID: id,
			Input:  input,
		})

	case "tool_call_update":
		id := su.ToolCallID
		if id == "" {
			return
		}
		if !seenUse[id] {
			seenUse[id] = true
			name := grokToolName(su)
			var input map[string]any
			if len(su.RawInput) > 0 {
				_ = json.Unmarshal(su.RawInput, &input)
			}
			trySend(msgCh, Message{
				Type:   MessageToolUse,
				Tool:   name,
				CallID: id,
				Input:  input,
			})
		}
		status := strings.ToLower(strings.TrimSpace(su.Status))
		if status != "completed" && status != "failed" && status != "error" {
			return
		}
		if seenResult[id] {
			return
		}
		seenResult[id] = true
		trySend(msgCh, Message{
			Type:   MessageToolResult,
			CallID: id,
			Output: grokToolResultOutput(su),
		})
	}
}

func grokToolName(su grokSessionUpdate) string {
	if len(su.Meta) > 0 {
		var raw map[string]json.RawMessage
		if err := json.Unmarshal(su.Meta, &raw); err == nil {
			if t, ok := raw["x.ai/tool"]; ok {
				var tool struct {
					Name string `json:"name"`
				}
				if json.Unmarshal(t, &tool) == nil && tool.Name != "" {
					return tool.Name
				}
			}
		}
	}
	if su.Title != "" {
		title := strings.TrimSpace(su.Title)
		if !strings.ContainsAny(title, " \t/") {
			return title
		}
	}
	return "tool"
}

func grokToolResultOutput(su grokSessionUpdate) string {
	if len(su.Content) > 0 {
		var blocks []struct {
			Type    string `json:"type"`
			Content *struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content,omitempty"`
			Path string `json:"path,omitempty"`
		}
		if err := json.Unmarshal(su.Content, &blocks); err == nil {
			var parts []string
			for _, b := range blocks {
				if b.Content != nil && b.Content.Text != "" {
					parts = append(parts, b.Content.Text)
				} else if b.Type == "diff" && b.Path != "" {
					parts = append(parts, fmt.Sprintf("diff %s", b.Path))
				}
			}
			if len(parts) > 0 {
				return strings.Join(parts, "\n")
			}
		}
	}
	if len(su.RawOutput) > 0 {
		var raw map[string]any
		if err := json.Unmarshal(su.RawOutput, &raw); err == nil {
			if s, ok := raw["tool_output_for_prompt"].(string); ok && s != "" {
				return s
			}
			if edits, ok := raw["EditsApplied"].(map[string]any); ok {
				if s, ok := edits["tool_output_for_prompt"].(string); ok && s != "" {
					return s
				}
			}
		}
		return string(su.RawOutput)
	}
	return ""
}

// ── Arg builder ──

var grokBlockedArgs = map[string]blockedArgMode{
	"-p":                       blockedWithValue,
	"--single":                 blockedWithValue,
	"--output-format":          blockedWithValue,
	"--always-approve":         blockedStandalone,
	"--yolo":                   blockedStandalone,
	"--permission-mode":        blockedWithValue,
	"--cwd":                    blockedWithValue,
	"--resume":                 blockedWithValue,
	"-r":                       blockedWithValue,
	"--session-id":             blockedWithValue,
	"-s":                       blockedWithValue,
	"--model":                  blockedWithValue,
	"-m":                       blockedWithValue,
	"--max-turns":              blockedWithValue,
	"--reasoning-effort":       blockedWithValue,
	"--effort":                 blockedWithValue,
	"--system-prompt-override": blockedWithValue,
	"--rules":                  blockedWithValue,
	"--no-memory":              blockedStandalone, // owned by daemon isolation
}

// buildGrokArgs assembles the argv for a one-shot grok headless invocation.
func buildGrokArgs(prompt, sessionID string, newSession bool, opts ExecOptions, logger *slog.Logger) []string {
	args := []string{
		"-p", prompt,
		"--output-format", "streaming-json",
		"--always-approve",
		// Task isolation: Multica carries context via --resume / AGENTS.md,
		// not Grok's cross-session memory store.
		"--no-memory",
	}
	if opts.Model != "" {
		args = append(args, "--model", opts.Model)
	}
	if opts.ThinkingLevel != "" {
		args = append(args, "--reasoning-effort", opts.ThinkingLevel)
	}
	if opts.MaxTurns > 0 {
		args = append(args, "--max-turns", fmt.Sprintf("%d", opts.MaxTurns))
	}
	// --rules appends (like Pi --append-system-prompt); do not replace the
	// built-in agent system prompt.
	if opts.SystemPrompt != "" {
		args = append(args, "--rules", opts.SystemPrompt)
	}
	if sessionID != "" {
		if newSession {
			args = append(args, "--session-id", sessionID)
		} else {
			args = append(args, "--resume", sessionID)
		}
	}
	if opts.Cwd != "" {
		args = append(args, "--cwd", opts.Cwd)
	}
	args = append(args, filterCustomArgs(opts.ExtraArgs, grokBlockedArgs, logger)...)
	args = append(args, filterCustomArgs(opts.CustomArgs, grokBlockedArgs, logger)...)
	return args
}
