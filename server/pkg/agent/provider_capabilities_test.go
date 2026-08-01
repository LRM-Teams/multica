package agent

import (
	"sort"
	"testing"
)

// knownTypesFromNew mirrors the switch arms in New. Kept as a literal so a
// new New() case that forgets the capability table fails this test — the
// whole point of task #47's "half-filled → test fails" acceptance.
var knownTypesFromNew = []string{
	"claude", "codebuddy", "codex", "copilot", "opencode", "openclaw",
	"hermes", "gemini", "pi", "cursor", "kimi", "kiro", "antigravity", "grok",
}

func TestProviderCapabilitiesCoverAllKnownTypes(t *testing.T) {
	t.Parallel()
	for _, name := range knownTypesFromNew {
		if _, ok := providerCapabilities[name]; !ok {
			t.Errorf("New() accepts %q but providerCapabilities has no row — add caps(...) for it", name)
		}
	}
	if got, want := len(providerCapabilities), len(knownTypesFromNew); got != want {
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
	if Capabilities("claude").CanonicalResident {
		t.Error("claude must not be CanonicalResident")
	}

	// Inline system prompt — was providerNeedsInlineSystemPrompt.
	for _, name := range []string{"openclaw", "kiro", "kimi"} {
		if !Capabilities(name).NeedsInlineSystemPrompt {
			t.Errorf("%q must NeedInlineSystemPrompt", name)
		}
	}
	if Capabilities("cursor").NeedsInlineSystemPrompt {
		t.Error("cursor must not NeedInlineSystemPrompt")
	}

	// Custom model id — Raft handbook allow-list (ex-CustomModelIDSupported switch).
	for _, name := range []string{"claude", "codex", "cursor", "copilot", "pi"} {
		if !Capabilities(name).CustomModelIDSupported {
			t.Errorf("%q must support custom model IDs", name)
		}
	}
	for _, name := range []string{"opencode", "hermes", "gemini", "grok", "antigravity", "openclaw"} {
		if Capabilities(name).CustomModelIDSupported {
			t.Errorf("%q must not support custom model IDs", name)
		}
	}

	// Thinking discovery — providers that call annotate*Thinking in ListModels.
	for _, name := range []string{"claude", "codex", "codebuddy"} {
		if !Capabilities(name).ThinkingDiscovery {
			t.Errorf("%q must advertise ThinkingDiscovery", name)
		}
	}
	if Capabilities("cursor").ThinkingDiscovery {
		t.Error("cursor must not advertise ThinkingDiscovery today")
	}

	// Model selection — every built-in currently true (hermes/antigravity regressions).
	for _, name := range knownTypesFromNew {
		if !Capabilities(name).ModelSelectionSupported {
			t.Errorf("%q must support model selection", name)
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
	if !ModelSelectionSupported("hermes") {
		t.Error("hermes wrapper must read ModelSelectionSupported from the table")
	}
	if !CustomModelIDSupported("claude") {
		t.Error("claude wrapper must read CustomModelIDSupported from the table")
	}
	if CustomModelIDSupported("opencode") {
		t.Error("opencode wrapper must read CustomModelIDSupported=false from the table")
	}
}
