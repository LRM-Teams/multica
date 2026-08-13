package main

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/multica-ai/multica/server/internal/daemon"
)

func TestForwardAgentProxyCLIUsesLaunchPinnedWrapper(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX wrapper execution test")
	}
	wrapper := filepath.Join(t.TempDir(), "multica")
	if err := os.WriteFile(wrapper, []byte("#!/bin/sh\nprintf 'forwarded=%s\\nwrapper_env=%s\\n' \"$*\" \"$MULTICA_AGENT_CLI_WRAPPER\"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv(daemon.AgentProxyCLIWrapperEnv, wrapper)

	var stdout, stderr bytes.Buffer
	handled, exitCode, err := forwardAgentProxyCLI(
		[]string{"message", "check"},
		strings.NewReader(""),
		&stdout,
		&stderr,
	)
	if err != nil {
		t.Fatalf("forward Agent Proxy CLI: %v", err)
	}
	if !handled || exitCode != 0 {
		t.Fatalf("forward result handled=%v exitCode=%d", handled, exitCode)
	}
	if got := stdout.String(); got != "forwarded=message check\nwrapper_env=\n" {
		t.Fatalf("forwarded output = %q", got)
	}
}

func TestForwardAgentProxyCLIRejectsRelativeWrapperPath(t *testing.T) {
	t.Setenv(daemon.AgentProxyCLIWrapperEnv, "relative/multica")

	handled, _, err := forwardAgentProxyCLI(nil, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{})
	if !handled || err == nil || !strings.Contains(err.Error(), "absolute") {
		t.Fatalf("relative wrapper result handled=%v err=%v", handled, err)
	}
}
