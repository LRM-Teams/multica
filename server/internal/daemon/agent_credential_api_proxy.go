package daemon

import (
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/internal/cli"
)

// ActiveProviderToolContext is the daemon-observed provider/tool context used
// to associate trusted visible actions. It is never accepted from agent text.
type ActiveProviderToolContext struct {
	AgentID    string
	CallID     string
	ToolCallID string
}

// CanonicalActionAssociation records a successful canonical message or
// reaction observed by the credential proxy while a provider/tool context was
// active for the same agent.
type CanonicalActionAssociation struct {
	Kind           string // "message" or "reaction"
	CanonicalID    string
	ProducerCallID string
	ToolCallID     string
}

type credentialProxyProvenanceState struct {
	mu           sync.Mutex
	active       map[string]ActiveProviderToolContext
	associations map[string][]CanonicalActionAssociation
}

var credentialProxyProvenanceByDaemon sync.Map // *Daemon -> *credentialProxyProvenanceState

func provenanceStateFor(d *Daemon) *credentialProxyProvenanceState {
	if d == nil {
		return &credentialProxyProvenanceState{
			active:       make(map[string]ActiveProviderToolContext),
			associations: make(map[string][]CanonicalActionAssociation),
		}
	}
	if existing, ok := credentialProxyProvenanceByDaemon.Load(d); ok {
		return existing.(*credentialProxyProvenanceState)
	}
	created := &credentialProxyProvenanceState{
		active:       make(map[string]ActiveProviderToolContext),
		associations: make(map[string][]CanonicalActionAssociation),
	}
	actual, _ := credentialProxyProvenanceByDaemon.LoadOrStore(d, created)
	return actual.(*credentialProxyProvenanceState)
}

// SetActiveProviderToolContext records the currently observed provider/tool
// context for an agent. Empty CallID clears any prior context.
func (d *Daemon) SetActiveProviderToolContext(ctx ActiveProviderToolContext) {
	state := provenanceStateFor(d)
	state.mu.Lock()
	defer state.mu.Unlock()
	agentID := strings.TrimSpace(ctx.AgentID)
	if agentID == "" {
		return
	}
	if strings.TrimSpace(ctx.CallID) == "" {
		delete(state.active, agentID)
		return
	}
	ctx.AgentID = agentID
	ctx.CallID = strings.TrimSpace(ctx.CallID)
	ctx.ToolCallID = strings.TrimSpace(ctx.ToolCallID)
	state.active[agentID] = ctx
}

// ObservedCanonicalActionAssociations returns trusted associations recorded for
// the agent. The slice is a copy and may be empty.
func (d *Daemon) ObservedCanonicalActionAssociations(agentID string) []CanonicalActionAssociation {
	state := provenanceStateFor(d)
	state.mu.Lock()
	defer state.mu.Unlock()
	src := state.associations[strings.TrimSpace(agentID)]
	out := make([]CanonicalActionAssociation, len(src))
	copy(out, src)
	return out
}

// observeCanonicalActionOutcome associates a canonical send/reaction ID with
// the active provider/tool context only when the upstream canonical operation
// succeeded. Agent-declared provenance is never consulted.
func (d *Daemon) observeCanonicalActionOutcome(agentID, kind, canonicalID string, succeeded bool) {
	if !succeeded {
		return
	}
	agentID = strings.TrimSpace(agentID)
	kind = strings.TrimSpace(kind)
	canonicalID = strings.TrimSpace(canonicalID)
	if agentID == "" || (kind != "message" && kind != "reaction") {
		return
	}
	if _, err := uuid.Parse(canonicalID); err != nil {
		return
	}
	state := provenanceStateFor(d)
	state.mu.Lock()
	defer state.mu.Unlock()
	active, ok := state.active[agentID]
	if !ok || strings.TrimSpace(active.CallID) == "" || strings.TrimSpace(active.ToolCallID) == "" {
		return
	}
	for _, existing := range state.associations[agentID] {
		if existing.Kind == kind && existing.CanonicalID == canonicalID {
			return
		}
	}
	state.associations[agentID] = append(state.associations[agentID], CanonicalActionAssociation{
		Kind:           kind,
		CanonicalID:    canonicalID,
		ProducerCallID: active.CallID,
		ToolCallID:     active.ToolCallID,
	})
}

// credentialProxyAgentAPIHandler is the machine-local credential boundary for
// every agent CLI API request. It forwards the exact API method, path, query,
// body, and response while supplying the daemon-owned durable credential.
// Agent code therefore never receives a bearer token and cannot restore the
// retired task, inbox-delivery, or lease authorization path.
func (d *Daemon) credentialProxyAgentAPIHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/api/") {
			http.NotFound(w, r)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodPost && r.Method != http.MethodPut && r.Method != http.MethodPatch && r.Method != http.MethodDelete {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		for _, header := range []string{
			"X-Task-ID",
			"X-Agent-Inbox-Event-ID",
			"X-Agent-Inbox-Delivery-ID",
			"X-Agent-Inbox-Lease-Token",
		} {
			if strings.TrimSpace(r.Header.Get(header)) != "" {
				http.Error(w, "agent credential proxy does not accept task or inbox delivery context", http.StatusBadRequest)
				return
			}
		}

		agentID := strings.TrimSpace(r.Header.Get("X-Agent-ID"))
		workspaceID := strings.TrimSpace(r.Header.Get("X-Workspace-ID"))
		if agentID == "" || workspaceID == "" {
			http.Error(w, "agent_id and workspace_id are required", http.StatusBadRequest)
			return
		}
		credential, ok := readCachedAgentCredentialForMessage(d.cfg, workspaceID, agentID, time.Now())
		if !ok {
			http.Error(w, "agent credential is unavailable", http.StatusConflict)
			return
		}

		upstreamURL := strings.TrimRight(d.cfg.ServerBaseURL, "/") + r.URL.RequestURI()
		ctx, cancel := cli.APIContext(r.Context())
		defer cancel()
		upstreamRequest, err := http.NewRequestWithContext(ctx, r.Method, upstreamURL, r.Body)
		if err != nil {
			http.Error(w, "prepare agent credential request", http.StatusBadRequest)
			return
		}
		copyCredentialProxyRequestHeaders(upstreamRequest.Header, r.Header)
		upstreamRequest.Header.Set("Authorization", "Bearer "+credential.Token)
		upstreamRequest.Header.Set("X-Agent-ID", agentID)
		upstreamRequest.Header.Set("X-Workspace-ID", workspaceID)

		response, err := (&http.Client{Timeout: cli.APITimeout()}).Do(upstreamRequest)
		if err != nil {
			http.Error(w, "agent credential proxy request: "+err.Error(), http.StatusBadGateway)
			return
		}
		defer response.Body.Close()
		copyCredentialProxyResponseHeaders(w.Header(), response.Header)
		w.WriteHeader(response.StatusCode)
		if _, err := io.Copy(w, response.Body); err != nil && d.logger != nil {
			d.logger.Warn("write agent credential proxy response", "error", err, "path", r.URL.Path)
		}
	}
}

func copyCredentialProxyRequestHeaders(dst, src http.Header) {
	for name, values := range src {
		if credentialProxyRequestHeaderForbidden(name) {
			continue
		}
		for _, value := range values {
			dst.Add(name, value)
		}
	}
}

func credentialProxyRequestHeaderForbidden(name string) bool {
	switch strings.ToLower(name) {
	case "authorization", "cookie", "connection", "keep-alive", "proxy-authenticate", "proxy-authorization", "te", "trailer", "transfer-encoding", "upgrade",
		"x-agent-id", "x-workspace-id", "x-task-id", "x-agent-inbox-event-id", "x-agent-inbox-delivery-id", "x-agent-inbox-lease-token",
		"x-agent-credential-id", "x-actor-source", "x-user-id", "x-user-email", "x-multica-control-token":
		return true
	default:
		return false
	}
}

func copyCredentialProxyResponseHeaders(dst, src http.Header) {
	for name, values := range src {
		if strings.EqualFold(name, "Set-Cookie") || strings.EqualFold(name, "Connection") || strings.EqualFold(name, "Transfer-Encoding") {
			continue
		}
		for _, value := range values {
			dst.Add(name, value)
		}
	}
}
