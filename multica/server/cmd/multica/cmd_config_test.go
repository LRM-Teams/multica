package main

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// TestConfigSetWarnsOnFirstServerURL pins task #32's follow-up: hand-rolling
// first-time setup via `config set server_url` + `login --token` (instead of
// `multica setup`) was the exact pattern that pointed s144 at a stale/wrong
// server_url on 2026-08-01, and it skips setup's device-code sign-in (which
// already works without a browser on this machine — no manual token needed
// even headless). This is a warning, not a block: self-host operators
// legitimately repoint an already-configured install via this same command.
func TestConfigSetWarnsOnFirstServerURL(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cmd := &cobra.Command{}

	stderr := captureStderr(t)
	if err := runConfigSet(cmd, []string{"server_url", "https://example.com"}); err != nil {
		t.Fatalf("runConfigSet: %v", err)
	}
	out := stderr.read()

	if !strings.Contains(out, "multica setup") {
		t.Fatalf("first-time server_url set produced no `multica setup` guidance, got: %q", out)
	}
}

// TestConfigSetNoWarningOnRepoint pins the other half: once server_url is
// already set, `config set server_url` is a legitimate reconfiguration path
// (switching self-host environments, troubleshooting) and must stay silent
// about `multica setup` — that command resets the whole config and starts a
// fresh sign-in, which is not what a repoint is trying to do.
func TestConfigSetNoWarningOnRepoint(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cmd := &cobra.Command{}

	// First set establishes a non-empty server_url baseline.
	if err := runConfigSet(cmd, []string{"server_url", "https://old.example.com"}); err != nil {
		t.Fatalf("runConfigSet (baseline): %v", err)
	}

	stderr := captureStderr(t)
	if err := runConfigSet(cmd, []string{"server_url", "https://new.example.com"}); err != nil {
		t.Fatalf("runConfigSet (repoint): %v", err)
	}
	out := stderr.read()

	if strings.Contains(out, "multica setup") {
		t.Fatalf("repointing an already-configured server_url should not suggest `multica setup`, got: %q", out)
	}
}

// TestConfigSetNoWarningForOtherKeys pins the scope: the warning is specific
// to server_url (the exact step `multica setup` replaces), not a blanket
// nag on every `config set` call.
func TestConfigSetNoWarningForOtherKeys(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cmd := &cobra.Command{}

	stderr := captureStderr(t)
	if err := runConfigSet(cmd, []string{"workspace_id", "ws-123"}); err != nil {
		t.Fatalf("runConfigSet: %v", err)
	}
	out := stderr.read()

	if strings.Contains(out, "multica setup") {
		t.Fatalf("setting workspace_id should not trigger the setup-bypass warning, got: %q", out)
	}
}
