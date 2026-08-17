package agent

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestPiCLIBackendRejectsResidentMixedRunSurface(t *testing.T) {
	cli := &piBackend{cfg: Config{Logger: slog.Default()}}
	if _, ok := any(cli).(PiRPCBackend); ok {
		t.Fatal("one-shot pi CLI backend must not implement PiRPCBackend")
	}
	if _, err := AsPiRPCBackend(cli); !errors.Is(err, ErrPiCLIResidentUnsupported) {
		t.Fatalf("AsPiRPCBackend(cli) err=%v, want ErrPiCLIResidentUnsupported", err)
	}
	rpc := NewPiRPCBackend(Config{Logger: slog.Default()})
	t.Cleanup(rpc.Close)
	got, err := AsPiRPCBackend(rpc)
	if err != nil || got == nil {
		t.Fatalf("AsPiRPCBackend(rpc) = %v, %v", got, err)
	}
}

func TestBuildPiArgsNoToolAllowlist(t *testing.T) {
	// Extension tools registered via Pi's registerTool() must not be
	// filtered out by a hardcoded --tools allowlist. Omitting --tools
	// lets Pi use its full tool registry. See #2379.
	args := buildPiArgs("test prompt", "/tmp/session.jsonl", ExecOptions{}, slog.Default())
	for i, arg := range args {
		if arg == "--tools" {
			t.Errorf("buildPiArgs emits --tools %q; should not restrict tool registry (see #2379)", args[i+1])
		}
	}
}

func TestBuildPiArgsRestrictedProfileUsesEmptyToolAllowlist(t *testing.T) {
	for name, args := range map[string][]string{
		"one-shot": buildPiArgs("probe", "/tmp/session.jsonl", ExecOptions{DisableTools: true, MaxOutputTokens: 96, piOutputLimitExtension: "/tmp/output-limit.mjs", CustomArgs: []string{"--tools", "bash", "--extension", "/tmp/evil.mjs"}}, slog.Default()),
		"rpc":      buildPiRPCArgs("/tmp/session.jsonl", ExecOptions{DisableTools: true, MaxOutputTokens: 96, piOutputLimitExtension: "/tmp/output-limit.mjs", CustomArgs: []string{"--tools", "bash", "--extension", "/tmp/evil.mjs"}}, slog.Default()),
	} {
		found := 0
		controlExtension := 0
		requiredStandalone := map[string]bool{
			"--no-extensions":       false,
			"--no-skills":           false,
			"--no-prompt-templates": false,
			"--no-context-files":    false,
		}
		for i, arg := range args {
			if arg == "--tools" && i+1 < len(args) && args[i+1] == "" {
				found++
			}
			if arg == "--extension" && i+1 < len(args) && args[i+1] == "/tmp/output-limit.mjs" {
				controlExtension++
			}
			if _, ok := requiredStandalone[arg]; ok {
				requiredStandalone[arg] = true
			}
			if arg == "bash" || arg == "/tmp/evil.mjs" {
				t.Fatalf("%s args let custom args override the empty tool registry: %#v", name, args)
			}
		}
		if found != 1 {
			t.Fatalf("%s args do not enforce an empty tool allowlist: %#v", name, args)
		}
		if name == "one-shot" && controlExtension != 1 {
			t.Fatalf("%s args do not load exactly one trusted output-limit extension: %#v", name, args)
		}
		if name == "one-shot" {
			for flag, present := range requiredStandalone {
				if !present {
					t.Fatalf("%s args missing %s: %#v", name, flag, args)
				}
			}
		}
	}
}

func TestPiOutputLimitExtensionCapsActiveModel(t *testing.T) {
	path, err := newPiOutputLimitExtension(96)
	if err != nil {
		t.Fatalf("newPiOutputLimitExtension: %v", err)
	}
	t.Cleanup(func() { _ = os.Remove(path) })
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read output-limit extension: %v", err)
	}
	for _, want := range []string{"const limit = 96", "model.maxTokens = Math.min", "before_agent_start", "model_select", "ctx.abort()"} {
		if !strings.Contains(string(content), want) {
			t.Fatalf("output-limit extension missing %q:\n%s", want, content)
		}
	}
}

func TestEnforcePiOutputTokenLimitFailsClosed(t *testing.T) {
	status, errText := enforcePiOutputTokenLimit("completed", "", map[string]TokenUsage{
		"model-a": {OutputTokens: 60},
		"model-b": {OutputTokens: 37},
	}, 96)
	if status != "failed" || !strings.Contains(errText, "97 tokens") {
		t.Fatalf("output limit result = status:%q error:%q", status, errText)
	}
	status, errText = enforcePiOutputTokenLimit("completed", "", map[string]TokenUsage{"model": {OutputTokens: 96}}, 96)
	if status != "completed" || errText != "" {
		t.Fatalf("boundary result = status:%q error:%q", status, errText)
	}
}

func TestPiReportedSessionIDOmittedForEphemeralRun(t *testing.T) {
	if got := piReportedSessionID("/tmp/probe.jsonl", true); got != "" {
		t.Fatalf("ephemeral session id = %q, want empty", got)
	}
	if got := piReportedSessionID("/tmp/chat.jsonl", false); got != "/tmp/chat.jsonl" {
		t.Fatalf("durable session id = %q", got)
	}
}

func TestPiExecuteEphemeralSessionIsDeletedAndNotReported(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test executable uses a POSIX shell")
	}
	dir := t.TempDir()
	fakePath := filepath.Join(dir, "pi")
	markerPath := filepath.Join(dir, "session-path")
	script := `#!/bin/sh
session=""
take_next=0
for arg in "$@"; do
  if [ "$take_next" = 1 ]; then session="$arg"; take_next=0; continue; fi
  if [ "$arg" = "--session" ]; then take_next=1; fi
done
printf '%s' "$session" > "$PI_EPHEMERAL_MARKER"
cat >/dev/null
printf '%s\n' '{"type":"agent_start"}'
printf '%s\n' '{"type":"turn_end","message":{"role":"assistant","model":"test","stopReason":"stop","usage":{"input":1,"output":1}}}'
`
	writeTestExecutable(t, fakePath, []byte(script))
	backend := &piBackend{cfg: Config{ExecutablePath: fakePath, Env: map[string]string{"PI_EPHEMERAL_MARKER": markerPath}, Logger: slog.Default()}}
	session, err := backend.Execute(context.Background(), "probe", ExecOptions{Cwd: dir, EphemeralSession: true})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	result := <-session.Result
	if result.SessionID != "" {
		t.Fatalf("ephemeral result exposed session id %q", result.SessionID)
	}
	pathBytes, err := os.ReadFile(markerPath)
	if err != nil {
		t.Fatalf("read session marker: %v", err)
	}
	ephemeralPath := string(pathBytes)
	deadline := time.Now().Add(2 * time.Second)
	for {
		_, statErr := os.Stat(ephemeralPath)
		if os.IsNotExist(statErr) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("ephemeral session file still exists: %s (stat error: %v)", ephemeralPath, statErr)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestBuildPiEnvFiltersInheritedPackageDir(t *testing.T) {
	t.Parallel()

	env := mergeEnvWithInheritedBlocklist([]string{
		"PATH=/usr/bin",
		"PI_PACKAGE_DIR=/Users/frank/.slock/runtime-pkg",
		"MULTICA_TOKEN=must-not-leak",
	}, nil, piInheritedEnvBlocklist)

	for _, entry := range env {
		if strings.HasPrefix(entry, piPackageDirEnvKey+"=") {
			t.Fatalf("inherited %s leaked to Pi: %v", piPackageDirEnvKey, env)
		}
	}
	if !containsEnvEntry(env, "PATH=/usr/bin") {
		t.Fatalf("PATH was not preserved: %v", env)
	}
	if containsEnvEntry(env, "MULTICA_TOKEN=must-not-leak") {
		t.Fatalf("ordinary child env filtering regressed: %v", env)
	}
}

func TestBuildPiEnvPreservesExplicitPackageDirOverride(t *testing.T) {
	t.Parallel()

	env := mergeEnvWithInheritedBlocklist([]string{
		"PATH=/usr/bin",
		"PI_PACKAGE_DIR=/Users/frank/.slock/runtime-pkg",
	}, map[string]string{piPackageDirEnvKey: "/opt/pi-package"}, piInheritedEnvBlocklist)

	if !containsEnvEntry(env, "PI_PACKAGE_DIR=/opt/pi-package") {
		t.Fatalf("explicit Pi package dir override was not preserved: %v", env)
	}
	if containsEnvEntry(env, "PI_PACKAGE_DIR=/Users/frank/.slock/runtime-pkg") {
		t.Fatalf("inherited Pi package dir leaked alongside override: %v", env)
	}
}

func TestBuildPiEnvPinsDefaultCodingAgentDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(piCodingAgentDirEnvKey, "")

	env := buildPiEnv(nil)
	want := piCodingAgentDirEnvKey + "=" + filepath.Join(home, ".pi", "agent")
	if !containsEnvEntry(env, want) {
		t.Fatalf("Pi coding agent dir was not pinned to the effective home: %v", env)
	}
}

func TestBuildPiEnvPreservesExplicitCodingAgentDir(t *testing.T) {
	t.Setenv(piCodingAgentDirEnvKey, filepath.Join(t.TempDir(), "inherited"))
	explicit := filepath.Join(t.TempDir(), "explicit")

	env := buildPiEnv(map[string]string{piCodingAgentDirEnvKey: explicit})
	if !containsEnvEntry(env, piCodingAgentDirEnvKey+"="+explicit) {
		t.Fatalf("explicit Pi coding agent dir override was not preserved: %v", env)
	}
}

func containsEnvEntry(env []string, want string) bool {
	for _, entry := range env {
		if entry == want {
			return true
		}
	}
	return false
}

func TestBuildPiArgsBasicFlags(t *testing.T) {
	const sessionID = "019ffcb7-0848-7087-a6fd-c14da72509ea"
	args := buildPiArgs("hello world", sessionID, ExecOptions{
		Cwd:           "/agent-root",
		Model:         "anthropic/claude-sonnet-4-20250514",
		SystemPrompt:  "be helpful",
		ThinkingLevel: "high",
	}, slog.Default())

	joined := strings.Join(args, " ")
	for _, want := range []string{"-p", "--mode json", "--session-id " + sessionID, "--session-dir /agent-root/.pi-sessions", "--provider anthropic", "--model claude-sonnet-4-20250514", "--thinking high", "--append-system-prompt"} {
		if !strings.Contains(joined, want) {
			t.Errorf("expected %q in args, got: %v", want, args)
		}
	}

	// Prompt must be the last positional argument.
	if args[len(args)-1] != "hello world" {
		t.Errorf("prompt should be last arg, got %q", args[len(args)-1])
	}
}

func TestBuildPiArgsUsesAgentLocalDirectoryForOpaqueSessionID(t *testing.T) {
	cwd := filepath.Join(t.TempDir(), "agent-root")
	const sessionID = "019ffcb7-0848-7087-a6fd-c14da72509ea"

	for name, args := range map[string][]string{
		"one-shot": buildPiArgs("hello", sessionID, ExecOptions{Cwd: cwd}, slog.Default()),
		"rpc":      buildPiRPCArgs(sessionID, ExecOptions{Cwd: cwd}, slog.Default()),
	} {
		joined := strings.Join(args, " ")
		want := "--session-id " + sessionID + " --session-dir " + filepath.Join(cwd, ".pi-sessions")
		if !strings.Contains(joined, want) {
			t.Fatalf("%s args = %v, want %q", name, args, want)
		}
		if strings.Contains(joined, "--session ") {
			t.Fatalf("%s treated opaque session ID as a path: %v", name, args)
		}
	}
}

func TestBuildPiArgsNeverInterpretsSessionIDAsPath(t *testing.T) {
	args := buildPiArgs("hello", "/tmp/legacy.jsonl", ExecOptions{Cwd: "/agent-root"}, slog.Default())
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "--session-id /tmp/legacy.jsonl") {
		t.Fatalf("session value was not passed through the opaque-ID flag: %v", args)
	}
	if strings.Contains(joined, "--session /tmp/legacy.jsonl") {
		t.Fatalf("session ID was interpreted as a file path: %v", args)
	}
}

func TestBuildPiArgsBlocksCustomSessionIdentityOverrides(t *testing.T) {
	const sessionID = "019ffcb7-0848-7087-a6fd-c14da72509ea"
	args := buildPiArgs("hello", sessionID, ExecOptions{
		Cwd:        "/agent-root",
		CustomArgs: []string{"--session-id", "wrong-id", "--session-dir", "/tmp/wrong"},
	}, slog.Default())
	joined := strings.Join(args, " ")
	if strings.Contains(joined, "wrong-id") || strings.Contains(joined, "/tmp/wrong") {
		t.Fatalf("custom args overrode daemon-owned Pi session identity: %v", args)
	}
}

func TestBuildPiArgsForExecutionMovesPromptToStdinOnEveryPlatform(t *testing.T) {
	prompt := strings.Repeat("group context\n", 32*1024)
	args, stdinPrompt := buildPiArgsForExecution(prompt, "019ffcb7-0848-7087-a6fd-c14da72509ea", ExecOptions{Cwd: "/agent-root"}, slog.Default())
	if stdinPrompt != prompt {
		t.Fatalf("stdin prompt was not preserved")
	}
	for _, arg := range args {
		if strings.Contains(arg, "group context") {
			t.Fatalf("prompt leaked into argv: %#v", args)
		}
	}
}

func TestBuildPiArgsCustomArgsAppended(t *testing.T) {
	// Users can still restrict tools via custom_args if desired.
	args := buildPiArgs("prompt", "/tmp/s.jsonl", ExecOptions{
		CustomArgs: []string{"--tools", "read,bash"},
	}, slog.Default())

	found := false
	for i, arg := range args {
		if arg == "--tools" && i+1 < len(args) && args[i+1] == "read,bash" {
			found = true
		}
	}
	if !found {
		t.Errorf("custom --tools should pass through via custom_args, got: %v", args)
	}
}

// TestPiExecuteAttachesStdinPipe verifies that the Pi backend spawns the
// child with an explicit stdin pipe (FIFO) instead of leaving cmd.Stdin
// nil. Without an explicit pipe, Pi has been observed to block under
// systemd waiting for stdin events (#2188); attaching and immediately
// closing a pipe delivers a clean EOF on a FIFO and unblocks Pi.
//
// The probe is structural rather than behavioral: a shell script in
// place of `pi` inspects /proc/self/fd/0 and only emits a valid event
// stream if stdin is a FIFO. If the fix regresses (stdin nil → /dev/null
// char device), the fake exits non-zero and the test fails.
func TestPiExecuteAttachesStdinPipe(t *testing.T) {
	t.Parallel()
	if runtime.GOOS != "linux" {
		// /proc/self/fd/0 is Linux-specific; skipping elsewhere keeps
		// the assertion portable without losing CI coverage.
		t.Skip("stdin fd inspection relies on /proc/self/fd/0")
	}

	fakePath := filepath.Join(t.TempDir(), "pi")
	script := "#!/bin/sh\n" +
		"kind=$(stat -c '%F' -L /proc/self/fd/0 2>/dev/null || echo unknown)\n" +
		"case \"$kind\" in\n" +
		"  fifo|*pipe*)\n" +
		// Consume the prompt before emitting events, matching Pi's stdin contract
		// and avoiding a scheduler-dependent broken pipe in this test double.
		"    cat >/dev/null\n" +
		"    printf '%s\\n' '{\"type\":\"agent_start\"}'\n" +
		"    printf '%s\\n' '{\"type\":\"turn_end\",\"message\":{\"role\":\"assistant\",\"model\":\"test\",\"usage\":{\"input\":1,\"output\":1,\"cacheRead\":0,\"cacheWrite\":0,\"totalTokens\":2}}}'\n" +
		"    exit 0\n" +
		"    ;;\n" +
		"esac\n" +
		"printf 'stdin was %s; expected fifo\\n' \"$kind\" >&2\n" +
		"exit 1\n"
	writeTestExecutable(t, fakePath, []byte(script))

	backend, err := New("pi", Config{ExecutablePath: fakePath, Logger: slog.Default()})
	if err != nil {
		t.Fatalf("new pi backend: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	session, err := backend.Execute(ctx, "prompt-ignored", ExecOptions{Timeout: 5 * time.Second})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	go func() {
		for range session.Messages {
		}
	}()

	select {
	case result, ok := <-session.Result:
		if !ok {
			t.Fatal("result channel closed without a value")
		}
		if result.Status != "completed" {
			t.Fatalf("expected status=completed (stdin attached as fifo), got %q (error=%q)", result.Status, result.Error)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("timeout waiting for result")
	}
}

func TestPiExecuteKeepsLargePromptOffArgvAndWritesItToStdin(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("fake executable is a POSIX shell script")
	}

	const promptBytes = 256 * 1024
	fakePath := filepath.Join(t.TempDir(), "pi")
	script := "#!/bin/sh\n" +
		"bytes=$(wc -c | tr -d ' ')\n" +
		"if [ \"$bytes\" != \"262144\" ]; then\n" +
		"  printf 'stdin bytes=%s; want 262144\\n' \"$bytes\" >&2\n" +
		"  exit 1\n" +
		"fi\n" +
		"printf '%s\\n' '{\"type\":\"agent_start\"}'\n" +
		"printf '%s\\n' '{\"type\":\"turn_end\",\"message\":{\"role\":\"assistant\",\"model\":\"test\",\"usage\":{\"input\":1,\"output\":1,\"cacheRead\":0,\"cacheWrite\":0,\"totalTokens\":2}}}'\n"
	writeTestExecutable(t, fakePath, []byte(script))

	backend, err := New("pi", Config{ExecutablePath: fakePath, Logger: slog.Default()})
	if err != nil {
		t.Fatalf("new pi backend: %v", err)
	}
	session, err := backend.Execute(t.Context(), strings.Repeat("x", promptBytes), ExecOptions{Timeout: 5 * time.Second})
	if err != nil {
		t.Fatalf("execute large prompt: %v", err)
	}
	go func() {
		for range session.Messages {
		}
	}()
	result := <-session.Result
	if result.Status != "completed" {
		t.Fatalf("large stdin prompt status=%q error=%q", result.Status, result.Error)
	}
}

func TestPiExecuteClearsResumeSessionWhenPiFailsBeforeAgentStart(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("fake executable is a POSIX shell script")
	}

	fakePath := filepath.Join(t.TempDir(), "pi")
	writeTestExecutable(t, fakePath, []byte("#!/bin/sh\nprintf '%s\\n' 'resume session is unreadable' >&2\nexit 1\n"))

	backend, err := New("pi", Config{ExecutablePath: fakePath, Logger: slog.Default()})
	if err != nil {
		t.Fatalf("new pi backend: %v", err)
	}
	resumePath := filepath.Join(t.TempDir(), "stale-session.jsonl")
	session, err := backend.Execute(t.Context(), "retry this prompt", ExecOptions{
		ResumeSessionID: resumePath,
		Timeout:         5 * time.Second,
	})
	if err != nil {
		t.Fatalf("execute resumed Pi session: %v", err)
	}
	go func() {
		for range session.Messages {
		}
	}()
	result := <-session.Result
	if result.Status != "failed" {
		t.Fatalf("resume failure status=%q error=%q", result.Status, result.Error)
	}
	if result.SessionID != "" {
		t.Fatalf("resume failure before agent_start returned session ID %q; daemon fresh-session retry will not run", result.SessionID)
	}
}

func TestPiExecuteFailsWhenChildDoesNotReadLargeStdinPrompt(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("fake executable is a POSIX shell script")
	}

	fakePath := filepath.Join(t.TempDir(), "pi")
	// Emit a superficially successful terminal event without consuming stdin.
	// A prompt larger than the pipe buffer must not be reported as completed:
	// Pi never received the request it was supposed to execute.
	script := "#!/bin/sh\n" +
		"printf '%s\\n' '{\"type\":\"turn_end\",\"message\":{\"role\":\"assistant\",\"model\":\"test\",\"stopReason\":\"stop\",\"usage\":{\"input\":0,\"output\":0}}}'\n" +
		"exit 0\n"
	writeTestExecutable(t, fakePath, []byte(script))

	backend, err := New("pi", Config{ExecutablePath: fakePath, Logger: slog.Default()})
	if err != nil {
		t.Fatalf("new pi backend: %v", err)
	}
	session, err := backend.Execute(t.Context(), strings.Repeat("x", 1024*1024), ExecOptions{Timeout: 5 * time.Second})
	if err != nil {
		t.Fatalf("execute Pi with unread stdin: %v", err)
	}
	go func() {
		for range session.Messages {
		}
	}()
	result := <-session.Result
	if result.Status != "failed" {
		t.Fatalf("unread stdin prompt status=%q error=%q, want failed", result.Status, result.Error)
	}
	if !strings.Contains(result.Error, "stdin prompt") {
		t.Fatalf("unread stdin prompt error=%q, want delivery failure", result.Error)
	}
}

func TestStripPiToolCallMarkup(t *testing.T) {
	tests := map[string]string{
		`before call:bash{command:<|"|>cd repo/path && ls -F<|"|>}<tool_call|> after`:                           "before  after",
		`before call:read{path:<|"|>repo/path/roles/example/verify.yml<|"|>} after`:                             "before  after",
		`before response:bash{command:<|"|>multica issue comment list issue-id --all --output json<|"|>} after`: "before  after",
		`before call:bash{command:<|"|>printf '{"key":"value"}'<|"|>} after`:                                    "before  after",
		`before <|turn>model after`: "before  after",
	}
	for in, want := range tests {
		got := stripPiToolCallMarkup(in)
		if got != want {
			t.Fatalf("unexpected stripped text: %q, want %q", got, want)
		}
	}
}

func TestDrainPiTextBufferSplitToolCall(t *testing.T) {
	chunks := []string{
		"before ca",
		`ll:bash{command:<|"|>ls -R repo/path`,
		`/roles/example<|"|>}`,
		" after",
	}
	var buf strings.Builder
	var got strings.Builder
	for _, chunk := range chunks {
		got.WriteString(drainPiTextBuffer(&buf, chunk))
	}
	got.WriteString(flushPiTextBuffer(&buf))
	if got.String() != "before  after" {
		t.Fatalf("unexpected streamed text: %q", got.String())
	}
}

func TestDrainPiTextBufferSplitControlToken(t *testing.T) {
	chunks := []string{"before <|tu", "rn>model after"}
	var buf strings.Builder
	var got strings.Builder
	for _, chunk := range chunks {
		got.WriteString(drainPiTextBuffer(&buf, chunk))
	}
	got.WriteString(flushPiTextBuffer(&buf))
	if got.String() != "before  after" {
		t.Fatalf("unexpected streamed text: %q", got.String())
	}
}

func TestPiExecuteFailsAssistantStopReasonError(t *testing.T) {
	t.Parallel()

	fakePath := filepath.Join(t.TempDir(), "pi")
	script := "#!/bin/sh\n" +
		"printf '%s\\n' '{\"type\":\"agent_start\"}'\n" +
		"printf '%s\\n' '{\"type\":\"message_end\",\"message\":{\"role\":\"assistant\",\"model\":\"test\",\"stopReason\":\"error\",\"errorMessage\":\"proxy returned malformed JSON\"}}'\n" +
		"printf '%s\\n' '{\"type\":\"turn_end\",\"message\":{\"role\":\"assistant\",\"model\":\"test\",\"stopReason\":\"error\",\"errorMessage\":\"proxy returned malformed JSON\",\"usage\":{\"input\":1,\"output\":0,\"cacheRead\":0,\"cacheWrite\":0,\"totalTokens\":1}}}'\n"
	writeTestExecutable(t, fakePath, []byte(script))

	backend, err := New("pi", Config{ExecutablePath: fakePath, Logger: slog.Default()})
	if err != nil {
		t.Fatalf("new pi backend: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	resumePath := filepath.Join(t.TempDir(), "established-session.jsonl")
	session, err := backend.Execute(ctx, "prompt-ignored", ExecOptions{
		ResumeSessionID: resumePath,
		Timeout:         5 * time.Second,
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	go func() {
		for range session.Messages {
		}
	}()

	select {
	case result, ok := <-session.Result:
		if !ok {
			t.Fatal("result channel closed without a value")
		}
		if result.Status != "failed" {
			t.Fatalf("expected status=failed, got %q (error=%q)", result.Status, result.Error)
		}
		if result.Error != "proxy returned malformed JSON" {
			t.Fatalf("expected propagated error message, got %q", result.Error)
		}
		if result.SessionID != resumePath {
			t.Fatalf("failure after agent_start session=%q, want established resume path %q", result.SessionID, resumePath)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("timeout waiting for result")
	}
}

func TestPiStopReasonAllowsEarlyComplete(t *testing.T) {
	// Strict allowlist: only a genuinely terminal "stop" turn early-completes.
	allow := []string{"stop", "STOP", " stop "}
	for _, s := range allow {
		if !piStopReasonAllowsEarlyComplete(s) {
			t.Errorf("piStopReasonAllowsEarlyComplete(%q) = false, want true", s)
		}
	}
	// Tool-use continuations and any unseen/empty reason must NOT early-complete
	// (fail direction is "slow", never "mute").
	deny := []string{"toolUse", "tool_use", "error", "", "endTurn", "maxTokens", "length", "unknown"}
	for _, s := range deny {
		if piStopReasonAllowsEarlyComplete(s) {
			t.Errorf("piStopReasonAllowsEarlyComplete(%q) = true, want false", s)
		}
	}
}

// TestPiExecuteToolUseTurnDoesNotEarlyComplete guards the "先查后答" silent-DM
// regression: a chat run whose first turn ends by calling a tool
// (stopReason=toolUse) must NOT early-complete on that turn_end. Doing so
// makes the daemon kill Pi before the follow-up turn that produces the
// visible answer, yielding an empty output the daemon mis-reads as no_reply.
// The fixed backend early-completes only on the later terminal turn, so the
// Result carries the model's actual answer emitted after the tool result.
func TestPiExecuteToolUseTurnDoesNotEarlyComplete(t *testing.T) {
	t.Parallel()

	fakePath := filepath.Join(t.TempDir(), "pi")
	// Turn 1 ends with a tool call (toolUse) — the model has not answered yet.
	// Turn 2 streams the real answer and ends terminally (stopReason=stop).
	script := "#!/bin/sh\n" +
		"cat >/dev/null\n" +
		"printf '%s\\n' '{\"type\":\"agent_start\"}'\n" +
		"printf '%s\\n' '{\"type\":\"turn_end\",\"message\":{\"role\":\"assistant\",\"model\":\"test\",\"stopReason\":\"toolUse\",\"usage\":{\"input\":1,\"output\":1,\"cacheRead\":0,\"cacheWrite\":0,\"totalTokens\":2}}}'\n" +
		"printf '%s\\n' '{\"type\":\"message_update\",\"assistantMessageEvent\":{\"type\":\"text_delta\",\"delta\":\"final answer\"}}'\n" +
		"printf '%s\\n' '{\"type\":\"turn_end\",\"message\":{\"role\":\"assistant\",\"model\":\"test\",\"stopReason\":\"stop\",\"usage\":{\"input\":1,\"output\":2,\"cacheRead\":0,\"cacheWrite\":0,\"totalTokens\":3}}}'\n" +
		"exit 0\n"
	writeTestExecutable(t, fakePath, []byte(script))

	backend, err := New("pi", Config{ExecutablePath: fakePath, Logger: slog.Default()})
	if err != nil {
		t.Fatalf("new pi backend: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	session, err := backend.Execute(ctx, "prompt-ignored", ExecOptions{Timeout: 5 * time.Second})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	go func() {
		for range session.Messages {
		}
	}()

	select {
	case result, ok := <-session.Result:
		if !ok {
			t.Fatal("result channel closed without a value")
		}
		if result.Status != "completed" {
			t.Fatalf("expected status=completed, got %q (error=%q)", result.Status, result.Error)
		}
		// With the bug, early-complete fired on the toolUse turn_end and the
		// Result carried an empty output. The fix defers to the terminal turn.
		if result.Output != "final answer" {
			t.Fatalf("expected output %q (answer produced after the tool turn), got %q — toolUse turn_end must not early-complete", "final answer", result.Output)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("timeout waiting for result")
	}
}

func TestBuildPiRPCArgsLoadsCaptureExtensionWithNormalToolsEnabled(t *testing.T) {
	captureExt := filepath.Join(t.TempDir(), "capture.mjs")
	writeTestFile(t, captureExt, []byte("// capture"))
	args := buildPiRPCArgs("/tmp/s.jsonl", ExecOptions{piCaptureExtension: captureExt}, slog.Default())
	foundCapture := false
	for i, arg := range args {
		if arg == "--tools" && i+1 < len(args) && args[i+1] == "" {
			t.Fatalf("capture must load with normal tools enabled, got empty allowlist: %#v", args)
		}
		if arg == "--extension" && i+1 < len(args) && args[i+1] == captureExt {
			foundCapture = true
		}
	}
	if !foundCapture {
		t.Fatalf("expected trusted capture extension in RPC args: %#v", args)
	}
}

func TestBuildPiRPCArgsBlocksCustomExtensionOverrideWhenCaptureLoaded(t *testing.T) {
	captureExt := filepath.Join(t.TempDir(), "capture.mjs")
	writeTestFile(t, captureExt, []byte("// capture"))
	evilExt := filepath.Join(t.TempDir(), "evil.mjs")
	writeTestFile(t, evilExt, []byte("// evil"))
	args := buildPiRPCArgs("/tmp/s.jsonl", ExecOptions{
		piCaptureExtension: captureExt,
		CustomArgs:         []string{"--extension", evilExt, "-e", evilExt},
	}, slog.Default())
	foundCapture := false
	for i, arg := range args {
		if arg == "--extension" || arg == "-e" {
			if i+1 >= len(args) {
				t.Fatalf("extension flag missing value: %#v", args)
			}
			if args[i+1] == evilExt {
				t.Fatalf("custom args must not inject untrusted extensions while capture is loaded: %#v", args)
			}
			if args[i+1] == captureExt {
				foundCapture = true
			}
		}
	}
	if !foundCapture {
		t.Fatalf("trusted capture extension missing after custom-arg filtering: %#v", args)
	}
}

func TestBuildPiArgsTrustedExtension_Disabled(t *testing.T) {
	// TrustedExtensionPaths is not accepted unless DisableTools is true.
	_, err := validateTrustedExtensionPaths([]string{"/tmp/d.ts"}, "/tmp", false)
	if err == nil {
		t.Fatal("expected error when TrustedExtensionPaths is set without DisableTools")
	}
}

func TestBuildPiArgsTrustedExtension_EmitsExtensionFlag(t *testing.T) {
	trustedRoot := t.TempDir()
	extPath := filepath.Join(trustedRoot, "diagnosis.ts")
	writeTestFile(t, extPath, []byte("// trusted extension"))

	args := buildPiArgs("prompt", "/tmp/s.jsonl", ExecOptions{
		DisableTools:          true,
		TrustedExtensionPaths: []string{extPath},
		TrustedExtensionRoot:  trustedRoot,
	}, slog.Default())

	found := false
	for _, arg := range args {
		if arg == "--extension" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected --extension flag for trusted extension, got: %v", args)
	}
	// --no-extensions should still be present (disables discovery but explicit
	// extensions are still loaded).
	hasNoExtensions := false
	for _, arg := range args {
		if arg == "--no-extensions" {
			hasNoExtensions = true
		}
	}
	if !hasNoExtensions {
		t.Fatalf("expected --no-extensions flag, got: %v", args)
	}
}

func TestBuildPiArgsTrustedExtension_RejectsRelativePath(t *testing.T) {
	_, err := validateTrustedExtensionPaths([]string{"relative/path.ts"}, "/tmp", true)
	if err == nil {
		t.Fatal("expected error for relative path")
	}
}

func TestBuildPiArgsTrustedExtension_RejectsMissingFile(t *testing.T) {
	_, err := validateTrustedExtensionPaths([]string{"/nonexistent/extension.ts"}, "/nonexistent", true)
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestBuildPiArgsTrustedExtension_RejectsDirectory(t *testing.T) {
	trustedRoot := t.TempDir()
	_, err := validateTrustedExtensionPaths([]string{trustedRoot}, trustedRoot, true)
	if err == nil {
		t.Fatal("expected error for directory path")
	}
}

func TestBuildPiArgsTrustedExtension_RejectsOutsideRoot(t *testing.T) {
	trustedRoot := t.TempDir()
	outsideRoot := t.TempDir()
	extPath := filepath.Join(outsideRoot, "extension.ts")
	writeTestFile(t, extPath, []byte("// outside"))

	_, err := validateTrustedExtensionPaths([]string{extPath}, trustedRoot, true)
	if err == nil {
		t.Fatal("expected error for path outside trusted root")
	}
}

func TestBuildPiArgsTrustedExtension_RejectsDuplicatePaths(t *testing.T) {
	trustedRoot := t.TempDir()
	extPath := filepath.Join(trustedRoot, "ext.ts")
	writeTestFile(t, extPath, []byte("// ext"))

	paths, err := validateTrustedExtensionPaths([]string{extPath, extPath}, trustedRoot, true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(paths) != 1 {
		t.Fatalf("expected 1 deduplicated path, got %d: %v", len(paths), paths)
	}
}

func TestBuildPiRPCArgsTrustedExtension_DisabledProfile(t *testing.T) {
	trustedRoot := t.TempDir()
	extPath := filepath.Join(trustedRoot, "diagnosis.ts")
	writeTestFile(t, extPath, []byte("// trusted extension"))

	args := buildPiRPCArgs("/tmp/s.jsonl", ExecOptions{
		DisableTools:          true,
		TrustedExtensionPaths: []string{extPath},
		TrustedExtensionRoot:  trustedRoot,
	}, slog.Default())

	found := false
	for _, arg := range args {
		if arg == "--extension" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected --extension flag in RPC args, got: %v", args)
	}
}

func writeTestFile(t *testing.T, path string, content []byte) {
	t.Helper()
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatalf("write test file: %v", err)
	}
}

func TestFlushPiTextBufferKeepsUnmatchedToolPrefixes(t *testing.T) {
	tests := []string{
		"plain response: see below",
		"plain call: see below",
		`plain call:bash{command:<|"|>unterminated`,
	}
	for _, want := range tests {
		var buf strings.Builder
		got := drainPiTextBuffer(&buf, want)
		got += flushPiTextBuffer(&buf)
		if got != want {
			t.Fatalf("unexpected flushed text: %q, want %q", got, want)
		}
	}
}
