// SPDX-License-Identifier: Apache-2.0

package service

import (
	"strings"
	"testing"
)

// Spec §2 / brief D22: server environment provides defaults and hard safety
// ceilings; unset env falls back to the D25 built-ins.
func TestLoadGraphMemoryLimitsBuiltins(t *testing.T) {
	limits := LoadGraphMemoryLimits(func(string) string { return "" })
	def := limits.Defaults
	if def.TTTConcurrency != 4 || def.ExploreNodesPerExpansion != 1 ||
		def.MaxHierarchyFanout != 8 || def.MaxRelationEdgesPerNode != 8 ||
		def.DiveMaxRounds != 6 || def.DiveMaxViewedNodes != 24 ||
		def.DiveMaxSourceFiles != 4 || def.DiveTimeoutSeconds != 600 ||
		def.WRound != 0.1 || def.SourceMaxFileBytes != 20<<20 ||
		def.SourceMaxTotalBytes != 50<<20 || def.SourceMaxPDFPages != 50 ||
		def.SourceMaxAVSeconds != 600 || def.SourceMaxImageMegapixels != 40 {
		t.Fatalf("builtin defaults = %+v", def)
	}
	// Ceilings default to the storage sanity bounds; env may only lower them.
	if limits.Ceilings.TTTConcurrency != 64 || limits.Ceilings.MaxHierarchyFanout != 64 ||
		limits.Ceilings.DiveMaxRounds != 64 || limits.Ceilings.DiveTimeoutSeconds != 7200 {
		t.Fatalf("builtin ceilings = %+v", limits.Ceilings)
	}
	if len(limits.DiveProviders) != 0 || len(limits.DiveModels) != 0 {
		t.Fatal("dive override allow-list must default to empty (fail closed)")
	}
}

// Spec §2: env overrides retune defaults and lower ceilings; attempts to
// raise a ceiling past the storage sanity bound fail closed to the bound.
func TestLoadGraphMemoryLimitsEnvOverrides(t *testing.T) {
	env := map[string]string{
		"MULTICA_GRAPH_MEMORY_DEFAULT_TTT_CONCURRENCY":  "8",
		"MULTICA_GRAPH_MEMORY_MAX_TTT_CONCURRENCY":      "12",
		"MULTICA_GRAPH_MEMORY_MAX_DIVE_TIMEOUT_SECONDS": "999999999",
		"MULTICA_GRAPH_MEMORY_DEFAULT_W_ROUND":          "0.25",
		"MULTICA_GRAPH_MEMORY_MAX_HIERARCHY_FANOUT":     "bogus",
		"MULTICA_GRAPH_MEMORY_DIVE_PROVIDERS":           "ark, anthropic",
		"MULTICA_GRAPH_MEMORY_DIVE_MODELS":              "glm-5.3",
	}
	limits := LoadGraphMemoryLimits(func(k string) string { return env[k] })
	if limits.Defaults.TTTConcurrency != 8 || limits.Ceilings.TTTConcurrency != 12 {
		t.Fatalf("env override: %+v", limits)
	}
	// Over-bound ceiling clamps to the storage sanity bound (7200).
	if limits.Ceilings.DiveTimeoutSeconds != 7200 {
		t.Fatalf("ceiling must clamp to storage bound, got %d", limits.Ceilings.DiveTimeoutSeconds)
	}
	// Unparseable env fails closed to builtin, never to zero.
	if limits.Ceilings.MaxHierarchyFanout != 64 {
		t.Fatalf("bogus ceiling env must fail closed to builtin, got %d", limits.Ceilings.MaxHierarchyFanout)
	}
	if limits.Defaults.WRound != 0.25 {
		t.Fatalf("w_round default override = %v", limits.Defaults.WRound)
	}
	if len(limits.DiveProviders) != 2 || limits.DiveProviders[0] != "ark" || limits.DiveProviders[1] != "anthropic" {
		t.Fatalf("dive providers = %v", limits.DiveProviders)
	}
}

// Spec §16 / A31: workspace writes above the ceiling, non-finite, or
// negative-where-invalid are rejected with the offending field named.
func TestGraphMemoryLimitsValidate(t *testing.T) {
	limits := LoadGraphMemoryLimits(func(string) string { return "" })
	ok := DefaultGraphMemoryTunables()
	if err := limits.Validate(ok); err != nil {
		t.Fatalf("defaults must validate: %v", err)
	}
	over := DefaultGraphMemoryTunables()
	over.MaxHierarchyFanout = limits.Ceilings.MaxHierarchyFanout + 1
	if err := limits.Validate(over); err == nil || !strings.Contains(err.Error(), "max_hierarchy_fanout") {
		t.Fatalf("over-ceiling fanout must name the field, got %v", err)
	}
	neg := DefaultGraphMemoryTunables()
	neg.WRound = -0.1
	if err := limits.Validate(neg); err == nil || !strings.Contains(err.Error(), "w_round") {
		t.Fatalf("negative w_round rejected, got %v", err)
	}
	nan := DefaultGraphMemoryTunables()
	nan.WRound = nan.WRound / 0 // +Inf; NaN covered below
	if err := limits.Validate(nan); err == nil {
		t.Fatal("non-finite w_round must be rejected")
	}
	zero := DefaultGraphMemoryTunables()
	zero.TTTConcurrency = 0
	if err := limits.Validate(zero); err == nil || !strings.Contains(err.Error(), "ttt_concurrency") {
		t.Fatalf("zero K rejected, got %v", err)
	}
}

// Brief D24: workspace Dive model/provider overrides pass only inside the
// server-configured allow-list; empty override means "inherit Explore".
func TestGraphMemoryLimitsDiveOverridePolicy(t *testing.T) {
	closed := LoadGraphMemoryLimits(func(string) string { return "" })
	if err := closed.ValidateDiveOverride("", ""); err != nil {
		t.Fatalf("empty override (inherit) must pass: %v", err)
	}
	if err := closed.ValidateDiveOverride("ark", "glm-5.3"); err == nil {
		t.Fatal("override without configured allow-list must fail closed")
	}
	open := LoadGraphMemoryLimits(func(k string) string {
		if k == "MULTICA_GRAPH_MEMORY_DIVE_PROVIDERS" {
			return "ark"
		}
		if k == "MULTICA_GRAPH_MEMORY_DIVE_MODELS" {
			return "glm-5.3,kimi-k3"
		}
		return ""
	})
	if err := open.ValidateDiveOverride("ark", "glm-5.3"); err != nil {
		t.Fatalf("allow-listed override must pass: %v", err)
	}
	if err := open.ValidateDiveOverride("ark", "gpt-5.6"); err == nil {
		t.Fatal("model outside allow-list must be rejected")
	}
	if err := open.ValidateDiveOverride("openai", "glm-5.3"); err == nil {
		t.Fatal("provider outside allow-list must be rejected")
	}
}
