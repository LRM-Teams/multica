// SPDX-License-Identifier: Apache-2.0

package handler

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/multica-ai/multica/server/internal/memorygraph"
	"github.com/multica-ai/multica/server/internal/service"
	"github.com/multica-ai/multica/server/internal/util"
)

func TestGraphMemoryAttachBacktestGroundTruth(t *testing.T) {
	if testPool == nil {
		t.Skip("handler test database unavailable")
	}
	ctx := context.Background()
	fx := mustGraphMemoryRecallFixture(t)
	recallID := mustInsertGraphMemoryRecall(t, fx, "backtest-attach-"+uuid.NewString()[:8])
	mustInsertGraphMemoryTrajectory(t, fx, recallID, 0)
	mustInsertGraphMemoryTrajectory(t, fx, recallID, 1)
	mustTerminalTrajectoryWithRounds(t, recallID, 0, "found", 4)
	mustTerminalTrajectoryWithRounds(t, recallID, 1, "miss", 9)
	catalog := service.NewGraphMemoryInfoCatalogService(testPool)
	if _, err := catalog.UpsertDiveInformationItems(ctx, testPool, util.UUIDToString(recallID), []memorygraph.DiveInformationItem{
		{Statement: "Fact one", NodeIDs: []string{"n1", "n1b"}, SourceRefs: []string{"source-1"}},
		{Statement: "Fact two", NodeIDs: []string{"n2"}, SourceRefs: []string{"source-2"}},
	}, true); err != nil {
		t.Fatal(err)
	}
	q := &memorygraph.BacktestQuery{TraceID: "backtest-attach-" + "placeholder", BaselineRounds: 2, BaselineFound: false}
	if _, err := testPool.Exec(ctx, `UPDATE graph_memory_recall SET trace_id = $2, query = 'ledger query' WHERE id = $1`, recallID, q.TraceID); err != nil {
		t.Fatal(err)
	}
	if err := catalog.AttachBacktestGroundTruth(ctx, "project", util.UUIDToString(fx.projectID), []*memorygraph.BacktestQuery{q}); err != nil {
		t.Fatal(err)
	}
	if len(q.Items) != 2 || q.BaselineRounds != 4 || !q.BaselineFound {
		t.Fatalf("query = %+v, want two items and ledger baseline 4/found", q)
	}
	if q.Items[0].Statement != "fact one" || len(q.Items[0].NodeIDs) != 2 || len(q.Items[1].SourceRefs) != 1 {
		t.Fatalf("attached items = %+v", q.Items)
	}
	if q.Query != "ledger query" {
		t.Fatalf("query text = %q, want ledger query", q.Query)
	}
}

func TestGraphMemoryAttachBacktestGroundTruthIncompleteUnknownAndPagination(t *testing.T) {
	if testPool == nil {
		t.Skip("handler test database unavailable")
	}
	ctx := context.Background()
	fx := mustGraphMemoryRecallFixture(t)
	catalog := service.NewGraphMemoryInfoCatalogService(testPool)

	incomplete := mustInsertGraphMemoryRecall(t, fx, "backtest-incomplete-"+uuid.NewString()[:8])
	if _, err := catalog.UpsertDiveInformationItems(ctx, testPool, util.UUIDToString(incomplete), []memorygraph.DiveInformationItem{{Statement: "Incomplete", NodeIDs: []string{"old"}}}, false); err != nil {
		t.Fatal(err)
	}
	incompleteQ := &memorygraph.BacktestQuery{TraceID: "backtest-incomplete-" + "placeholder"}
	if _, err := testPool.Exec(ctx, `UPDATE graph_memory_recall SET trace_id = $2 WHERE id = $1`, incomplete, incompleteQ.TraceID); err != nil {
		t.Fatal(err)
	}

	// Insert one full keyset page before the target. The target's later
	// timestamp must be discovered exactly once on the second page.
	for i := 0; i < 500; i++ {
		id := mustInsertGraphMemoryRecall(t, fx, fmt.Sprintf("backtest-page-%03d-%s", i, uuid.NewString()[:6]))
		if _, err := testPool.Exec(ctx, `UPDATE graph_memory_recall SET created_at = now() - interval '1 hour' WHERE id = $1`, id); err != nil {
			t.Fatal(err)
		}
	}
	targetTrace := "backtest-page-target-" + uuid.NewString()[:8]
	target := mustInsertGraphMemoryRecall(t, fx, targetTrace)
	mustInsertGraphMemoryTrajectory(t, fx, target, 0)
	mustTerminalTrajectoryWithRounds(t, target, 0, "found", 6)
	if _, err := catalog.UpsertDiveInformationItems(ctx, testPool, util.UUIDToString(target), []memorygraph.DiveInformationItem{{Statement: "Paged fact", NodeIDs: []string{"paged"}}}, true); err != nil {
		t.Fatal(err)
	}

	unknown := &memorygraph.BacktestQuery{TraceID: "not-in-ledger", BaselineRounds: 2, BaselineFound: false}
	targetQ := &memorygraph.BacktestQuery{TraceID: targetTrace, BaselineRounds: 2, BaselineFound: false}
	if err := catalog.AttachBacktestGroundTruth(ctx, "project", util.UUIDToString(fx.projectID), []*memorygraph.BacktestQuery{incompleteQ, unknown, targetQ}); err != nil {
		t.Fatal(err)
	}
	if len(incompleteQ.Items) != 0 {
		t.Fatalf("incomplete items entered backtest: %+v", incompleteQ.Items)
	}
	if unknown.BaselineRounds != 2 || unknown.BaselineFound || len(unknown.Items) != 0 {
		t.Fatalf("unknown query changed: %+v", unknown)
	}
	if len(targetQ.Items) != 1 || targetQ.BaselineRounds != 6 || !targetQ.BaselineFound {
		t.Fatalf("paged query = %+v, want one item and ledger baseline 6/found", targetQ)
	}

	// Keep time imported in this DB-backed test while making the initial
	// watermark behavior explicit: later recalls are outside a completed call.
	_ = time.Second
}
