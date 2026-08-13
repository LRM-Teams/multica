package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/multica-ai/multica/server/internal/cli"
)

// newWorkspaceSwitchTestCmd builds a standalone cobra command with the flags
// runWorkspaceSwitch reads. We can't reuse the real workspaceSwitchCmd because
// it has no parent root carrying --workspace-id / --profile / --server-url.
func newWorkspaceSwitchTestCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "switch"}
	cmd.Flags().String("workspace-id", "", "")
	cmd.Flags().String("profile", "", "")
	cmd.Flags().String("server-url", "", "")
	return cmd
}

func TestRunWorkspaceSwitch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/workspaces" {
			http.NotFound(w, r)
			return
		}
		json.NewEncoder(w).Encode([]map[string]any{
			{"id": "11111111-1111-1111-1111-111111111111", "name": "Alpha", "slug": "alpha"},
			{"id": "22222222-2222-2222-2222-222222222222", "name": "Beta", "slug": "beta"},
		})
	}))
	defer srv.Close()

	// Isolate HOME so the test never touches the developer's ~/.multica.
	t.Setenv("HOME", t.TempDir())
	t.Setenv("MULTICA_SERVER_URL", srv.URL)
	t.Setenv("MULTICA_TOKEN", "test-token")
	t.Setenv("MULTICA_WORKSPACE_ID", "")

	t.Run("switches by slug and persists workspace_id", func(t *testing.T) {
		cmd := newWorkspaceSwitchTestCmd()
		if err := runWorkspaceSwitch(cmd, []string{"beta"}); err != nil {
			t.Fatalf("runWorkspaceSwitch: %v", err)
		}
		cfg, err := cli.LoadCLIConfig()
		if err != nil {
			t.Fatalf("LoadCLIConfig: %v", err)
		}
		if cfg.WorkspaceID != "22222222-2222-2222-2222-222222222222" {
			t.Errorf("workspace_id = %q, want Beta's id", cfg.WorkspaceID)
		}
	})

	t.Run("rejects unknown workspace and leaves config untouched", func(t *testing.T) {
		// Seed a known workspace_id so we can verify it is NOT clobbered on
		// failure — the issue's acceptance criteria explicitly call this out.
		if err := cli.SaveCLIConfig(cli.CLIConfig{WorkspaceID: "11111111-1111-1111-1111-111111111111"}); err != nil {
			t.Fatalf("seed config: %v", err)
		}

		cmd := newWorkspaceSwitchTestCmd()
		err := runWorkspaceSwitch(cmd, []string{"does-not-exist"})
		if err == nil {
			t.Fatal("expected error for unknown workspace")
		}

		cfg, _ := cli.LoadCLIConfig()
		if cfg.WorkspaceID != "11111111-1111-1111-1111-111111111111" {
			t.Errorf("workspace_id = %q, expected it to stay on Alpha's id when switch fails", cfg.WorkspaceID)
		}
	})

	t.Run("isolates by profile", func(t *testing.T) {
		cmd := newWorkspaceSwitchTestCmd()
		_ = cmd.Flags().Set("profile", "staging")
		if err := runWorkspaceSwitch(cmd, []string{"alpha"}); err != nil {
			t.Fatalf("runWorkspaceSwitch: %v", err)
		}

		// The staging profile picked up Alpha; the default profile (touched
		// earlier in this test) must remain unaffected.
		stagingCfg, err := cli.LoadCLIConfigForProfile("staging")
		if err != nil {
			t.Fatalf("load staging config: %v", err)
		}
		if stagingCfg.WorkspaceID != "11111111-1111-1111-1111-111111111111" {
			t.Errorf("staging workspace_id = %q, want Alpha's id", stagingCfg.WorkspaceID)
		}

		// Verify the staging profile config landed in the expected path.
		path, _ := cli.CLIConfigPathForProfile("staging")
		wantSuffix := filepath.Join(".multica", "profiles", "staging", "config.json")
		if !strings.HasSuffix(path, wantSuffix) {
			t.Errorf("staging config path = %q, want suffix %q", path, wantSuffix)
		}
		if _, err := os.Stat(path); err != nil {
			t.Errorf("expected staging config file at %s, got %v", path, err)
		}
	})
}

func TestResolveWorkspaceByIDOrSlug(t *testing.T) {
	workspaces := []workspaceSummary{
		{ID: "11111111-1111-1111-1111-111111111111", Name: "Alpha", Slug: "alpha"},
		{ID: "22222222-2222-2222-2222-222222222222", Name: "Beta", Slug: "beta"},
	}

	t.Run("matches by exact UUID", func(t *testing.T) {
		ws, err := resolveWorkspaceByIDOrSlug(workspaces, "22222222-2222-2222-2222-222222222222")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if ws.Name != "Beta" {
			t.Errorf("got %q, want Beta", ws.Name)
		}
	})

	t.Run("matches by slug", func(t *testing.T) {
		ws, err := resolveWorkspaceByIDOrSlug(workspaces, "alpha")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if ws.ID != "11111111-1111-1111-1111-111111111111" {
			t.Errorf("got id %q, want alpha's id", ws.ID)
		}
	})

	t.Run("slug match is case-insensitive", func(t *testing.T) {
		ws, err := resolveWorkspaceByIDOrSlug(workspaces, "ALPHA")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if ws.Slug != "alpha" {
			t.Errorf("got %q, want alpha", ws.Slug)
		}
	})

	t.Run("unknown target returns access-style error", func(t *testing.T) {
		_, err := resolveWorkspaceByIDOrSlug(workspaces, "gamma")
		if err == nil {
			t.Fatal("expected error for unknown workspace")
		}
		// The error should hint at running 'workspace list' so the user has an
		// actionable next step. We treat it as a soft contract because it is
		// the message users see when they typo a slug.
		if !strings.Contains(err.Error(), "workspace list") {
			t.Errorf("error %q should reference 'workspace list'", err)
		}
	})

	t.Run("empty target is rejected", func(t *testing.T) {
		_, err := resolveWorkspaceByIDOrSlug(workspaces, "   ")
		if err == nil {
			t.Fatal("expected error for empty target")
		}
	})

	t.Run("whitespace-padded target is trimmed", func(t *testing.T) {
		ws, err := resolveWorkspaceByIDOrSlug(workspaces, "  beta  ")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if ws.Name != "Beta" {
			t.Errorf("got %q, want Beta", ws.Name)
		}
	})

	t.Run("matches unique short UUID prefix", func(t *testing.T) {
		ws, err := resolveWorkspaceByIDOrSlug(workspaces, "2222")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if ws.Name != "Beta" {
			t.Errorf("got %q, want Beta", ws.Name)
		}
	})

	t.Run("short UUID prefix with dashes is accepted", func(t *testing.T) {
		ws, err := resolveWorkspaceByIDOrSlug(workspaces, "1111-11")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if ws.Name != "Alpha" {
			t.Errorf("got %q, want Alpha", ws.Name)
		}
	})

	t.Run("ambiguous prefix lists all matches", func(t *testing.T) {
		ambiguous := []workspaceSummary{
			{ID: "ab123456-0000-0000-0000-000000000001", Name: "First", Slug: "first"},
			{ID: "ab129999-0000-0000-0000-000000000002", Name: "Second", Slug: "second"},
		}
		_, err := resolveWorkspaceByIDOrSlug(ambiguous, "ab12")
		if err == nil {
			t.Fatal("expected ambiguous prefix error")
		}
		if !strings.Contains(err.Error(), "ambiguous") {
			t.Errorf("error = %q, want it to mention 'ambiguous'", err)
		}
		// Both candidate IDs must surface so the user can disambiguate without
		// re-running `workspace list`.
		if !strings.Contains(err.Error(), "ab123456") || !strings.Contains(err.Error(), "ab129999") {
			t.Errorf("error = %q, want both candidate IDs", err)
		}
	})

	t.Run("slug wins over colliding UUID prefix", func(t *testing.T) {
		// If a workspace's slug equals another workspace's UUID prefix, the
		// slug must take priority — that's the value users actually see in
		// `workspace list`.
		collision := []workspaceSummary{
			{ID: "deadbeef-0000-0000-0000-000000000001", Name: "Hex", Slug: "hex"},
			{ID: "feedface-0000-0000-0000-000000000002", Name: "Decoy", Slug: "deadbeef"},
		}
		ws, err := resolveWorkspaceByIDOrSlug(collision, "deadbeef")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if ws.Name != "Decoy" {
			t.Errorf("got %q, want Decoy (slug match should beat UUID prefix)", ws.Name)
		}
	})

	t.Run("non-hex unknown target falls through to not-found", func(t *testing.T) {
		// 'gamma' has letters outside the hex range, so it cannot reach the
		// prefix branch — must surface the not-found error pointing the user
		// at `workspace list`.
		_, err := resolveWorkspaceByIDOrSlug(workspaces, "gamma")
		if err == nil {
			t.Fatal("expected error for unknown workspace")
		}
		if !strings.Contains(err.Error(), "workspace list") {
			t.Errorf("error = %q, want it to reference 'workspace list'", err)
		}
	})

	t.Run("prefix shorter than 4 hex chars is rejected", func(t *testing.T) {
		// Too-short prefixes would collide with random hex substrings; the
		// resolver must surface the not-found error rather than silently
		// returning a wrong workspace.
		_, err := resolveWorkspaceByIDOrSlug(workspaces, "11")
		if err == nil {
			t.Fatal("expected error for 2-char prefix")
		}
		if !strings.Contains(err.Error(), "workspace list") {
			t.Errorf("error = %q, want it to reference 'workspace list'", err)
		}
	})
}

// resetWorkspaceUpdateFlags clears every flag on workspaceUpdateCmd and marks
// each as not-Changed. The cobra.Command instance is a process-wide singleton,
// so previous subtests leak state into the next one without this guard.
func resetWorkspaceUpdateFlags(t *testing.T) {
	t.Helper()
	flags := workspaceUpdateCmd.Flags()
	for _, name := range []string{"name", "description", "context", "issue-prefix"} {
		_ = flags.Set(name, "")
		if f := flags.Lookup(name); f != nil {
			f.Changed = false
		}
	}
	for _, name := range []string{"description-stdin", "context-stdin"} {
		_ = flags.Set(name, "false")
		if f := flags.Lookup(name); f != nil {
			f.Changed = false
		}
	}
}

func setStringFlag(t *testing.T, name, value string) {
	t.Helper()
	if err := workspaceUpdateCmd.Flags().Set(name, value); err != nil {
		t.Fatalf("set --%s: %v", name, err)
	}
}

func setBoolFlag(t *testing.T, name string, value bool) {
	t.Helper()
	v := "false"
	if value {
		v = "true"
	}
	if err := workspaceUpdateCmd.Flags().Set(name, v); err != nil {
		t.Fatalf("set --%s: %v", name, err)
	}
}

func TestBuildWorkspaceUpdateBody(t *testing.T) {
	t.Run("only changed flags appear in body", func(t *testing.T) {
		resetWorkspaceUpdateFlags(t)
		setStringFlag(t, "name", "Acme Eng")

		body, err := buildWorkspaceUpdateBody(workspaceUpdateCmd)
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if got, _ := body["name"].(string); got != "Acme Eng" {
			t.Errorf("name = %v, want Acme Eng", body["name"])
		}
		for _, key := range []string{"description", "context", "issue_prefix"} {
			if _, present := body[key]; present {
				t.Errorf("%s should not appear when its flag was not set, got %v", key, body)
			}
		}
	})

	t.Run("multiple fields combine into one PATCH body", func(t *testing.T) {
		resetWorkspaceUpdateFlags(t)
		setStringFlag(t, "name", "Acme")
		setStringFlag(t, "description", `line1\nline2`)
		setStringFlag(t, "issue-prefix", "ENG")

		body, err := buildWorkspaceUpdateBody(workspaceUpdateCmd)
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if body["name"] != "Acme" {
			t.Errorf("name = %v, want Acme", body["name"])
		}
		// resolveTextFlag decodes \n in inline values.
		if body["description"] != "line1\nline2" {
			t.Errorf("description = %q, want decoded newline", body["description"])
		}
		if body["issue_prefix"] != "ENG" {
			t.Errorf("issue_prefix = %v, want ENG", body["issue_prefix"])
		}
	})

	t.Run("inline + stdin is rejected for description", func(t *testing.T) {
		resetWorkspaceUpdateFlags(t)
		setStringFlag(t, "description", "inline")
		setBoolFlag(t, "description-stdin", true)

		if _, err := buildWorkspaceUpdateBody(workspaceUpdateCmd); err == nil {
			t.Fatalf("expected mutually-exclusive error for --description and --description-stdin")
		}
	})

	t.Run("context-stdin reads from stdin", func(t *testing.T) {
		resetWorkspaceUpdateFlags(t)
		setBoolFlag(t, "context-stdin", true)

		stdinBody := "first\nsecond line with literal \\n\n"
		var got map[string]any
		pipeStdin(t, stdinBody, func() {
			b, err := buildWorkspaceUpdateBody(workspaceUpdateCmd)
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
			got = b
		})
		want := "first\nsecond line with literal \\n"
		if got["context"] != want {
			t.Errorf("context = %q, want %q", got["context"], want)
		}
	})

	t.Run("empty issue-prefix is rejected", func(t *testing.T) {
		resetWorkspaceUpdateFlags(t)
		setStringFlag(t, "issue-prefix", "")
		// Force Changed=true so the flag is treated as "explicitly passed".
		if f := workspaceUpdateCmd.Flags().Lookup("issue-prefix"); f != nil {
			f.Changed = true
		}

		_, err := buildWorkspaceUpdateBody(workspaceUpdateCmd)
		if err == nil {
			t.Fatalf("expected error when --issue-prefix is empty")
		}
		if !strings.Contains(err.Error(), "cannot be empty") {
			t.Errorf("error = %q, want it to mention 'cannot be empty'", err)
		}
	})

	t.Run("whitespace-only issue-prefix is rejected", func(t *testing.T) {
		resetWorkspaceUpdateFlags(t)
		setStringFlag(t, "issue-prefix", "   ")
		if f := workspaceUpdateCmd.Flags().Lookup("issue-prefix"); f != nil {
			f.Changed = true
		}
		if _, err := buildWorkspaceUpdateBody(workspaceUpdateCmd); err == nil {
			t.Fatalf("expected error when --issue-prefix is whitespace-only")
		}
	})

	t.Run("no flags set produces empty body", func(t *testing.T) {
		resetWorkspaceUpdateFlags(t)
		body, err := buildWorkspaceUpdateBody(workspaceUpdateCmd)
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if len(body) != 0 {
			t.Errorf("body = %v, want empty", body)
		}
	})
}

func TestStickyTaskErrorsByAgent(t *testing.T) {
	t.Run("failed outcome sticks when no active task", func(t *testing.T) {
		tasks := []map[string]any{
			{
				"agent_id":       "a1",
				"status":         "failed",
				"completed_at":   "2026-08-03T10:00:00Z",
				"error":          `429: {"message":"已达到7天使用上限"}`,
				"failure_reason": "agent_error.provider_capacity_or_rate_limit",
			},
		}
		got := stickyTaskErrorsByAgent(tasks)
		se, ok := got["a1"]
		if !ok {
			t.Fatal("expected sticky error for a1")
		}
		if !strings.Contains(se.Error, "已达到7天使用上限") {
			t.Errorf("error = %q, want quota text", se.Error)
		}
		if se.FailureReason != "agent_error.provider_capacity_or_rate_limit" {
			t.Errorf("failure_reason = %q", se.FailureReason)
		}
	})

	t.Run("active task clears sticky failure", func(t *testing.T) {
		tasks := []map[string]any{
			{
				"agent_id":     "a1",
				"status":       "failed",
				"completed_at": "2026-08-03T10:00:00Z",
				"error":        "quota exceeded",
			},
			{
				"agent_id": "a1",
				"status":   "running",
			},
		}
		got := stickyTaskErrorsByAgent(tasks)
		if _, ok := got["a1"]; ok {
			t.Fatal("expected no sticky error while task is running")
		}
	})

	t.Run("later completed outcome wins over earlier failure", func(t *testing.T) {
		tasks := []map[string]any{
			{
				"agent_id":     "a1",
				"status":       "failed",
				"completed_at": "2026-08-03T10:00:00Z",
				"error":        "old failure",
			},
			{
				"agent_id":     "a1",
				"status":       "completed",
				"completed_at": "2026-08-03T11:00:00Z",
				"error":        "",
			},
		}
		got := stickyTaskErrorsByAgent(tasks)
		if _, ok := got["a1"]; ok {
			t.Fatal("expected no sticky error after a later success")
		}
	})

	t.Run("newer failure replaces older failure", func(t *testing.T) {
		tasks := []map[string]any{
			{
				"agent_id":     "a1",
				"status":       "failed",
				"completed_at": "2026-08-03T10:00:00Z",
				"error":        "old",
			},
			{
				"agent_id":     "a1",
				"status":       "failed",
				"completed_at": "2026-08-03T12:00:00Z",
				"error":        "new",
			},
		}
		got := stickyTaskErrorsByAgent(tasks)
		if got["a1"].Error != "new" {
			t.Errorf("error = %q, want new", got["a1"].Error)
		}
	})
}

func TestFormatAgentStatusLineIncludesError(t *testing.T) {
	line := formatAgentStatusLine(workspaceInfoAgentRow{
		Status:               "idle",
		RuntimeDisplayStatus: "online",
		Error:                `429: 已达到7天使用上限`,
	})
	if !strings.Contains(line, "idle") {
		t.Errorf("line %q missing status", line)
	}
	if !strings.Contains(line, "runtime=online") {
		t.Errorf("line %q missing runtime", line)
	}
	if !strings.Contains(line, "error: 429") {
		t.Errorf("line %q missing error text", line)
	}
}

func TestComputerErrorTextPrefersUpdateError(t *testing.T) {
	rt := map[string]any{
		"update_error": "activation timed out",
		"auto_update": map[string]any{
			"error_message": "download failed",
		},
	}
	if got := computerErrorText(rt); got != "activation timed out" {
		t.Errorf("got %q, want update_error", got)
	}
	rt2 := map[string]any{
		"auto_update": map[string]any{"error_message": "download failed"},
	}
	if got := computerErrorText(rt2); got != "download failed" {
		t.Errorf("got %q, want auto_update error", got)
	}
}

func TestRunWorkspaceInfo(t *testing.T) {
	const wsID = "7beafc96-3c51-4fcc-9fe7-8c36ceb482ff"
	const agentID = "8f04317a-0000-0000-0000-000000000001"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/workspaces/"+wsID:
			json.NewEncoder(w).Encode(map[string]any{
				"id": wsID, "name": "LRM-team", "slug": "lrm-team",
			})
		case r.URL.Path == "/api/agents":
			json.NewEncoder(w).Encode([]map[string]any{
				{
					"id": agentID, "name": "alice", "display_name": "Alice",
					"status": "idle", "runtime_status": "online",
					"runtime_display_status": "online", "runtime_name": "s144",
				},
			})
		case r.URL.Path == "/api/runtimes":
			json.NewEncoder(w).Encode([]map[string]any{
				{
					"id": "rt1", "name": "s144", "display_name": "",
					"provider": "cursor", "status": "online",
					"runtime_health": "ok", "current_version": "0.3.99",
					"update_error": nil,
				},
				{
					"id": "rt2", "name": "old-box", "provider": "pi",
					"status": "offline", "current_version": "0.3.97",
					"update_error": "activation did not complete within 20 minutes",
				},
			})
		case r.URL.Path == "/api/agent-task-snapshot":
			json.NewEncoder(w).Encode([]map[string]any{
				{
					"agent_id": agentID, "status": "failed",
					"completed_at":   "2026-08-03T05:00:00Z",
					"error":          `429: {"message":"已达到 7 天使用上限"}`,
					"failure_reason": "agent_error.provider_capacity_or_rate_limit",
				},
			})
		case r.URL.Path == "/api/projects":
			json.NewEncoder(w).Encode(map[string]any{
				"projects": []map[string]any{
					{
						"id": "proj-1", "title": "Agent UX 2026", "status": "in_progress",
						"resources": []map[string]any{
							{
								"id": "res-1", "resource_type": "github_repo",
								"resource_ref": map[string]any{"url": "https://github.com/org/agent-ux"},
								"label":        "agent-ux",
							},
						},
					},
				},
				"total": 1,
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	t.Setenv("HOME", t.TempDir())
	t.Setenv("MULTICA_SERVER_URL", srv.URL)
	t.Setenv("MULTICA_TOKEN", "test-token")
	t.Setenv("MULTICA_WORKSPACE_ID", wsID)

	cmd := &cobra.Command{Use: "info"}
	cmd.Flags().String("workspace-id", "", "")
	cmd.Flags().String("profile", "", "")
	cmd.Flags().String("server-url", "", "")
	cmd.Flags().String("output", "table", "")
	cmd.Flags().Bool("include-archived", false, "")
	cmd.Flags().Bool("agents", false, "")
	cmd.Flags().Bool("computers", false, "")
	cmd.Flags().Bool("projects", false, "")
	cmd.Flags().String("query", "", "")
	cmd.Flags().Int("limit", 0, "")
	cmd.Flags().Int("offset", 0, "")

	// Capture stdout.
	rOut, wOut, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stdout
	os.Stdout = wOut
	errRun := runWorkspaceInfo(cmd, nil)
	_ = wOut.Close()
	os.Stdout = old
	if errRun != nil {
		t.Fatalf("runWorkspaceInfo: %v", errRun)
	}
	var buf strings.Builder
	_, _ = io.Copy(&buf, rOut)
	_ = rOut.Close()
	out := buf.String()

	for _, want := range []string{
		"## Workspace",
		"LRM-team",
		"## Agents (1)",
		"alice",
		"error: 429",
		"已达到 7 天使用上限",
		"## Computers (2)",
		"s144",
		"old-box",
		"activation did not complete",
		"## Projects (1)",
		"Agent UX 2026",
		"https://github.com/org/agent-ux",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q\nfull:\n%s", want, out)
		}
	}
}

func TestFilterAndPageWorkspaceInfo(t *testing.T) {
	agents := []workspaceInfoAgentRow{
		{Name: "alice", Status: "idle", Error: "quota"},
		{Name: "bob", Status: "working"},
		{Name: "carol", Status: "offline", Error: "429"},
	}
	got := filterWorkspaceInfoAgents(agents, "quota")
	if len(got) != 1 || got[0].Name != "alice" {
		t.Fatalf("query quota: %+v", got)
	}
	got = filterWorkspaceInfoAgents(agents, "429")
	if len(got) != 1 || got[0].Name != "carol" {
		t.Fatalf("query 429: %+v", got)
	}
	paged := pageWorkspaceInfoSlice(agents, 1, 1)
	if len(paged) != 1 || paged[0].Name != "bob" {
		t.Fatalf("page: %+v", paged)
	}
	if len(pageWorkspaceInfoSlice(agents, 10, 5)) != 0 {
		t.Fatal("offset past end")
	}
}
