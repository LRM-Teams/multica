package main

import (
	"testing"

	"github.com/multica-ai/multica/server/internal/cli"
)

func TestLegacyCompatibilityCommandsRemainAvailable(t *testing.T) {
	t.Run("workspace get remains available", func(t *testing.T) {
		if _, _, err := workspaceCmd.Find([]string{"get"}); err != nil {
			t.Fatalf("expected workspace get command to exist: %v", err)
		}
	})

	t.Run("workspace member list remains available", func(t *testing.T) {
		if _, _, err := workspaceCmd.Find([]string{"member", "list"}); err != nil {
			t.Fatalf("expected workspace member list command to exist: %v", err)
		}
	})

	t.Run("config show and set remain available", func(t *testing.T) {
		if _, _, err := configCmd.Find([]string{"show"}); err != nil {
			t.Fatalf("expected config show command to exist: %v", err)
		}
		if _, _, err := configCmd.Find([]string{"set"}); err != nil {
			t.Fatalf("expected config set command to exist: %v", err)
		}
	})

	t.Run("top-level message compatibility aliases are removed", func(t *testing.T) {
		for _, name := range []string{"send", "react"} {
			cmd, _, err := rootCmd.Find([]string{name})
			if err == nil && cmd != nil && cmd.Name() == name {
				t.Fatalf("top-level %s alias should not exist", name)
			}
		}
	})
}

func TestMessageCommandSurface(t *testing.T) {
	t.Run("only canonical message commands exist", func(t *testing.T) {
		want := map[string]bool{"send": true, "check": true, "read": true, "search": true, "resolve": true, "react": true}
		for _, command := range messageCmd.Commands() {
			if !want[command.Name()] {
				t.Fatalf("message command %q must not be public", command.Name())
			}
			delete(want, command.Name())
		}
		for name := range want {
			t.Fatalf("missing message %s command", name)
		}
	})

	t.Run("message send exposes Draft replay flags without an Agent idempotency key", func(t *testing.T) {
		for _, name := range []string{"attachment-id", "target", "send-draft", "anyway"} {
			if messageSendCmd.Flags().Lookup(name) == nil {
				t.Fatalf("message send missing --%s", name)
			}
		}
		for _, name := range []string{"message", "message-stdin", "message-file", "seen", "sticker", "voice", "client-message-id", "idempotency-key", "output"} {
			if messageSendCmd.Flags().Lookup(name) != nil {
				t.Fatalf("message send must not expose --%s", name)
			}
		}
	})

	t.Run("removed commands cannot resolve", func(t *testing.T) {
		for _, name := range []string{"ask-choice", "a2a-control"} {
			cmd, _, err := messageCmd.Find([]string{name})
			if err == nil && cmd != nil && cmd.Name() == name {
				t.Fatalf("message %s must not resolve", name)
			}
		}
	})
}

func TestRunConfigSetPersistsValues(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cmd := testCmd()

	if err := runConfigSet(cmd, []string{"server_url", "http://example.com"}); err != nil {
		t.Fatalf("runConfigSet(server_url) error = %v", err)
	}
	if err := runConfigSet(cmd, []string{"workspace_id", "ws-123"}); err != nil {
		t.Fatalf("runConfigSet(workspace_id) error = %v", err)
	}
	if err := runConfigSet(cmd, []string{"proxy.http", "http://user:secret@proxy.internal:8080"}); err != nil {
		t.Fatalf("runConfigSet(proxy.http) error = %v", err)
	}
	if err := runConfigSet(cmd, []string{"proxy.no_proxy", ".corp.example"}); err != nil {
		t.Fatalf("runConfigSet(proxy.no_proxy) error = %v", err)
	}

	cfg, err := cli.LoadCLIConfig()
	if err != nil {
		t.Fatalf("LoadCLIConfig() error = %v", err)
	}
	if cfg.ServerURL != "http://example.com" {
		t.Fatalf("ServerURL = %q, want %q", cfg.ServerURL, "http://example.com")
	}
	if cfg.WorkspaceID != "ws-123" {
		t.Fatalf("WorkspaceID = %q, want %q", cfg.WorkspaceID, "ws-123")
	}
	if cfg.Proxy == nil {
		t.Fatal("Proxy config is nil")
	}
	if cfg.Proxy.HTTP != "http://user:secret@proxy.internal:8080" {
		t.Fatalf("Proxy.HTTP = %q", cfg.Proxy.HTTP)
	}
	if cfg.Proxy.NoProxy != ".corp.example" {
		t.Fatalf("Proxy.NoProxy = %q", cfg.Proxy.NoProxy)
	}
	if got := secretPresence(cfg.Proxy.HTTP); got != "(set)" {
		t.Fatalf("secretPresence(proxy.http) = %q, want (set)", got)
	}
	if got := secretPresence(""); got != "(not set)" {
		t.Fatalf("secretPresence(empty) = %q, want (not set)", got)
	}
}
