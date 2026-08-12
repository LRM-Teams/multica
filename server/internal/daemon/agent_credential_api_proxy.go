package daemon

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/internal/cli"
)

const canonicalActionProxyResponseCaptureLimit = 64 * 1024
const canonicalActionAssociationLimitPerTurn = 1024

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
	SucceededAt    time.Time
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

// beginCanonicalActionTurn atomically advances the agent's turn-scoped
// provenance boundary. Any records left by an interrupted prior turn are
// discarded before the next provider input can run.
func (d *Daemon) beginCanonicalActionTurn(agentID string) {
	state := provenanceStateFor(d)
	state.mu.Lock()
	defer state.mu.Unlock()
	agentID = strings.TrimSpace(agentID)
	delete(state.active, agentID)
	delete(state.associations, agentID)
}

// clearActiveProviderToolContext clears only the matching tool. A late result
// for an older tool must not clear a newer active tool context.
func (d *Daemon) clearActiveProviderToolContext(agentID, toolCallID string) {
	state := provenanceStateFor(d)
	state.mu.Lock()
	defer state.mu.Unlock()
	agentID = strings.TrimSpace(agentID)
	toolCallID = strings.TrimSpace(toolCallID)
	if active, ok := state.active[agentID]; ok && active.ToolCallID == toolCallID {
		delete(state.active, agentID)
	}
}

// endCanonicalActionTurn atomically clears active context and drains the
// current turn's associations so they can be uploaded at most once.
func (d *Daemon) endCanonicalActionTurn(agentID string) []CanonicalActionAssociation {
	state := provenanceStateFor(d)
	state.mu.Lock()
	defer state.mu.Unlock()
	agentID = strings.TrimSpace(agentID)
	delete(state.active, agentID)
	src := state.associations[agentID]
	delete(state.associations, agentID)
	out := make([]CanonicalActionAssociation, len(src))
	copy(out, src)
	return out
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
	parsed, err := uuid.Parse(canonicalID)
	if err != nil || canonicalID != parsed.String() {
		return
	}
	state := provenanceStateFor(d)
	state.mu.Lock()
	defer state.mu.Unlock()
	active, ok := state.active[agentID]
	if !ok || strings.TrimSpace(active.ToolCallID) == "" {
		return
	}
	for _, existing := range state.associations[agentID] {
		if existing.Kind == kind && existing.CanonicalID == canonicalID {
			return
		}
	}
	if len(state.associations[agentID]) >= canonicalActionAssociationLimitPerTurn {
		return
	}
	state.associations[agentID] = append(state.associations[agentID], CanonicalActionAssociation{
		Kind:           kind,
		CanonicalID:    canonicalID,
		ProducerCallID: active.CallID,
		ToolCallID:     active.ToolCallID,
		SucceededAt:    time.Now().UTC(),
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
		captured := &boundedResponseCapture{limit: canonicalActionProxyResponseCaptureLimit}
		if _, err := io.Copy(io.MultiWriter(w, captured), response.Body); err != nil && d.logger != nil {
			d.logger.Warn("write agent credential proxy response", "error", err, "path", r.URL.Path)
		}
		kind, canonicalID, ok := canonicalActionFromProxyResponse(
			r.Method, r.URL.Path, response.StatusCode, captured.Bytes(),
		)
		if ok && !captured.Overflowed() {
			d.observeCanonicalActionOutcome(agentID, kind, canonicalID, true)
		}
	}
}

type boundedResponseCapture struct {
	buffer     bytes.Buffer
	limit      int
	overflowed bool
}

func (c *boundedResponseCapture) Write(p []byte) (int, error) {
	written := len(p)
	remaining := c.limit - c.buffer.Len()
	if remaining <= 0 {
		c.overflowed = c.overflowed || written > 0
		return written, nil
	}
	if len(p) > remaining {
		p = p[:remaining]
		c.overflowed = true
	}
	_, _ = c.buffer.Write(p)
	return written, nil
}

func (c *boundedResponseCapture) Bytes() []byte {
	return c.buffer.Bytes()
}

func (c *boundedResponseCapture) Overflowed() bool {
	return c.overflowed
}

func canonicalActionFromProxyResponse(method, path string, status int, body []byte) (kind, canonicalID string, ok bool) {
	if method != http.MethodPost || status < http.StatusOK || status >= http.StatusMultipleChoices {
		return "", "", false
	}

	switch path {
	case "/api/agent/messages/send":
		var response struct {
			Created bool `json:"created"`
			Message struct {
				ID string `json:"id"`
			} `json:"message"`
		}
		if err := json.Unmarshal(body, &response); err != nil || !response.Created {
			return "", "", false
		}
		kind, canonicalID = "message", response.Message.ID
	case "/api/agent/messages/react":
		var response struct {
			Added    bool `json:"added"`
			Reaction struct {
				ID string `json:"id"`
			} `json:"reaction"`
		}
		if err := json.Unmarshal(body, &response); err != nil || !response.Added {
			return "", "", false
		}
		kind, canonicalID = "reaction", response.Reaction.ID
	default:
		return "", "", false
	}

	canonicalID = strings.TrimSpace(canonicalID)
	parsed, err := uuid.Parse(canonicalID)
	if err != nil || canonicalID != parsed.String() {
		return "", "", false
	}
	return kind, canonicalID, true
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
