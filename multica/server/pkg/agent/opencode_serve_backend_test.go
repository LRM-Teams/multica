package agent

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

const openCodeServeHelperEnv = "MULTICA_OPENCODE_SERVE_HELPER"
const openCodeServeHelperPortFileEnv = "MULTICA_OPENCODE_SERVE_HELPER_PORT_FILE"

// openCodeServeHelperEventDelayEnv / openCodeServeHelperHandshakeMarkerFileEnv
// let TestEnsureServerBlocksUntilSSEHandshakeCompletes control and observe
// the fake process's /event handshake timing from the outer test process —
// see fakeOpenCodeServeServer.eventDelay/handshakeMarkerFile.
const openCodeServeHelperEventDelayEnv = "MULTICA_OPENCODE_SERVE_HELPER_EVENT_DELAY_MS"
const openCodeServeHelperHandshakeMarkerFileEnv = "MULTICA_OPENCODE_SERVE_HELPER_HANDSHAKE_MARKER_FILE"
const openCodeServeHelperEventStatusEnv = "MULTICA_OPENCODE_SERVE_HELPER_EVENT_STATUS"

// fakeOpenCodeServeBinaryScript is a POSIX-sh shim impersonating `opencode
// serve --port N --hostname H`. It cannot itself speak HTTP, so it extracts
// --port from its argv, writes it where the Go helper test can find it, and
// re-execs the test binary selecting TestOpenCodeServeHelperProcess — which
// then runs the real fakeOpenCodeServeServer over net/http. This mirrors
// fakeOpencodeScript()'s existing argv/env-capture pattern in opencode_test.go,
// extended with a re-exec step because a persistent HTTP server can't be
// implemented in portable POSIX sh alone.
func fakeOpenCodeServeBinaryScript(testBinary string) string {
	return fmt.Sprintf(`#!/bin/sh
port=""
while [ $# -gt 0 ]; do
  case "$1" in
    --port) port="$2"; shift 2 ;;
    *) shift ;;
  esac
done
printf '%%s' "$port" > "$MULTICA_OPENCODE_SERVE_HELPER_PORT_FILE"
exec %s -test.run='^TestOpenCodeServeHelperProcess$' -test.v=false
`, testBinary)
}

// TestOpenCodeServeHelperProcess is not a real test — gated behind
// openCodeServeHelperEnv, it is re-exec'd by fakeOpenCodeServeBinaryScript to
// impersonate a live `opencode serve` process for TestEnsureServerReusesProcess
// and TestCloseTerminatesServeProcess. It blocks serving HTTP until killed.
func TestOpenCodeServeHelperProcess(t *testing.T) {
	if os.Getenv(openCodeServeHelperEnv) != "1" {
		t.Skip("not running as opencode serve helper")
	}
	portFile := os.Getenv(openCodeServeHelperPortFileEnv)
	portBytes, err := os.ReadFile(portFile)
	if err != nil {
		t.Fatalf("read port file: %v", err)
	}
	fake := newFakeOpenCodeServeServer()
	fake.scriptEvents = func(sessionID string) []string { return []string{sessionIdleEvent(sessionID)} }
	fake.scriptMessages = func(sessionID string) []opencodeServeMessage {
		return []opencodeServeMessage{{ID: "msg_1", Parts: []opencodeServeMessagePart{{Type: "text"}}}}
	}
	if ms := os.Getenv(openCodeServeHelperEventDelayEnv); ms != "" {
		if n, err := strconv.Atoi(ms); err == nil {
			fake.eventDelay = time.Duration(n) * time.Millisecond
		}
	}
	fake.handshakeMarkerFile = os.Getenv(openCodeServeHelperHandshakeMarkerFileEnv)
	if status := os.Getenv(openCodeServeHelperEventStatusEnv); status != "" {
		if n, err := strconv.Atoi(status); err == nil {
			fake.eventStatus = n
		}
	}
	srv := &http.Server{Addr: "127.0.0.1:" + string(portBytes), Handler: fake.handler()}
	_ = srv.ListenAndServe()
}

func newTestOpenCodeServeBackendConfig(t *testing.T) Config {
	t.Helper()
	tempDir := t.TempDir()
	portFile := filepath.Join(tempDir, "port")
	if err := os.WriteFile(portFile, nil, 0o644); err != nil {
		t.Fatalf("create port file: %v", err)
	}
	testBinary, err := os.Executable()
	if err != nil {
		t.Fatalf("resolve test binary: %v", err)
	}
	fakePath := filepath.Join(tempDir, "opencode")
	writeTestExecutable(t, fakePath, []byte(fakeOpenCodeServeBinaryScript(testBinary)))
	return Config{
		ExecutablePath: fakePath,
		Env: map[string]string{
			openCodeServeHelperEnv:         "1",
			openCodeServeHelperPortFileEnv: portFile,
		},
	}
}

// TestEnsureServerReusesProcess is the actual residency proof: two
// sequential turns against one backend instance must not spawn a second
// `opencode serve` process.
func TestEnsureServerReusesProcess(t *testing.T) {
	backend := newOpenCodeServeBackend(newTestOpenCodeServeBackendConfig(t))
	t.Cleanup(backend.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	first, err := backend.ensureServer(ctx, ExecOptions{})
	if err != nil {
		t.Fatalf("ensureServer (1st): %v", err)
	}
	second, err := backend.ensureServer(ctx, ExecOptions{})
	if err != nil {
		t.Fatalf("ensureServer (2nd): %v", err)
	}
	if first != second {
		t.Fatalf("ensureServer returned a different process on the 2nd call — not resident")
	}
	if first.cmd.Process.Pid == 0 {
		t.Fatalf("resident process has no pid")
	}
}

// TestCloseTerminatesServeProcess proves Close() actually kills the spawned
// process rather than leaking it.
func TestCloseTerminatesServeProcess(t *testing.T) {
	backend := newOpenCodeServeBackend(newTestOpenCodeServeBackendConfig(t))

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	p, err := backend.ensureServer(ctx, ExecOptions{})
	if err != nil {
		t.Fatalf("ensureServer: %v", err)
	}
	proc := p.cmd.Process

	backend.Close()

	if _, err := proc.Wait(); err != nil {
		// Wait returning an error here (e.g. "wait: no child processes") is
		// acceptable if the process already exited/was reaped; what matters
		// is that it is no longer alive.
		t.Logf("proc.Wait after Close: %v (expected once killed)", err)
	}
	alive, known := processAlive(proc)
	if known && alive {
		t.Fatalf("process still alive after Close()")
	}
}

// TestExecuteEndToEndAgainstFakeServeProcess exercises the full path —
// spawn, create session, send message, session.idle, reconcile — against a
// real (fake) HTTP server running in a separate process, not just the
// in-process httptest.Server used by the client-level tests.
//
// Task #44 history: this used a fixed ExecOptions.Timeout (10s, then 30s) as
// the pass/fail bound, and flaked twice under heavy CI load — once at each
// value — because turnWatchdogTimeout races that same duration against the
// real session.idle SSE event. Any fixed bound in the "normal completion"
// range is a coin flip against scheduler preemption; raising it only lowers
// the odds without removing them (30.13s was the second flake, confirmed by
// timestamp to be *after* the first fix, not a stale CI run).
//
// The actual completion signal is already deterministic (session.idle), so
// this test passes ExecOptions{} and lets turnWatchdogTimeout fall back to
// its production default (10 minutes) — a pure deadlock backstop, not an
// estimate of expected duration. On a loaded runner this test should get
// slower, never red; the 200ms-timeout case in
// opencode_serve_client_test.go covers the "watchdog actually fires"
// behavior with a deliberately-short deadline instead.
func TestExecuteEndToEndAgainstFakeServeProcess(t *testing.T) {
	backend := newOpenCodeServeBackend(newTestOpenCodeServeBackendConfig(t))
	t.Cleanup(backend.Close)

	session, err := backend.Execute(context.Background(), "hello", ExecOptions{})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	for range session.Messages {
	}
	result := <-session.Result
	if result.Status != "completed" {
		t.Fatalf("result.Status = %q, want completed (result=%+v)", result.Status, result)
	}
}

// TestEnsureServerBlocksUntilSSEHandshakeCompletes proves ensureServer
// itself waits for the SSE subscription to be live before returning a
// usable process — not just that runEventLoop eventually closes
// connectedCh, which TestRunEventLoopConnectedCh* already covers in
// opencode_serve_client_test.go.
//
// That distinction matters because those two tests exercise runEventLoop in
// isolation and never touch ensureServer's return timing at all. Alice
// proved this by mutation: deleting the `select { case
// <-client.connectedCh: ... }` block from ensureServer (goroutine and
// signature unchanged) left both of those tests fully green — the exact gap
// Parker/Alice/Vera/Nash independently converged on for this PR.
//
// Deliberately an ordering assertion, not a duration one: CI load already
// makes fixed-duration thresholds flake elsewhere in this package (see task
// #44's history above). The fake process's /event handler withholds its
// handshake for a fixed delay, then writes a marker file at the exact
// instant it completes. If ensureServer returns before that marker exists,
// it returned before a listener was actually registered — the #49 race,
// reintroduced through ensureServer instead of through runEventLoop.
func TestEnsureServerBlocksUntilSSEHandshakeCompletes(t *testing.T) {
	cfg := newTestOpenCodeServeBackendConfig(t)
	markerFile := filepath.Join(t.TempDir(), "handshake-marker")
	cfg.Env[openCodeServeHelperEventDelayEnv] = "300"
	cfg.Env[openCodeServeHelperHandshakeMarkerFileEnv] = markerFile

	backend := newOpenCodeServeBackend(cfg)
	t.Cleanup(backend.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := backend.ensureServer(ctx, ExecOptions{}); err != nil {
		t.Fatalf("ensureServer: %v", err)
	}

	if _, err := os.Stat(markerFile); err != nil {
		t.Fatalf("ensureServer returned before the SSE handshake marker was written — it did not actually wait for the subscription to be live: %v", err)
	}
}

// TestEnsureServerRejectsNon2xxEventResponse proves the second door onto
// the same race (Parker's finding): opencode can answer /event with a
// non-2xx status — plausible during its own startup window — before a
// subscription is genuinely live. http.Do returns no transport error for
// that response, so ensureServer must not treat "got a response" alone as
// "connected"; it must surface a connection failure instead of proceeding
// as if a listener were registered.
func TestEnsureServerRejectsNon2xxEventResponse(t *testing.T) {
	cfg := newTestOpenCodeServeBackendConfig(t)
	cfg.Env[openCodeServeHelperEventStatusEnv] = "503"

	backend := newOpenCodeServeBackend(cfg)
	t.Cleanup(backend.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := backend.ensureServer(ctx, ExecOptions{}); err == nil {
		t.Fatal("ensureServer succeeded against a /event endpoint returning 503, want a connection error")
	}
}
