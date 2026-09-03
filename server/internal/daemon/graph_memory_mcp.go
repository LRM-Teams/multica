package daemon

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

const graphMemoryMCPServerName = "multica_graph_memory"

// managedGraphMemoryMCPConfig appends Multica's local stdio bridge only for a
// managed channel Graph Memory Agent. The bridge inherits the launch-scoped
// Agent Proxy wrapper; it receives no server credentials or graph identity.
func managedGraphMemoryMCPConfig(raw json.RawMessage, managedRole string) (json.RawMessage, error) {
	if !strings.EqualFold(strings.TrimSpace(managedRole), "graph_memory_channel") {
		return append(json.RawMessage(nil), raw...), nil
	}
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		trimmed = []byte(`{}`)
	}
	var config map[string]json.RawMessage
	if err := json.Unmarshal(trimmed, &config); err != nil || config == nil {
		if err == nil {
			err = fmt.Errorf("MCP config must be a JSON object")
		}
		return nil, fmt.Errorf("managed Graph Memory MCP config: %w", err)
	}
	servers := map[string]json.RawMessage{}
	if existing, ok := config["mcpServers"]; ok && len(bytes.TrimSpace(existing)) > 0 && !bytes.Equal(bytes.TrimSpace(existing), []byte("null")) {
		if err := json.Unmarshal(existing, &servers); err != nil || servers == nil {
			if err == nil {
				err = fmt.Errorf("mcpServers must be a JSON object")
			}
			return nil, fmt.Errorf("managed Graph Memory MCP config: %w", err)
		}
	}
	entry, err := json.Marshal(map[string]any{
		"command": "multica",
		"args":    []string{"graph-memory-mcp"},
	})
	if err != nil {
		return nil, fmt.Errorf("marshal managed Graph Memory MCP bridge: %w", err)
	}
	// This is an application-reserved server name. Overwrite stale daemon
	// versions rather than allowing a user-configured command to impersonate a
	// privileged bridge in a managed memory-agent process.
	servers[graphMemoryMCPServerName] = entry
	encoded, err := json.Marshal(servers)
	if err != nil {
		return nil, fmt.Errorf("marshal managed Graph Memory MCP servers: %w", err)
	}
	config["mcpServers"] = encoded
	merged, err := json.Marshal(config)
	if err != nil {
		return nil, fmt.Errorf("marshal managed Graph Memory MCP config: %w", err)
	}
	return merged, nil
}
