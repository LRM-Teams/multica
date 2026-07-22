package handler

import "testing"

func TestFilepathToSlash(t *testing.T) {
	if got := filepathToSlash(`memory\MEMORY.md`); got != "memory/MEMORY.md" {
		t.Fatalf("got %q", got)
	}
}

func TestAllowedAgentMemoryScopeTypes(t *testing.T) {
	for _, scope := range []string{"agent_global", "agent_state", "user", "channel", "project"} {
		if _, ok := allowedAgentMemoryScopeTypes[scope]; !ok {
			t.Fatalf("missing %s", scope)
		}
	}
}
