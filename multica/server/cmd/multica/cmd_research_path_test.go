package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/cobra"
)

func TestResearchAPIPath_AgentTokenRewrites(t *testing.T) {
	dir := t.TempDir()
	tokenFile := filepath.Join(dir, "token")
	if err := os.WriteFile(tokenFile, []byte("mat_test_token_for_research_path\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MULTICA_TOKEN", "")
	t.Setenv("MULTICA_TOKEN_FILE", tokenFile)

	cmd := &cobra.Command{Use: "research"}
	got := researchAPIPath(cmd, "/api/research/fleet/members")
	want := "/api/agent/research/fleet/members"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
	got = researchAPIPath(cmd, "/api/research/sessions/abc/graph/nodes")
	want = "/api/agent/research/sessions/abc/graph/nodes"
	if got != want {
		t.Fatalf("session path got %q want %q", got, want)
	}
}

func TestResearchAPIPath_HumanTokenUnchanged(t *testing.T) {
	t.Setenv("MULTICA_TOKEN", "mul_human_token")
	t.Setenv("MULTICA_TOKEN_FILE", "")
	cmd := &cobra.Command{Use: "research"}
	path := "/api/research/fleet/members"
	if got := researchAPIPath(cmd, path); got != path {
		t.Fatalf("got %q want unchanged %q", got, path)
	}
}
