package agent

import (
	"sort"
	"testing"
)

func TestProviderCapabilitiesCoverAllKnownTypes(t *testing.T) {
	t.Parallel()
	for _, name := range KnownAgentTypes() {
		if _, ok := providerCapabilities[name]; !ok {
			t.Errorf("New() accepts %q but providerCapabilities has no row — add caps(...) for it", name)
		}
	}
	if got, want := len(providerCapabilities), len(KnownAgentTypes()); got != want {
		extra := KnownProviderTypes()
		sort.Strings(extra)
		t.Errorf("providerCapabilities has %d rows, New() has %d arms; table=%v", got, want, extra)
	}
}

func TestProviderCapabilitiesPinnedValues(t *testing.T) {
	t.Parallel()

	// Canonical resident set — was isCanonicalResidentProvider / #1623.
	for _, name := range []string{"pi", "grok", "cursor", "opencode"} {
		if !Capabilities(name).CanonicalResident {
			t.Errorf("%q must be CanonicalResident", name)
		}
	}
	if !Capabilities("claude").CanonicalResident {
		t.Error("claude must be CanonicalResident (ACP adapter resident)")
	}

	// Inline system prompt — was providerNeedsInlineSystemPrompt.
	for _, name := range []string{"kiro"} {
		if !Capabilities(name).NeedsInlineSystemPrompt {
			t.Errorf("%q must NeedInlineSystemPrompt", name)
		}
	}
	if Capabilities("cursor").NeedsInlineSystemPrompt {
		t.Error("cursor must not NeedInlineSystemPrompt")
	}

	// Custom model id — Raft handbook allow-list (ex-CustomModelIDSupported switch).
	for _, name := range []string{"claude", "codex", "cursor", "pi"} {
		if !Capabilities(name).CustomModelIDSupported {
			t.Errorf("%q must support custom model IDs", name)
		}
	}
	for _, name := range []string{"opencode", "grok", "kiro"} {
		if Capabilities(name).CustomModelIDSupported {
			t.Errorf("%q must not support custom model IDs", name)
		}
	}

	// Thinking discovery — every provider that injects thinking/effort into
	// the CLI today (#59). Keep this list in lockstep with backends that
	// read ExecOptions.ThinkingLevel.
	for _, name := range []string{"claude", "codex", "pi", "grok", "opencode"} {
		if !Capabilities(name).ThinkingDiscovery {
			t.Errorf("%q must advertise ThinkingDiscovery", name)
		}
	}
	if Capabilities("cursor").ThinkingDiscovery {
		t.Error("cursor must not advertise ThinkingDiscovery today")
	}

	// Model selection — every built-in currently true (hermes/antigravity regressions).
	for _, name := range KnownAgentTypes() {
		if !Capabilities(name).ModelSelectionSupported {
			t.Errorf("%q must support model selection", name)
		}
	}

	// Force restart — DERIVED from ResidentRuntimeForceKillable (#62), not
	// hand-filled. Today equal to the canonical-resident set; pinned so a
	// future split does not silently widen/narrow the FE restart button.
	for _, name := range []string{"pi", "grok", "cursor", "opencode", "kiro", "codex", "claude"} {
		if !Capabilities(name).ForceRestart {
			t.Errorf("%q must advertise ForceRestart (resident backend must implement ForceKill)", name)
		}
		if !ForceRestartSupported(name) {
			t.Errorf("%q ForceRestartSupported wrapper must read true from the table", name)
		}
	}
}

// TestForceRestartDerivedFromResidentForceKillable locks the Parker #62 rule:
// the capability bit comes from a type-assert on the resident backend, not a
// hand-typed bool. Mutation proof (the "hand-fill true / no interface" shape):
// a constructor returning a non-ForceKillable Backend must derive false.
func TestForceRestartDerivedFromResidentForceKillable(t *testing.T) {
	t.Parallel()

	for name, ctor := range forceRestartResidentConstructors {
		killable := residentBackendForceKillable(ctor)
		if !killable {
			t.Errorf("%q is in forceRestartResidentConstructors but %T does not implement ResidentRuntimeForceKillable — remove it from the map or add ForceKill()", name, ctor(Config{}))
		}
		if Capabilities(name).ForceRestart != killable {
			t.Errorf("%q: derived ForceRestart=%v, type-assert killable=%v", name, Capabilities(name).ForceRestart, killable)
		}
	}

	// Non-resident providers must stay false — only the resident constructor
	// map feeds this bit (a ForceKill on the one-shot New() backend is ignored).
	for _, name := range KnownAgentTypes() {
		_, registered := forceRestartResidentConstructors[name]
		if !registered && Capabilities(name).ForceRestart {
			t.Errorf("%q ForceRestart=true but not in forceRestartResidentConstructors", name)
		}
	}

	// Parker mutation: "table true / interface missing" must not derive true.
	if residentBackendForceKillable(func(Config) Backend { return &claudeBackend{} }) {
		t.Fatal("non-ForceKillable Backend must derive ForceRestart=false")
	}
}

func TestAllKnownProvidersAreCanonicalResident(t *testing.T) {
	t.Parallel()
	for _, name := range KnownAgentTypes() {
		if !Capabilities(name).CanonicalResident {
			t.Errorf("%q is registered without a resident adapter", name)
		}
	}
}

func TestCapabilitiesUnknownProviderFailClosed(t *testing.T) {
	t.Parallel()
	got := Capabilities("not-a-real-provider")
	if got != (ProviderCapabilities{}) {
		t.Fatalf("unknown provider must be zero-value fail-closed, got %+v", got)
	}
}

func TestModelSelectionAndCustomIDWrappers(t *testing.T) {
	t.Parallel()
	if !ModelSelectionSupported("kiro") {
		t.Error("kiro wrapper must read ModelSelectionSupported from the table")
	}
	if !CustomModelIDSupported("claude") {
		t.Error("claude wrapper must read CustomModelIDSupported from the table")
	}
	if CustomModelIDSupported("opencode") {
		t.Error("opencode wrapper must read CustomModelIDSupported=false from the table")
	}
}

// TestThinkingEnumsStayInsideCapabilityTable pins that providerThinkingEnums
// never invents a provider the capability table says has no thinking support
// — otherwise IsKnownThinkingValue would accept tokens for a runtime that
// Capabilities().ThinkingDiscovery=false (the #47 scatter failure mode).
func TestThinkingEnumsStayInsideCapabilityTable(t *testing.T) {
	t.Parallel()
	for name := range providerThinkingEnums {
		if !Capabilities(name).ThinkingDiscovery {
			t.Errorf("providerThinkingEnums has %q but Capabilities(%q).ThinkingDiscovery=false", name, name)
		}
	}
	// Every ThinkingDiscovery provider except opencode (dynamic variants)
	// must have a server-side enum so CreateAgent can cheap-reject garbage.
	for _, name := range KnownAgentTypes() {
		if !Capabilities(name).ThinkingDiscovery || name == "opencode" {
			continue
		}
		if _, ok := providerThinkingEnums[name]; !ok {
			t.Errorf("Capabilities(%q).ThinkingDiscovery=true but providerThinkingEnums has no row", name)
		}
	}
}
