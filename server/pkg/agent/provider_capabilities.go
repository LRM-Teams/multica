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
	// CanonicalResident: this provider uses the shared agent×runtime
	// resident pool. Every shipped provider is resident; a new provider
	// must ship a resident adapter before it can be registered.
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
	// interrupted via restart? DERIVED at init from whether the provider's
	// resident backend implements ResidentRuntimeForceKillable — never
	// hand-filled in caps(...) (Parker #62: a second hand-typed bool would
	// drift from the real interface). FE uses this to show/hide the restart
	// button; the daemon also fail-closes when the interface is missing.
	ForceRestart bool
}

// caps is the only constructor for a table row. All five fields are required
// parameters so omitting one is a compile-time error ("half-filled row").
// ForceRestart is intentionally NOT a caps() argument — see deriveForceRestart.
func caps(
	canonicalResident bool,
	needsInlineSystemPrompt bool,
	modelSelectionSupported bool,
	customModelIDSupported bool,
	thinkingDiscovery bool,
) ProviderCapabilities {
	return ProviderCapabilities{
		CanonicalResident:       canonicalResident,
		NeedsInlineSystemPrompt: needsInlineSystemPrompt,
		ModelSelectionSupported: modelSelectionSupported,
		CustomModelIDSupported:  customModelIDSupported,
		ThinkingDiscovery:       thinkingDiscovery,
	}
}

// providerCapabilities covers every agent type accepted by New.
// Adding a provider to New without a row here fails TestProviderCapabilitiesCoverAllKnownTypes.
// ForceRestart is filled by deriveForceRestart (not by caps).
var providerCapabilities = map[string]ProviderCapabilities{
	//                    canonical, inlinePrompt, modelSel, customID, thinking
	ProviderClaude:   caps(true, false, true, true, true),  // resident via claude-agent-acp adapter
	ProviderCodex:    caps(true, false, true, true, true),  // resident app-server; chat full only
	ProviderOpenCode: caps(true, false, true, false, true), // thinking catalogs + CLI injection (#59)
	ProviderPi:       caps(true, false, true, true, true),
	ProviderCursor:   caps(true, false, true, true, false),
	ProviderKiro:     caps(true, true, true, false, false), // resident ACP (session/load)
	ProviderGrok:     caps(true, false, true, false, true),
}

// forceRestartResidentConstructors maps provider → the Backend the daemon
// actually pools for canonical-resident runs. ForceRestart is true iff that
// Backend type-asserts to ResidentRuntimeForceKillable. Adding a ForceKill
// method without registering the constructor here leaves ForceRestart false
// (FE button stays hidden — fail closed). Removing ForceKill makes the
// derived bit flip to false automatically.
var forceRestartResidentConstructors = map[string]func(Config) Backend{
	ProviderCursor:   func(cfg Config) Backend { return newCursorACPBackend(cfg) },
	ProviderPi:       func(cfg Config) Backend { return newPiRPCBackend(cfg) },
	ProviderGrok:     func(cfg Config) Backend { return newGrokACPBackend(cfg) },
	ProviderOpenCode: func(cfg Config) Backend { return newOpenCodeServeBackend(cfg) },
	ProviderKiro:     func(cfg Config) Backend { return newKiroACPBackend(cfg) },
	ProviderCodex:    func(cfg Config) Backend { return newCodexAppServerBackend(cfg) },
	ProviderClaude:   func(cfg Config) Backend { return newClaudeStreamJSONBackend(cfg) },
}

func init() {
	deriveForceRestart()
}

func deriveForceRestart() {
	for name, row := range providerCapabilities {
		row.ForceRestart = false
		if ctor, ok := forceRestartResidentConstructors[name]; ok {
			if residentBackendForceKillable(ctor) {
				row.ForceRestart = true
			}
		}
		providerCapabilities[name] = row
	}
}

func residentBackendForceKillable(ctor func(Config) Backend) bool {
	_, killable := ctor(Config{}).(ResidentRuntimeForceKillable)
	return killable
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
