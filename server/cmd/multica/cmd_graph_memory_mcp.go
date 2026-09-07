package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/multica-ai/multica/server/internal/daemon"
)

const graphMemoryMCPProtocolVersion = "2024-11-05"

var graphMemoryMCPCmd = &cobra.Command{
	Use:          "graph-memory-mcp",
	Hidden:       true,
	SilenceUsage: true,
	Args:         cobra.NoArgs,
	RunE: func(_ *cobra.Command, _ []string) error {
		return serveGraphMemoryMCP(os.Stdin, os.Stdout)
	},
}

type graphMemoryMCPRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

type graphMemoryMCPResponse struct {
	JSONRPC string               `json:"jsonrpc"`
	ID      json.RawMessage      `json:"id,omitempty"`
	Result  any                  `json:"result,omitempty"`
	Error   *graphMemoryMCPError `json:"error,omitempty"`
}

type graphMemoryMCPError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type graphMemoryMCPBridge struct {
	proxyURL string
	token    string
	client   *http.Client
}

func serveGraphMemoryMCP(in io.Reader, out io.Writer) error {
	bridge, err := newGraphMemoryMCPBridgeFromEnv()
	if err != nil {
		return err
	}
	scanner := bufio.NewScanner(in)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	encoder := json.NewEncoder(out)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		var request graphMemoryMCPRequest
		if err := json.Unmarshal(line, &request); err != nil {
			if err := encoder.Encode(graphMemoryMCPResponse{JSONRPC: "2.0", Error: &graphMemoryMCPError{Code: -32700, Message: "parse error"}}); err != nil {
				return err
			}
			continue
		}
		response := bridge.handle(request)
		// JSON-RPC notifications have no id and must not receive a response.
		if len(bytes.TrimSpace(request.ID)) == 0 {
			continue
		}
		if err := encoder.Encode(response); err != nil {
			return err
		}
	}
	return scanner.Err()
}

func newGraphMemoryMCPBridgeFromEnv() (*graphMemoryMCPBridge, error) {
	proxyURL := strings.TrimSpace(os.Getenv(daemon.AgentProxyURLEnv))
	tokenPath := strings.TrimSpace(os.Getenv(daemon.AgentProxyTokenFileEnv))
	if proxyURL == "" || tokenPath == "" {
		return nil, fmt.Errorf("Graph Memory MCP requires the launch-scoped Agent Proxy")
	}
	parsed, err := url.Parse(proxyURL)
	if err != nil || parsed.Scheme != "http" || parsed.Host == "" || !isLoopbackHost(parsed.Hostname()) {
		return nil, fmt.Errorf("Graph Memory MCP Agent Proxy must be a loopback HTTP URL")
	}
	info, err := os.Stat(tokenPath)
	if err != nil || !info.Mode().IsRegular() {
		return nil, fmt.Errorf("Graph Memory MCP Agent Proxy token file is unavailable")
	}
	tokenBytes, err := os.ReadFile(tokenPath)
	if err != nil || strings.TrimSpace(string(tokenBytes)) == "" {
		return nil, fmt.Errorf("Graph Memory MCP Agent Proxy token is unavailable")
	}
	return &graphMemoryMCPBridge{
		proxyURL: strings.TrimRight(proxyURL, "/"), token: strings.TrimSpace(string(tokenBytes)),
		client: &http.Client{Timeout: 45 * time.Second},
	}, nil
}

func isLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func (b *graphMemoryMCPBridge) handle(request graphMemoryMCPRequest) graphMemoryMCPResponse {
	response := graphMemoryMCPResponse{JSONRPC: "2.0", ID: request.ID}
	if request.JSONRPC != "2.0" {
		response.Error = &graphMemoryMCPError{Code: -32600, Message: "invalid JSON-RPC version"}
		return response
	}
	switch request.Method {
	case "initialize":
		response.Result = map[string]any{
			"protocolVersion": graphMemoryMCPProtocolVersion,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]string{"name": "multica-graph-memory", "version": version},
		}
	case "notifications/initialized":
		response.Result = map[string]any{}
	case "ping":
		response.Result = map[string]any{}
	case "tools/list":
		response.Result = map[string]any{"tools": graphMemoryMCPTools()}
	case "tools/call":
		response.Result = b.callTool(request.Params)
	default:
		response.Error = &graphMemoryMCPError{Code: -32601, Message: "method not found"}
	}
	return response
}

func graphMemoryMCPTools() []map[string]any {
	base := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"channel_id":      map[string]string{"type": "string", "description": "Current managed channel id."},
			"idempotency_key": map[string]string{"type": "string", "description": "Stable key unique to this message and operation."},
		},
		"required":             []string{"channel_id", "idempotency_key"},
		"additionalProperties": false,
	}
	tool := func(name, description string, properties map[string]any, required []string) map[string]any {
		schema := cloneMCPObject(base)
		for key, value := range properties {
			schema["properties"].(map[string]any)[key] = value
		}
		schema["required"] = append(schema["required"].([]string), required...)
		return map[string]any{"name": name, "description": description, "inputSchema": schema}
	}
	return []map[string]any{
		tool("graph_memory_start", "Start a bounded Graph Memory run for the current channel.", map[string]any{"query": map[string]string{"type": "string"}}, []string{"query"}),
		tool("graph_memory_explore", "Inspect only node ids or MemoryRef values previously returned by Graph Memory.", map[string]any{
			"trajectory_id": map[string]string{"type": "string"}, "node_ids": map[string]any{"type": "array", "items": map[string]string{"type": "string"}}, "ref": map[string]any{"type": "object"},
		}, []string{"trajectory_id"}),
		tool("graph_memory_redirect", "Redirect an active run after a directed correction.", map[string]any{
			"trajectory_id": map[string]string{"type": "string"}, "query": map[string]string{"type": "string"}, "focus": map[string]string{"type": "string"}, "steering_message_id": map[string]string{"type": "string"},
		}, []string{"trajectory_id", "steering_message_id"}),
		tool("graph_memory_submit", "Submit a completed Graph Memory run (V1 may require a summary and cited node ids).", map[string]any{
			"trajectory_id": map[string]string{"type": "string"}, "found": map[string]string{"type": "boolean"}, "summary": map[string]string{"type": "string"}, "node_ids": map[string]any{"type": "array", "items": map[string]string{"type": "string"}},
		}, []string{"trajectory_id"}),
	}
}

func cloneMCPObject(value map[string]any) map[string]any {
	properties := map[string]any{}
	for key, item := range value["properties"].(map[string]any) {
		properties[key] = item
	}
	return map[string]any{"type": value["type"], "properties": properties, "required": append([]string(nil), value["required"].([]string)...), "additionalProperties": false}
}

func (b *graphMemoryMCPBridge) callTool(raw json.RawMessage) map[string]any {
	var call struct {
		Name      string         `json:"name"`
		Arguments map[string]any `json:"arguments"`
	}
	if err := json.Unmarshal(raw, &call); err != nil {
		return graphMemoryMCPToolError("invalid tool request")
	}
	operation, ok := strings.CutPrefix(strings.TrimSpace(call.Name), "graph_memory_")
	if !ok || !isGraphMemoryMCPOperation(operation) {
		return graphMemoryMCPToolError("unknown Graph Memory tool")
	}
	body, channelID, err := graphMemoryMCPRequestBody(operation, call.Arguments)
	if err != nil {
		return graphMemoryMCPToolError(err.Error())
	}
	response, err := b.callGateway(operation, channelID, body)
	if err != nil {
		return graphMemoryMCPToolError(err.Error())
	}
	return map[string]any{"content": []map[string]string{{"type": "text", "text": response}}}
}

func isGraphMemoryMCPOperation(operation string) bool {
	switch operation {
	case "start", "explore", "redirect", "submit":
		return true
	default:
		return false
	}
}

func graphMemoryMCPToolError(message string) map[string]any {
	return map[string]any{"content": []map[string]string{{"type": "text", "text": message}}, "isError": true}
}

func graphMemoryMCPRequestBody(operation string, arguments map[string]any) ([]byte, string, error) {
	channelID := mcpRequiredString(arguments, "channel_id")
	idempotencyKey := mcpRequiredString(arguments, "idempotency_key")
	if channelID == "" || idempotencyKey == "" {
		return nil, "", fmt.Errorf("channel_id and idempotency_key are required")
	}
	body := map[string]any{"idempotency_key": idempotencyKey}
	switch operation {
	case "start":
		query := mcpRequiredString(arguments, "query")
		if query == "" {
			return nil, "", fmt.Errorf("query is required")
		}
		body["query"] = query
	case "explore":
		if trajectory := mcpRequiredString(arguments, "trajectory_id"); trajectory != "" {
			body["trajectory_id"] = trajectory
		} else {
			return nil, "", fmt.Errorf("trajectory_id is required")
		}
		if nodeIDs, ok := arguments["node_ids"]; ok {
			body["node_ids"] = nodeIDs
		}
		if ref, ok := arguments["ref"]; ok {
			body["ref"] = ref
		}
		if _, nodes := body["node_ids"]; !nodes {
			if _, ref := body["ref"]; !ref {
				return nil, "", fmt.Errorf("node_ids or ref is required")
			}
		}
	case "redirect":
		trajectory := mcpRequiredString(arguments, "trajectory_id")
		steering := mcpRequiredString(arguments, "steering_message_id")
		query := mcpOptionalString(arguments, "query")
		if query == "" {
			query = mcpOptionalString(arguments, "focus")
		}
		if trajectory == "" || steering == "" || query == "" {
			return nil, "", fmt.Errorf("trajectory_id, query or focus, and steering_message_id are required")
		}
		body["trajectory_id"], body["query"], body["steering_message_id"] = trajectory, query, steering
	case "submit":
		trajectory := mcpRequiredString(arguments, "trajectory_id")
		if trajectory == "" {
			return nil, "", fmt.Errorf("trajectory_id is required")
		}
		body["trajectory_id"] = trajectory
		for _, key := range []string{"found", "summary", "node_ids"} {
			if value, ok := arguments[key]; ok {
				body[key] = value
			}
		}
	case "checkpoint":
		trajectory := mcpRequiredString(arguments, "trajectory_id")
		if trajectory == "" {
			return nil, "", fmt.Errorf("trajectory_id is required")
		}
		body["trajectory_id"] = trajectory
	}
	encoded, err := json.Marshal(body)
	return encoded, channelID, err
}

func mcpRequiredString(arguments map[string]any, key string) string {
	return strings.TrimSpace(mcpOptionalString(arguments, key))
}
func mcpOptionalString(arguments map[string]any, key string) string {
	value, _ := arguments[key].(string)
	return value
}

func (b *graphMemoryMCPBridge) callGateway(operation, channelID string, body []byte) (string, error) {
	request, err := http.NewRequest(http.MethodPost, b.proxyURL+"/api/agent/channels/"+url.PathEscape(channelID)+"/graph-memory/"+operation, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	request.Header.Set("Authorization", "Bearer "+b.token)
	request.Header.Set("Content-Type", "application/json")
	response, err := b.client.Do(request)
	if err != nil {
		return "", fmt.Errorf("Graph Memory gateway unavailable")
	}
	defer response.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(response.Body, 256*1024+1))
	if err != nil {
		return "", fmt.Errorf("read Graph Memory gateway response")
	}
	if len(payload) > 256*1024 {
		return "", fmt.Errorf("Graph Memory gateway response exceeds limit")
	}
	text := strings.TrimSpace(string(payload))
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		if text == "" {
			text = response.Status
		}
		return "", fmt.Errorf("Graph Memory gateway rejected request: %s", text)
	}
	if text == "" {
		text = `{}`
	}
	return text, nil
}
