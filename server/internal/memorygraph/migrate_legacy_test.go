package memorygraph

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMigrateLegacyQueryLogsMarksFlatEntriesAndExcludesBacktests(t *testing.T) {
	store := newTestStore(t)
	seedGraphNode(t, store, 1, "n1", "alpha")
	if err := store.AppendQueryLog("mixed", &QueryLogEntry{
		TraceID: "new-trace", Query: "new query", Version: 1, Found: true, Rounds: 2,
		JudgeDone: true, JudgeScore: 0.9, RelevantNodes: []string{"n1"},
		InfoItems: []BacktestItem{{ID: "item-1", Statement: "alpha", NodeIDs: []string{"n1"}}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendQueryLog("mixed", &QueryLogEntry{
		Query: "legacy query", Version: 1, Found: true, Rounds: 3,
		JudgeDone: true, JudgeScore: 0.7, RelevantNodes: []string{"n1"},
	}); err != nil {
		t.Fatal(err)
	}

	result, err := MigrateLegacyQueryLogs(store)
	if err != nil {
		t.Fatalf("MigrateLegacyQueryLogs: %v", err)
	}
	if result.Scanned != 2 || result.Marked != 1 || result.Quarantined != 0 {
		t.Fatalf("migration result = %+v, want scanned=2 marked=1 quarantined=0", result)
	}
	entries, err := store.ReadQueryLog("mixed")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 || entries[0].LegacyNonAuthoritative || !entries[1].LegacyNonAuthoritative {
		t.Fatalf("migrated entries = %+v, want only flat entry marked", entries)
	}
	if len(entries[0].InfoItems) != 1 || entries[0].TraceID != "new-trace" {
		t.Fatalf("new-format entry changed: %+v", entries[0])
	}
	queries, err := BacktestQueries(store, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(queries) != 1 || queries[0].TraceID != "new-trace" {
		t.Fatalf("BacktestQueries = %+v, want only the new-format query", queries)
	}
}

func TestMigrateLegacyQueryLogsIsIdempotentAndResumesPartialWindow(t *testing.T) {
	store := newTestStore(t)
	for _, query := range []string{"first", "second"} {
		if err := store.AppendQueryLog("resume", &QueryLogEntry{Query: query, Version: 1, JudgeDone: true, JudgeScore: 0.8}); err != nil {
			t.Fatal(err)
		}
	}
	if found, err := store.UpdateQueryLogEntry("resume", "", func(e *QueryLogEntry) {
		e.LegacyNonAuthoritative = true
	}); err != nil || !found {
		t.Fatalf("simulate interrupted migration: found=%v err=%v", found, err)
	}

	first, err := MigrateLegacyQueryLogs(store)
	if err != nil {
		t.Fatal(err)
	}
	if first.Marked != 1 || first.Skipped != 1 {
		t.Fatalf("resumed migration result = %+v, want marked=1 skipped=1", first)
	}
	if _, err := os.Stat(filepath.Join(store.Root, "legacy_migration", "resume.json")); err != nil {
		t.Fatalf("window checkpoint missing: %v", err)
	}
	second, err := MigrateLegacyQueryLogs(store)
	if err != nil {
		t.Fatal(err)
	}
	if second.Marked != 0 || second.Skipped != 2 {
		t.Fatalf("second migration result = %+v, want marked=0 skipped=2", second)
	}
	entries, err := store.ReadQueryLog("resume")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if !entry.LegacyNonAuthoritative {
			t.Fatalf("partial migration did not converge: %+v", entries)
		}
	}
}

func TestMigrateLegacyQueryLogsDoesNotFabricateAuthoritativeData(t *testing.T) {
	store := newTestStore(t)
	legacy := &QueryLogEntry{
		Query: "legacy", Version: 1, Found: true, Rounds: 4, AgentRuns: 0,
		JudgeDone: true, JudgeScore: 0.42, RelevantNodes: []string{"recorded-node"},
	}
	if err := store.AppendQueryLog("audit", legacy); err != nil {
		t.Fatal(err)
	}
	if _, err := MigrateLegacyQueryLogs(store); err != nil {
		t.Fatal(err)
	}
	entries, err := store.ReadQueryLog("audit")
	if err != nil {
		t.Fatal(err)
	}
	got := entries[0]
	if !got.LegacyNonAuthoritative || got.Query != legacy.Query || got.JudgeScore != legacy.JudgeScore ||
		got.Found != legacy.Found || got.Rounds != legacy.Rounds || got.AgentRuns != 0 ||
		len(got.NodeIDs) != 0 || len(got.InfoItems) != 0 || got.LedgerID != "" || got.TrajectoryID != "" {
		t.Fatalf("migration fabricated or changed legacy fields: %+v", got)
	}
	if _, err := os.Stat(filepath.Join(store.Root, "info_catalog.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("migration must not fabricate a catalog file, stat err=%v", err)
	}
}

func TestMigrateLegacyQueryLogsQuarantinesMalformedAndOverCeilingData(t *testing.T) {
	store := newTestStore(t)
	path := filepath.Join(store.Root, "query_log", "bad.jsonl")
	if err := os.WriteFile(path, []byte("{\"trace_id\":\n{\"query\":\"overflow\",\"judge_done\":true,\"judge_score\":1e300}\n{\"unexpected\":\"shape\"}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	result, err := MigrateLegacyQueryLogs(store)
	if err != nil {
		t.Fatal(err)
	}
	if result.Quarantined != 3 || result.Marked != 0 {
		t.Fatalf("migration result = %+v, want three quarantined entries", result)
	}
	entries, err := store.ReadQueryLog("bad")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("quarantined rows remained eligible in query log: %+v", entries)
	}
	quarantine, err := os.ReadFile(filepath.Join(store.Root, "legacy_migration", "quarantine.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(strings.TrimSpace(string(quarantine)), "\n") + 1; got != 3 {
		t.Fatalf("quarantine entries = %d, want 3: %s", got, quarantine)
	}
}

func TestMigrateLegacyQueryLogsLeavesOnlyLegacyStoreWithoutEligibleGroundTruth(t *testing.T) {
	store := newTestStore(t)
	seedGraphNode(t, store, 1, "n1", "alpha")
	candidate, err := store.CreateVersionFrom(1, "test")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AppendQueryLog("legacy", &QueryLogEntry{
		Query: "legacy", Version: 1, JudgeDone: true, JudgeScore: 1, RelevantNodes: []string{"n1"},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := MigrateLegacyQueryLogs(store); err != nil {
		t.Fatal(err)
	}
	queries, err := BacktestQueries(store, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(queries) != 0 {
		t.Fatalf("fully legacy store produced backtest queries: %+v", queries)
	}
	stats := NewBacktester(store, BacktestConfig{}).EvaluateCandidate(t.Context(), candidate, 1, queries)
	if stats.Passed || !strings.Contains(strings.Join(stats.GateFailures, ";"), "no_eligible_backtest_ground_truth") {
		t.Fatalf("candidate = %+v, want no_eligible_backtest_ground_truth", stats)
	}
}
