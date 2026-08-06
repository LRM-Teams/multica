package agent

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func quietAntigravityLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestBuildAntigravityArgsBasic(t *testing.T) {
	t.Parallel()

	args := buildAntigravityArgs(
		"hello",
		"/tmp/agy.log",
		20*time.Minute,
		ExecOptions{Cwd: "/work"},
		quietAntigravityLogger(),
	)

	want := []string{
		"-p", "hello",
		"--dangerously-skip-permissions",
		"--print-timeout", "20m0s",
		"--log-file", "/tmp/agy.log",
		"--add-dir", "/work",
	}
	if !slices.Equal(args, want) {
		t.Fatalf("buildAntigravityArgs basic mismatch\n got: %v\nwant: %v", args, want)
	}
}

func TestBuildAntigravityArgsModel(t *testing.T) {
	t.Parallel()

	// agy 1.0.6's --model takes the exact human display string (spaces +
	// parens), not a slug. It must ride as a single argv element so no shell
	// quoting is required, and it must sit before the user's custom args.
	args := buildAntigravityArgs(
		"hello",
		"/tmp/agy.log",
		20*time.Minute,
		ExecOptions{Cwd: "/work", Model: "Claude Opus 4.6 (Thinking)"},
		quietAntigravityLogger(),
	)

	want := []string{
		"-p", "hello",
		"--dangerously-skip-permissions",
		"--model", "Claude Opus 4.6 (Thinking)",
		"--print-timeout", "20m0s",
		"--log-file", "/tmp/agy.log",
		"--add-dir", "/work",
	}
	if !slices.Equal(args, want) {
		t.Fatalf("buildAntigravityArgs with model mismatch\n got: %v\nwant: %v", args, want)
	}

	// Empty model must omit the flag entirely so agy resolves its own default.
	bare := buildAntigravityArgs("hi", "/tmp/agy.log", 0, ExecOptions{}, quietAntigravityLogger())
	if slices.Contains(bare, "--model") {
		t.Fatalf("--model must be omitted when opts.Model is empty; got %v", bare)
	}
}

func TestBuildAntigravityArgsNoTimeoutOmitsPrintTimeout(t *testing.T) {
	t.Parallel()

	// timeout <= 0 means "no wall-clock cap" (MUL-3064): agy must be launched
	// WITHOUT --print-timeout, otherwise antigravityFormatTimeout(0) clamps to
	// 1s and the run is killed almost immediately — the opposite of "no cap".
	args := buildAntigravityArgs(
		"hello",
		"/tmp/agy.log",
		0,
		ExecOptions{Cwd: "/work"},
		quietAntigravityLogger(),
	)

	want := []string{
		"-p", "hello",
		"--dangerously-skip-permissions",
		"--log-file", "/tmp/agy.log",
		"--add-dir", "/work",
	}
	if !slices.Equal(args, want) {
		t.Fatalf("buildAntigravityArgs(timeout=0) mismatch\n got: %v\nwant: %v", args, want)
	}
	if slices.Contains(args, "--print-timeout") {
		t.Fatalf("--print-timeout must be omitted when timeout <= 0; got %v", args)
	}
}

func TestBuildAntigravityArgsResume(t *testing.T) {
	t.Parallel()

	args := buildAntigravityArgs(
		"continue",
		"/tmp/agy.log",
		20*time.Minute,
		ExecOptions{ResumeSessionID: "b8b263a4-4b2f-4339-acc9-78b248e2b606"},
		quietAntigravityLogger(),
	)

	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "--conversation b8b263a4-4b2f-4339-acc9-78b248e2b606") {
		t.Fatalf("expected --conversation flag with id; got %v", args)
	}
}

func TestBuildAntigravityArgsFiltersBlockedCustomArgs(t *testing.T) {
	t.Parallel()

	args := buildAntigravityArgs(
		"go",
		"/tmp/agy.log",
		time.Minute,
		ExecOptions{
			// Each blocked flag below must be stripped silently — the daemon
			// owns these because they're required for non-interactive,
			// resume-aware operation.
			CustomArgs: []string{
				"-p", "hijacked-prompt",
				"--continue",
				"-c",
				"--conversation", "bad-id",
				"--model", "sneaky-model", // managed via ExecOptions.Model
				"--dangerously-skip-permissions",
				"--print-timeout", "1h",
				"--log-file", "/elsewhere.log",
				"--add-dir", "/extra", // user-added workspace dir should survive
			},
		},
		quietAntigravityLogger(),
	)

	joined := strings.Join(args, " ")
	// Prompt argument should appear exactly once — the daemon's, not the
	// user's hijacked copy.
	pCount := 0
	for _, a := range args {
		if a == "-p" {
			pCount++
		}
	}
	if pCount != 1 {
		t.Errorf("expected exactly one -p flag, got args=%v", args)
	}
	if strings.Contains(joined, "hijacked-prompt") {
		t.Errorf("custom -p value leaked through filter: %v", args)
	}
	if strings.Contains(joined, "bad-id") {
		t.Errorf("custom --conversation value leaked through filter: %v", args)
	}
	if strings.Contains(joined, "sneaky-model") {
		t.Errorf("custom --model value leaked through filter: %v", args)
	}
	if strings.Contains(joined, "/elsewhere.log") {
		t.Errorf("custom --log-file value leaked through filter: %v", args)
	}
	if !strings.Contains(joined, "--add-dir /extra") {
		t.Errorf("non-blocked --add-dir flag should pass through: %v", args)
	}
}

func TestAntigravityFormatTimeoutClampsSubSecond(t *testing.T) {
	t.Parallel()
	if got := antigravityFormatTimeout(0); got != "1s" {
		t.Errorf("antigravityFormatTimeout(0) = %q, want 1s", got)
	}
	if got := antigravityFormatTimeout(20 * time.Minute); got != "20m0s" {
		t.Errorf("antigravityFormatTimeout(20m) = %q, want 20m0s", got)
	}
}

func TestIsAntigravityWrapperLine(t *testing.T) {
	t.Parallel()

	for _, line := range []string{
		"[Slock Wrapper] Starting Antigravity CLI...",
		"[Multica Wrapper] Preparing workspace",
		"Slock Wrapper: booting",
	} {
		if !isAntigravityWrapperLine(line) {
			t.Fatalf("isAntigravityWrapperLine(%q) = false, want true", line)
		}
	}

	for _, line := range []string{
		"I will check the available permissions...",
		"Here is the file content.",
	} {
		if isAntigravityWrapperLine(line) {
			t.Fatalf("isAntigravityWrapperLine(%q) = true, want false", line)
		}
	}
}

func TestAntigravityExecuteSplitsWrapperAndProse(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	agyPath := filepath.Join(dir, "agy")
	script := `#!/bin/sh
while [ "$#" -gt 0 ]; do
	if [ "$1" = "--log-file" ]; then
		shift
		log_file="$1"
	fi
	shift
done
printf '%s\n' '[Slock Wrapper] Starting Antigravity CLI...'
printf '%s\n' 'I will write the requested file.'
printf '%s\n' '[Multica Wrapper] Done'
printf '%s\n' 'Created hello.txt.'
if [ -n "$log_file" ]; then
	printf '%s\n' 'I0528 13:36:23.318877 73304 printmode.go:130] Print mode: conversation=b8b263a4-4b2f-4339-acc9-78b248e2b606, sending message' > "$log_file"
fi
`
	if err := os.WriteFile(agyPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	backend := &antigravityBackend{cfg: Config{
		ExecutablePath: agyPath,
		Logger:         quietAntigravityLogger(),
	}}
	session, err := backend.Execute(context.Background(), "write a file", ExecOptions{})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}

	var events []Message
	for msg := range session.Messages {
		if msg.Type != MessageStatus {
			events = append(events, msg)
		}
	}
	result, ok := <-session.Result
	if !ok {
		t.Fatal("result channel closed without a result")
	}

	wantTypes := []MessageType{MessageLog, MessageThinking, MessageLog, MessageThinking}
	if len(events) != len(wantTypes) {
		t.Fatalf("events = %+v, want %d non-status events", events, len(wantTypes))
	}
	for i, want := range wantTypes {
		if events[i].Type != want {
			t.Fatalf("event[%d].Type = %q, want %q; events=%+v", i, events[i].Type, want, events)
		}
	}
	if events[0].Level != "debug" || events[2].Level != "debug" {
		t.Fatalf("wrapper events = %+v / %+v, want debug log events", events[0], events[2])
	}
	if strings.Contains(result.Output, "Wrapper") {
		t.Fatalf("Result.Output leaked wrapper plumbing: %q", result.Output)
	}
	if want := "I will write the requested file.\nCreated hello.txt."; result.Output != want {
		t.Fatalf("Result.Output = %q, want %q", result.Output, want)
	}
	if result.SessionID != "b8b263a4-4b2f-4339-acc9-78b248e2b606" {
		t.Fatalf("SessionID = %q, want conversation id from log", result.SessionID)
	}
}

func TestReadAntigravityConversationID(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	logPath := filepath.Join(dir, "agy.log")

	// Sample log content modelled on real agy glog output: the
	// conversation= line is what printmode.go writes once per dispatch.
	logBody := strings.Join([]string{
		`I0528 13:36:19.959748 73304 printmode.go:71] Print mode: starting (promptLength=18, model="", conversationID="")`,
		`I0528 13:36:23.318877 73304 printmode.go:130] Print mode: conversation=b8b263a4-4b2f-4339-acc9-78b248e2b606, sending message`,
		`I0528 13:36:23.318892 73304 server.go:1083] Sending user message to conversation b8b263a4-4b2f-4339-acc9-78b248e2b606 (items=1, media=0)`,
	}, "\n")
	if err := os.WriteFile(logPath, []byte(logBody), 0o644); err != nil {
		t.Fatal(err)
	}

	got := readAntigravityConversationID(logPath)
	want := "b8b263a4-4b2f-4339-acc9-78b248e2b606"
	if got != want {
		t.Fatalf("readAntigravityConversationID = %q, want %q", got, want)
	}
}

func TestReadAntigravityConversationIDMissingFile(t *testing.T) {
	t.Parallel()
	if got := readAntigravityConversationID("/nonexistent/path"); got != "" {
		t.Errorf("expected empty string for missing file, got %q", got)
	}
	if got := readAntigravityConversationID(""); got != "" {
		t.Errorf("expected empty string for empty path, got %q", got)
	}
}

// TestAntigravityModelError is the regression guard for the silent-no-op fix:
// agy exits 0 with empty output on an unrecognised --model, so Execute must
// reject a non-empty model that isn't in the `agy models` catalog instead of
// letting it run to a fake "completed + empty" success. This covers the same
// validation regardless of whether opts.Model originated from agent.model, a
// persisted/API value, or the daemon-wide MULTICA_ANTIGRAVITY_MODEL default —
// they all collapse to opts.Model before Execute runs this check.
func TestAntigravityModelError(t *testing.T) {
	t.Parallel()

	catalog := []Model{
		{ID: "Gemini 3.5 Flash (Medium)", Label: "Gemini 3.5 Flash (Medium)", Provider: "antigravity"},
		{ID: "Claude Opus 4.6 (Thinking)", Label: "Claude Opus 4.6 (Thinking)", Provider: "antigravity"},
	}

	// Exact catalog hit → accepted.
	if err := antigravityModelError("Claude Opus 4.6 (Thinking)", catalog); err != nil {
		t.Errorf("valid model rejected: %v", err)
	}

	// Empty model → accepted (flag omitted, agy resolves its own default).
	if err := antigravityModelError("", catalog); err != nil {
		t.Errorf("empty model should not error: %v", err)
	}

	// Empty / nil catalog → fail open (discovery couldn't produce a list, so we
	// can't prove the value is bad — let agy decide rather than block the run).
	if err := antigravityModelError("anything at all", nil); err != nil {
		t.Errorf("empty catalog should fail open, got: %v", err)
	}

	// Unknown model with a known catalog → actionable error that names the
	// rejected value and points at `agy models`. THIS is the case that stops
	// the silent empty-success.
	err := antigravityModelError("Totally Made Up Model", catalog)
	if err == nil {
		t.Fatal("unknown model should be rejected, not silently accepted")
	}
	if !strings.Contains(err.Error(), "Totally Made Up Model") {
		t.Errorf("error should name the rejected model: %v", err)
	}
	if !strings.Contains(err.Error(), "agy models") {
		t.Errorf("error should point the user at `agy models`: %v", err)
	}

	// Near-miss (trailing space / dropped suffix) → still rejected, because agy
	// needs the exact display string and would no-op on anything else.
	if err := antigravityModelError("Claude Opus 4.6 (Thinking) ", catalog); err == nil {
		t.Error("near-miss model (trailing space) should be rejected")
	}
	if err := antigravityModelError("Claude Opus 4.6", catalog); err == nil {
		t.Error("near-miss model (dropped suffix) should be rejected")
	}
}
