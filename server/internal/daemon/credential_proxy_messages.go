package daemon

import (
	"context"
	"encoding/json"
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
		credential, err := d.messageAgentCredential(r.Context(), request.WorkspaceID, request.AgentID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusConflict)
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
func (d *Daemon) credentialProxyAgentMessageClient(ctx context.Context, workspaceID, agentID string) (*cli.APIClient, error) {
	credential, err := d.messageAgentCredential(ctx, workspaceID, agentID)
	if err != nil {
		return nil, err
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

func (d *Daemon) credentialProxyMessageSearchHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var request credentialProxyMessageSearchRequest
		if !decodeCredentialProxyRequest(w, r, &request) || !normalizeCredentialProxyIdentity(&request.AgentID, &request.WorkspaceID) {
			if request.AgentID == "" || request.WorkspaceID == "" {
				http.Error(w, "agent_id and workspace_id are required", http.StatusBadRequest)
			}
			return
		}
		client, err := d.credentialProxyAgentMessageClient(r.Context(), request.WorkspaceID, request.AgentID)
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
	return d.credentialProxyMessageMutationHandler("/api/agent/messages/resolve", func(request credentialProxyMessageResolveRequest) map[string]any {
		return map[string]any{"message_id": strings.TrimSpace(request.MessageID)}
	})
}

func (d *Daemon) credentialProxyMessageReactHandler() http.HandlerFunc {
	return d.credentialProxyMessageMutationHandler("/api/agent/messages/react", func(request credentialProxyMessageReactRequest) map[string]any {
		return map[string]any{"message_id": strings.TrimSpace(request.MessageID), "emoji": strings.TrimSpace(request.Emoji), "remove": request.Remove}
	})
}

func (d *Daemon) credentialProxyMessageMutationHandler(path string, bodyFor any) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var agentID, workspaceID string
		var body map[string]any
		switch makeBody := bodyFor.(type) {
		case func(credentialProxyMessageResolveRequest) map[string]any:
			var request credentialProxyMessageResolveRequest
			if !decodeCredentialProxyRequest(w, r, &request) {
				return
			}
			agentID, workspaceID = request.AgentID, request.WorkspaceID
			body = makeBody(request)
		case func(credentialProxyMessageReactRequest) map[string]any:
			var request credentialProxyMessageReactRequest
			if !decodeCredentialProxyRequest(w, r, &request) {
				return
			}
			agentID, workspaceID = request.AgentID, request.WorkspaceID
			body = makeBody(request)
		default:
			http.Error(w, "Credential Proxy misconfigured", http.StatusInternalServerError)
			return
		}
		if !normalizeCredentialProxyIdentity(&agentID, &workspaceID) || strings.TrimSpace(fmt.Sprint(body["message_id"])) == "" || (path == "/api/agent/messages/react" && strings.TrimSpace(fmt.Sprint(body["emoji"])) == "") {
			http.Error(w, "agent_id, workspace_id, and message identity are required", http.StatusBadRequest)
			return
		}
		client, err := d.credentialProxyAgentMessageClient(r.Context(), workspaceID, agentID)
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
	mux.HandleFunc("POST /credential-proxy/messages/check", d.credentialProxyMessageCheckHandler())
	mux.HandleFunc("POST /credential-proxy/messages/read", d.credentialProxyMessageReadHandler())
	mux.HandleFunc("POST /credential-proxy/messages/send", d.credentialProxyMessageSendHandler())
	mux.HandleFunc("POST /credential-proxy/messages/search", d.credentialProxyMessageSearchHandler())
	mux.HandleFunc("POST /credential-proxy/messages/resolve", d.credentialProxyMessageResolveHandler())
	mux.HandleFunc("POST /credential-proxy/messages/react", d.credentialProxyMessageReactHandler())
	mux.HandleFunc("POST "+MessageCoverageCommitPath, d.credentialProxyMessageCoverageCommitHandler())
}
