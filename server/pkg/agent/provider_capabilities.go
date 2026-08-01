package agent

import "strings"

// ProviderCapabilities is the single source of truth for "what can this
// provider do?" questions that used to be scattered across switch lists
// (task #47). Call sites must read fields from here rather than re-listing
// provider names.
//
// Each field answers ONE question — do not merge two fields just because
// their allow-lists look similar today (Parker/Felix: same name ≠ same
// meaning; blind merge silently breaks retry paths).
type ProviderCapabilities struct {
	// CanonicalResident: can this provider use the shared agent×runtime
	// canonical resident pool (vs one-shot per-turn backends)?
	CanonicalResident bool

	// NeedsInlineSystemPrompt: must the system/brief prompt be inlined into
	// the provider invocation (no external brief-file handoff)?
	NeedsInlineSystemPrompt bool

	// ModelSelectionSupported: does setting agent.model have any effect for
	// this provider? When false the UI shows a disabled "Managed by runtime"
	// picker instead of a silently-ignored dropdown.
	ModelSelectionSupported bool

	// CustomModelIDSupported: may the user type an arbitrary model id outside
	// the discovered dropdown? (Raft handbook allow-list; free-form input for
	// unsupported providers produces a silent CLI failure.)
	CustomModelIDSupported bool

	// ThinkingDiscovery: does ListModels attempt to discover a per-model
	// reasoning/effort catalog for this provider? When false the UI hides
	// the thinking-level picker for every model of this runtime (#59).
	ThinkingDiscovery bool

	// ForceRestart: can a busy/stuck agent of this provider be force-
	// interrupted via restart (backend implements ResidentRuntimeForceKillable)?
	// FE uses this to show/hide the restart button; the daemon also fail-closes
	// when the interface is missing (task #62). Same allow-list as
	// CanonicalResident today — keep as a separate field (Parker/Felix: same
	// name ≠ same meaning).
	ForceRestart bool
}

// caps is the only constructor for a table row. All six fields are required
// parameters so omitting one is a compile-time error ("half-filled row").
func caps(
	canonicalResident bool,
	needsInlineSystemPrompt bool,
	modelSelectionSupported bool,
	customModelIDSupported bool,
	thinkingDiscovery bool,
	forceRestart bool,
) ProviderCapabilities {
	return ProviderCapabilities{
		CanonicalResident:       canonicalResident,
		NeedsInlineSystemPrompt: needsInlineSystemPrompt,
		ModelSelectionSupported: modelSelectionSupported,
		CustomModelIDSupported:  customModelIDSupported,
		ThinkingDiscovery:       thinkingDiscovery,
		ForceRestart:            forceRestart,
	}
}

// providerCapabilities covers every agent type accepted by New.
// Adding a provider to New without a row here fails TestProviderCapabilitiesCoverAllKnownTypes.
var providerCapabilities = map[string]ProviderCapabilities{
	//                    canonical, inlinePrompt, modelSel, customID, thinking, forceRestart
	"claude":      caps(false, false, true, true, true, false),
	"codebuddy":   caps(false, false, true, false, true, false),
	"codex":       caps(false, false, true, true, true, false),
	"copilot":     caps(false, false, true, true, false, false),
	"opencode":    caps(true, false, true, false, false, true),
	"openclaw":    caps(false, true, true, false, false, false),
	"hermes":      caps(false, false, true, false, false, false),
	"gemini":      caps(false, false, true, false, false, false),
	"pi":          caps(true, false, true, true, false, true),
	"cursor":      caps(true, false, true, true, false, true),
	"kimi":        caps(false, true, true, false, false, false),
	"kiro":        caps(false, true, true, false, false, false),
	"antigravity": caps(false, false, true, false, false, false),
	"grok":        caps(true, false, true, false, false, true),
}

// KnownProviderTypes returns every provider that has a capability row
// (the closed set accepted by New). Order is unstable — sort in tests.
func KnownProviderTypes() []string {
	out := make([]string, 0, len(providerCapabilities))
	for name := range providerCapabilities {
		out = append(out, name)
	}
	return out
}

// Capabilities returns the capability row for provider. Unknown providers
// get the zero value (all false) so fail-closed is the default for new
// call sites that forget to register a row.
func Capabilities(provider string) ProviderCapabilities {
	trimmed := strings.ToLower(strings.TrimSpace(provider))
	if c, ok := providerCapabilities[trimmed]; ok {
		return c
	}
	return ProviderCapabilities{}
}
