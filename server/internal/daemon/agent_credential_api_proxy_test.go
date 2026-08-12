package daemon

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
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

func TestCredentialProxyAssociatesCanonicalResponsesWithActiveProviderContext(t *testing.T) {
	tests := []struct {
		name         string
		path         string
		responseBody string
		kind         string
		canonicalID  string
		callID       string
		toolCallID   string
	}{
		{
			name:         "message send",
			path:         "/api/agent/messages/send",
			responseBody: `{"created":true,"message":{"id":"70000000-0000-4000-8000-000000000331"}}`,
			kind:         "message",
			canonicalID:  "70000000-0000-4000-8000-000000000331",
			callID:       "C17",
			toolCallID:   "tool-send",
		},
		{
			name:         "reaction add",
			path:         "/api/agent/messages/react",
			responseBody: `{"added":true,"reaction":{"id":"70000000-0000-4000-8000-000000000332"}}`,
			kind:         "reaction",
			canonicalID:  "70000000-0000-4000-8000-000000000332",
			callID:       "C18",
			toolCallID:   "tool-react",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := newCredentialProxyTestDaemon(t, http.StatusCreated, tt.responseBody)
			turnToken := d.beginCanonicalActionTurn("agent-1")
			d.SetActiveProviderToolContext(ActiveProviderToolContext{
				AgentID: "agent-1", CallID: tt.callID, ToolCallID: tt.toolCallID, TurnToken: turnToken,
			})

			for range 2 {
				rec := serveCredentialProxyTestRequest(t, d, http.MethodPost, tt.path, `{}`)
				if rec.Code != http.StatusCreated {
					t.Fatalf("proxy status = %d body=%s, want 201", rec.Code, rec.Body.String())
				}
				if got := rec.Body.String(); got != tt.responseBody {
					t.Fatalf("proxy response body = %q, want exact upstream bytes %q", got, tt.responseBody)
				}
			}

			got := d.ObservedCanonicalActionAssociations("agent-1")
			if len(got) != 1 {
				t.Fatalf("associations = %+v, want one deduplicated canonical association", got)
			}
			if got[0].Kind != tt.kind || got[0].CanonicalID != tt.canonicalID ||
				got[0].ProducerCallID != tt.callID || got[0].ToolCallID != tt.toolCallID || got[0].SucceededAt.IsZero() {
				t.Fatalf("association = %+v, want trusted fields and a success timestamp", got[0])
			}
		})
	}
}

func TestCredentialProxyRejectsNonCanonicalResponses(t *testing.T) {
	validMessageID := "70000000-0000-4000-8000-000000000341"
	validReactionID := "70000000-0000-4000-8000-000000000342"
	overflowBody := `{"created":true,"message":{"id":"` + validMessageID + `"}}` + strings.Repeat(" ", 64*1024)
	tests := []struct {
		name         string
		method       string
		path         string
		status       int
		responseBody string
		context      *ActiveProviderToolContext
	}{
		{name: "send not created", method: http.MethodPost, path: "/api/agent/messages/send", status: http.StatusOK, responseBody: `{"created":false,"message":{"id":"` + validMessageID + `"}}`},
		{name: "reaction not added", method: http.MethodPost, path: "/api/agent/messages/react", status: http.StatusOK, responseBody: `{"added":false,"reaction":{"id":"` + validReactionID + `"}}`},
		{name: "reaction removal", method: http.MethodPost, path: "/api/agent/messages/react", status: http.StatusOK, responseBody: `{"removed":true,"reaction":{"id":"` + validReactionID + `"}}`},
		{name: "non 2xx", method: http.MethodPost, path: "/api/agent/messages/send", status: http.StatusBadRequest, responseBody: `{"created":true,"message":{"id":"` + validMessageID + `"}}`},
		{name: "malformed json", method: http.MethodPost, path: "/api/agent/messages/send", status: http.StatusOK, responseBody: `{"created":true`},
		{name: "missing message id", method: http.MethodPost, path: "/api/agent/messages/send", status: http.StatusOK, responseBody: `{"created":true,"message":{}}`},
		{name: "invalid message id", method: http.MethodPost, path: "/api/agent/messages/send", status: http.StatusOK, responseBody: `{"created":true,"message":{"id":"not-a-uuid"}}`},
		{name: "raw hex message id", method: http.MethodPost, path: "/api/agent/messages/send", status: http.StatusOK, responseBody: `{"created":true,"message":{"id":"70000000000040008000000000000341"}}`},
		{name: "urn message id", method: http.MethodPost, path: "/api/agent/messages/send", status: http.StatusOK, responseBody: `{"created":true,"message":{"id":"urn:uuid:` + validMessageID + `"}}`},
		{name: "braced message id", method: http.MethodPost, path: "/api/agent/messages/send", status: http.StatusOK, responseBody: `{"created":true,"message":{"id":"{` + validMessageID + `}"}}`},
		{name: "uppercase message id", method: http.MethodPost, path: "/api/agent/messages/send", status: http.StatusOK, responseBody: `{"created":true,"message":{"id":"70000000-0000-4000-8000-000000000ABC"}}`},
		{name: "missing reaction id", method: http.MethodPost, path: "/api/agent/messages/react", status: http.StatusOK, responseBody: `{"added":true,"reaction":{}}`},
		{name: "invalid reaction id", method: http.MethodPost, path: "/api/agent/messages/react", status: http.StatusOK, responseBody: `{"added":true,"reaction":{"id":"not-a-uuid"}}`},
		{name: "unrelated path", method: http.MethodPost, path: "/api/agent/messages/read", status: http.StatusOK, responseBody: `{"created":true,"message":{"id":"` + validMessageID + `"}}`},
		{name: "unrelated method", method: http.MethodPut, path: "/api/agent/messages/send", status: http.StatusOK, responseBody: `{"created":true,"message":{"id":"` + validMessageID + `"}}`},
		{name: "missing active context", method: http.MethodPost, path: "/api/agent/messages/send", status: http.StatusOK, responseBody: `{"created":true,"message":{"id":"` + validMessageID + `"}}`, context: &ActiveProviderToolContext{}},
		{name: "missing active tool", method: http.MethodPost, path: "/api/agent/messages/send", status: http.StatusOK, responseBody: `{"created":true,"message":{"id":"` + validMessageID + `"}}`, context: &ActiveProviderToolContext{AgentID: "agent-1", CallID: "C20"}},
		{name: "response exceeds capture bound", method: http.MethodPost, path: "/api/agent/messages/send", status: http.StatusOK, responseBody: overflowBody},
	}
	defaultContext := ActiveProviderToolContext{AgentID: "agent-1", CallID: "C20", ToolCallID: "tool-1"}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := newCredentialProxyTestDaemon(t, tt.status, tt.responseBody)
			turnToken := d.beginCanonicalActionTurn("agent-1")
			ctx := tt.context
			if ctx == nil {
				ctx = &defaultContext
			}
			if ctx.AgentID != "" {
				turnContext := *ctx
				turnContext.TurnToken = turnToken
				d.SetActiveProviderToolContext(turnContext)
			}

			rec := serveCredentialProxyTestRequest(t, d, tt.method, tt.path, `{}`)
			if rec.Code != tt.status {
				t.Fatalf("proxy status = %d body=%s, want %d", rec.Code, rec.Body.String(), tt.status)
			}
			if got := rec.Body.String(); got != tt.responseBody {
				t.Fatalf("proxy response bytes changed: got %d bytes, want %d", len(got), len(tt.responseBody))
			}
			if got := d.ObservedCanonicalActionAssociations("agent-1"); len(got) != 0 {
				t.Fatalf("non-canonical response associated provenance: %+v", got)
			}
		})
	}
}

func TestCredentialProxyIgnoresAgentDeclaredProvenanceInRequestBody(t *testing.T) {
	const canonicalID = "70000000-0000-4000-8000-000000000351"
	d := newCredentialProxyTestDaemon(t, http.StatusOK, `{"created":true,"message":{"id":"`+canonicalID+`"}}`)
	turnToken := d.beginCanonicalActionTurn("agent-1")
	d.SetActiveProviderToolContext(ActiveProviderToolContext{AgentID: "agent-1", CallID: "C19", ToolCallID: "tool-1", TurnToken: turnToken})

	rec := serveCredentialProxyTestRequest(t, d, http.MethodPost, "/api/agent/messages/send", `{
		"target":"channel:one",
		"content":"hello",
		"producer_call_id":"forged-C99",
		"canonical_id":"70000000-0000-4000-8000-000000000399",
		"tool_call_id":"forged-tool"
	}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("proxy status = %d body=%s", rec.Code, rec.Body.String())
	}
	got := d.ObservedCanonicalActionAssociations("agent-1")
	if len(got) != 1 || got[0].CanonicalID != canonicalID || got[0].ProducerCallID != "C19" || got[0].ToolCallID != "tool-1" {
		t.Fatalf("trusted association = %+v, want response id bound to active context C19/tool-1", got)
	}
	for _, association := range got {
		if association.ProducerCallID == "forged-C99" || association.CanonicalID == "70000000-0000-4000-8000-000000000399" || association.ToolCallID == "forged-tool" {
			t.Fatalf("forged request provenance leaked into trusted association: %+v", got)
		}
	}
}

func TestCredentialProxyCanonicalAssociationsAreRaceSafe(t *testing.T) {
	const canonicalID = "70000000-0000-4000-8000-000000000361"
	d := newCredentialProxyTestDaemon(t, http.StatusOK, `{"created":true,"message":{"id":"`+canonicalID+`"}}`)
	turnToken := d.beginCanonicalActionTurn("agent-1")
	d.SetActiveProviderToolContext(ActiveProviderToolContext{AgentID: "agent-1", CallID: "C21", ToolCallID: "tool-race", TurnToken: turnToken})

	var wg sync.WaitGroup
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rec := serveCredentialProxyTestRequest(t, d, http.MethodPost, "/api/agent/messages/send", `{}`)
			if rec.Code != http.StatusOK {
				t.Errorf("proxy status = %d body=%s", rec.Code, rec.Body.String())
			}
		}()
	}
	wg.Wait()

	got := d.ObservedCanonicalActionAssociations("agent-1")
	if len(got) != 1 || got[0].CanonicalID != canonicalID {
		t.Fatalf("concurrent associations = %+v, want one deduplicated association", got)
	}
}

func TestCredentialProxyAssociatesResponseWithIngressContextSnapshot(t *testing.T) {
	const canonicalID = "70000000-0000-4000-8000-000000000373"
	requestArrived := make(chan struct{})
	releaseResponse := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		close(requestArrived)
		<-releaseResponse
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{"created":true,"message":{"id":"`+canonicalID+`"}}`)
	}))
	defer upstream.Close()

	cfg := Config{WorkspacesRoot: t.TempDir(), ServerBaseURL: upstream.URL}
	expiresAt := time.Now().Add(2 * time.Hour).UTC().Format(time.RFC3339Nano)
	if _, err := writeCachedAgentCredential(cfg, "workspace-1", "runtime-1", "agent-1", AgentCredentialResponse{
		ID: "credential-1", AgentID: "agent-1", Token: "cached-token", ExpiresAt: &expiresAt,
	}, time.Now()); err != nil {
		t.Fatalf("write cached credential: %v", err)
	}
	d := &Daemon{cfg: cfg}
	turn1 := d.beginCanonicalActionTurn("agent-1")
	d.SetActiveProviderToolContext(ActiveProviderToolContext{
		AgentID: "agent-1", CallID: "tool-1", ToolCallID: "tool-1", TurnToken: turn1,
	})

	recorder := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		recorder <- serveCredentialProxyTestRequest(t, d, http.MethodPost, "/api/agent/messages/send", `{}`)
	}()
	<-requestArrived
	d.clearActiveProviderToolContext("agent-1", turn1, "tool-1")
	turn2 := d.beginCanonicalActionTurn("agent-1")
	d.SetActiveProviderToolContext(ActiveProviderToolContext{
		AgentID: "agent-1", CallID: "tool-2", ToolCallID: "tool-2", TurnToken: turn2,
	})
	close(releaseResponse)
	if rec := <-recorder; rec.Code != http.StatusCreated {
		t.Fatalf("proxy status = %d body=%s", rec.Code, rec.Body.String())
	}

	drained1 := d.endCanonicalActionTurn("agent-1", turn1)
	if len(drained1.Associations) != 1 || drained1.Associations[0].ToolCallID != "tool-1" || drained1.Associations[0].TurnToken != turn1 {
		t.Fatalf("turn1 associations = %+v, want ingress tool-1 context", drained1.Associations)
	}
	drained2 := d.endCanonicalActionTurn("agent-1", turn2)
	if len(drained2.Associations) != 0 {
		t.Fatalf("response was rebound to replacement context: %+v", drained2.Associations)
	}
}

func TestCanonicalActionTurnsDrainOnlyMatchingToken(t *testing.T) {
	d := &Daemon{}
	turn1 := d.beginCanonicalActionTurn("agent-1")
	context1 := ActiveProviderToolContext{AgentID: "agent-1", CallID: "tool-1", ToolCallID: "tool-1", TurnToken: turn1}
	d.SetActiveProviderToolContext(context1)
	d.observeCanonicalActionOutcome(context1, "message", "70000000-0000-4000-8000-000000000374", true)

	turn2 := d.beginCanonicalActionTurn("agent-1")
	context2 := ActiveProviderToolContext{AgentID: "agent-1", CallID: "tool-2", ToolCallID: "tool-2", TurnToken: turn2}
	d.SetActiveProviderToolContext(context2)
	d.observeCanonicalActionOutcome(context2, "reaction", "70000000-0000-4000-8000-000000000375", true)

	drained1 := d.endCanonicalActionTurn("agent-1", turn1)
	if len(drained1.Associations) != 1 || drained1.Associations[0].TurnToken != turn1 {
		t.Fatalf("turn1 drain = %+v", drained1.Associations)
	}
	active2, ok := d.activeProviderToolContextSnapshot("agent-1")
	if !ok || active2.TurnToken != turn2 || active2.ToolCallID != "tool-2" {
		t.Fatalf("turn1 drain cleared turn2 active context: %+v, ok=%v", active2, ok)
	}
	d.observeCanonicalActionOutcome(active2, "message", "70000000-0000-4000-8000-000000000376", true)
	drained2 := d.endCanonicalActionTurn("agent-1", turn2)
	if len(drained2.Associations) != 2 {
		t.Fatalf("turn2 drain = %+v, want both turn2 records", drained2.Associations)
	}
	for _, association := range drained2.Associations {
		if association.TurnToken != turn2 {
			t.Fatalf("turn2 stole foreign association: %+v", drained2.Associations)
		}
	}
}

func newCredentialProxyTestDaemon(t *testing.T, status int, responseBody string) *Daemon {
	t.Helper()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
		_, _ = io.WriteString(w, responseBody)
	}))
	t.Cleanup(upstream.Close)

	cfg := Config{WorkspacesRoot: t.TempDir(), ServerBaseURL: upstream.URL}
	expiresAt := time.Now().Add(2 * time.Hour).UTC().Format(time.RFC3339Nano)
	if _, err := writeCachedAgentCredential(cfg, "workspace-1", "runtime-1", "agent-1", AgentCredentialResponse{
		ID: "credential-1", AgentID: "agent-1", Prefix: "mac_test", Token: "cached-token", ExpiresAt: &expiresAt,
	}, time.Now()); err != nil {
		t.Fatalf("write cached credential: %v", err)
	}
	return &Daemon{cfg: cfg}
}

func serveCredentialProxyTestRequest(t *testing.T, d *Daemon, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("X-Agent-ID", "agent-1")
	req.Header.Set("X-Workspace-ID", "workspace-1")
	rec := httptest.NewRecorder()
	d.credentialProxyAgentAPIHandler().ServeHTTP(rec, req)
	return rec
}
