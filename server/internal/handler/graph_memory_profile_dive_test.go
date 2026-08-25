package handler

import (
	"context"
	"strings"
	"testing"
)

// Spec §2 / brief D2+D25: the workspace graph-memory profile carries the
// Dive-Judge-era tunables with the specified defaults. explore_agents is the
// saved per-recall TTT concurrency K (D2); ttt_enabled gates whether K>1 is
// effective. Storage-level CHECKs reject out-of-range and non-finite values
// before they become authoritative (spec §16, A31).
func TestGraphMemoryProfileDiveTunableDefaults(t *testing.T) {
	if testPool == nil {
		t.Skip("handler test database unavailable")
	}
	ctx := context.Background()
	workspaceID := createGraphMemoryTestWorkspace(t)
	mustGraphMemoryMember(t, workspaceID, "owner")
	if _, err := testPool.Exec(ctx,
		`INSERT INTO graph_memory_profile (workspace_id, memory_type) VALUES ($1, 'graph')`, workspaceID); err != nil {
		t.Fatal(err)
	}
	var (
		tttEnabled               bool
		savedK                   int32
		nodesPerExpansion        int32
		maxHierarchyFanout       int32
		maxRelationEdges         int32
		diveMaxRounds            int32
		diveMaxViewedNodes       int32
		diveMaxSourceFiles       int32
		diveTimeoutSeconds       int32
		wRound                   float64
		sourceMaxFileBytes       int64
		sourceMaxTotalBytes      int64
		sourceMaxPDFPages        int32
		sourceMaxAVSeconds       int32
		sourceMaxImageMegapixels int32
		diveModel                string
		diveProvider             string
		configVersion            int64
		schemaVersion            int32
	)
	err := testPool.QueryRow(ctx, `
		SELECT ttt_enabled, explore_agents, explore_nodes_per_expansion,
		       max_hierarchy_fanout, max_relation_edges_per_node,
		       dive_max_rounds, dive_max_viewed_nodes, dive_max_source_files,
		       dive_timeout_seconds, w_round,
		       source_max_file_bytes, source_max_total_bytes, source_max_pdf_pages,
		       source_max_av_seconds, source_max_image_megapixels,
		       dive_model, dive_provider, config_version, schema_version
		FROM graph_memory_profile WHERE workspace_id = $1`, workspaceID.String()).Scan(
		&tttEnabled, &savedK, &nodesPerExpansion, &maxHierarchyFanout, &maxRelationEdges,
		&diveMaxRounds, &diveMaxViewedNodes, &diveMaxSourceFiles, &diveTimeoutSeconds, &wRound,
		&sourceMaxFileBytes, &sourceMaxTotalBytes, &sourceMaxPDFPages, &sourceMaxAVSeconds,
		&sourceMaxImageMegapixels, &diveModel, &diveProvider, &configVersion, &schemaVersion)
	if err != nil {
		t.Fatalf("profile dive tunables: %v", err)
	}
	if tttEnabled {
		t.Fatal("ttt_enabled must default to false (existing profiles migrate TTT-disabled)")
	}
	want := map[string]int64{
		"saved_k explore_agents":      4,
		"explore_nodes_per_expansion": 1,
		"max_hierarchy_fanout":        8,
		"max_relation_edges_per_node": 8,
		"dive_max_rounds":             6,
		"dive_max_viewed_nodes":       24,
		"dive_max_source_files":       4,
		"dive_timeout_seconds":        600,
		"source_max_file_bytes":       20 << 20,
		"source_max_total_bytes":      50 << 20,
		"source_max_pdf_pages":        50,
		"source_max_av_seconds":       600,
		"source_max_image_megapixels": 40,
		"config_version":              1,
		"schema_version":              1,
	}
	got := map[string]int64{
		"saved_k explore_agents":      int64(savedK),
		"explore_nodes_per_expansion": int64(nodesPerExpansion),
		"max_hierarchy_fanout":        int64(maxHierarchyFanout),
		"max_relation_edges_per_node": int64(maxRelationEdges),
		"dive_max_rounds":             int64(diveMaxRounds),
		"dive_max_viewed_nodes":       int64(diveMaxViewedNodes),
		"dive_max_source_files":       int64(diveMaxSourceFiles),
		"dive_timeout_seconds":        int64(diveTimeoutSeconds),
		"source_max_file_bytes":       sourceMaxFileBytes,
		"source_max_total_bytes":      sourceMaxTotalBytes,
		"source_max_pdf_pages":        int64(sourceMaxPDFPages),
		"source_max_av_seconds":       int64(sourceMaxAVSeconds),
		"source_max_image_megapixels": int64(sourceMaxImageMegapixels),
		"config_version":              configVersion,
		"schema_version":              int64(schemaVersion),
	}
	for name, wantVal := range want {
		if got[name] != wantVal {
			t.Fatalf("%s default = %d, want %d", name, got[name], wantVal)
		}
	}
	if wRound != 0.1 {
		t.Fatalf("w_round default = %v, want 0.1", wRound)
	}
	if diveModel != "" || diveProvider != "" {
		t.Fatalf("dive model/provider default must be empty (inherit Explore), got %q/%q", diveModel, diveProvider)
	}
}

// Spec §16 / A31: numeric boundaries fail closed at the storage layer —
// negative, zero-where-invalid, over-ceiling, and non-finite values never
// become authoritative profile state.
func TestGraphMemoryProfileTunableStorageBounds(t *testing.T) {
	if testPool == nil {
		t.Skip("handler test database unavailable")
	}
	ctx := context.Background()
	workspaceID := createGraphMemoryTestWorkspace(t)
	mustGraphMemoryMember(t, workspaceID, "owner")
	if _, err := testPool.Exec(ctx,
		`INSERT INTO graph_memory_profile (workspace_id, memory_type) VALUES ($1, 'graph')`, workspaceID); err != nil {
		t.Fatal(err)
	}
	badUpdates := map[string]string{
		"zero fanout":        `UPDATE graph_memory_profile SET max_hierarchy_fanout = 0 WHERE workspace_id = $1`,
		"negative relation":  `UPDATE graph_memory_profile SET max_relation_edges_per_node = -1 WHERE workspace_id = $1`,
		"zero view width":    `UPDATE graph_memory_profile SET explore_nodes_per_expansion = 0 WHERE workspace_id = $1`,
		"negative w_round":   `UPDATE graph_memory_profile SET w_round = -0.5 WHERE workspace_id = $1`,
		"NaN w_round":        `UPDATE graph_memory_profile SET w_round = 'NaN'::float8 WHERE workspace_id = $1`,
		"+Inf w_round":       `UPDATE graph_memory_profile SET w_round = 'Infinity'::float8 WHERE workspace_id = $1`,
		"over-ceiling dives": `UPDATE graph_memory_profile SET dive_max_rounds = 100000 WHERE workspace_id = $1`,
		"zero timeout":       `UPDATE graph_memory_profile SET dive_timeout_seconds = 0 WHERE workspace_id = $1`,
	}
	for name, q := range badUpdates {
		if _, err := testPool.Exec(ctx, q, workspaceID.String()); err == nil {
			t.Fatalf("%s: update must be rejected by storage CHECK", name)
		} else if !strings.Contains(err.Error(), "23514") {
			t.Fatalf("%s: want check violation (23514), got %v", name, err)
		}
	}
	// The row remains at its defaults after every rejected write.
	var fanout int32
	if err := testPool.QueryRow(ctx,
		`SELECT max_hierarchy_fanout FROM graph_memory_profile WHERE workspace_id = $1`, workspaceID.String()).Scan(&fanout); err != nil {
		t.Fatal(err)
	}
	if fanout != 8 {
		t.Fatalf("rejected writes must leave the row unchanged; fanout=%d", fanout)
	}
}
