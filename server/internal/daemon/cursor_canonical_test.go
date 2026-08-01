package daemon

import (
	"testing"

	"github.com/multica-ai/multica/server/pkg/agent"
)

func TestUsesCanonicalResidentChatRuntimeIncludesCursorFullChat(t *testing.T) {
	chatTask := Task{ChatSessionID: "chat-1"}
	issueTask := Task{ChatSessionID: ""}
	if !usesCanonicalResidentChatRuntime("cursor", chatTask) {
		t.Fatal("cursor full chat should enter canonical resident path")
	}
	if usesCanonicalResidentChatRuntime("cursor", issueTask) {
		t.Fatal("cursor without ChatSessionID must stay one-shot")
	}
	if !usesCanonicalResidentChatRuntime("grok", chatTask) {
		t.Fatal("grok full chat should enter canonical path")
	}
	if !usesCanonicalResidentChatRuntime("pi", chatTask) {
		t.Fatal("pi full chat should enter canonical path")
	}
	if !usesCanonicalResidentChatRuntime("opencode", chatTask) {
		t.Fatal("opencode full chat should enter canonical resident path")
	}
	if usesCanonicalResidentChatRuntime("claude", chatTask) {
		t.Fatal("claude has no canonical resident adapter")
	}
}

// TestCanonicalResidentProviderListsStayInSync guards against the #1623 class
// of bug: usesCanonicalResidentChatRuntime routed opencode full chat into the
// canonical path, but tryCanonicalChatBackend's separate hardcoded provider
// switch didn't recognize "opencode" yet, so the task failed closed with
// "canonical chat entry required for opencode full chat" instead of ever
// reaching a backend. Both functions must agree on the same provider set.
func TestCanonicalResidentProviderListsStayInSync(t *testing.T) {
	chatTask := Task{ChatSessionID: "chat-1"}
	providers := []string{"grok", "pi", "cursor", "opencode"}
	for _, provider := range providers {
		if !usesCanonicalResidentChatRuntime(provider, chatTask) {
			t.Fatalf("test fixture assumption wrong: %q should route to canonical resident chat", provider)
		}
		if !isCanonicalResidentProvider(provider) {
			t.Errorf("%q routes to canonical resident chat via usesCanonicalResidentChatRuntime, "+
				"but isCanonicalResidentProvider (tryCanonicalChatBackend's entry gate) does not recognize it "+
				"— the provider lists have drifted out of sync", provider)
		}
		if !agent.Capabilities(provider).CanonicalResident {
			t.Errorf("%q must be CanonicalResident in agent.Capabilities (task #47 table)", provider)
		}
	}
	if usesCanonicalResidentChatRuntime("claude", chatTask) != isCanonicalResidentProvider("claude") {
		t.Error("claude must be consistently rejected by both the dispatch switch and the entry gate")
	}
	if agent.Capabilities("claude").CanonicalResident {
		t.Error("claude must not be CanonicalResident in the capability table")
	}
}

// TestForceRestartCapabilityMatchesPooledResidentBackend locks Parker #62:
// agent.Capabilities.ForceRestart must match whether the Backend that
// defaultCanonicalRuntimeFactory actually pools implements
// ResidentRuntimeForceKillable. Mutation: flip the derived bit / remove
// ForceKill / drop a factory arm → this goes red (no second hand-typed list).
func TestForceRestartCapabilityMatchesPooledResidentBackend(t *testing.T) {
	for _, provider := range agent.KnownAgentTypes() {
		want := agent.Capabilities(provider).ForceRestart
		backend, cleanup, err := defaultCanonicalRuntimeFactory(provider, canonicalRuntimeResident)(agent.Config{})
		if err != nil {
			if want {
				t.Errorf("%q ForceRestart=true but resident factory failed: %v", provider, err)
			}
			continue
		}
		if cleanup != nil {
			cleanup()
		}
		_, got := backend.(agent.ResidentRuntimeForceKillable)
		if got != want {
			t.Errorf("%q: Capability ForceRestart=%v, pooled backend (%T) ForceKillable=%v",
				provider, want, backend, got)
		}
	}
}
