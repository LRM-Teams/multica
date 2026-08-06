package agent

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestNewReturnsGrokBackend(t *testing.T) {
	t.Parallel()
	b, err := New("grok", Config{ExecutablePath: "/nonexistent/grok"})
	if err != nil {
		t.Fatalf("New(grok) error: %v", err)
	}
	if _, ok := b.(*grokBackend); !ok {
		t.Fatalf("expected *grokBackend, got %T", b)
	}
}

var _ AuthPreflight = (*grokBackend)(nil)

func TestBuildGrokArgsBaseline(t *testing.T) {
	t.Parallel()

	args := buildGrokArgs("write a haiku", "sess-new", true, ExecOptions{}, slog.Default())
	if !containsArg(args, "-p") || !containsArg(args, "write a haiku") {
		t.Fatalf("expected -p prompt, got %v", args)
	}
	if !containsPair(args, "--output-format", "streaming-json") {
		t.Fatalf("expected streaming-json output format, got %v", args)
	}
	if !containsArg(args, "--always-approve") {
		t.Fatalf("expected --always-approve, got %v", args)
	}
	if !containsArg(args, "--no-memory") {
		t.Fatalf("expected --no-memory for task isolation, got %v", args)
	}
	if !containsPair(args, "--session-id", "sess-new") {
		t.Fatalf("expected --session-id for new session, got %v", args)
	}
	if containsArg(args, "--resume") {
		t.Fatalf("did not expect --resume for new session, got %v", args)
	}
}

func TestBuildGrokArgsResume(t *testing.T) {
	t.Parallel()

	args := buildGrokArgs("continue", "sess-old", false, ExecOptions{}, slog.Default())
	if !containsPair(args, "--resume", "sess-old") {
		t.Fatalf("expected --resume for resumed session, got %v", args)
	}
	if containsArg(args, "--session-id") {
		t.Fatalf("did not expect --session-id when resuming, got %v", args)
	}
}

func TestBuildGrokArgsWithOptions(t *testing.T) {
	t.Parallel()

	args := buildGrokArgs("hi", "s1", true, ExecOptions{
		Model:         "grok-4.5",
		ThinkingLevel: "high",
		MaxTurns:      12,
		SystemPrompt:  "be careful",
		Cwd:           "/tmp/work",
		CustomArgs:    []string{"--no-subagents", "--disable-web-search"},
	}, slog.Default())

	checks := [][2]string{
		{"--model", "grok-4.5"},
		{"--reasoning-effort", "high"},
		{"--max-turns", "12"},
		{"--rules", "be careful"},
		{"--session-id", "s1"},
		{"--cwd", "/tmp/work"},
	}
	for _, c := range checks {
		if !containsPair(args, c[0], c[1]) {
			t.Fatalf("expected %s %s in args, got %v", c[0], c[1], args)
		}
	}
	if !containsArg(args, "--no-subagents") || !containsArg(args, "--disable-web-search") {
		t.Fatalf("expected custom args to pass through, got %v", args)
	}
}

func TestBuildGrokArgsFiltersBlockedCustomArgs(t *testing.T) {
	t.Parallel()

	args := buildGrokArgs("hi", "s1", true, ExecOptions{
		CustomArgs: []string{
			"--output-format", "plain",
			"--always-approve",
			"--yolo",
			"--permission-mode", "default",
			"--model", "hijack",
			"--resume", "bad",
			"--session-id", "evil",
			"--sandbox", "workspace",
		},
	}, slog.Default())

	if !containsPair(args, "--output-format", "streaming-json") {
		t.Fatalf("expected forced streaming-json, got %v", args)
	}
	if containsPair(args, "--output-format", "plain") {
		t.Fatalf("blocked --output-format plain leaked: %v", args)
	}
	if containsPair(args, "--model", "hijack") {
		t.Fatalf("blocked --model hijack leaked: %v", args)
	}
	if containsPair(args, "--resume", "bad") {
		t.Fatalf("blocked --resume bad leaked: %v", args)
	}
	if containsPair(args, "--session-id", "evil") {
		t.Fatalf("blocked --session-id evil leaked: %v", args)
	}
	if !containsPair(args, "--sandbox", "workspace") {
		t.Fatalf("expected --sandbox to pass through, got %v", args)
	}
}

func TestParseGrokModels(t *testing.T) {
	t.Parallel()

	out := `You are logged in with grok.com.

Default model: grok-4.5

Available models:
  * grok-4.5 (default)
  - grok-composer-2.5-fast
  * grok-4.5 (default)
`
	models := parseGrokModels(out)
	if len(models) != 2 {
		t.Fatalf("expected 2 models, got %d: %+v", len(models), models)
	}
	if models[0].ID != "grok-4.5" || !models[0].Default {
		t.Fatalf("expected first model grok-4.5 default, got %+v", models[0])
	}
	if models[1].ID != "grok-composer-2.5-fast" || models[1].Default {
		t.Fatalf("expected second model grok-composer-2.5-fast non-default, got %+v", models[1])
	}
	if models[0].Thinking == nil || len(models[0].Thinking.SupportedLevels) == 0 {
		t.Fatal("expected thinking catalog on grok models")
	}
}

func TestGrokStaticModelsFallback(t *testing.T) {
	t.Parallel()
	models := grokStaticModels()
	if len(models) == 0 {
		t.Fatal("expected non-empty static catalog")
	}
	var hasDefault bool
	for _, m := range models {
		if m.Default {
			hasDefault = true
		}
		if m.Provider != "grok" {
			t.Fatalf("expected provider grok, got %q", m.Provider)
		}
	}
	if !hasDefault {
		t.Fatal("expected a default model")
	}
}

func TestGrokCwdEncodingsIncludeRealpath(t *testing.T) {
	t.Parallel()
	encs := grokCwdEncodings("/tmp")
	if len(encs) == 0 {
		t.Fatal("expected at least one encoding")
	}
	// QueryEscape of /tmp or /private/tmp
	joined := strings.Join(encs, " ")
	if !strings.Contains(joined, "%2F") {
		t.Fatalf("expected percent-encoded path, got %v", encs)
	}
}

func TestHandleGrokUpdateLineToolCallAndResult(t *testing.T) {
	t.Parallel()

	msgCh := make(chan Message, 8)
	seenUse := map[string]bool{}
	seenResult := map[string]bool{}

	toolCall := `{"method":"session/update","params":{"update":{"sessionUpdate":"tool_call","toolCallId":"c1","title":"read_file","rawInput":{"target_file":"/tmp/a.txt"},"_meta":{"x.ai/tool":{"name":"read_file"}}}}}`
	handleGrokUpdateLine(toolCall, seenUse, seenResult, msgCh)

	select {
	case m := <-msgCh:
		if m.Type != MessageToolUse || m.Tool != "read_file" || m.CallID != "c1" {
			t.Fatalf("unexpected tool use: %+v", m)
		}
		if m.Input["target_file"] != "/tmp/a.txt" {
			t.Fatalf("unexpected input: %+v", m.Input)
		}
	default:
		t.Fatal("expected MessageToolUse")
	}

	toolDone := `{"method":"session/update","params":{"update":{"sessionUpdate":"tool_call_update","toolCallId":"c1","status":"completed","content":[{"type":"content","content":{"type":"text","text":"hello"}}]}}}`
	handleGrokUpdateLine(toolDone, seenUse, seenResult, msgCh)

	select {
	case m := <-msgCh:
		if m.Type != MessageToolResult || m.CallID != "c1" || m.Output != "hello" {
			t.Fatalf("unexpected tool result: %+v", m)
		}
	default:
		t.Fatal("expected MessageToolResult")
	}

	// Duplicate completed must not re-emit.
	handleGrokUpdateLine(toolDone, seenUse, seenResult, msgCh)
	select {
	case m := <-msgCh:
		t.Fatalf("unexpected extra message: %+v", m)
	default:
	}
}

func TestGrokExecuteStreamingJSONWithTools(t *testing.T) {
	t.Parallel()

	// Isolate session home via cfg.Env (not t.Setenv) so the test stays
	// parallel-safe and does not pollute ~/.grok.
	home := t.TempDir()
	writeTestGrokAuth(t, home)

	cwd := t.TempDir()
	// The fake CLI writes tools into GROK_HOME/sessions/<enc-cwd>/<session-id>/
	// matching findGrokUpdatesFile.
	fakePath := filepath.Join(t.TempDir(), "grok")
	script := `#!/bin/sh
# parse --session-id and --cwd from argv
sid=""
cwd=""
while [ $# -gt 0 ]; do
  case "$1" in
    --session-id) sid="$2"; shift 2 ;;
    --cwd) cwd="$2"; shift 2 ;;
    *) shift ;;
  esac
done
# encode cwd the way Grok does (percent-encode /)
enc=$(python3 -c "import urllib.parse,sys; print(urllib.parse.quote(sys.argv[1], safe=''))" "$cwd")
dir="$GROK_HOME/sessions/$enc/$sid"
mkdir -p "$dir"
# emit a tool_call + completed update (Pi-equivalent live tools)
printf '%s\n' '{"method":"session/update","params":{"update":{"sessionUpdate":"tool_call","toolCallId":"t1","title":"read_file","rawInput":{"target_file":"note.txt"},"_meta":{"x.ai/tool":{"name":"read_file"}}}}}' >> "$dir/updates.jsonl"
printf '%s\n' '{"method":"session/update","params":{"update":{"sessionUpdate":"tool_call_update","toolCallId":"t1","status":"completed","content":[{"type":"content","content":{"type":"text","text":"note body"}}]}}}' >> "$dir/updates.jsonl"
# stdout stream
printf '%s\n' '{"type":"thought","data":"thinking "}'
printf '%s\n' '{"type":"thought","data":"now"}'
sleep 0.2
printf '%s\n' '{"type":"text","data":"hel"}'
printf '%s\n' '{"type":"text","data":"lo"}'
printf '%s\n' "{\"type\":\"end\",\"stopReason\":\"EndTurn\",\"sessionId\":\"$sid\",\"requestId\":\"req-1\"}"
exit 0
`
	writeTestExecutable(t, fakePath, []byte(script))

	backend, err := New("grok", Config{
		ExecutablePath: fakePath,
		Logger:         slog.Default(),
		Env:            map[string]string{"GROK_HOME": home},
	})
	if err != nil {
		t.Fatalf("new grok backend: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	session, err := backend.Execute(ctx, "say hello", ExecOptions{Timeout: 10 * time.Second, Cwd: cwd})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}

	var texts, thoughts []string
	var toolUses, toolResults []Message
	var statusSessionID string
	for msg := range session.Messages {
		switch msg.Type {
		case MessageText:
			texts = append(texts, msg.Content)
		case MessageThinking:
			thoughts = append(thoughts, msg.Content)
		case MessageToolUse:
			toolUses = append(toolUses, msg)
		case MessageToolResult:
			toolResults = append(toolResults, msg)
		case MessageStatus:
			if msg.SessionID != "" {
				statusSessionID = msg.SessionID
			}
		}
	}

	select {
	case result, ok := <-session.Result:
		if !ok {
			t.Fatal("result channel closed without a value")
		}
		if result.Status != "completed" {
			t.Fatalf("expected completed, got %q error=%q", result.Status, result.Error)
		}
		if result.Output != "hello" {
			t.Fatalf("expected output hello, got %q", result.Output)
		}
		if result.SessionID == "" {
			t.Fatal("expected non-empty session id")
		}
		if statusSessionID == "" {
			t.Fatal("expected early SessionID pin on MessageStatus")
		}
		if statusSessionID != result.SessionID {
			t.Fatalf("status session %q != result session %q", statusSessionID, result.SessionID)
		}
		if strings.Join(texts, "") != "hello" {
			t.Fatalf("expected streamed text hello, got %v", texts)
		}
		if strings.Join(thoughts, "") != "thinking now" {
			t.Fatalf("expected streamed thought, got %v", thoughts)
		}
		if len(toolUses) != 1 || toolUses[0].Tool != "read_file" {
			t.Fatalf("expected one read_file tool use, got %+v", toolUses)
		}
		if len(toolResults) != 1 || toolResults[0].Output != "note body" {
			t.Fatalf("expected one tool result, got %+v", toolResults)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("timeout waiting for result")
	}
}

func TestGrokExecuteErrorEvent(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	writeTestGrokAuth(t, home)

	fakePath := filepath.Join(t.TempDir(), "grok")
	script := "#!/bin/sh\n" +
		"printf '%s\\n' '{\"type\":\"error\",\"message\":\"unknown model id\"}'\n" +
		"exit 1\n"
	writeTestExecutable(t, fakePath, []byte(script))

	backend, err := New("grok", Config{ExecutablePath: fakePath, Logger: slog.Default(), Env: map[string]string{"GROK_HOME": home}})
	if err != nil {
		t.Fatalf("new grok backend: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	session, err := backend.Execute(ctx, "hi", ExecOptions{Timeout: 5 * time.Second, Model: "bad"})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	for range session.Messages {
	}

	select {
	case result, ok := <-session.Result:
		if !ok {
			t.Fatal("result channel closed without a value")
		}
		if result.Status != "failed" {
			t.Fatalf("expected failed, got %q", result.Status)
		}
		if !strings.Contains(result.Error, "unknown model id") {
			t.Fatalf("expected model error in result, got %q", result.Error)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("timeout waiting for result")
	}
}

func TestGrokExecuteTimesOutWhenNoStreamingEventArrives(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("shell-script fixture is POSIX-only")
	}

	fakePath := filepath.Join(t.TempDir(), "grok")
	home := t.TempDir()
	writeTestGrokAuth(t, home)
	script := "#!/bin/sh\n" +
		"while :; do :; done\n"
	writeTestExecutable(t, fakePath, []byte(script))

	backend, err := New("grok", Config{ExecutablePath: fakePath, Logger: slog.Default(), Env: map[string]string{"GROK_HOME": home}})
	if err != nil {
		t.Fatalf("new grok backend: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	session, err := backend.Execute(ctx, "hi", ExecOptions{
		Timeout:                   5 * time.Second,
		SemanticInactivityTimeout: 50 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	for range session.Messages {
	}

	select {
	case result, ok := <-session.Result:
		if !ok {
			t.Fatal("result channel closed without a value")
		}
		if result.Status != "timeout" {
			t.Fatalf("expected timeout, got %q error=%q", result.Status, result.Error)
		}
		for _, want := range []string{
			GrokFirstStreamEventTimeoutMarker,
			"streaming-json event",
			"session_id=",
		} {
			if !strings.Contains(result.Error, want) {
				t.Fatalf("expected error to contain %q, got %q", want, result.Error)
			}
		}
		if result.SessionID == "" {
			t.Fatal("expected session id to be preserved")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for result")
	}
}

func TestGrokExecuteMissingBinary(t *testing.T) {
	t.Parallel()
	backend, err := New("grok", Config{ExecutablePath: filepath.Join(t.TempDir(), "no-such-grok"), Logger: slog.Default()})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	_, err = backend.Execute(context.Background(), "hi", ExecOptions{})
	if err == nil {
		t.Fatal("expected error for missing binary")
	}
}

func TestGrokToolResultOutputFromDiff(t *testing.T) {
	t.Parallel()
	su := grokSessionUpdate{
		Content: json.RawMessage(`[{"type":"diff","path":"/tmp/OUT.txt","oldText":"","newText":"OK\n"}]`),
	}
	got := grokToolResultOutput(su)
	if !strings.Contains(got, "OUT.txt") {
		t.Fatalf("expected path in output, got %q", got)
	}
}

func TestEnsureGrokRuntimeHomeRespectsEnvOverride(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	got, err := ensureGrokRuntimeHome(map[string]string{"GROK_HOME": home}, slog.Default())
	if err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if got != home {
		t.Fatalf("expected override home %q, got %q", home, got)
	}
}

func TestValidateGrokAuthRejectsMissingOrEmptyAuth(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	if err := validateGrokAuth(home, nil); err == nil || !strings.Contains(err.Error(), "not logged in") {
		t.Fatalf("missing auth error = %v, want not logged in", err)
	}
	if err := validateGrokAuth(home, map[string]string{"XAI_API_KEY": "test-key"}); err != nil {
		t.Fatalf("XAI_API_KEY should allow missing auth.json: %v", err)
	}

	if err := os.WriteFile(filepath.Join(home, "auth.json"), nil, 0o600); err != nil {
		t.Fatalf("write empty auth: %v", err)
	}
	if err := validateGrokAuth(home, nil); err == nil || !strings.Contains(err.Error(), "not logged in") {
		t.Fatalf("empty auth error = %v, want not logged in", err)
	}

	if err := os.WriteFile(filepath.Join(home, "auth.json"), []byte(`{"token":"redacted"}`), 0o600); err != nil {
		t.Fatalf("write auth: %v", err)
	}
	if err := validateGrokAuth(home, nil); err != nil {
		t.Fatalf("valid auth error = %v", err)
	}
}

func writeTestGrokAuth(t *testing.T, home string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(home, "auth.json"), []byte(`{"token":"redacted"}`), 0o600); err != nil {
		t.Fatalf("write grok auth: %v", err)
	}
}

func TestBuildGrokEnvSetsHomeAndAutoupdater(t *testing.T) {
	t.Parallel()
	env := buildGrokEnv(map[string]string{"FOO": "bar"}, "/tmp/grok-rt")
	if got, ok := envLookup(env, "GROK_HOME"); !ok || got != "/tmp/grok-rt" {
		t.Fatalf("GROK_HOME: got %q ok=%v", got, ok)
	}
	if got, ok := envLookup(env, "GROK_DISABLE_AUTOUPDATER"); !ok || got != "1" {
		t.Fatalf("GROK_DISABLE_AUTOUPDATER: got %q ok=%v", got, ok)
	}
	if got, ok := envLookup(env, "FOO"); !ok || got != "bar" {
		t.Fatalf("FOO passthrough: got %q ok=%v", got, ok)
	}
	// Explicit GROK_HOME in extra wins.
	env2 := buildGrokEnv(map[string]string{"GROK_HOME": "/custom"}, "/tmp/grok-rt")
	if got, ok := envLookup(env2, "GROK_HOME"); !ok || got != "/custom" {
		t.Fatalf("explicit GROK_HOME should win, got %q", got)
	}
}

func containsArg(args []string, want string) bool {
	for _, a := range args {
		if a == want {
			return true
		}
	}
	return false
}

func containsPair(args []string, flag, value string) bool {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == flag && args[i+1] == value {
			return true
		}
	}
	return false
}
