package daemon

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/multica-ai/multica/server/internal/cli"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

type credentialProxyMessageCheckRequest struct {
	AgentID string `json:"agent_id"`
}

type credentialProxyMessageReadRequest struct {
	AgentID string `json:"agent_id"`

	WorkspaceID string `json:"workspace_id"`
	Target      string `json:"target"`
	BeforeID    string `json:"before_id,omitempty"`
	BeforeSeq   int64  `json:"before_seq,omitempty"`
	AfterID     string `json:"after_id,omitempty"`
	AfterSeq    int64  `json:"after_seq,omitempty"`
	AroundID    string `json:"around_id,omitempty"`
	AroundSeq   int64  `json:"around_seq,omitempty"`
	Limit       int    `json:"limit"`
}

type credentialProxyMessageSearchRequest struct {
	AgentID     string `json:"agent_id"`
	WorkspaceID string `json:"workspace_id"`
	Query       string `json:"query,omitempty"`
	Target      string `json:"target,omitempty"`
	Sender      string `json:"sender,omitempty"`
	Sort        string `json:"sort,omitempty"`
	Before      string `json:"before,omitempty"`
	After       string `json:"after,omitempty"`
	Limit       int    `json:"limit"`
	Offset      int    `json:"offset"`
}

type credentialProxyMessageResolveRequest struct {
	AgentID     string `json:"agent_id"`
	WorkspaceID string `json:"workspace_id"`
	MessageID   string `json:"message_id"`
}

type credentialProxyMessageReactRequest struct {
	AgentID     string `json:"agent_id"`
	WorkspaceID string `json:"workspace_id"`
	MessageID   string `json:"message_id"`
	Emoji       string `json:"emoji"`
	Remove      bool   `json:"remove"`
}

func (d *Daemon) credentialProxyMessageCheckHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var request credentialProxyMessageCheckRequest
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&request); err != nil {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		if err := decoder.Decode(&struct{}{}); err != io.EOF {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		request.AgentID = strings.TrimSpace(request.AgentID)
		if request.AgentID == "" {
			http.Error(w, "agent_id is required", http.StatusBadRequest)
			return
		}
		if !credentialProxyIdentityMatches(r, request.AgentID, "") {
			http.Error(w, "Agent Proxy credential scope mismatch", http.StatusForbidden)
			return
		}
		result, err := d.CredentialProxy().CheckMessages(request.AgentID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(result); err != nil && d.logger != nil {
			d.logger.Warn("write Credential Proxy message check response", "error", err)
		}
	}
}

func (d *Daemon) credentialProxyMessageReadHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var request credentialProxyMessageReadRequest
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&request); err != nil {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		if err := decoder.Decode(&struct{}{}); err != io.EOF {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		request.AgentID = strings.TrimSpace(request.AgentID)
		request.WorkspaceID = strings.TrimSpace(request.WorkspaceID)
		request.Target = strings.TrimSpace(request.Target)
		if request.AgentID == "" || request.WorkspaceID == "" || request.Target == "" {
			http.Error(w, "agent_id, workspace_id, and target are required", http.StatusBadRequest)
			return
		}
		if !credentialProxyIdentityMatches(r, request.AgentID, request.WorkspaceID) {
			http.Error(w, "Agent Proxy credential scope mismatch", http.StatusForbidden)
			return
		}
		credential, ok := agentProxyServerCredential(r)
		if !ok && !agentProxyRequestAuthenticated(r) {
			var err error
			credential, err = d.messageAgentCredential(r.Context(), request.WorkspaceID, request.AgentID)
			if err != nil {
				http.Error(w, err.Error(), http.StatusConflict)
				return
			}
		}
		if strings.TrimSpace(credential.Token) == "" {
			http.Error(w, "Agent credential is unavailable", http.StatusConflict)
			return
		}

		client := cli.NewAPIClient(d.cfg.ServerBaseURL, request.WorkspaceID, credential.Token)
		client.AgentID = request.AgentID
		upstreamRequest := map[string]any{
			"target": request.Target,
			"limit":  request.Limit,
		}
		if request.BeforeID != "" {
			upstreamRequest["before_id"] = request.BeforeID
		}
		if request.BeforeSeq != 0 {
			upstreamRequest["before_seq"] = request.BeforeSeq
		}
		if request.AfterID != "" {
			upstreamRequest["after_id"] = request.AfterID
		}
		if request.AfterSeq != 0 {
			upstreamRequest["after_seq"] = request.AfterSeq
		}
		if request.AroundID != "" {
			upstreamRequest["around_id"] = request.AroundID
		}
		if request.AroundSeq != 0 {
			upstreamRequest["around_seq"] = request.AroundSeq
		}
		ctx, cancel := cli.APIContext(r.Context())
		defer cancel()
		var response map[string]any
		if err := client.PostJSON(ctx, "/api/agent/messages/read", upstreamRequest, &response); err != nil {
			http.Error(w, "read messages through Credential Proxy: "+err.Error(), http.StatusBadGateway)
			return
		}
		contextTarget, _ := response["context_target"].(string)
		contextTarget = strings.TrimSpace(contextTarget)
		seenUpToSeq, _ := response["seenUpToSeq"].(float64)
		if contextTarget == "" || seenUpToSeq < 0 || seenUpToSeq != float64(int64(seenUpToSeq)) {
			http.Error(w, "invalid read response from server", http.StatusBadGateway)
			return
		}
		var messages []protocol.AgentMessageProjection
		if rawMessages, marshalErr := json.Marshal(response["messages"]); marshalErr != nil || json.Unmarshal(rawMessages, &messages) != nil {
			http.Error(w, "invalid read response from server", http.StatusBadGateway)
			return
		}
		offer, err := d.CredentialProxy().PrepareMessageRead(request.AgentID, contextTarget, int64(seenUpToSeq), messages)
		if err != nil {
			http.Error(w, "invalid read coverage from server", http.StatusBadGateway)
			return
		}
		delete(response, MessageCoverageReceiptField)
		if offer.ReceiptID != "" {
			response[MessageCoverageReceiptField] = offer.ReceiptID
		}
		// Context target and sequence are proxy-only facts. A Message command
		// never exposes a cursor-like read state to the Agent process.
		delete(response, "context_target")
		delete(response, "seenUpToSeq")
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(response); err != nil && d.logger != nil {
			d.logger.Warn("write Credential Proxy message read response", "error", err)
		}
	}
}

// credentialProxyAgentMessageClient resolves the durable credential locally.
// The Agent process never receives it, nor any task/lease/execution envelope.
func (d *Daemon) credentialProxyAgentMessageClient(r *http.Request, workspaceID, agentID string) (*cli.APIClient, error) {
	credential, ok := agentProxyServerCredential(r)
	if !ok && !agentProxyRequestAuthenticated(r) {
		var err error
		credential, err = d.messageAgentCredential(r.Context(), workspaceID, agentID)
		if err != nil {
			return nil, err
		}
	}
	if strings.TrimSpace(credential.Token) == "" {
		return nil, errors.New("Agent credential is unavailable")
	}
	client := cli.NewAPIClient(d.cfg.ServerBaseURL, workspaceID, credential.Token)
	client.AgentID = agentID
	return client, nil
}

func decodeCredentialProxyRequest(w http.ResponseWriter, r *http.Request, request any) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(request); err != nil || decoder.Decode(&struct{}{}) != io.EOF {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return false
	}
	return true
}

func normalizeCredentialProxyIdentity(agentID, workspaceID *string) bool {
	*agentID = strings.TrimSpace(*agentID)
	*workspaceID = strings.TrimSpace(*workspaceID)
	return *agentID != "" && *workspaceID != ""
}

func credentialProxyIdentityMatches(r *http.Request, agentID, workspaceID string) bool {
	if _, authenticated := r.Context().Value(agentProxyAuthContextKey{}).(authenticatedAgentProxy); !authenticated {
		return true
	}
	return strings.TrimSpace(agentID) == r.Header.Get("X-Agent-ID") &&
		(strings.TrimSpace(workspaceID) == "" || strings.TrimSpace(workspaceID) == r.Header.Get("X-Workspace-ID"))
}

func (d *Daemon) credentialProxyMessageSearchHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var request credentialProxyMessageSearchRequest
		if !decodeCredentialProxyRequest(w, r, &request) || !normalizeCredentialProxyIdentity(&request.AgentID, &request.WorkspaceID) {
			if request.AgentID == "" || request.WorkspaceID == "" {
				http.Error(w, "agent_id and workspace_id are required", http.StatusBadRequest)
			}
			return
		}
		if !credentialProxyIdentityMatches(r, request.AgentID, request.WorkspaceID) {
			http.Error(w, "Agent Proxy credential scope mismatch", http.StatusForbidden)
			return
		}
		client, err := d.credentialProxyAgentMessageClient(r, request.WorkspaceID, request.AgentID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		body := map[string]any{"query": request.Query, "limit": request.Limit, "offset": request.Offset}
		for key, value := range map[string]string{"target": request.Target, "sender": request.Sender, "sort": request.Sort, "before": request.Before, "after": request.After} {
			if value = strings.TrimSpace(value); value != "" {
				body[key] = value
			}
		}
		ctx, cancel := cli.APIContext(r.Context())
		defer cancel()
		var response map[string]any
		if err := client.PostJSON(ctx, "/api/agent/messages/search", body, &response); err != nil {
			http.Error(w, "search messages through Credential Proxy: "+err.Error(), http.StatusBadGateway)
			return
		}
		sanitizeCredentialProxyMessageSendResponse(response)
		writeCredentialProxyMessageJSON(w, response)
	}
}

func (d *Daemon) credentialProxyMessageResolveHandler() http.HandlerFunc {
	return d.credentialProxyMessageMutationHandler("/api/agent/messages/resolve", func() credentialProxyMessageMutationRequest {
		return &credentialProxyMessageResolveRequest{}
	})
}

func (d *Daemon) credentialProxyMessageReactHandler() http.HandlerFunc {
	return d.credentialProxyMessageMutationHandler("/api/agent/messages/react", func() credentialProxyMessageMutationRequest {
		return &credentialProxyMessageReactRequest{}
	})
}

type credentialProxyMessageMutationRequest interface {
	identity() (agentID, workspaceID string)
	upstreamBody() (map[string]any, error)
}

func (request *credentialProxyMessageResolveRequest) identity() (string, string) {
	return request.AgentID, request.WorkspaceID
}

func (request *credentialProxyMessageResolveRequest) upstreamBody() (map[string]any, error) {
	messageID := strings.TrimSpace(request.MessageID)
	if messageID == "" {
		return nil, fmt.Errorf("message identity is required")
	}
	return map[string]any{"message_id": messageID}, nil
}

func (request *credentialProxyMessageReactRequest) identity() (string, string) {
	return request.AgentID, request.WorkspaceID
}

func (request *credentialProxyMessageReactRequest) upstreamBody() (map[string]any, error) {
	messageID := strings.TrimSpace(request.MessageID)
	emoji := strings.TrimSpace(request.Emoji)
	if messageID == "" || emoji == "" {
		return nil, fmt.Errorf("message identity is required")
	}
	return map[string]any{"message_id": messageID, "emoji": emoji, "remove": request.Remove}, nil
}

func (d *Daemon) credentialProxyMessageMutationHandler(path string, newRequest func() credentialProxyMessageMutationRequest) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if newRequest == nil {
			http.Error(w, "Credential Proxy misconfigured", http.StatusInternalServerError)
			return
		}
		request := newRequest()
		if request == nil {
			http.Error(w, "Credential Proxy misconfigured", http.StatusInternalServerError)
			return
		}
		if !decodeCredentialProxyRequest(w, r, request) {
			return
		}
		agentID, workspaceID := request.identity()
		if !normalizeCredentialProxyIdentity(&agentID, &workspaceID) {
			http.Error(w, "agent_id, workspace_id, and message identity are required", http.StatusBadRequest)
			return
		}
		if !credentialProxyIdentityMatches(r, agentID, workspaceID) {
			http.Error(w, "Agent Proxy credential scope mismatch", http.StatusForbidden)
			return
		}
		body, err := request.upstreamBody()
		if err != nil {
			http.Error(w, "agent_id, workspace_id, and message identity are required", http.StatusBadRequest)
			return
		}
		client, err := d.credentialProxyAgentMessageClient(r, workspaceID, agentID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		ctx, cancel := cli.APIContext(r.Context())
		defer cancel()
		var response map[string]any
		if err := client.PostJSON(ctx, path, body, &response); err != nil {
			http.Error(w, "message action through Credential Proxy: "+err.Error(), http.StatusBadGateway)
			return
		}
		sanitizeCredentialProxyMessageSendResponse(response)
		writeCredentialProxyMessageJSON(w, response)
	}
}

// registerCredentialProxyMessageRoutes wires only message and coverage
// endpoints; local listener assembly belongs to local_control.go.
func (d *Daemon) registerCredentialProxyMessageRoutes(mux *http.ServeMux) {
	mux.Handle("POST /credential-proxy/messages/check", d.authenticateAgentProxyRequest(d.credentialProxyMessageCheckHandler()))
	mux.Handle("POST /credential-proxy/messages/read", d.authenticateAgentProxyRequest(d.credentialProxyMessageReadHandler()))
	mux.Handle("POST /credential-proxy/messages/send", d.authenticateAgentProxyRequest(d.credentialProxyMessageSendHandler()))
	mux.Handle("POST /credential-proxy/messages/search", d.authenticateAgentProxyRequest(d.credentialProxyMessageSearchHandler()))
	mux.Handle("POST /credential-proxy/messages/resolve", d.authenticateAgentProxyRequest(d.credentialProxyMessageResolveHandler()))
	mux.Handle("POST /credential-proxy/messages/react", d.authenticateAgentProxyRequest(d.credentialProxyMessageReactHandler()))
	mux.HandleFunc("POST "+MessageCoverageCommitPath, d.credentialProxyMessageCoverageCommitHandler())
}
