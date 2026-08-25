// SPDX-License-Identifier: Apache-2.0

package handler

import (
	"context"
	"sort"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/memorygraph"
	"github.com/multica-ai/multica/server/internal/service"
	"github.com/multica-ai/multica/server/internal/util"
)

// Spec §8 / §16: persistent graph-scoped necessary-information catalog with
// stable identities, per-query required links, incomplete/legacy exclusion
// from management backtests, and storage-level identity triggers.

func TestGraphMemoryInfoCatalogCreate(t *testing.T) {
	if testPool == nil {
		t.Skip("handler test database unavailable")
	}
	fx, recallID := mustOfflineRLExportFixture(t, "info-create-"+uuid.NewString()[:8], 1)
	mustTerminalTrajectoryWithRounds(t, recallID, 0, "found", 1)
	t0 := trajectoryIDBySeed(t, recallID, 0)
	items := []memorygraph.DiveInformationItem{
		{
			Statement:  "Router retries use backoff.",
			SourceRefs: []string{"src-a"},
			NodeIDs:    []string{"n1", "n2"},
			Rationale:  "from viewed nodes",
		},
		{
			Statement:  "Timeouts are retried once.",
			SourceRefs: []string{"src-b"},
			NodeIDs:    []string{"n3"},
			Rationale:  "from summary",
		},
	}
	mustGradeOfflineRecallWithInfo(t, recallID, []memorygraph.DiveTrajectoryScore{
		{TrajectoryID: t0, Relevance: 0.5, Groundedness: 0.5, Completeness: 0.5},
	}, items, false)

	catalog := service.NewGraphMemoryInfoCatalogService(testPool)
	got, err := catalog.ItemsForRecall(context.Background(), util.UUIDToString(recallID))
	if err != nil {
		t.Fatalf("ItemsForRecall: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("ItemsForRecall len = %d, want 2", len(got))
	}
	first := infoItemByStatement(t, got, "Router retries use backoff.")
	second := infoItemByStatement(t, got, "Timeouts are retried once.")
	if first.Status != "authoritative" || second.Status != "authoritative" {
		t.Fatalf("status = (%q, %q), want authoritative", first.Status, second.Status)
	}
	if !sameInfoNodeIDs(first.NodeIDs, []string{"n1", "n2"}) {
		t.Fatalf("first NodeIDs = %v, want [n1 n2]", first.NodeIDs)
	}
	if !sameInfoNodeIDs(second.NodeIDs, []string{"n3"}) {
		t.Fatalf("second NodeIDs = %v, want [n3]", second.NodeIDs)
	}

	eligible, err := catalog.BacktestEligibleItems(context.Background(), "project", util.UUIDToString(fx.projectID))
	if err != nil {
		t.Fatalf("BacktestEligibleItems: %v", err)
	}
	if len(eligible) != 2 {
		t.Fatalf("BacktestEligibleItems len = %d, want 2", len(eligible))
	}
}

func TestGraphMemoryInfoCatalogDedupStableID(t *testing.T) {
	if testPool == nil {
		t.Skip("handler test database unavailable")
	}
	fx, recall1 := mustOfflineRLExportFixture(t, "info-dedup-a-"+uuid.NewString()[:8], 1)
	mustTerminalTrajectoryWithRounds(t, recall1, 0, "found", 1)
	mustGradeOfflineRecallWithInfo(t, recall1, []memorygraph.DiveTrajectoryScore{
		{TrajectoryID: trajectoryIDBySeed(t, recall1, 0), Relevance: 0.5, Groundedness: 0.5, Completeness: 0.5},
	}, []memorygraph.DiveInformationItem{{
		Statement:  "The API uses exponential backoff.",
		SourceRefs: []string{"src-1"},
		NodeIDs:    []string{"node-a"},
		Rationale:  "first write",
	}}, false)

	catalog := service.NewGraphMemoryInfoCatalogService(testPool)
	first, err := catalog.ItemsForRecall(context.Background(), util.UUIDToString(recall1))
	if err != nil || len(first) != 1 {
		t.Fatalf("first ItemsForRecall: len=%d err=%v", len(first), err)
	}
	wantID := first[0].ID
	wantStatement := first[0].Statement

	recall2 := mustScopedOfflineRecall(t, fx, "info-dedup-b-"+uuid.NewString()[:8])
	mustTerminalTrajectoryWithRounds(t, recall2, 0, "found", 1)
	mustGradeOfflineRecallWithInfo(t, recall2, []memorygraph.DiveTrajectoryScore{
		{TrajectoryID: trajectoryIDBySeed(t, recall2, 0), Relevance: 0.6, Groundedness: 0.6, Completeness: 0.6},
	}, []memorygraph.DiveInformationItem{{
		Statement:  "  THE  api   USES   Exponential Backoff.  ",
		SourceRefs: []string{"src-2"},
		NodeIDs:    []string{"node-b"},
		Rationale:  "second write must not replace text",
	}}, false)

	second, err := catalog.ItemsForRecall(context.Background(), util.UUIDToString(recall2))
	if err != nil || len(second) != 1 {
		t.Fatalf("second ItemsForRecall: len=%d err=%v", len(second), err)
	}
	if second[0].ID != wantID {
		t.Fatalf("item id = %s, want stable %s", second[0].ID, wantID)
	}
	if second[0].Statement != wantStatement {
		t.Fatalf("statement = %q, want first-write %q", second[0].Statement, wantStatement)
	}
	if !sameInfoNodeIDs(second[0].NodeIDs, []string{"node-a", "node-b"}) {
		t.Fatalf("node union = %v, want [node-a node-b]", second[0].NodeIDs)
	}

	linked1, err := catalog.ItemsForRecall(context.Background(), util.UUIDToString(recall1))
	if err != nil || len(linked1) != 1 || linked1[0].ID != wantID {
		t.Fatalf("first recall lost its link: %+v err=%v", linked1, err)
	}
}

func TestGraphMemoryInfoCatalogMembershipIsNotRequired(t *testing.T) {
	if testPool == nil {
		t.Skip("handler test database unavailable")
	}
	fx, recall1 := mustOfflineRLExportFixture(t, "info-mem-a-"+uuid.NewString()[:8], 1)
	mustTerminalTrajectoryWithRounds(t, recall1, 0, "found", 1)
	mustGradeOfflineRecallWithInfo(t, recall1, []memorygraph.DiveTrajectoryScore{
		{TrajectoryID: trajectoryIDBySeed(t, recall1, 0), Relevance: 0.5, Groundedness: 0.5, Completeness: 0.5},
	}, []memorygraph.DiveInformationItem{{
		Statement: "Catalog membership is not per-query required.",
		NodeIDs:   []string{"n-mem"},
	}}, false)

	recall3 := mustScopedOfflineRecall(t, fx, "info-mem-c-"+uuid.NewString()[:8])
	mustTerminalTrajectoryWithRounds(t, recall3, 0, "found", 1)
	mustGradeOfflineRecallWithInfo(t, recall3, []memorygraph.DiveTrajectoryScore{
		{TrajectoryID: trajectoryIDBySeed(t, recall3, 0), Relevance: 0.5, Groundedness: 0.5, Completeness: 0.5},
	}, nil, false)

	catalog := service.NewGraphMemoryInfoCatalogService(testPool)
	got, err := catalog.ItemsForRecall(context.Background(), util.UUIDToString(recall3))
	if err != nil {
		t.Fatalf("ItemsForRecall: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("unlinked recall ItemsForRecall = %+v, want empty (catalog membership ≠ required)", got)
	}
	eligible, err := catalog.BacktestEligibleItems(context.Background(), "project", util.UUIDToString(fx.projectID))
	if err != nil {
		t.Fatalf("BacktestEligibleItems: %v", err)
	}
	if len(eligible) != 1 {
		t.Fatalf("scope still has the catalog item, eligible=%d want 1", len(eligible))
	}
}

func TestGraphMemoryInfoCatalogIncomplete(t *testing.T) {
	if testPool == nil {
		t.Skip("handler test database unavailable")
	}
	fx, recallAuth := mustOfflineRLExportFixture(t, "info-inc-auth-"+uuid.NewString()[:8], 1)
	mustTerminalTrajectoryWithRounds(t, recallAuth, 0, "found", 1)
	mustGradeOfflineRecallWithInfo(t, recallAuth, []memorygraph.DiveTrajectoryScore{
		{TrajectoryID: trajectoryIDBySeed(t, recallAuth, 0), Relevance: 0.5, Groundedness: 0.5, Completeness: 0.5},
	}, []memorygraph.DiveInformationItem{{
		Statement: "Authoritative sibling fact.",
		NodeIDs:   []string{"n-auth"},
	}}, false)

	recallInc := mustScopedOfflineRecall(t, fx, "info-inc-b-"+uuid.NewString()[:8])
	mustTerminalTrajectoryWithRounds(t, recallInc, 0, "found", 1)
	mustGradeOfflineRecallWithInfo(t, recallInc, []memorygraph.DiveTrajectoryScore{
		{TrajectoryID: trajectoryIDBySeed(t, recallInc, 0), Relevance: 0.4, Groundedness: 0.4, Completeness: 0.4},
	}, []memorygraph.DiveInformationItem{
		{
			Statement: "Authoritative sibling fact.",
			NodeIDs:   []string{"n-auth-again"},
			Rationale: "must not downgrade",
		},
		{
			Statement: "Incomplete-only fact.",
			NodeIDs:   []string{"n-inc"},
		},
	}, true)

	catalog := service.NewGraphMemoryInfoCatalogService(testPool)
	linked, err := catalog.ItemsForRecall(context.Background(), util.UUIDToString(recallInc))
	if err != nil {
		t.Fatalf("ItemsForRecall: %v", err)
	}
	auth := infoItemByStatement(t, linked, "Authoritative sibling fact.")
	inc := infoItemByStatement(t, linked, "Incomplete-only fact.")
	if auth.Status != "authoritative" {
		t.Fatalf("re-referenced item status = %q, want authoritative (no downgrade)", auth.Status)
	}
	if inc.Status != "incomplete" {
		t.Fatalf("new incomplete item status = %q, want incomplete", inc.Status)
	}

	eligible, err := catalog.BacktestEligibleItems(context.Background(), "project", util.UUIDToString(fx.projectID))
	if err != nil {
		t.Fatalf("BacktestEligibleItems: %v", err)
	}
	if len(eligible) != 1 || eligible[0].ID != auth.ID {
		t.Fatalf("eligible = %+v, want only the authoritative sibling", eligible)
	}
	for _, it := range eligible {
		if it.ID == inc.ID || it.Status == "incomplete" {
			t.Fatalf("incomplete item leaked into BacktestEligibleItems: %+v", it)
		}
	}
}

func TestGraphMemoryInfoCatalogIdentityTrigger(t *testing.T) {
	if testPool == nil {
		t.Skip("handler test database unavailable")
	}
	_, recallID := mustOfflineRLExportFixture(t, "info-id-a-"+uuid.NewString()[:8], 1)
	mustTerminalTrajectoryWithRounds(t, recallID, 0, "found", 1)
	mustGradeOfflineRecallWithInfo(t, recallID, []memorygraph.DiveTrajectoryScore{
		{TrajectoryID: trajectoryIDBySeed(t, recallID, 0), Relevance: 0.5, Groundedness: 0.5, Completeness: 0.5},
	}, []memorygraph.DiveInformationItem{{
		Statement: "Identity-trigger fact.",
		NodeIDs:   []string{"n-id"},
	}}, false)

	catalog := service.NewGraphMemoryInfoCatalogService(testPool)
	items, err := catalog.ItemsForRecall(context.Background(), util.UUIDToString(recallID))
	if err != nil || len(items) != 1 {
		t.Fatalf("seed item: len=%d err=%v", len(items), err)
	}

	_, otherRecall := mustOfflineRLExportFixture(t, "info-id-b-"+uuid.NewString()[:8], 1)
	if _, err := testPool.Exec(context.Background(), `
		INSERT INTO graph_memory_recall_info_item (recall_id, item_id)
		VALUES ($1, $2)
	`, otherRecall, items[0].ID); err == nil {
		t.Fatal("recall_info_item linking a recall from a different workspace must be rejected")
	}
}

func TestGraphMemoryInfoCatalogLegacyExcluded(t *testing.T) {
	if testPool == nil {
		t.Skip("handler test database unavailable")
	}
	fx, recallID := mustOfflineRLExportFixture(t, "info-legacy-"+uuid.NewString()[:8], 1)
	mustTerminalTrajectoryWithRounds(t, recallID, 0, "found", 1)
	mustGradeOfflineRecallWithInfo(t, recallID, []memorygraph.DiveTrajectoryScore{
		{TrajectoryID: trajectoryIDBySeed(t, recallID, 0), Relevance: 0.5, Groundedness: 0.5, Completeness: 0.5},
	}, []memorygraph.DiveInformationItem{{
		Statement: "Live authoritative fact.",
		NodeIDs:   []string{"n-live"},
	}}, false)

	if _, err := testPool.Exec(context.Background(), `
		INSERT INTO graph_memory_info_item (
		  workspace_id, graph_kind, graph_owner_id, statement, statement_hash, status
		) VALUES ($1, 'project', $2, 'legacy row', $3, 'legacy_non_authoritative')
	`, fx.workspaceID, fx.projectID, "legacy-hash-"+uuid.NewString()); err != nil {
		t.Fatalf("hand-insert legacy item: %v", err)
	}

	catalog := service.NewGraphMemoryInfoCatalogService(testPool)
	eligible, err := catalog.BacktestEligibleItems(context.Background(), "project", util.UUIDToString(fx.projectID))
	if err != nil {
		t.Fatalf("BacktestEligibleItems: %v", err)
	}
	if len(eligible) != 1 {
		t.Fatalf("eligible = %d, want 1 (legacy excluded)", len(eligible))
	}
	if eligible[0].Status != "authoritative" || eligible[0].Statement != "Live authoritative fact." {
		t.Fatalf("eligible item = %+v, want the live authoritative fact", eligible[0])
	}
}

func TestGraphMemoryInfoCatalogNormalizeInfoStatement(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"Hello World", "hello world"},
		{"  Hello   WORLD  ", "hello world"},
		{"Hello\t\nWorld", "hello world"},
		{"Hello,  World", "hello, world"},
		{"FOO  ,  BAR", "foo , bar"},
		{"a.  b", "a. b"},
		{"  ", ""},
		{"", ""},
	}
	for _, tc := range cases {
		if got := service.NormalizeInfoStatement(tc.in); got != tc.want {
			t.Errorf("NormalizeInfoStatement(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func mustGradeOfflineRecallWithInfo(t *testing.T, recallID pgtype.UUID, scores []memorygraph.DiveTrajectoryScore, items []memorygraph.DiveInformationItem, incomplete bool) {
	t.Helper()
	ctx := context.Background()
	dive := service.NewGraphMemoryDiveService(testPool)
	if _, err := dive.EnqueueIfBarrierMet(ctx, util.UUIDToString(recallID)); err != nil {
		t.Fatal(err)
	}
	job, err := dive.Lease(ctx, "catalog-worker", time.Minute)
	if err != nil || job == nil {
		t.Fatalf("lease: job=%v err=%v", job, err)
	}
	ok, err := dive.ApplyDiveResult(ctx, job.ID, "catalog-worker", &memorygraph.DiveResult{
		Scores:               scores,
		NecessaryInformation: items,
		Incomplete:           incomplete,
	}, 0.1)
	if err != nil || !ok {
		t.Fatalf("ApplyDiveResult: ok=%v err=%v", ok, err)
	}
}

func mustScopedOfflineRecall(t *testing.T, fx recallLedgerFixture, traceID string) pgtype.UUID {
	t.Helper()
	recallID := mustInsertGraphMemoryRecall(t, fx, traceID)
	if _, err := testPool.Exec(context.Background(), `
		UPDATE graph_memory_recall SET training_mode = 'offline_rl' WHERE id = $1
	`, recallID); err != nil {
		t.Fatal(err)
	}
	mustInsertGraphMemoryTrajectory(t, fx, recallID, 0)
	return recallID
}

func infoItemByStatement(t *testing.T, items []service.GraphMemoryInfoItem, statement string) service.GraphMemoryInfoItem {
	t.Helper()
	for _, it := range items {
		if it.Statement == statement {
			return it
		}
	}
	t.Fatalf("no catalog item with statement %q in %+v", statement, items)
	return service.GraphMemoryInfoItem{}
}

func sameInfoNodeIDs(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	gs := append([]string(nil), got...)
	ws := append([]string(nil), want...)
	sort.Strings(gs)
	sort.Strings(ws)
	for i := range gs {
		if gs[i] != ws[i] {
			return false
		}
	}
	return true
}
