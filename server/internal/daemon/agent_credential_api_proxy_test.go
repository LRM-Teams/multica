package daemon

import (
	"bytes"
	"encoding/json"
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

func TestCredentialProxyAssociatesOnlySuccessfulCanonicalSendWithActiveProviderContext(t *testing.T) {
	d := &Daemon{}
	d.SetActiveProviderToolContext(ActiveProviderToolContext{
		AgentID: "agent-1", CallID: "C17", ToolCallID: "tool-1",
	})

	d.observeCanonicalActionOutcome("agent-1", "message", "70000000-0000-4000-8000-000000000301", false)
	if got := d.ObservedCanonicalActionAssociations("agent-1"); len(got) != 0 {
		t.Fatalf("failed send must not associate: %+v", got)
	}

	d.observeCanonicalActionOutcome("agent-1", "message", "70000000-0000-4000-8000-000000000302", true)
	got := d.ObservedCanonicalActionAssociations("agent-1")
	if len(got) != 1 {
		t.Fatalf("successful send associations = %+v, want one trusted message association", got)
	}
	if got[0].Kind != "message" || got[0].CanonicalID != "70000000-0000-4000-8000-000000000302" {
		t.Fatalf("association = %+v, want successful message canonical id", got[0])
	}
	if got[0].ProducerCallID != "C17" || got[0].ToolCallID != "tool-1" {
		t.Fatalf("association must bind active provider/tool context, got %+v", got[0])
	}
}

func TestCredentialProxyAssociatesOnlySuccessfulCanonicalReactionWithActiveProviderContext(t *testing.T) {
	d := &Daemon{}
	d.SetActiveProviderToolContext(ActiveProviderToolContext{
		AgentID: "agent-1", CallID: "C18", ToolCallID: "tool-react",
	})

	d.observeCanonicalActionOutcome("agent-1", "reaction", "70000000-0000-4000-8000-000000000311", false)
	if got := d.ObservedCanonicalActionAssociations("agent-1"); len(got) != 0 {
		t.Fatalf("failed reaction must not associate: %+v", got)
	}

	d.observeCanonicalActionOutcome("agent-1", "reaction", "70000000-0000-4000-8000-000000000312", true)
	got := d.ObservedCanonicalActionAssociations("agent-1")
	if len(got) != 1 || got[0].Kind != "reaction" || got[0].CanonicalID != "70000000-0000-4000-8000-000000000312" {
		t.Fatalf("successful reaction associations = %+v", got)
	}
	if got[0].ProducerCallID != "C18" || got[0].ToolCallID != "tool-react" {
		t.Fatalf("reaction association missing active context: %+v", got[0])
	}
}

func TestCredentialProxyDoesNotAssociateCanonicalActionWithoutActiveProviderContext(t *testing.T) {
	d := &Daemon{}
	d.observeCanonicalActionOutcome("agent-1", "message", "70000000-0000-4000-8000-000000000321", true)
	if got := d.ObservedCanonicalActionAssociations("agent-1"); len(got) != 0 {
		t.Fatalf("success without active provider/tool context must not associate: %+v", got)
	}
}

func TestCredentialProxyIgnoresAgentDeclaredProvenanceInRequestBody(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if strings.Contains(string(body), "forged-C99") {
			// Generic API proxy may forward unknown fields; trusted provenance
			// must still come only from daemon-observed success + active context.
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"70000000-0000-4000-8000-000000000331"}`))
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
	d.SetActiveProviderToolContext(ActiveProviderToolContext{AgentID: "agent-1", CallID: "C19", ToolCallID: "tool-1"})

	req := httptest.NewRequest(http.MethodPost, "/api/agent/messages/send", bytes.NewBufferString(`{
		"target":"channel:one",
		"content":"hello",
		"producer_call_id":"forged-C99",
		"canonical_id":"forged-canonical",
		"tool_call_id":"forged-tool"
	}`))
	req.Header.Set("X-Agent-ID", "agent-1")
	req.Header.Set("X-Workspace-ID", "workspace-1")
	rec := httptest.NewRecorder()
	d.credentialProxyAgentAPIHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("proxy status = %d body=%s", rec.Code, rec.Body.String())
	}
	// Forwarding agent-declared provenance fields must not create associations
	// by itself; only a successful observed canonical id may bind.
	if got := d.ObservedCanonicalActionAssociations("agent-1"); len(got) != 0 {
		t.Fatalf("request-body provenance must not create associations: %+v", got)
	}
	var response map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode proxy response: %v", err)
	}
	canonicalID, _ := response["id"].(string)
	if canonicalID == "" {
		t.Fatal("successful send response must expose a canonical id")
	}
	d.observeCanonicalActionOutcome("agent-1", "message", canonicalID, true)
	got := d.ObservedCanonicalActionAssociations("agent-1")
	if len(got) != 1 || got[0].CanonicalID != canonicalID || got[0].ProducerCallID != "C19" || got[0].ToolCallID != "tool-1" {
		t.Fatalf("trusted association = %+v, want response id bound to active context C19/tool-1", got)
	}
	for _, association := range got {
		if association.ProducerCallID == "forged-C99" || association.CanonicalID == "forged-canonical" {
			t.Fatalf("forged request provenance leaked into trusted association: %+v", got)
		}
	}
}
