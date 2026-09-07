package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestGraphMemoryMCPToolsExposeOnlyModelDrivenOperations(t *testing.T) {
	tools := graphMemoryMCPTools()
	if len(tools) != 4 {
		t.Fatalf("tools = %d, want 4", len(tools))
	}
	seen := map[string]bool{}
	for _, tool := range tools {
		seen[tool["name"].(string)] = true
	}
	for _, name := range []string{"graph_memory_start", "graph_memory_explore", "graph_memory_redirect", "graph_memory_submit"} {
		if !seen[name] {
			t.Fatalf("missing %s", name)
		}
	}
	if seen["graph_memory_checkpoint"] {
		t.Fatal("checkpoint must be daemon-rule-owned, not model-visible")
	}
}

func TestGraphMemoryMCPRequestBodyDropsModelControlledScope(t *testing.T) {
	body, channelID, err := graphMemoryMCPRequestBody("start", map[string]any{
		"channel_id": "channel-1", "idempotency_key": "message-1:start", "query": "recover deploy policy", "graph": "research",
	})
	if err != nil {
		t.Fatal(err)
	}
	if channelID != "channel-1" {
		t.Fatalf("channel = %q", channelID)
	}
	var decoded map[string]any
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatal(err)
	}
	if _, leaked := decoded["graph"]; leaked {
		t.Fatalf("model-controlled graph selector leaked: %s", body)
	}
	if decoded["query"] != "recover deploy policy" {
		t.Fatalf("body = %s", body)
	}
}

func TestGraphMemoryMCPCallsExistingGatewayViaAgentProxy(t *testing.T) {
	var gotPath, gotAuth, gotBody string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotAuth = r.URL.Path, r.Header.Get("Authorization")
		body, _ := io.ReadAll(r.Body)
		gotBody = string(body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"trajectory_id":"t1","seeds":[]}`))
	}))
	defer server.Close()
	bridge := &graphMemoryMCPBridge{proxyURL: server.URL, token: "launch-token", client: server.Client()}
	result := bridge.callTool(json.RawMessage(`{"name":"graph_memory_start","arguments":{"channel_id":"channel-1","idempotency_key":"m1:start","query":"what changed"}}`))
	if result["isError"] == true {
		t.Fatalf("tool error: %#v", result)
	}
	if gotPath != "/api/agent/channels/channel-1/graph-memory/start" || gotAuth != "Bearer launch-token" {
		t.Fatalf("gateway request path=%q auth=%q", gotPath, gotAuth)
	}
	if !strings.Contains(gotBody, `"query":"what changed"`) || strings.Contains(gotBody, "channel_id") {
		t.Fatalf("gateway body = %s", gotBody)
	}
}
