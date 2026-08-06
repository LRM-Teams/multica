package daemon

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestCredentialProxyAgentAPIForwardsDurableCredentialAndBusinessRequest(t *testing.T) {
	var calls int
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != http.MethodPost || r.URL.Path != "/api/agent/reminders/list" || r.URL.RawQuery != "status=active" {
			t.Errorf("upstream request = %s %s?%s", r.Method, r.URL.Path, r.URL.RawQuery)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer cached-token" {
			t.Errorf("Authorization = %q, want durable credential", got)
		}
		for name, want := range map[string]string{
			"X-Agent-ID":        "agent-1",
			"X-Workspace-ID":    "workspace-1",
			"Idempotency-Key":   "reminder-list-1",
			"X-Client-Platform": "cli",
		} {
			if got := r.Header.Get(name); got != want {
				t.Errorf("%s = %q, want %q", name, got, want)
			}
		}
		for _, name := range []string{
			"X-Task-ID", "X-Agent-Inbox-Event-ID", "X-Agent-Inbox-Delivery-ID", "X-Agent-Inbox-Lease-Token",
			"X-Actor-Source", "X-Agent-Credential-ID", "X-User-ID", "X-User-Email", "Cookie",
		} {
			if got := r.Header.Get(name); got != "" {
				t.Errorf("%s = %q, want stripped", name, got)
			}
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read upstream body: %v", err)
		}
		if got := string(body); got != `{"status":"active"}` {
			t.Errorf("upstream body = %q", got)
		}
		w.Header().Set("X-Proxy-Response", "preserved")
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"reminders":[]}`))
	}))
	defer upstream.Close()

	cfg := Config{WorkspacesRoot: t.TempDir(), ServerBaseURL: upstream.URL}
	expiresAt := time.Now().Add(2 * time.Hour).UTC().Format(time.RFC3339Nano)
	if _, err := writeCachedAgentCredential(cfg, "workspace-1", "runtime-1", "agent-1", AgentCredentialResponse{
		ID: "credential-1", AgentID: "agent-1", Prefix: "mac_test", Token: "cached-token", ExpiresAt: &expiresAt,
	}, time.Now()); err != nil {
		t.Fatalf("write cached credential: %v", err)
	}
	d := &Daemon{cfg: cfg}

	req := httptest.NewRequest(http.MethodPost, "/api/agent/reminders/list?status=active", bytes.NewBufferString(`{"status":"active"}`))
	req.Header.Set("Authorization", "Bearer agent-controlled-token")
	req.Header.Set("X-Agent-ID", "agent-1")
	req.Header.Set("X-Workspace-ID", "workspace-1")
	req.Header.Set("X-Task-ID", "")
	req.Header.Set("X-Actor-Source", "forged")
	req.Header.Set("X-User-ID", "forged-user")
	req.Header.Set("X-User-Email", "forged@example.com")
	req.Header.Set("Cookie", "session=forged")
	req.Header.Set("Idempotency-Key", "reminder-list-1")
	req.Header.Set("X-Client-Platform", "cli")

	rec := httptest.NewRecorder()
	d.credentialProxyAgentAPIHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("proxy status = %d body=%s", rec.Code, rec.Body.String())
	}
	if calls != 1 {
		t.Fatalf("upstream calls = %d, want 1", calls)
	}
	if got := rec.Header().Get("X-Proxy-Response"); got != "preserved" {
		t.Fatalf("response header = %q, want preserved", got)
	}
	if got := rec.Body.String(); got != `{"reminders":[]}` {
		t.Fatalf("proxy response body = %q", got)
	}
}

func TestCredentialProxyAgentAPIRejectsRetiredExecutionHeaders(t *testing.T) {
	var calls int
	upstream := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls++ }))
	defer upstream.Close()
	d := &Daemon{cfg: Config{ServerBaseURL: upstream.URL}}

	for _, header := range []string{"X-Task-ID", "X-Agent-Inbox-Event-ID", "X-Agent-Inbox-Delivery-ID", "X-Agent-Inbox-Lease-Token"} {
		t.Run(strings.ToLower(header), func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/api/agent/reminders/list", nil)
			req.Header.Set("X-Agent-ID", "agent-1")
			req.Header.Set("X-Workspace-ID", "workspace-1")
			req.Header.Set(header, "retired-context")
			rec := httptest.NewRecorder()
			d.credentialProxyAgentAPIHandler().ServeHTTP(rec, req)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d body=%s, want 400", rec.Code, rec.Body.String())
			}
		})
	}
	if calls != 0 {
		t.Fatalf("upstream calls = %d, want 0", calls)
	}
}
