package main

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

// TestEnvDispatchChannelFirstAndProjectFirstRoutesRegistered asserts the
// channel-first facades are wired in, and that the project-first routes remain
// available. It only checks that each route is matched (anything other than a
// router 404) - auth/workspace middleware legitimately rejects these
// unauthenticated probes with 401/400, which still proves the route exists.
// Runs against the shared testServer; skipped without a DB.
func TestEnvDispatchChannelFirstAndProjectFirstRoutesRegistered(t *testing.T) {
	if testServer == nil {
		t.Skip("database not available")
	}
	const id = "00000000-0000-0000-0000-000000000000"
	cases := []struct {
		method string
		path   string
		body   string
	}{
		// Channel-first facades.
		{http.MethodGet, "/api/v1/env-dispatch/channels/" + id + "/dag", ""},
		{http.MethodDelete, "/api/v1/env-dispatch/channels/" + id, ""},
		{http.MethodGet, "/api/v1/channels/" + id + "/env-checkpoints", ""},
		// Project-first routes remain available (issue dispatch + compatibility).
		{http.MethodDelete, "/api/v1/env-dispatch/" + id, ""},
		{http.MethodGet, "/api/v1/env-dispatch/" + id + "/dag", ""},
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
