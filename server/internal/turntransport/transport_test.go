package turntransport

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

func TestPrepareKeepsFixedWrapperAndRefreshesBinary(t *testing.T) {
	root := filepath.Join(t.TempDir(), "transport")
	firstBinary := filepath.Join(t.TempDir(), "multica-v1")
	secondBinary := filepath.Join(t.TempDir(), "multica-v2")

	first, err := Prepare(root, firstBinary)
	if err != nil {
		t.Fatalf("Prepare(first): %v", err)
	}
	second, err := Prepare(root, secondBinary)
	if err != nil {
		t.Fatalf("Prepare(second): %v", err)
	}
	if first.WrapperPath() != second.WrapperPath() {
		t.Fatalf("wrapper path changed across prepare: %q -> %q", first.WrapperPath(), second.WrapperPath())
	}
	if want := filepath.Join(root, "bin", "multica"); second.WrapperPath() != want {
		t.Fatalf("WrapperPath() = %q, want %q", second.WrapperPath(), want)
	}

	body, err := os.ReadFile(second.WrapperPath())
	if err != nil {
		t.Fatalf("ReadFile(wrapper): %v", err)
	}
	text := string(body)
	if strings.Contains(text, firstBinary) {
		t.Fatalf("wrapper still references previous binary:\n%s", text)
	}
	if !strings.Contains(text, shellQuote(secondBinary)) {
		t.Fatalf("wrapper does not reference refreshed binary:\n%s", text)
	}
	if !strings.Contains(text, EnvelopePathEnv+"="+shellQuote(second.CurrentEnvelopePath())) {
		t.Fatalf("wrapper does not resolve fixed current envelope:\n%s", text)
	}
	info, err := os.Stat(second.WrapperPath())
	if err != nil {
		t.Fatalf("Stat(wrapper): %v", err)
	}
	if got := info.Mode().Perm(); got != 0o700 {
		t.Fatalf("wrapper mode = %o, want 700", got)
	}
	if runtime.GOOS != "windows" {
		if output, err := exec.Command("sh", "-n", second.WrapperPath()).CombinedOutput(); err != nil {
			t.Fatalf("wrapper shell syntax: %v\n%s", err, output)
		}
	}
}

func TestStableWrapperOmitsEnvelopeMarkerWhenNoTurnIsBound(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX wrapper execution test")
	}
	root := t.TempDir()
	binary := filepath.Join(root, "real-multica")
	if err := os.WriteFile(binary, []byte("#!/bin/sh\nprintf '%s' \"$"+EnvelopePathEnv+"\"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	transport, err := Prepare(filepath.Join(root, "transport"), binary)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	output, err := exec.Command(transport.WrapperPath()).CombinedOutput()
	if err != nil {
		t.Fatalf("execute stable wrapper: %v: %s", err, output)
	}
	if got := string(output); got != "" {
		t.Fatalf("inactive envelope marker = %q, want empty", got)
	}
}

func TestBindApplyAndUnbindFailClosed(t *testing.T) {
	transport := mustPrepare(t)
	binding, err := transport.Bind("turn-a", "token-a", map[string]string{
		"MULTICA_TASK_ID":                 "turn-a",
		"MULTICA_RUN_ID":                  "run-a",
		"MULTICA_AGENT_INBOX_LEASE_TOKEN": "lease-a",
		"MULTICA_QUICK_CREATE_TASK_ID":    "turn-a",
	})
	if err != nil {
		t.Fatalf("Bind: %v", err)
	}
	assertPrivateFile(t, binding.TokenFile, 0o600)
	assertPrivateFile(t, transport.CurrentEnvelopePath(), 0o600)
	envelopeBytes, err := os.ReadFile(transport.CurrentEnvelopePath())
	if err != nil {
		t.Fatalf("ReadFile(current envelope): %v", err)
	}
	if strings.Contains(string(envelopeBytes), "token-a") {
		t.Fatal("current envelope contains raw turn token")
	}

	t.Setenv(EnvelopePathEnv, transport.CurrentEnvelopePath())
	t.Setenv("MULTICA_TOKEN", "stale-raw-token")
	t.Setenv("MULTICA_TOKEN_FILE", filepath.Join(t.TempDir(), "stale-token"))
	t.Setenv("MULTICA_TASK_ID", "stale-task")
	t.Setenv("MULTICA_AGENT_INBOX_DELIVERY_ID", "stale-delivery")
	if err := ApplyFromEnvironment(); err != nil {
		t.Fatalf("ApplyFromEnvironment: %v", err)
	}
	if got := os.Getenv("MULTICA_TASK_ID"); got != "turn-a" {
		t.Fatalf("MULTICA_TASK_ID = %q, want turn-a", got)
	}
	if got := os.Getenv("MULTICA_AGENT_INBOX_DELIVERY_ID"); got != "" {
		t.Fatalf("stale delivery survived apply: %q", got)
	}
	if got := os.Getenv("MULTICA_TOKEN"); got != "" {
		t.Fatalf("raw token survived apply: %q", got)
	}
	if got := os.Getenv("MULTICA_TOKEN_FILE"); got != binding.TokenFile {
		t.Fatalf("MULTICA_TOKEN_FILE = %q, want %q", got, binding.TokenFile)
	}
	if got := os.Getenv(EnvelopePathEnv); got != "" {
		t.Fatalf("%s survived apply: %q", EnvelopePathEnv, got)
	}

	removed, err := Unbind(binding)
	if err != nil {
		t.Fatalf("Unbind: %v", err)
	}
	if !removed {
		t.Fatal("Unbind removed = false, want true for current generation")
	}
	t.Setenv(EnvelopePathEnv, transport.CurrentEnvelopePath())
	if err := ApplyFromEnvironment(); err == nil {
		t.Fatal("ApplyFromEnvironment after unbind succeeded, want fail-closed error")
	}
	if _, err := os.Stat(binding.TokenFile); !os.IsNotExist(err) {
		t.Fatalf("token file after unbind error = %v, want not exist", err)
	}
}

func TestLateUnbindCannotRemoveNewTurnOrReuseOldToken(t *testing.T) {
	transport := mustPrepare(t)
	turnA, err := transport.Bind("turn-a", "token-a", map[string]string{
		"MULTICA_TASK_ID": "turn-a",
	})
	if err != nil {
		t.Fatalf("Bind(turn-a): %v", err)
	}
	turnB, err := transport.Bind("turn-b", "token-b", map[string]string{
		"MULTICA_TASK_ID":                 "turn-b",
		"MULTICA_AGENT_INBOX_LEASE_TOKEN": "lease-b",
	})
	if err != nil {
		t.Fatalf("Bind(turn-b): %v", err)
	}
	if _, err := os.Stat(turnA.TokenFile); !os.IsNotExist(err) {
		t.Fatalf("turn-a token after bind B error = %v, want not exist", err)
	}

	removed, err := Unbind(turnA)
	if err != nil {
		t.Fatalf("late Unbind(turn-a): %v", err)
	}
	if removed {
		t.Fatal("late Unbind(turn-a) removed current turn B")
	}
	if _, err := os.Stat(transport.CurrentEnvelopePath()); err != nil {
		t.Fatalf("current envelope after late unbind: %v", err)
	}
	if _, err := os.Stat(turnB.TokenFile); err != nil {
		t.Fatalf("turn-b token after late unbind: %v", err)
	}

	t.Setenv(EnvelopePathEnv, transport.CurrentEnvelopePath())
	t.Setenv("MULTICA_TASK_ID", "turn-a")
	t.Setenv("MULTICA_AGENT_INBOX_LEASE_TOKEN", "lease-a")
	t.Setenv("MULTICA_TOKEN_FILE", turnA.TokenFile)
	if err := ApplyFromEnvironment(); err != nil {
		t.Fatalf("ApplyFromEnvironment(turn-b): %v", err)
	}
	if got := os.Getenv("MULTICA_TASK_ID"); got != "turn-b" {
		t.Fatalf("MULTICA_TASK_ID = %q, want turn-b", got)
	}
	if got := os.Getenv("MULTICA_AGENT_INBOX_LEASE_TOKEN"); got != "lease-b" {
		t.Fatalf("lease token = %q, want lease-b", got)
	}
	if got := os.Getenv("MULTICA_TOKEN_FILE"); got != turnB.TokenFile {
		t.Fatalf("token file = %q, want turn-b %q", got, turnB.TokenFile)
	}
}

func TestConcurrentRebindAndLateUnbindPreserveNewTurn(t *testing.T) {
	for attempt := 0; attempt < 50; attempt++ {
		transport := mustPrepare(t)
		turnA, err := transport.Bind("turn-a", "token-a", map[string]string{
			"MULTICA_TASK_ID": "turn-a",
		})
		if err != nil {
			t.Fatalf("attempt %d Bind(turn-a): %v", attempt, err)
		}

		start := make(chan struct{})
		var wg sync.WaitGroup
		var turnB Binding
		var bindErr, unbindErr error
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			turnB, bindErr = transport.Bind("turn-b", "token-b", map[string]string{
				"MULTICA_TASK_ID": "turn-b",
			})
		}()
		go func() {
			defer wg.Done()
			<-start
			_, unbindErr = Unbind(turnA)
		}()
		close(start)
		wg.Wait()
		if bindErr != nil {
			t.Fatalf("attempt %d Bind(turn-b): %v", attempt, bindErr)
		}
		if unbindErr != nil {
			t.Fatalf("attempt %d late Unbind(turn-a): %v", attempt, unbindErr)
		}

		current, err := readEnvelope(transport.CurrentEnvelopePath())
		if err != nil {
			t.Fatalf("attempt %d read current: %v", attempt, err)
		}
		if current.Generation != turnB.Generation || current.TurnID != "turn-b" {
			t.Fatalf("attempt %d current = %+v, want turn-b generation %s", attempt, current, turnB.Generation)
		}
		token, err := os.ReadFile(turnB.TokenFile)
		if err != nil {
			t.Fatalf("attempt %d read turn-b token: %v", attempt, err)
		}
		if string(token) != "token-b" {
			t.Fatalf("attempt %d turn-b token = %q", attempt, token)
		}
	}
}

func TestRecoverRemovesTurnAuthorityButPreservesWrapper(t *testing.T) {
	transport := mustPrepare(t)
	binding, err := transport.Bind("turn-a", "token-a", map[string]string{
		"MULTICA_TASK_ID": "turn-a",
	})
	if err != nil {
		t.Fatalf("Bind: %v", err)
	}
	if err := Recover(transport.Root()); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	if _, err := os.Stat(transport.CurrentEnvelopePath()); !os.IsNotExist(err) {
		t.Fatalf("current envelope after recover error = %v, want not exist", err)
	}
	if _, err := os.Stat(binding.TokenFile); !os.IsNotExist(err) {
		t.Fatalf("token after recover error = %v, want not exist", err)
	}
	if _, err := os.Stat(transport.WrapperPath()); err != nil {
		t.Fatalf("wrapper after recover: %v", err)
	}
}

func TestSplitEnvironmentSeparatesReusableProcessFromTurn(t *testing.T) {
	stable, turn, err := SplitEnvironment(map[string]string{
		"MULTICA_SERVER_URL":                  "https://example.test",
		"MULTICA_WORKSPACE_ID":                "workspace-1",
		"MULTICA_AGENT_ID":                    "agent-1",
		"MULTICA_TASK_ID":                     "task-1",
		"MULTICA_AGENT_INBOX_DELIVERY_ID":     "delivery-1",
		"MULTICA_QUICK_CREATE_ATTACHMENT_IDS": `["attachment-1"]`,
		AttemptPathEnv:                        "/tmp/runtime/transport-attempt",
		"CODEX_HOME":                          "/stable/codex-home",
	})
	if err != nil {
		t.Fatalf("SplitEnvironment: %v", err)
	}
	if _, ok := stable["MULTICA_TASK_ID"]; ok {
		t.Fatal("stable process env contains task id")
	}
	if _, ok := stable["MULTICA_AGENT_INBOX_DELIVERY_ID"]; ok {
		t.Fatal("stable process env contains delivery id")
	}
	if got := stable["MULTICA_AGENT_ID"]; got != "agent-1" {
		t.Fatalf("stable agent id = %q, want agent-1", got)
	}
	if got := turn["MULTICA_TASK_ID"]; got != "task-1" {
		t.Fatalf("turn task id = %q, want task-1", got)
	}
	if got := turn["MULTICA_QUICK_CREATE_ATTACHMENT_IDS"]; got != `["attachment-1"]` {
		t.Fatalf("turn attachments = %q", got)
	}
	if got := turn[AttemptPathEnv]; got != "/tmp/runtime/transport-attempt" {
		t.Fatalf("turn transport attempt path = %q", got)
	}
	if _, ok := stable[AttemptPathEnv]; ok {
		t.Fatal("stable process env contains transport attempt path")
	}

	for _, key := range []string{"MULTICA_TOKEN", "MULTICA_TOKEN_FILE", EnvelopePathEnv} {
		if _, _, err := SplitEnvironment(map[string]string{key: "secret"}); err == nil {
			t.Fatalf("SplitEnvironment accepted %s in provider process env", key)
		}
	}
	if _, _, err := SplitEnvironment(map[string]string{
		"MULTICA_FUTURE_TURN_FIELD": "would silently leak without classification",
	}); err == nil {
		t.Fatal("SplitEnvironment accepted an unclassified Multica key")
	}
}

func TestBindRejectsEnvironmentOutsideExplicitAllowlist(t *testing.T) {
	transport := mustPrepare(t)
	if _, err := transport.Bind("turn-a", "token-a", map[string]string{
		"MULTICA_AGENT_ID": "agent-from-turn",
	}); err == nil {
		t.Fatal("Bind accepted stable agent identity in current-turn envelope")
	}
	if _, err := os.Stat(transport.CurrentEnvelopePath()); !os.IsNotExist(err) {
		t.Fatalf("current envelope after rejected bind error = %v, want not exist", err)
	}
}

func TestRecordAttemptFromEnvironmentWritesPrivateMarker(t *testing.T) {
	path := AttemptPath(t.TempDir())
	t.Setenv(AttemptPathEnv, path)

	if err := RecordAttemptFromEnvironment(); err != nil {
		t.Fatalf("RecordAttemptFromEnvironment: %v", err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(attempt marker): %v", err)
	}
	if got := string(body); got != "attempted\n" {
		t.Fatalf("attempt marker = %q, want attempted newline", got)
	}
	assertPrivateFile(t, path, 0o600)
}

func TestRecordAttemptFromEnvironmentRejectsUnexpectedPath(t *testing.T) {
	t.Setenv(AttemptPathEnv, filepath.Join(t.TempDir(), "other-name"))
	if err := RecordAttemptFromEnvironment(); err == nil {
		t.Fatal("RecordAttemptFromEnvironment accepted unexpected marker basename")
	}
}

func TestApplyRejectsEnvelopeWhoseTaskDoesNotMatchTurn(t *testing.T) {
	transport := mustPrepare(t)
	binding, err := transport.Bind("turn-a", "token-a", map[string]string{
		"MULTICA_TASK_ID": "turn-a",
	})
	if err != nil {
		t.Fatalf("Bind: %v", err)
	}
	current, err := readEnvelope(transport.CurrentEnvelopePath())
	if err != nil {
		t.Fatalf("readEnvelope: %v", err)
	}
	current.TurnID = "turn-b"
	raw, err := json.Marshal(current)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if err := writeFileAtomic(transport.CurrentEnvelopePath(), raw, 0o600); err != nil {
		t.Fatalf("write tampered envelope: %v", err)
	}

	t.Setenv(EnvelopePathEnv, transport.CurrentEnvelopePath())
	t.Setenv("MULTICA_TASK_ID", "stale-task")
	if err := ApplyFromEnvironment(); err == nil {
		t.Fatal("ApplyFromEnvironment accepted mismatched turn/task identity")
	}
	if got := os.Getenv("MULTICA_TASK_ID"); got != "stale-task" {
		t.Fatalf("failed apply mutated task id to %q", got)
	}
	if _, err := os.Stat(binding.TokenFile); err != nil {
		t.Fatalf("token unexpectedly removed: %v", err)
	}
}

func mustPrepare(t *testing.T) *Transport {
	t.Helper()
	root := filepath.Join(t.TempDir(), "agent-root", "runtime", "cli-transport")
	binary := filepath.Join(t.TempDir(), "multica")
	transport, err := Prepare(root, binary)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	return transport
}

func assertPrivateFile(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat(%s): %v", path, err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Fatalf("%s mode = %o, want %o", path, got, want)
	}
}

// TestWindowsWrapperBodyIsCmdNotShell verifies the stable-transport wrapper on
// Windows is a cmd.exe batch, not a bare extensionless #!/bin/sh shim.
func TestWindowsWrapperBodyIsCmdNotShell(t *testing.T) {
	keys := []string{"MULTICA_TOKEN", "MULTICA_TOKEN_FILE", "MULTICA_TASK_ID"}
	body := windowsWrapperBody(EnvelopePathEnv, `C:\transport\current-turn.json`, keys, `C:\multica\multica.exe`)

	if strings.HasPrefix(body, "#!") {
		t.Fatalf("windows wrapper starts with shebang: %q", body)
	}
	for _, want := range []string{
		"@echo off",
		`set "MULTICA_TOKEN="`,
		"set \"" + EnvelopePathEnv + `=C:\transport\current-turn.json"`,
		`call "C:\multica\multica.exe" %*`,
		"exit /b %ERRORLEVEL%",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("windows wrapper missing %q: %q", want, body)
		}
	}
}
