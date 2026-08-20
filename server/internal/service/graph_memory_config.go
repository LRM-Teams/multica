// SPDX-License-Identifier: Apache-2.0

package service

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// GraphMemoryTunables mirrors the workspace-tunable graph_memory_profile
// columns added by migration 418 (plus the pre-existing explore knobs).
// The workspace profile is the business-tunables authority; the server
// environment supplies defaults and hard safety ceilings (spec §2, D22).
type GraphMemoryTunables struct {
	TTTConcurrency           int     // saved per-recall K (graph_memory_profile.explore_agents)
	ExploreMaxRounds         int     // pre-existing explore round cap
	ExploreNodesPerExpansion int     // full-view width per expansion batch
	MaxHierarchyFanout       int     // outgoing summarizes children cap
	MaxRelationEdgesPerNode  int     // total incident node-to-node relation degree cap
	DiveMaxRounds            int     // Dive graph traversal rounds
	DiveMaxViewedNodes       int     // Dive full views
	DiveMaxSourceFiles       int     // Dive loaded source files
	DiveTimeoutSeconds       int     // Dive wall time
	WRound                   float64 // reward round-cost weight
	SourceMaxFileBytes       int64   // per-file media bytes
	SourceMaxTotalBytes      int64   // total media bytes per Dive
	SourceMaxPDFPages        int     // decoded PDF pages
	SourceMaxAVSeconds       int     // decoded audio/video duration
	SourceMaxImageMegapixels int     // decoded image pixels
}

// GraphMemoryLimits is the server-side authority for graph-memory business
// tunables: Defaults apply when a workspace has no profile row (or a field
// predates the column); Ceilings bound what a workspace may persist.
// DiveProviders/DiveModels is the workspace Dive model/provider override
// allow-list (brief D24); empty means overrides fail closed.
type GraphMemoryLimits struct {
	Defaults      GraphMemoryTunables
	Ceilings      GraphMemoryTunables
	DiveProviders []string
	DiveModels    []string
}

// Storage sanity bounds from migration 418 CHECK constraints. Env ceilings
// may lower these but never raise them.
var graphMemoryStorageBounds = GraphMemoryTunables{
	TTTConcurrency:           64,
	ExploreMaxRounds:         128,
	ExploreNodesPerExpansion: 16,
	MaxHierarchyFanout:       64,
	MaxRelationEdgesPerNode:  64,
	DiveMaxRounds:            64,
	DiveMaxViewedNodes:       1024,
	DiveMaxSourceFiles:       64,
	DiveTimeoutSeconds:       7200,
	WRound:                   10,
	SourceMaxFileBytes:       4294967296,
	SourceMaxTotalBytes:      17179869184,
	SourceMaxPDFPages:        5000,
	SourceMaxAVSeconds:       14400,
	SourceMaxImageMegapixels: 1000,
}

// DefaultGraphMemoryTunables returns the brief D25 built-in defaults.
func DefaultGraphMemoryTunables() GraphMemoryTunables {
	return GraphMemoryTunables{
		TTTConcurrency:           4,
		ExploreMaxRounds:         3,
		ExploreNodesPerExpansion: 1,
		MaxHierarchyFanout:       8,
		MaxRelationEdgesPerNode:  8,
		DiveMaxRounds:            6,
		DiveMaxViewedNodes:       24,
		DiveMaxSourceFiles:       4,
		DiveTimeoutSeconds:       600,
		WRound:                   0.1,
		SourceMaxFileBytes:       20 << 20,
		SourceMaxTotalBytes:      50 << 20,
		SourceMaxPDFPages:        50,
		SourceMaxAVSeconds:       600,
		SourceMaxImageMegapixels: 40,
	}
}

type graphMemoryEnvKnob struct {
	name    string
	storage int64 // absolute storage sanity bound
	isFloat bool
}

var graphMemoryEnvKnobs = []graphMemoryEnvKnob{
	{"TTT_CONCURRENCY", 64, false},
	{"EXPLORE_MAX_ROUNDS", 128, false},
	{"EXPLORE_NODES_PER_EXPANSION", 16, false},
	{"HIERARCHY_FANOUT", 64, false},
	{"RELATION_EDGES_PER_NODE", 64, false},
	{"DIVE_MAX_ROUNDS", 64, false},
	{"DIVE_MAX_VIEWED_NODES", 1024, false},
	{"DIVE_MAX_SOURCE_FILES", 64, false},
	{"DIVE_TIMEOUT_SECONDS", 7200, false},
	{"W_ROUND", 10, true},
	{"SOURCE_MAX_FILE_BYTES", 4294967296, false},
	{"SOURCE_MAX_TOTAL_BYTES", 17179869184, false},
	{"SOURCE_MAX_PDF_PAGES", 5000, false},
	{"SOURCE_MAX_AV_SECONDS", 14400, false},
	{"SOURCE_MAX_IMAGE_MEGAPIXELS", 1000, false},
}

const (
	graphMemoryEnvDefaultPrefix = "MULTICA_GRAPH_MEMORY_DEFAULT_"
	graphMemoryEnvMaxPrefix     = "MULTICA_GRAPH_MEMORY_MAX_"
	graphMemoryDiveProvidersEnv = "MULTICA_GRAPH_MEMORY_DIVE_PROVIDERS"
	graphMemoryDiveModelsEnv    = "MULTICA_GRAPH_MEMORY_DIVE_MODELS"
)

// LoadGraphMemoryLimits reads defaults and ceilings from the environment.
// Unparseable or out-of-bound values fail closed to the built-in; ceilings
// can only be lowered from the storage sanity bounds, never raised.
func LoadGraphMemoryLimits(getenv func(string) string) GraphMemoryLimits {
	limits := GraphMemoryLimits{
		Defaults: DefaultGraphMemoryTunables(),
		Ceilings: graphMemoryStorageBounds,
	}
	for i, knob := range graphMemoryEnvKnobs {
		if v, ok := parseGraphMemoryEnvNumber(getenv(graphMemoryEnvDefaultPrefix+knob.name), knob); ok {
			setGraphMemoryTunable(&limits.Defaults, i, v)
		}
		if v, ok := parseGraphMemoryEnvNumber(getenv(graphMemoryEnvMaxPrefix+knob.name), knob); ok {
			cur := getGraphMemoryTunable(limits.Ceilings, i)
			if v < cur { // ceilings only ratchet down
				setGraphMemoryTunable(&limits.Ceilings, i, v)
			}
		}
	}
	limits.DiveProviders = splitGraphMemoryEnvList(getenv(graphMemoryDiveProvidersEnv))
	limits.DiveModels = splitGraphMemoryEnvList(getenv(graphMemoryDiveModelsEnv))
	return limits
}

// parseGraphMemoryEnvNumber parses an env override. The second return value
// is false for empty, unparseable, non-finite, or out-of-storage-bound input
// so bad env can never weaken the built-in posture.
func parseGraphMemoryEnvNumber(raw string, knob graphMemoryEnvKnob) (float64, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, false
	}
	v, err := strconv.ParseFloat(raw, 64)
	if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
		return 0, false
	}
	if v < 0 || v > float64(knob.storage) {
		return 0, false
	}
	if !knob.isFloat && v != math.Trunc(v) {
		return 0, false
	}
	return v, true
}

func splitGraphMemoryEnvList(raw string) []string {
	var out []string
	for _, part := range strings.Split(raw, ",") {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func graphMemoryTunableFields(t *GraphMemoryTunables) []any {
	return []any{
		&t.TTTConcurrency, &t.ExploreMaxRounds, &t.ExploreNodesPerExpansion,
		&t.MaxHierarchyFanout, &t.MaxRelationEdgesPerNode, &t.DiveMaxRounds,
		&t.DiveMaxViewedNodes, &t.DiveMaxSourceFiles, &t.DiveTimeoutSeconds,
		&t.WRound, &t.SourceMaxFileBytes, &t.SourceMaxTotalBytes,
		&t.SourceMaxPDFPages, &t.SourceMaxAVSeconds, &t.SourceMaxImageMegapixels,
	}
}

func getGraphMemoryTunable(t GraphMemoryTunables, i int) float64 {
	f := graphMemoryTunableFields(&t)[i]
	switch p := f.(type) {
	case *int:
		return float64(*p)
	case *int64:
		return float64(*p)
	case *float64:
		return *p
	}
	return 0
}

func setGraphMemoryTunable(t *GraphMemoryTunables, i int, v float64) {
	f := graphMemoryTunableFields(t)[i]
	switch p := f.(type) {
	case *int:
		*p = int(v)
	case *int64:
		*p = int64(v)
	case *float64:
		*p = v
	}
}

// graphMemoryTunableWireNames matches graphMemoryEnvKnobs order and names
// the profile columns in validation errors.
var graphMemoryTunableWireNames = []string{
	"ttt_concurrency", "explore_max_rounds", "explore_nodes_per_expansion",
	"max_hierarchy_fanout", "max_relation_edges_per_node", "dive_max_rounds",
	"dive_max_viewed_nodes", "dive_max_source_files", "dive_timeout_seconds",
	"w_round", "source_max_file_bytes", "source_max_total_bytes",
	"source_max_pdf_pages", "source_max_av_seconds", "source_max_image_megapixels",
}

// Validate rejects workspace tunables that are non-finite, negative where
// invalid, or above the server ceiling (spec §16 fail-closed numerics).
func (l GraphMemoryLimits) Validate(t GraphMemoryTunables) error {
	for i, name := range graphMemoryTunableWireNames {
		v := getGraphMemoryTunable(t, i)
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return fmt.Errorf("%s must be finite", name)
		}
		if v < 0 || (i != indexOfGraphMemoryKnob("W_ROUND") && v < 1) {
			return fmt.Errorf("%s is out of range", name)
		}
		if v > getGraphMemoryTunable(l.Ceilings, i) {
			return fmt.Errorf("%s exceeds the server safety ceiling", name)
		}
	}
	return nil
}

func indexOfGraphMemoryKnob(name string) int {
	for i, k := range graphMemoryEnvKnobs {
		if k.name == name {
			return i
		}
	}
	return -1
}

// ValidateDiveOverride gates workspace Dive model/provider overrides behind
// the server allow-list (brief D24). Empty provider and model mean "inherit
// the Explore model/provider" and always pass.
func (l GraphMemoryLimits) ValidateDiveOverride(provider, model string) error {
	provider = strings.TrimSpace(provider)
	model = strings.TrimSpace(model)
	if provider == "" && model == "" {
		return nil
	}
	if provider == "" || model == "" {
		return fmt.Errorf("dive_model and dive_provider must be set together")
	}
	if !graphMemoryListContains(l.DiveProviders, provider) {
		return fmt.Errorf("dive_provider %q is outside the server safety policy", provider)
	}
	if !graphMemoryListContains(l.DiveModels, model) {
		return fmt.Errorf("dive_model %q is outside the server safety policy", model)
	}
	return nil
}

func graphMemoryListContains(list []string, v string) bool {
	for _, item := range list {
		if item == v {
			return true
		}
	}
	return false
}
