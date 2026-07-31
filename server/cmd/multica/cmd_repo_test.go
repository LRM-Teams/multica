package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// TestRunRepoCheckoutRequiresAgentID guards the daemon-side persistent-ReposDir
// fix (task #29, 2026-07-31): the daemon resolves the checkout location from
// (workspace_id, agent_id), so a request with no agent_id can no longer be
// served correctly. Fail fast client-side with a clear error instead of
// sending a request the daemon will reject.
func TestRunRepoCheckoutRequiresAgentID(t *testing.T) {
	t.Setenv("MULTICA_DAEMON_PORT", "1")
	t.Setenv("MULTICA_AGENT_ID", "")

	err := runRepoCheckout(repoCheckoutCmd, []string{"https://example.com/repo.git"})
	if err == nil {
		t.Fatal("expected error when MULTICA_AGENT_ID is unset")
	}
	if got := err.Error(); !strings.Contains(got, "MULTICA_AGENT_ID") {
		t.Fatalf("error = %q, want it to mention MULTICA_AGENT_ID", got)
	}
}

// TestRunRepoCheckoutSendsAgentID proves the CLI forwards MULTICA_AGENT_ID to
// the daemon's /repo/checkout endpoint, since that's what lets the daemon
// route the checkout into the agent's persistent ReposDir instead of the
// caller's ephemeral CWD.
func TestRunRepoCheckoutSendsAgentID(t *testing.T) {
	var gotBody map[string]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Errorf("decode request body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"path": "/repo", "branch_name": "agent/x/y"})
	}))
	t.Cleanup(srv.Close)

	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatalf("parse test server URL: %v", err)
	}
	t.Setenv("MULTICA_DAEMON_PORT", u.Port())
	t.Setenv("MULTICA_AGENT_ID", "22222222-2222-2222-2222-222222222222")
	t.Setenv("MULTICA_WORKSPACE_ID", "11111111-1111-1111-1111-111111111111")

	if err := runRepoCheckout(repoCheckoutCmd, []string{"https://example.com/repo.git"}); err != nil {
		t.Fatalf("runRepoCheckout: %v", err)
	}
	if gotBody["agent_id"] != "22222222-2222-2222-2222-222222222222" {
		t.Fatalf("request body agent_id = %q, want the MULTICA_AGENT_ID env value", gotBody["agent_id"])
	}
}
