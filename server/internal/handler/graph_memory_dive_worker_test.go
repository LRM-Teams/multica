// SPDX-License-Identifier: Apache-2.0

package handler

import (
	"context"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/memorygraph"
	"github.com/multica-ai/multica/server/internal/service"
	"github.com/multica-ai/multica/server/internal/util"
	"github.com/multica-ai/multica/server/pkg/agent"
)

// Spec §5/§6/§7, acceptance A8/A12/A25/A29: the Dive worker leases a durable
// job, grades pinned-version trajectories, persists scores through
// ApplyDiveResult, enqueues online-RL rewards before Complete, and fail-closes
// on missing pins, exec errors, and fenced leases.

type scriptedDiveBackend struct {
	mu     sync.Mutex
	output string
	err    error
	calls  int
}

func (s *scriptedDiveBackend) Execute(_ context.Context, _ string, _ agent.ExecOptions) (*agent.Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	if s.err != nil {
		return nil, s.err
	}
	return diveCompletedSession(s.output), nil
}

func (s *scriptedDiveBackend) setOutput(out string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.output = out
}

func (s *scriptedDiveBackend) setErr(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.err = err
}

func diveCompletedSession(output string) *agent.Session {
	msgs := make(chan agent.Message)
	close(msgs)
	results := make(chan agent.Result, 1)
	results <- agent.Result{Status: "completed", Output: output}
	close(results)
	return &agent.Session{Messages: msgs, Result: results}
}

func diveScoresJSON(foundID, missID string) string {
	return fmt.Sprintf(`{"scores": [
  {"trajectory_id": %q, "relevance": 0.9, "groundedness": 0.4, "completeness": 0.7},
  {"trajectory_id": %q, "relevance": 0.2, "groundedness": 0.2, "completeness": 0.2}
], "necessary_information": [{"statement": "router retries use backoff", "source_refs": ["seg:s1"], "node_ids": ["n1"], "rationale": "audit trail"}], "incomplete": false}`, foundID, missID)
}

func mustSeedPinnedGraph(t *testing.T, root string, fx recallLedgerFixture) *memorygraph.Store {
	t.Helper()
	ws := util.UUIDToString(fx.workspaceID)
	owner := util.UUIDToString(fx.projectID)
	dir, err := memorygraph.EnsureScopedDir(root, ws, memorygraph.GraphDirKindProject, owner)
	if err != nil {
		t.Fatalf("EnsureScopedDir: %v", err)
	}
	store := memorygraph.NewStore(dir)
	if err := store.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	n := &memorygraph.Node{
		NodeID:         "n1",
		Body:           "the dispatch router retries failed batch jobs with exponential backoff",
		Epistemic:      memorygraph.StatusAccepted,
		TemporalStatus: memorygraph.TemporalCurrent,
		CreatedBy:      memorygraph.CreatorIngester,
		CreatedVersion: 1,
		UpdatedVersion: 1,
		ObservedAt:     time.Now().UTC(),
	}
	if err := store.SaveNode(1, n); err != nil {
		t.Fatalf("SaveNode: %v", err)
	}
	return store
}

func mustEnqueueReadyDive(t *testing.T, recallID pgtype.UUID, statuses ...string) {
	t.Helper()
	for i, st := range statuses {
		rounds := 0
		switch st {
		case "found":
			rounds = 3
		case "miss":
			rounds = 5
		}
		mustTerminalTrajectoryWithRounds(t, recallID, i, st, rounds)
	}
	ctx := context.Background()
	dive := service.NewGraphMemoryDiveService(testPool)
	if _, err := dive.EnqueueIfBarrierMet(ctx, util.UUIDToString(recallID)); err != nil {
		t.Fatalf("EnqueueIfBarrierMet: %v", err)
	}
}

func defaultTestLimits() service.GraphMemoryLimits {
	return service.LoadGraphMemoryLimits(func(string) string { return "" })
}

func newTestDiveWorker(t *testing.T, root string, rl *service.GraphMemoryRLSessionService, backend memorygraph.AgentBackend, limits service.GraphMemoryLimits) *service.GraphMemoryDiveWorker {
	t.Helper()
	if limits.Defaults == (service.GraphMemoryTunables{}) {
		limits = defaultTestLimits()
	}
	return service.NewGraphMemoryDiveWorker(
		testPool,
		service.NewGraphMemoryDiveService(testPool),
		rl,
		limits,
		root,
		func(context.Context, string, string) (memorygraph.AgentBackend, error) {
			return backend, nil
		},
	)
}

func TestGraphMemoryDiveWorkerEndToEnd(t *testing.T) {
	if testPool == nil {
		t.Skip("handler test database unavailable")
	}
	ctx := context.Background()
	fx, recallID := mustGraphMemoryDiveFixture(t, "trace-dive-worker-e2e-"+uuid.NewString()[:8], 3)
	mustEnqueueReadyDive(t, recallID, "found", "miss", "error")
	foundID := trajectoryIDBySeed(t, recallID, 0)
	missID := trajectoryIDBySeed(t, recallID, 1)
	errID := trajectoryIDBySeed(t, recallID, 2)

	root := t.TempDir()
	mustSeedPinnedGraph(t, root, fx)
	backend := &scriptedDiveBackend{output: diveScoresJSON(foundID, missID)}
	w := newTestDiveWorker(t, root, nil, backend, service.GraphMemoryLimits{})

	did, err := w.RunOnce(ctx, "worker-e2e")
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if !did {
		t.Fatal("RunOnce reported no work")
	}

	type row struct {
		diveStatus                    string
		relevance, groundedness, comp *float64
		overall, reward               *float64
	}
	read := func(tid string) row {
		var r row
		if err := testPool.QueryRow(ctx, `
			SELECT dive_status, score_relevance, score_groundedness, score_completeness, overall_score, reward
			FROM graph_memory_trajectory WHERE id = $1
		`, tid).Scan(&r.diveStatus, &r.relevance, &r.groundedness, &r.comp, &r.overall, &r.reward); err != nil {
			t.Fatal(err)
		}
		return r
	}
	r1 := read(foundID)
	if r1.diveStatus != "graded" || r1.relevance == nil || *r1.relevance != 0.9 ||
		r1.overall == nil || *r1.overall != 0.4 || r1.reward == nil || *r1.reward < 0.1-1e-9 || *r1.reward > 0.1+1e-9 {
		t.Fatalf("found row = %+v, want graded/overall 0.4/reward 0.1", r1)
	}
	r2 := read(missID)
	if r2.diveStatus != "graded" || r2.reward == nil || *r2.reward < -0.3-1e-9 || *r2.reward > -0.3+1e-9 {
		t.Fatalf("miss row = %+v, want graded reward −0.3", r2)
	}
	r3 := read(errID)
	if r3.diveStatus != "bypassed" || r3.reward == nil || *r3.reward != 0 {
		t.Fatalf("error row = %+v, want bypassed/reward 0", r3)
	}
	if status := recallStatus(t, recallID); status != "completed" {
		t.Fatalf("recall status = %s, want completed", status)
	}
	var jobStatus string
	if err := testPool.QueryRow(ctx, `
		SELECT status FROM graph_memory_dive_job WHERE recall_id = $1
	`, recallID).Scan(&jobStatus); err != nil {
		t.Fatal(err)
	}
	if jobStatus != "completed" {
		t.Fatalf("job status = %s, want completed", jobStatus)
	}

	items, err := service.NewGraphMemoryInfoCatalogService(testPool).ItemsForRecall(ctx, util.UUIDToString(recallID))
	if err != nil {
		t.Fatalf("ItemsForRecall: %v", err)
	}
	if len(items) != 1 || items[0].Status != "authoritative" || items[0].Statement != "router retries use backoff" {
		t.Fatalf("catalog = %+v, want one authoritative backoff item", items)
	}
}

func TestGraphMemoryDiveWorkerNoWork(t *testing.T) {
	if testPool == nil {
		t.Skip("handler test database unavailable")
	}
	w := newTestDiveWorker(t, t.TempDir(), nil, &scriptedDiveBackend{}, service.GraphMemoryLimits{})
	did, err := w.RunOnce(context.Background(), "worker-idle")
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if did {
		t.Fatal("empty queue must report no work")
	}
}

func TestGraphMemoryDiveWorkerPinnedVersionMissing(t *testing.T) {
	if testPool == nil {
		t.Skip("handler test database unavailable")
	}
	ctx := context.Background()
	fx, recallID := mustGraphMemoryDiveFixture(t, "trace-dive-worker-pin-"+uuid.NewString()[:8], 2)
	mustEnqueueReadyDive(t, recallID, "found", "miss")
	if _, err := testPool.Exec(ctx, `
		UPDATE graph_memory_dive_job SET max_attempts = 2 WHERE recall_id = $1
	`, recallID); err != nil {
		t.Fatal(err)
	}

	root := t.TempDir()
	store := mustSeedPinnedGraph(t, root, fx)
	if err := os.RemoveAll(store.VersionDir(1)); err != nil {
		t.Fatal(err)
	}
	w := newTestDiveWorker(t, root, nil, &scriptedDiveBackend{output: `{"scores":[],"necessary_information":[],"incomplete":false}`}, service.GraphMemoryLimits{})

	if did, err := w.RunOnce(ctx, "worker-pin"); err != nil || !did {
		t.Fatalf("RunOnce 1: did=%v err=%v", did, err)
	}
	var status string
	var attempts int
	if err := testPool.QueryRow(ctx, `
		SELECT status, attempts FROM graph_memory_dive_job WHERE recall_id = $1
	`, recallID).Scan(&status, &attempts); err != nil {
		t.Fatal(err)
	}
	if status != "queued" || attempts != 1 {
		t.Fatalf("after first miss: status=%s attempts=%d, want queued/1", status, attempts)
	}

	if did, err := w.RunOnce(ctx, "worker-pin"); err != nil || !did {
		t.Fatalf("RunOnce 2: did=%v err=%v", did, err)
	}
	if got := recallStatus(t, recallID); got != "judge_failed" {
		t.Fatalf("recall status = %s, want judge_failed", got)
	}
	rows, err := testPool.Query(ctx, `
		SELECT reward, dive_status FROM graph_memory_trajectory
		WHERE recall_id = $1 AND status IN ('found', 'miss')
	`, recallID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	n := 0
	for rows.Next() {
		var reward *float64
		var ds string
		if err := rows.Scan(&reward, &ds); err != nil {
			t.Fatal(err)
		}
		if reward == nil || *reward != 0 || ds != "judge_failed" {
			t.Fatalf("normal run after terminal fail: reward=%v dive_status=%q", reward, ds)
		}
		n++
	}
	if n != 2 {
		t.Fatalf("graded %d normal runs, want 2", n)
	}
}

func TestGraphMemoryDiveWorkerExecErrorThenRecover(t *testing.T) {
	if testPool == nil {
		t.Skip("handler test database unavailable")
	}
	ctx := context.Background()
	fx, recallID := mustGraphMemoryDiveFixture(t, "trace-dive-worker-exec-"+uuid.NewString()[:8], 2)
	mustEnqueueReadyDive(t, recallID, "found", "miss")
	foundID := trajectoryIDBySeed(t, recallID, 0)
	missID := trajectoryIDBySeed(t, recallID, 1)

	root := t.TempDir()
	mustSeedPinnedGraph(t, root, fx)
	backend := &scriptedDiveBackend{err: fmt.Errorf("judge backend 503")}
	w := newTestDiveWorker(t, root, nil, backend, service.GraphMemoryLimits{})

	if did, err := w.RunOnce(ctx, "worker-exec"); err != nil || !did {
		t.Fatalf("RunOnce fail: did=%v err=%v", did, err)
	}
	var status string
	if err := testPool.QueryRow(ctx, `
		SELECT status FROM graph_memory_dive_job WHERE recall_id = $1
	`, recallID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "queued" {
		t.Fatalf("after exec fail status = %s, want queued", status)
	}

	backend.setErr(nil)
	backend.setOutput(diveScoresJSON(foundID, missID))
	if did, err := w.RunOnce(ctx, "worker-exec"); err != nil || !did {
		t.Fatalf("RunOnce recover: did=%v err=%v", did, err)
	}
	if got := recallStatus(t, recallID); got != "completed" {
		t.Fatalf("recall status = %s, want completed", got)
	}
}

func TestGraphMemoryDiveWorkerOnlineRLOutbox(t *testing.T) {
	if testPool == nil {
		t.Skip("handler test database unavailable")
	}
	ctx := context.Background()
	fx, recallID := mustGraphMemoryDiveFixture(t, "trace-dive-worker-rl-"+uuid.NewString()[:8], 3)
	if _, err := testPool.Exec(ctx, `
		UPDATE graph_memory_recall SET training_mode = 'online_rl' WHERE id = $1
	`, recallID); err != nil {
		t.Fatal(err)
	}
	mustEnqueueReadyDive(t, recallID, "found", "miss", "error")
	foundID := trajectoryIDBySeed(t, recallID, 0)
	missID := trajectoryIDBySeed(t, recallID, 1)
	errID := trajectoryIDBySeed(t, recallID, 2)

	root := t.TempDir()
	mustSeedPinnedGraph(t, root, fx)
	backend := &scriptedDiveBackend{output: diveScoresJSON(foundID, missID)}
	rl := service.NewGraphMemoryRLSessionService(testPool, &fakeRLStarter{}, &fakeRLRemover{})
	mustOpenRLSession(t, rl, foundID)
	mustOpenRLSession(t, rl, missID)
	mustOpenRLSession(t, rl, errID)
	w := newTestDiveWorker(t, root, rl, backend, service.GraphMemoryLimits{})

	if did, err := w.RunOnce(ctx, "worker-rl"); err != nil || !did {
		t.Fatalf("RunOnce: did=%v err=%v", did, err)
	}
	if outboxRowCount(t, foundID) != 1 || outboxRowCount(t, missID) != 1 || outboxRowCount(t, errID) != 1 {
		t.Fatalf("outbox counts found/miss/error = %d/%d/%d, want 1/1/1",
			outboxRowCount(t, foundID), outboxRowCount(t, missID), outboxRowCount(t, errID))
	}
	_, foundReward, _, _ := outboxRow(t, foundID)
	_, missReward, _, _ := outboxRow(t, missID)
	_, errReward, _, _ := outboxRow(t, errID)
	if foundReward < 0.1-1e-9 || foundReward > 0.1+1e-9 {
		t.Fatalf("found outbox reward = %v, want 0.1", foundReward)
	}
	if missReward < -0.3-1e-9 || missReward > -0.3+1e-9 {
		t.Fatalf("miss outbox reward = %v, want −0.3", missReward)
	}
	if errReward != 0 {
		t.Fatalf("bypassed outbox reward = %v, want 0", errReward)
	}
}

func TestGraphMemoryDiveWorkerNilRLSkipsOutbox(t *testing.T) {
	if testPool == nil {
		t.Skip("handler test database unavailable")
	}
	ctx := context.Background()
	fx, recallID := mustGraphMemoryDiveFixture(t, "trace-dive-worker-nirl-"+uuid.NewString()[:8], 2)
	if _, err := testPool.Exec(ctx, `
		UPDATE graph_memory_recall SET training_mode = 'online_rl' WHERE id = $1
	`, recallID); err != nil {
		t.Fatal(err)
	}
	mustEnqueueReadyDive(t, recallID, "found", "miss")
	foundID := trajectoryIDBySeed(t, recallID, 0)
	missID := trajectoryIDBySeed(t, recallID, 1)
	root := t.TempDir()
	mustSeedPinnedGraph(t, root, fx)
	backend := &scriptedDiveBackend{output: diveScoresJSON(foundID, missID)}
	w := newTestDiveWorker(t, root, nil, backend, service.GraphMemoryLimits{})
	if did, err := w.RunOnce(ctx, "worker-nirl"); err != nil || !did {
		t.Fatalf("RunOnce: did=%v err=%v", did, err)
	}
	if outboxRowCount(t, foundID) != 0 || outboxRowCount(t, missID) != 0 {
		t.Fatal("nil rl must not write outbox rows")
	}
	if got := recallStatus(t, recallID); got != "completed" {
		t.Fatalf("recall status = %s, want completed", got)
	}
}

func TestGraphMemoryDiveWorkerModelOverride(t *testing.T) {
	if testPool == nil {
		t.Skip("handler test database unavailable")
	}
	ctx := context.Background()

	run := func(t *testing.T, model, provider string, allow service.GraphMemoryLimits) (gotModel, gotProvider string) {
		t.Helper()
		fx, recallID := mustGraphMemoryDiveFixture(t, "trace-dive-model-"+uuid.NewString()[:8], 2)
		mustEnqueueReadyDive(t, recallID, "found", "miss")
		foundID := trajectoryIDBySeed(t, recallID, 0)
		missID := trajectoryIDBySeed(t, recallID, 1)
		if _, err := testPool.Exec(ctx, `
			INSERT INTO graph_memory_profile (workspace_id, memory_type, dive_model, dive_provider)
			VALUES ($1, 'graph', $2, $3)
		`, fx.workspaceID, model, provider); err != nil {
			t.Fatal(err)
		}
		root := t.TempDir()
		mustSeedPinnedGraph(t, root, fx)
		backend := &scriptedDiveBackend{output: diveScoresJSON(foundID, missID)}
		var seenModel, seenProvider string
		limits := allow
		if limits.Defaults == (service.GraphMemoryTunables{}) {
			limits.Defaults = defaultTestLimits().Defaults
		}
		w := service.NewGraphMemoryDiveWorker(
			testPool,
			service.NewGraphMemoryDiveService(testPool),
			nil,
			limits,
			root,
			func(_ context.Context, m, p string) (memorygraph.AgentBackend, error) {
				seenModel, seenProvider = m, p
				return backend, nil
			},
		)
		if did, err := w.RunOnce(ctx, "worker-model"); err != nil || !did {
			t.Fatalf("RunOnce: did=%v err=%v", did, err)
		}
		return seenModel, seenProvider
	}

	t.Run("within allow-list", func(t *testing.T) {
		limits := defaultTestLimits()
		limits.DiveModels = []string{"gpt-dive"}
		limits.DiveProviders = []string{"pi"}
		m, p := run(t, "gpt-dive", "pi", limits)
		if m != "gpt-dive" || p != "pi" {
			t.Fatalf("factory got (%q, %q), want (gpt-dive, pi)", m, p)
		}
	})
	t.Run("outside allow-list fails closed", func(t *testing.T) {
		limits := defaultTestLimits()
		limits.DiveModels = []string{"gpt-dive"}
		limits.DiveProviders = []string{"pi"}
		m, p := run(t, "evil-model", "pi", limits)
		if m != "" || p != "" {
			t.Fatalf("factory got (%q, %q), want empty default", m, p)
		}
	})
	t.Run("empty allow-list fails closed", func(t *testing.T) {
		limits := defaultTestLimits()
		m, p := run(t, "gpt-dive", "pi", limits)
		if m != "" || p != "" {
			t.Fatalf("empty allow-list factory got (%q, %q), want empty default", m, p)
		}
	})
}

func TestGraphMemoryDiveWorkerFencedComplete(t *testing.T) {
	if testPool == nil {
		t.Skip("handler test database unavailable")
	}
	ctx := context.Background()
	fx, recallID := mustGraphMemoryDiveFixture(t, "trace-dive-worker-fence-"+uuid.NewString()[:8], 2)
	mustEnqueueReadyDive(t, recallID, "found", "miss")
	foundID := trajectoryIDBySeed(t, recallID, 0)
	missID := trajectoryIDBySeed(t, recallID, 1)
	root := t.TempDir()
	mustSeedPinnedGraph(t, root, fx)

	backend := &leaseExpiringDiveBackend{output: diveScoresJSON(foundID, missID)}
	w := newTestDiveWorker(t, root, nil, backend, service.GraphMemoryLimits{})
	did, err := w.RunOnce(ctx, "worker-fence")
	if err != nil {
		t.Fatalf("fenced RunOnce must not error: %v", err)
	}
	if !did {
		t.Fatal("fenced RunOnce still processed a job")
	}
	if status := recallStatus(t, recallID); status == "completed" {
		t.Fatal("expired lease must not complete the recall")
	}
	var diveStatus string
	if err := testPool.QueryRow(ctx, `
		SELECT COALESCE(dive_status, '') FROM graph_memory_trajectory WHERE id = $1
	`, foundID).Scan(&diveStatus); err != nil {
		t.Fatal(err)
	}
	if diveStatus == "graded" {
		t.Fatal("fenced apply must not persist grades")
	}
	items, err := service.NewGraphMemoryInfoCatalogService(testPool).ItemsForRecall(ctx, util.UUIDToString(recallID))
	if err != nil {
		t.Fatalf("ItemsForRecall: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("fenced apply wrote catalog items: %+v", items)
	}
}

type leaseExpiringDiveBackend struct {
	output string
}

func (b *leaseExpiringDiveBackend) Execute(ctx context.Context, _ string, _ agent.ExecOptions) (*agent.Session, error) {
	if _, err := testPool.Exec(ctx, `
		UPDATE graph_memory_dive_job SET lease_expires_at = now() - interval '1 second'
		WHERE status = 'running'
	`); err != nil {
		return nil, err
	}
	return diveCompletedSession(b.output), nil
}
