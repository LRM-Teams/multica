package handler

import "github.com/multica-ai/multica/server/pkg/agent"

// ProviderCapabilitiesWire is the FE-facing projection of
// agent.ProviderCapabilities. Expose this object on the responses the UI
// already loads (runtime + lifecycle preflight) — do not add one-off
// top-level bools per capability (Parker #62: the table was consolidated
// server-side; scattering keys across APIs re-opens the same wound).
//
// custom_model_id_supported on the models API is left alone for now
// (migration is a separate change); new consumers should read this object.
type ProviderCapabilitiesWire struct {
	ForceRestart            bool `json:"force_restart"`
	CustomModelID           bool `json:"custom_model_id"`
	ModelSelection          bool `json:"model_selection"`
	ThinkingDiscovery       bool `json:"thinking_discovery"`
	CanonicalResident       bool `json:"canonical_resident"`
	NeedsInlineSystemPrompt bool `json:"needs_inline_system_prompt"`
}

func providerCapabilitiesWire(provider string) ProviderCapabilitiesWire {
	c := agent.Capabilities(provider)
	return ProviderCapabilitiesWire{
		ForceRestart:            c.ForceRestart,
		CustomModelID:           c.CustomModelIDSupported,
		ModelSelection:          c.ModelSelectionSupported,
		ThinkingDiscovery:       c.ThinkingDiscovery,
		CanonicalResident:       c.CanonicalResident,
		NeedsInlineSystemPrompt: c.NeedsInlineSystemPrompt,
	}
}
