package daemon

import (
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/multica-ai/multica/server/internal/cli"
)

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
