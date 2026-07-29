package main

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

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

	t.Run("top-level send alias is removed while react alias remains", func(t *testing.T) {
		if cmd, _, err := rootCmd.Find([]string{"send"}); err == nil && cmd != nil && cmd.Name() == "send" {
			t.Fatalf("top-level send alias should not exist")
		}
		if _, _, err := rootCmd.Find([]string{"react"}); err != nil {
			t.Fatalf("expected top-level react command to exist: %v", err)
		}
	})
}

func TestMessageGroupedSendReactCommands(t *testing.T) {
	t.Run("message send and message react exist", func(t *testing.T) {
		if _, _, err := messageCmd.Find([]string{"send"}); err != nil {
			t.Fatalf("expected message send command to exist: %v", err)
		}
		if _, _, err := messageCmd.Find([]string{"react"}); err != nil {
			t.Fatalf("expected message react command to exist: %v", err)
		}
	})

	t.Run("message send exposes sticker and attachment flags", func(t *testing.T) {
		for _, name := range []string{"message", "message-stdin", "message-file", "sticker", "voice", "attachment-id", "target", "client-message-id"} {
			if messageSendCmd.Flags().Lookup(name) == nil {
				t.Fatalf("message send missing --%s", name)
			}
		}
	})

	t.Run("top-level react alias exposes the same flags as the grouped form", func(t *testing.T) {
		pairs := []struct {
			name      string
			canonical *cobra.Command
			alias     *cobra.Command
		}{
			{"react", messageReactCmd, reactCmd},
		}
		for _, p := range pairs {
			canonicalFlags := map[string]bool{}
			p.canonical.Flags().VisitAll(func(f *pflag.Flag) { canonicalFlags[f.Name] = true })
			aliasFlags := map[string]bool{}
			p.alias.Flags().VisitAll(func(f *pflag.Flag) { aliasFlags[f.Name] = true })
			for name := range canonicalFlags {
				if !aliasFlags[name] {
					t.Errorf("%s: alias missing flag --%s", p.name, name)
				}
			}
			for name := range aliasFlags {
				if !canonicalFlags[name] {
					t.Errorf("%s: alias has extra flag --%s", p.name, name)
				}
			}
			if p.canonical.RunE == nil || p.alias.RunE == nil {
				t.Fatalf("%s: expected RunE on both grouped and alias forms", p.name)
			}
		}
	})

	t.Run("react alias help points at the grouped form", func(t *testing.T) {
		if !strings.Contains(reactCmd.Short, "multica message react") {
			t.Errorf("react alias Short should reference `multica message react`, got %q", reactCmd.Short)
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
