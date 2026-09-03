package daemon

import (
	"encoding/json"
	"testing"
)

func TestManagedGraphMemoryMCPConfigAddsLocalBridgeOnlyForManagedRole(t *testing.T) {
	original := json.RawMessage(`{"mcpServers":{"existing":{"command":"uvx","args":["fetch"]}}}`)
	merged, err := managedGraphMemoryMCPConfig(original, "graph_memory_channel")
	if err != nil {
		t.Fatal(err)
	}
	var parsed struct {
		MCPServers map[string]struct {
			Command string   `json:"command"`
			Args    []string `json:"args"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal(merged, &parsed); err != nil {
		t.Fatal(err)
	}
	if parsed.MCPServers["existing"].Command != "uvx" {
		t.Fatalf("existing MCP server was lost: %s", merged)
	}
	bridge, ok := parsed.MCPServers[graphMemoryMCPServerName]
	if !ok || bridge.Command != "multica" || len(bridge.Args) != 1 || bridge.Args[0] != "graph-memory-mcp" {
		t.Fatalf("bridge config = %+v, raw=%s", bridge, merged)
	}

	unchanged, err := managedGraphMemoryMCPConfig(original, "group_manager")
	if err != nil {
		t.Fatal(err)
	}
	if string(unchanged) != string(original) {
		t.Fatalf("non-memory managed role was modified: %s", unchanged)
	}
}

func TestManagedGraphMemoryMCPConfigRejectsMalformedConfig(t *testing.T) {
	if _, err := managedGraphMemoryMCPConfig(json.RawMessage(`[]`), "graph_memory_channel"); err == nil {
		t.Fatal("malformed MCP config was accepted")
	}
}
