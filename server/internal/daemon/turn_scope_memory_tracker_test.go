package daemon

import (
	"strings"
	"testing"

	"github.com/multica-ai/multica/server/internal/daemon/execenv"
)

func TestTurnScopeMemoryTrackerSkipsAlreadyInjectedScopes(t *testing.T) {
	tr := newTurnScopeMemoryTracker()
	key := issueTurnScopeSessionKey("agent-1", "rt-1", "sess-1")
	first := []execenv.MemoryContextForEnv{
		{Name: "u", Content: "Call me JHP", Scope: "user", SubjectID: "m1"},
		{Name: "p", Content: "Project Go", Scope: "project", SubjectID: "p1"},
		{Name: "g", Content: "graph hit", Scope: "workspace"},
	}
	got := tr.selectForInject(key, first, false)
	if len(got) != 3 {
		t.Fatalf("first select = %d, want 3", len(got))
	}
	tr.markInjected(key, got)

	second := []execenv.MemoryContextForEnv{
		{Name: "u", Content: "Call me JHP", Scope: "user", SubjectID: "m1"},
		{Name: "p", Content: "Project Go", Scope: "project", SubjectID: "p1"},
		{Name: "c", Content: "Channel zh", Scope: "channel", SubjectID: "c1"},
		{Name: "g", Content: "graph hit again", Scope: "workspace"},
	}
	got = tr.selectForInject(key, second, false)
	var combined strings.Builder
	for _, memory := range got {
		combined.WriteString(memory.Content)
	}
	content := combined.String()
	if len(got) != 2 {
		t.Fatalf("second select = %d (%q), want channel + graph only", len(got), content)
	}
	if !strings.Contains(content, "Channel zh") || !strings.Contains(content, "graph hit again") {
		t.Fatalf("second select missing new scopes: %q", content)
	}
	if strings.Contains(content, "Call me JHP") || strings.Contains(content, "Project Go") {
		t.Fatalf("second select reloaded old scopes: %q", content)
	}
}

func TestTurnScopeMemoryTrackerFreshSessionReloads(t *testing.T) {
	tr := newTurnScopeMemoryTracker()
	key := issueTurnScopeSessionKey("agent-1", "rt-1", "sess-1")
	memories := []execenv.MemoryContextForEnv{
		{Name: "u", Content: "Call me JHP", Scope: "user", SubjectID: "m1"},
	}
	tr.markInjected(key, memories)
	got := tr.selectForInject(key, memories, true)
	if len(got) != 1 || got[0].Content != "Call me JHP" {
		t.Fatalf("fresh session should re-inject: %+v", got)
	}
}

func TestTurnScopeMemoryTrackerClearResident(t *testing.T) {
	tr := newTurnScopeMemoryTracker()
	key := residentTurnScopeSessionKey("agent-1", "rt-1")
	memories := []execenv.MemoryContextForEnv{
		{Name: "u", Content: "Call me JHP", Scope: "user", SubjectID: "m1"},
	}
	tr.markInjected(key, memories)
	tr.clearResident("agent-1", "rt-1")
	got := tr.selectForInject(key, memories, false)
	if len(got) != 1 {
		t.Fatalf("after clearResident want reload, got %+v", got)
	}
}
