// Package openclawadapt keeps OpenClaw/Pi native memory from using the host
// ~/.openclaw workspace. Multica memory stays on MULTICA_AGENT_ROOT; the
// native memory plugin and dreaming are disabled so L1–L4 is the only curator.
package openclawadapt

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

const (
	StateDirName = ".openclaw"
	ConfigRel    = ".openclaw/openclaw.json"
)

// Apply writes a fail-open Agent-Root overlay: isolate OpenClaw state,
// disable the native memory plugin / dreaming, and leave pointer files so a
// leftover memory_search cannot read the host workspace.
func Apply(agentRoot, memberID string) error {
	agentRoot = strings.TrimSpace(agentRoot)
	if agentRoot == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Join(agentRoot, StateDirName), 0o755); err != nil {
		return err
	}
	if err := writeConfig(agentRoot); err != nil {
		return err
	}
	if err := writePointer(filepath.Join(agentRoot, "MEMORY.md"), "Multica memory lives at memory/MEMORY.md. Do not use this file or ~/.openclaw/workspace/MEMORY.md."); err != nil {
		return err
	}
	userHint := "Multica user memory lives at users/<member-id>/USER.md for the attested initiator. Do not use this file or a host OpenClaw USER.md."
	if id := strings.TrimSpace(memberID); id != "" && !strings.ContainsAny(id, `/\`) {
		userHint = "Multica user memory for this turn is users/" + id + "/USER.md. Do not use this file or a host OpenClaw USER.md."
	}
	return writePointer(filepath.Join(agentRoot, "USER.md"), userHint)
}

// Env returns daemon env that pins OpenClaw state under the Agent Root.
func Env(agentRoot string) map[string]string {
	agentRoot = strings.TrimSpace(agentRoot)
	if agentRoot == "" {
		return nil
	}
	state := filepath.Join(agentRoot, StateDirName)
	return map[string]string{
		"OPENCLAW_STATE_DIR": state,
		"OPENCLAW_WORKSPACE": agentRoot,
	}
}

func writeConfig(agentRoot string) error {
	payload := map[string]any{
		"plugins": map[string]any{
			"entries": map[string]any{
				"memory": map[string]any{"enabled": false},
			},
		},
		"memory": map[string]any{
			"dreaming": map[string]any{"enabled": false},
		},
	}
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(agentRoot, filepath.FromSlash(ConfigRel)), append(data, '\n'), 0o644)
}

func writePointer(path, body string) error {
	if _, err := os.Stat(path); err == nil {
		return nil
	}
	return os.WriteFile(path, []byte("# Redirect\n\n"+body+"\n"), 0o644)
}
