package main

import "encoding/json"

// summarizeResourceRef renders the common resource references used by
// workspace info and project-resource ID resolution. The project command was
// removed, but these read-only helpers remain part of the unified workspace
// inspection surface.
func summarizeResourceRef(raw any) string {
	m, ok := raw.(map[string]any)
	if !ok {
		return ""
	}
	if u, ok := m["url"].(string); ok && u != "" {
		return u
	}
	if p, ok := m["local_path"].(string); ok && p != "" {
		return p
	}
	if data, err := json.Marshal(m); err == nil {
		return string(data)
	}
	return ""
}
