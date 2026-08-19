package agent

import (
	"context"
	"encoding/json"
	"os/exec"
	"strings"
)

// thinking.go discovers per-model reasoning/effort catalogs for the
// claude, codex, opencode, and pi backends so the daemon can advertise them to
// the UI without hard-coding (and getting wrong) what's installed locally.
//
// MUL-2339: we deliberately do not flatten Claude's `low|medium|high|
// xhigh|max` and Codex's `none|minimal|low|medium|high|xhigh` onto a
// shared enum. OpenCode exposes provider-specific model variants through
// `opencode run --variant`, and those names can be extended by local
// opencode.json config. What users pick must round-trip exactly through
// each CLI's own value vocabulary.

// ── Codex ────────────────────────────────────────────────────────────
//
// `codex debug models` is the structured discovery hook Elon's review
// flagged. It returns the per-model reasoning catalog directly,
// including the model's documented default. We prefer this over the
// older config-error probe trick because:
//   1. It gives us per-model subsets without hand-maintained tables.
//   2. The schema is stable across CLI versions (Codex 0.131.0+).
//   3. It doesn't pollute stderr with an intentional misconfiguration.
//
// The subcommand emits JSON on stdout by default — there is no
// `--output json` flag (a prior version of this code passed one and
// silently failed on 0.131.0). We add `--bundled` to skip the network
// refresh: discovery runs on every daemon poll and a network hop here
// would block the picker behind whatever the user's connection allows.
// The bundled catalog is what determines which `model_reasoning_effort`
// tokens the local binary actually accepts, which is the only thing we
// need for validation.
//
// On older Codex versions / failures, the picker just disappears for
// that model rather than offering a wrong list.

// codexEffortLabel is the human display string for each Codex effort
// value, matching Codex's own TUI (`Extra high`, `Minimal`, …) so
// users see the same labels across our picker and `codex /model`.
var codexEffortLabel = map[string]string{
	"none":    "None",
	"minimal": "Minimal",
	"low":     "Low",
	"medium":  "Medium",
	"high":    "High",
	"xhigh":   "Extra high",
}

// codexDebugModelsResponse mirrors the JSON shape emitted by
// `codex debug models` (Codex 0.131.0+). Only the fields we
// consume are typed; unknown keys are ignored.
type codexDebugModelsResponse struct {
	Models []struct {
		Slug                    string `json:"slug"`
		DisplayName             string `json:"display_name"`
		Visibility              string `json:"visibility"`
		DefaultReasoningLevel   string `json:"default_reasoning_level"`
		SupportedReasoningLevel []struct {
			Effort      string `json:"effort"`
			Description string `json:"description"`
		} `json:"supported_reasoning_levels"`
	} `json:"models"`
}

// annotateCodexThinking decorates each model entry with its reasoning
// catalog. Models the CLI doesn't know about (older codex install,
// brand-new ID we haven't shipped) get Thinking=nil — the UI hides
// the picker for those rows rather than guessing.
var codexDebugModelsArgs = []string{"debug", "models", "--bundled"}

func runCodexDebugModels(ctx context.Context, executablePath string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, executablePath, codexDebugModelsArgs...)
	hideAgentWindow(cmd)
	return cmd.Output()
}

// parseCodexDebugModels takes the JSON payload from `codex debug
// models` and projects it into a per-model thinking catalog.
// Returns an empty map (never nil) so callers can compose safely
// without nil-checking the result.
func parseCodexDebugModels(raw []byte) map[string]*ModelThinking {
	out := map[string]*ModelThinking{}
	var resp codexDebugModelsResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return out
	}
	for _, m := range resp.Models {
		if m.Slug == "" || len(m.SupportedReasoningLevel) == 0 {
			continue
		}
		levels := make([]ThinkingLevel, 0, len(m.SupportedReasoningLevel))
		for _, lvl := range m.SupportedReasoningLevel {
			if lvl.Effort == "" {
				continue
			}
			label, ok := codexEffortLabel[lvl.Effort]
			if !ok {
				label = strings.Title(lvl.Effort) //nolint:staticcheck
			}
			levels = append(levels, ThinkingLevel{
				Value:       lvl.Effort,
				Label:       label,
				Description: lvl.Description,
			})
		}
		if len(levels) == 0 {
			continue
		}
		out[m.Slug] = &ModelThinking{
			SupportedLevels: levels,
			DefaultLevel:    m.DefaultReasoningLevel,
		}
	}
	return out
}

// parseCodexDebugModelsCatalog projects `codex debug models` JSON into
// picker rows. Only visibility=list (or empty visibility for older CLIs)
// is included; hide is omitted. Thinking levels are attached when present.
func parseCodexDebugModelsCatalog(raw []byte) []Model {
	var resp codexDebugModelsResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil
	}
	out := make([]Model, 0, len(resp.Models))
	for _, m := range resp.Models {
		if m.Slug == "" {
			continue
		}
		vis := strings.ToLower(strings.TrimSpace(m.Visibility))
		// Older CLIs omit visibility — treat as listable. Explicit hide stays out.
		if vis == "hide" {
			continue
		}
		label := strings.TrimSpace(m.DisplayName)
		if label == "" {
			label = m.Slug
		}
		entry := Model{
			ID:       m.Slug,
			Label:    label,
			Provider: "openai",
		}
		if len(m.SupportedReasoningLevel) > 0 {
			levels := make([]ThinkingLevel, 0, len(m.SupportedReasoningLevel))
			for _, lvl := range m.SupportedReasoningLevel {
				if lvl.Effort == "" {
					continue
				}
				lbl, ok := codexEffortLabel[lvl.Effort]
				if !ok {
					lbl = strings.Title(lvl.Effort) //nolint:staticcheck
				}
				levels = append(levels, ThinkingLevel{
					Value:       lvl.Effort,
					Label:       lbl,
					Description: lvl.Description,
				})
			}
			if len(levels) > 0 {
				entry.Thinking = &ModelThinking{
					SupportedLevels: levels,
					DefaultLevel:    m.DefaultReasoningLevel,
				}
			}
		}
		out = append(out, entry)
	}
	return out
}

// ── Shared validation ────────────────────────────────────────────────

// ValidateThinkingLevel reports whether `value` is in the supported
// catalog for the given (provider, model) pair. Empty value is always
// valid — it means "use the runtime default".
//
// Empty model is treated as "use the provider's default model"; we
// resolve it through ListModels so the daemon's pre-execution guard
// behaves the same whether the agent picked an explicit model or
// inherited the runtime default. Without this, a default-model task
// with a valid thinking_level would be rejected on the grounds that
// the empty string is not in the catalog — exactly the misjudgement
// Elon flagged in the PR1 review.
//
// The lookup goes through ListModels so it sees the *current* CLI
// catalog (including dynamic discovery for codex), not just a static
// map. The function is intentionally pure of HTTP concerns so the
// daemon's pre-execution guard and the server's UpdateAgent gate can
// share the same source of truth.
func ValidateThinkingLevel(ctx context.Context, providerType, executablePath, model, value string) (bool, error) {
	if value == "" {
		return true, nil
	}
	models, err := ListModels(ctx, providerType, executablePath)
	if err != nil {
		return false, err
	}
	// Compatibility model IDs are no longer merged into the picker or
	// validation catalog (Frank 2026-08-03: dynamic only).
	target := model
	if target == "" {
		// Default model = the entry the catalog marks as Default. If no
		// entry is flagged, fall through to the no-match return; that
		// matches the existing semantics where an unknown model fails
		// closed rather than guessing.
		for _, m := range models {
			if m.Default {
				target = m.ID
				break
			}
		}
		if target == "" {
			// OpenCode / Pi / Grok all attach a thinking catalog to every
			// advertised model, so an empty agent.model (runtime default)
			// should still accept known effort tokens rather than fail closed.
			if providerType == "opencode" || providerType == "pi" || providerType == "grok" {
				return anyModelSupportsThinkingValue(models, value), nil
			}
			return false, nil
		}
	}
	for _, m := range models {
		if m.ID != target {
			continue
		}
		if m.Thinking == nil {
			return false, nil
		}
		for _, lvl := range m.Thinking.SupportedLevels {
			if lvl.Value == value {
				return true, nil
			}
		}
		return false, nil
	}
	return false, nil
}

func anyModelSupportsThinkingValue(models []Model, value string) bool {
	for _, m := range models {
		if m.Thinking == nil {
			continue
		}
		for _, lvl := range m.Thinking.SupportedLevels {
			if lvl.Value == value {
				return true
			}
		}
	}
	return false
}

// providerThinkingEnums is the server-side accept-list for runtimes with a
// fixed reasoning-effort vocabulary. OpenCode is deliberately absent because
// its `--variant` values come from the local model catalog and custom
// opencode.json entries can define additional variant names.
//
// The server doesn't have local CLI binaries, so it cannot do per-model
// discovery the way the daemon can; what it CAN do is reject values that are
// not in any version of the provider's enum at all. Per-model gaps (e.g. user
// sets `xhigh` while the chosen model only supports up to `high`) are handled
// by the daemon's pre-execution guard, which logs and skips injection rather
// than mutating persisted agent state. That split keeps API behaviour
// consistent: always 400 on literal-invalid, never auto-clear on
// combination-invalid. See MUL-2339 review notes.
//
// Keep these lists permissive: they're a "is this a known token in this
// runtime's universe" check, not an "is this the right level for this
// model" check. Adding a new level upstream means adding it here too so
// users can persist it before the next discovery refresh.
var providerThinkingEnums = map[string]map[string]bool{
	"claude": {
		"low":    true,
		"medium": true,
		"high":   true,
		"xhigh":  true,
		"max":    true,
	},
	"codex": {
		"none":    true,
		"minimal": true,
		"low":     true,
		"medium":  true,
		"high":    true,
		"xhigh":   true,
	},
	"pi": {
		"off":     true,
		"minimal": true,
		"low":     true,
		"medium":  true,
		"high":    true,
		"xhigh":   true,
	},
	// Grok headless --reasoning-effort / --effort levels (docs + grok 0.2.93).
	"grok": {
		"none":    true,
		"minimal": true,
		"low":     true,
		"medium":  true,
		"high":    true,
		"xhigh":   true,
		"max":     true,
	},
}

// IsKnownThinkingValue reports whether `value` is a recognised effort
// token for the given provider. Empty string is always accepted (means
// "use runtime default"). Unknown providers (no thinking concept) accept
// only empty; OpenCode accepts well-formed variant names because its local
// catalog can be extended by opencode.json.
//
// This is the cheap synchronous gate the server uses on CreateAgent /
// UpdateAgent. Unlike ValidateThinkingLevel it does NOT consult the live
// catalog or per-model subset.
func IsKnownThinkingValue(providerType, value string) bool {
	if value == "" {
		return true
	}
	// Capability table is the membership gate (#47/#59). Enums below only
	// answer "is this a known token" — never "does this provider support
	// thinking at all".
	if !Capabilities(providerType).ThinkingDiscovery {
		return false
	}
	if providerType == "opencode" {
		return isValidOpenCodeVariantName(value)
	}
	enum, ok := providerThinkingEnums[providerType]
	if !ok {
		return false
	}
	return enum[value]
}

func isValidOpenCodeVariantName(value string) bool {
	if len(value) > 64 {
		return false
	}
	for i, r := range value {
		valid := r >= 'a' && r <= 'z' ||
			r >= 'A' && r <= 'Z' ||
			r >= '0' && r <= '9' ||
			r == '-' || r == '_' || r == '.'
		if !valid {
			return false
		}
		if i == 0 && (r == '-' || r == '_' || r == '.') {
			return false
		}
	}
	return true
}
