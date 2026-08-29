package daemon

import (
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
	TurnToken  canonicalActionTurnToken
}

type canonicalActionTurnToken uint64

// CanonicalActionAssociation records a successful canonical message or
// reaction observed by the credential proxy while a provider/tool context was
// active for the same agent.
type CanonicalActionAssociation struct {
	Kind           string // "message" or "reaction"
	CanonicalID    string
	ProducerCallID string
	ToolCallID     string
	TurnToken      canonicalActionTurnToken
	SucceededAt    time.Time
}

type canonicalActionTurnState struct {
	agentID      string
	associations []CanonicalActionAssociation
	overflow     bool
	ambiguous    bool
}

type canonicalActionTurnDrain struct {
	Associations []CanonicalActionAssociation
	Overflow     bool
	Ambiguous    bool
}

type credentialProxyProvenanceState struct {
	mu        sync.Mutex
	nextToken canonicalActionTurnToken
	active    map[string]ActiveProviderToolContext
	turns     map[canonicalActionTurnToken]*canonicalActionTurnState
}

var credentialProxyProvenanceByDaemon sync.Map // *Daemon -> *credentialProxyProvenanceState

func provenanceStateFor(d *Daemon) *credentialProxyProvenanceState {
	if d == nil {
		return &credentialProxyProvenanceState{
			active: make(map[string]ActiveProviderToolContext),
			turns:  make(map[canonicalActionTurnToken]*canonicalActionTurnState),
		}
	}
	if existing, ok := credentialProxyProvenanceByDaemon.Load(d); ok {
		return existing.(*credentialProxyProvenanceState)
	}
	created := &credentialProxyProvenanceState{
		active: make(map[string]ActiveProviderToolContext),
		turns:  make(map[canonicalActionTurnToken]*canonicalActionTurnState),
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
	turn, ok := state.turns[ctx.TurnToken]
	if !ok || turn.agentID != agentID || turn.ambiguous {
		return
	}
	if strings.TrimSpace(ctx.CallID) == "" {
		if active, exists := state.active[agentID]; exists && active.TurnToken == ctx.TurnToken {
			delete(state.active, agentID)
		}
		return
	}
	ctx.AgentID = agentID
	ctx.CallID = strings.TrimSpace(ctx.CallID)
	ctx.ToolCallID = strings.TrimSpace(ctx.ToolCallID)
	if active, exists := state.active[agentID]; exists &&
		(active.TurnToken != ctx.TurnToken || active.ToolCallID != ctx.ToolCallID) {
		turn.ambiguous = true
		if activeTurn, found := state.turns[active.TurnToken]; found && activeTurn.agentID == agentID {
			activeTurn.ambiguous = true
		}
		delete(state.active, agentID)
		return
	}
	state.active[agentID] = ctx
}

// beginCanonicalActionTurn creates an isolated provenance boundary. Existing
// turns for the same agent remain intact until their matching token drains.
func (d *Daemon) beginCanonicalActionTurn(agentID string) canonicalActionTurnToken {
	token := d.allocateCanonicalActionTurnToken()
	d.activateCanonicalActionTurn(agentID, token)
	return token
}

func (d *Daemon) allocateCanonicalActionTurnToken() canonicalActionTurnToken {
	state := provenanceStateFor(d)
	state.mu.Lock()
	defer state.mu.Unlock()
	state.nextToken++
	if state.nextToken == 0 {
		state.nextToken++
	}
	return state.nextToken
}

func (d *Daemon) activateCanonicalActionTurn(agentID string, token canonicalActionTurnToken) {
	state := provenanceStateFor(d)
	state.mu.Lock()
	defer state.mu.Unlock()
	agentID = strings.TrimSpace(agentID)
	if agentID == "" || token == 0 {
		return
	}
	turn := &canonicalActionTurnState{agentID: agentID}
	for _, live := range state.turns {
		if live.agentID == agentID {
			live.ambiguous = true
			turn.ambiguous = true
		}
	}
	state.turns[token] = turn
	if turn.ambiguous {
		delete(state.active, agentID)
	}
}

// clearActiveProviderToolContext clears only the matching tool. A late result
// for an older tool must not clear a newer active tool context.
func (d *Daemon) clearActiveProviderToolContext(agentID string, turnToken canonicalActionTurnToken, toolCallID string) {
	state := provenanceStateFor(d)
	state.mu.Lock()
	defer state.mu.Unlock()
	agentID = strings.TrimSpace(agentID)
	toolCallID = strings.TrimSpace(toolCallID)
	if active, ok := state.active[agentID]; ok && active.TurnToken == turnToken && active.ToolCallID == toolCallID {
		delete(state.active, agentID)
	}
}

// endCanonicalActionTurn atomically clears active context and drains the
// current turn's associations so they can be uploaded at most once.
func (d *Daemon) endCanonicalActionTurn(agentID string, turnToken canonicalActionTurnToken) canonicalActionTurnDrain {
	state := provenanceStateFor(d)
	state.mu.Lock()
	defer state.mu.Unlock()
	agentID = strings.TrimSpace(agentID)
	if active, ok := state.active[agentID]; ok && active.TurnToken == turnToken {
		delete(state.active, agentID)
	}
	turn, ok := state.turns[turnToken]
	if !ok || turn.agentID != agentID {
		return canonicalActionTurnDrain{}
	}
	delete(state.turns, turnToken)
	out := make([]CanonicalActionAssociation, len(turn.associations))
	copy(out, turn.associations)
	return canonicalActionTurnDrain{Associations: out, Overflow: turn.overflow, Ambiguous: turn.ambiguous}
}

func (d *Daemon) activeProviderToolContextSnapshot(agentID string) (ActiveProviderToolContext, bool) {
	state := provenanceStateFor(d)
	state.mu.Lock()
	defer state.mu.Unlock()
	agentID = strings.TrimSpace(agentID)
	active, ok := state.active[agentID]
	if !ok || active.TurnToken == 0 || strings.TrimSpace(active.ToolCallID) == "" {
		return ActiveProviderToolContext{}, false
	}
	turn, exists := state.turns[active.TurnToken]
	if !exists || turn.agentID != agentID || turn.ambiguous {
		return ActiveProviderToolContext{}, false
	}
	return active, true
}

// ObservedCanonicalActionAssociations returns trusted associations recorded for
// the agent. The slice is a copy and may be empty.
func (d *Daemon) ObservedCanonicalActionAssociations(agentID string) []CanonicalActionAssociation {
	state := provenanceStateFor(d)
	state.mu.Lock()
	defer state.mu.Unlock()
	agentID = strings.TrimSpace(agentID)
	var out []CanonicalActionAssociation
	for _, turn := range state.turns {
		if turn.agentID == agentID {
			out = append(out, turn.associations...)
		}
	}
	return out
}

// observeCanonicalActionOutcome associates a canonical send/reaction ID with
// the active provider/tool context only when the upstream canonical operation
// succeeded. Agent-declared provenance is never consulted.
func (d *Daemon) observeCanonicalActionOutcome(providerContext ActiveProviderToolContext, kind, canonicalID string, succeeded bool) {
	if !succeeded {
		return
	}
	agentID := strings.TrimSpace(providerContext.AgentID)
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
	turn, ok := state.turns[providerContext.TurnToken]
	if !ok || turn.agentID != agentID || turn.ambiguous || strings.TrimSpace(providerContext.ToolCallID) == "" {
		return
	}
	for _, existing := range turn.associations {
		if existing.Kind == kind && existing.CanonicalID == canonicalID {
			return
		}
	}
	if len(turn.associations) >= canonicalActionAssociationLimitPerTurn {
		turn.overflow = true
		return
	}
	turn.associations = append(turn.associations, CanonicalActionAssociation{
		Kind:           kind,
		CanonicalID:    canonicalID,
		ProducerCallID: providerContext.CallID,
		ToolCallID:     providerContext.ToolCallID,
		TurnToken:      providerContext.TurnToken,
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
		providerContext, hasProviderContext := d.activeProviderToolContextSnapshot(agentID)
		credential, ok := agentProxyServerCredential(r)
		if !ok && !agentProxyRequestAuthenticated(r) {
			credential, ok = d.activeAgentProxyServerCredential(workspaceID, "", agentID)
		}
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
		if isCanonicalActionProxyRequest(r.Method, r.URL.Path) {
			prefix, readErr := io.ReadAll(io.LimitReader(response.Body, canonicalActionProxyResponseCaptureLimit+1))
			overflow := len(prefix) > canonicalActionProxyResponseCaptureLimit
			if readErr == nil && !overflow && hasProviderContext {
				kind, canonicalID, canonical := canonicalActionFromProxyResponse(
					r.Method, r.URL.Path, response.StatusCode, prefix,
				)
				if canonical {
					d.observeCanonicalActionOutcome(providerContext, kind, canonicalID, true)
				}
			}
			w.WriteHeader(response.StatusCode)
			_, writeErr := w.Write(prefix)
			if writeErr == nil && overflow {
				_, writeErr = io.Copy(w, response.Body)
			}
			if err := firstProxyResponseError(readErr, writeErr); err != nil && d.logger != nil {
				d.logger.Warn("write agent credential proxy response", "error", err, "path", r.URL.Path)
			}
			return
		}
		w.WriteHeader(response.StatusCode)
		if _, err := io.Copy(w, response.Body); err != nil && d.logger != nil {
			d.logger.Warn("write agent credential proxy response", "error", err, "path", r.URL.Path)
		}
	}
}

func isCanonicalActionProxyRequest(method, path string) bool {
	return method == http.MethodPost && (path == "/api/agent/messages/send" || path == "/api/agent/messages/react")
}

func firstProxyResponseError(errors ...error) error {
	for _, err := range errors {
		if err != nil {
			return err
		}
	}
	return nil
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
