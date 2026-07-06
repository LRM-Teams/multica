package main

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

// TestForkAndSandboxRoutesAreRegistered asserts the Phase 1 routes are wired
// into the router. It only checks that each route is matched (anything other
// than a router 404) — auth / workspace-membership middleware legitimately
// rejects these unauthenticated probes with 401/400, which still proves the
// route exists. Runs against the shared testServer; skipped without a DB.
func TestForkAndSandboxRoutesAreRegistered(t *testing.T) {
	cases := []struct {
		method string
		path   string
		body   string
	}{
		{http.MethodPost, "/api/cloud-runtime/sandboxes/sbx-1/snapshot", ""},
		{http.MethodPost, "/api/cloud-runtime/sandboxes/fork", `{"snapshot_id":"snap-1"}`},
	}

	client := &http.Client{}
	for _, c := range cases {
		var body io.Reader
		if c.body != "" {
			body = strings.NewReader(c.body)
		}
		req, err := http.NewRequest(c.method, testServer.URL+c.path, body)
		if err != nil {
			t.Fatalf("build request %s %s: %v", c.method, c.path, err)
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("request %s %s: %v", c.method, c.path, err)
		}
		resp.Body.Close()
		if resp.StatusCode == http.StatusNotFound {
			t.Errorf("route %s %s not registered (got 404)", c.method, c.path)
		}
	}
}
