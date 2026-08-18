package agent

// Provider names are shared by runtime discovery, execution, and UI-facing
// metadata. Keep provider-specific paths and behavior keyed by these values
// instead of scattering string literals across packages.
const (
	ProviderClaude   = "claude"
	ProviderCodex    = "codex"
	ProviderOpenCode = "opencode"
	ProviderPi       = "pi"
	ProviderCursor   = "cursor"
	ProviderKiro     = "kiro"
	ProviderGrok     = "grok"
)
