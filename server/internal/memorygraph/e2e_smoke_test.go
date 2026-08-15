package memorygraph_test

// End-to-end smoke test of the graph memory reviewer against the REAL pi
// agent backend (no fakes): ingest -> consolidate (non-TTT) -> recall ->
// judge -> reward. This is the test that would have caught the dormant
// wiring bugs R1 (trajectory-less ingest), R3 (judge->reward chain no-op)
// and R10 (hard-coded judge baseline) from the review.
//
// Guarded by GRAPH_MEMORY_E2E=1 so CI never runs it. The pi binary is
// resolved from PI_E2E_BIN, defaulting to "pi" on PATH.
//
// Run with:
//
//	GRAPH_MEMORY_E2E=1 go test ./internal/memorygraph/ -run TestGraphMemoryE2ESmoke -count=1 -v -timeout 12m

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"strings"
	"testing"
	"time"

	agentpkg "github.com/multica-ai/multica/server/pkg/agent"

	mg "github.com/multica-ai/multica/server/internal/memorygraph"
)

// e2eKeyword is the distinctive token planted in the synthetic trajectory.
// The recall summary must mention it, proving the answer came from the
// ingested content rather than model prior knowledge.
const e2eKeyword = "frostbyte"

// e2eSegmentID is the staging segment the whole test pivots on.
const e2eSegmentID = "seg-e2e-0001"

// e2eWallBudget is the hard total wall-clock budget for the whole test.
const e2eWallBudget = 8 * time.Minute

func TestGraphMemoryE2ESmoke(t *testing.T) {
	if os.Getenv("GRAPH_MEMORY_E2E") != "1" {
		t.Skip("set GRAPH_MEMORY_E2E=1 to run the real-pi end-to-end smoke test")
	}
	testStart := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	// ── Stage 0: real pi backend + fail-fast probe ─────────────────────
	piBin := os.Getenv("PI_E2E_BIN")
	if piBin == "" {
		piBin = "pi"
	}
	backend, err := agentpkg.New("pi", agentpkg.Config{ExecutablePath: piBin})
	if err != nil {
		t.Fatalf("stage 0: build pi backend: %v", err)
	}
	probeStart := time.Now()
	probe := e2eExecute(t, ctx, backend, "probe",
		"Reply with exactly one JSON object and nothing else: {\"ok\":true}",
		3*time.Minute)
	if probe.Status != "completed" || !strings.Contains(probe.Output, "{") {
		t.Fatalf("stage 0: pi backend probe failed (status=%q error=%q output=%q) — cannot run E2E without a working pi CLI (%s)",
			probe.Status, probe.Error, truncateForLog(probe.Output, 200), piBin)
	}
	t.Logf("stage 0: pi probe OK in %s (output=%q)", time.Since(probeStart).Round(time.Millisecond), truncateForLog(probe.Output, 120))

	// ── Stage 1: ingest a synthetic segment with a realistic trajectory ──
	stageStart := time.Now()
	store := mg.NewStore(t.TempDir())
	if err := store.Init(); err != nil {
		t.Fatalf("stage 1: store init: %v", err)
	}
	ingester := mg.NewIngester(store, backend, "pi", "", 4*time.Minute)
	seg := mg.SegmentExport{
		SegmentID:    e2eSegmentID,
		AgentRunID:   "run-e2e-0001",
		ClosingEvent: "task_completed",
		Trajectory:   e2eTrajectory(t),
	}
	if err := ingester.Ingest(ctx, seg); err != nil {
		t.Fatalf("stage 1: ingest: %v", err)
	}
	staging, err := store.ReadStagingSegment(e2eSegmentID)
	if err != nil {
		t.Fatalf("stage 1: staging file for %s missing after ingest: %v", e2eSegmentID, err)
	}
	stagingText := string(staging)
	if strings.Contains(stagingText, "fallback: true") || strings.Contains(stagingText, "[extractive fallback]") {
		t.Fatalf("stage 1: summary used the extractive fallback — the real LLM did not produce it:\n%s", stagingText)
	}
	if !strings.Contains(stagingText, "fallback: false") {
		t.Fatalf("stage 1: staging frontmatter missing fallback:false marker:\n%s", stagingText)
	}
	// The summary body must exist beyond the yaml frontmatter.
	bodyStart := strings.Index(stagingText, "---\n\n")
	if bodyStart < 0 || strings.TrimSpace(stagingText[bodyStart:]) == "" {
		t.Fatalf("stage 1: staging summary body is empty:\n%s", stagingText)
	}
	t.Logf("stage 1: ingest OK in %s; staging summary:\n%s", time.Since(stageStart).Round(time.Millisecond), stagingText)

	// ── Stage 2: consolidate (non-TTT, TTVTrajectories=1) ──────────────
	stageStart = time.Now()
	consolidator := mg.NewConsolidator(store, backend, mg.ConsolidateConfig{
		TTVTrajectories: 1,
		OpBudget:        12,
		RoundBudget:     4,
		Timeout:         5 * time.Minute,
	}, "pi", nil, mg.NewTraceRecorder(store.Root))
	cres, err := consolidator.Consolidate(ctx)
	if err != nil {
		t.Fatalf("stage 2: consolidate: %v", err)
	}
	t.Logf("stage 2: consolidate done in %s: winner=v%d switched=%v ops_applied=%d rejected=%v",
		time.Since(stageStart).Round(time.Millisecond), cres.WinnerVersion, cres.Switched, cres.OpsApplied, cres.Rejected)
	if cres.OpsApplied < 1 {
		t.Fatalf("stage 2: no consolidation operations applied (rejected=%v)", cres.Rejected)
	}
	g, err := mg.LoadGraph(store, cres.WinnerVersion)
	if err != nil {
		t.Fatalf("stage 2: load graph v%d: %v", cres.WinnerVersion, err)
	}
	segReferenced := false
	for _, n := range g.Nodes() {
		for _, ref := range n.SegmentRefs {
			if ref == e2eSegmentID {
				segReferenced = true
				t.Logf("stage 2: node %q references %s; body: %s", n.NodeID, e2eSegmentID, truncateForLog(n.Body, 300))
			}
		}
	}
	if !segReferenced {
		t.Fatalf("stage 2: no graph node references segment %s (nodes=%d)", e2eSegmentID, len(g.Nodes()))
	}
	oplog := mg.NewOpLogger(store)
	opEntries, err := oplog.Read(cres.WinnerVersion)
	if err != nil {
		t.Fatalf("stage 2: read op log: %v", err)
	}
	if len(opEntries) == 0 {
		t.Fatalf("stage 2: op log of v%d is empty", cres.WinnerVersion)
	}
	manifest, err := store.LoadManifest(cres.WinnerVersion)
	if err != nil {
		t.Fatalf("stage 2: load manifest: %v", err)
	}
	if manifest.NodeCount == 0 {
		t.Fatalf("stage 2: manifest node count is 0")
	}
	if err := g.Validate(); err != nil {
		t.Fatalf("stage 2: graph validation failed: %v", err)
	}
	t.Logf("stage 2: graph v%d valid: %d nodes, %d hierarchy edges, %d relation edges, %d op-log entries",
		cres.WinnerVersion, manifest.NodeCount, manifest.HierEdgeCount, manifest.RelEdgeCount, len(opEntries))

	// ── Stage 3: recall via the explore agent over the real backend ────
	stageStart = time.Now()
	retriever := mg.NewHybridRetriever(store, nil, mg.RetrievalConfig{TopK: 5})
	if err := retriever.Rebuild(ctx); err != nil {
		t.Fatalf("stage 3: retriever rebuild: %v", err)
	}
	explorer := mg.NewExplorer(store, retriever, backend, mg.ExploreConfig{
		Agents:            1,
		MaxRounds:         3,
		MaxExpandPerRound: 4,
		Timeout:           5 * time.Minute,
	}, "pi", mg.NewTraceRecorder(store.Root))
	query := "What caused the \"concurrent map writes\" panic in the " + e2eKeyword + " scheduler, and how was it fixed?"
	recall, err := explorer.Explore(ctx, query)
	if err != nil {
		t.Fatalf("stage 3: explore: %v", err)
	}
	t.Logf("stage 3: explore done in %s: found=%v rounds=%d node_ids=%v summary=%q",
		time.Since(stageStart).Round(time.Millisecond), recall.Found, recall.Rounds, recall.NodeIDs, truncateForLog(recall.Summary, 400))
	for _, run := range recall.AgentRuns {
		t.Logf("stage 3: trajectory seed=%d found=%v rounds=%d error=%q", run.Seed, run.Found, run.Rounds, run.Error)
	}
	if !recall.Found {
		t.Fatalf("stage 3: recall miss — the explore agent did not find the ingested content (runs=%+v)", recall.AgentRuns)
	}
	if len(recall.NodeIDs) == 0 {
		t.Fatalf("stage 3: recall cites no nodes")
	}
	recallGraph, err := mg.LoadGraph(store, recall.Version)
	if err != nil {
		t.Fatalf("stage 3: load recall graph v%d: %v", recall.Version, err)
	}
	graphNodeCited := false
	for _, id := range recall.NodeIDs {
		switch {
		case recallGraph.Node(id) != nil:
			graphNodeCited = true
		case mg.IsStagingID(id):
			segID := strings.TrimPrefix(id, "seg:")
			if _, err := store.ReadStagingSegment(segID); err != nil {
				t.Fatalf("stage 3: cited staging id %q does not resolve: %v", id, err)
			}
			t.Logf("stage 3: cited id %q is a staging segment (accepted, but a graph node citation is also required)", id)
		default:
			t.Fatalf("stage 3: cited node id %q does not exist in graph v%d", id, recall.Version)
		}
	}
	if !graphNodeCited {
		t.Fatalf("stage 3: no cited id is a graph node (ids=%v) — recall did not exercise the consolidated graph", recall.NodeIDs)
	}
	if !strings.Contains(strings.ToLower(recall.Summary), e2eKeyword) {
		t.Fatalf("stage 3: recall summary does not mention planted keyword %q: %q", e2eKeyword, recall.Summary)
	}

	// ── Stage 4: judge + reward composition ────────────────────────────
	stageStart = time.Now()
	judge := mg.NewJudge(backend, mg.JudgeConfig{Timeout: 4 * time.Minute}, "pi")
	history := []mg.Message{
		{Role: "user", Content: "We're seeing `fatal error: concurrent map writes` from the " + e2eKeyword + " scheduler in staging again. Do we already know the root cause?"},
		{Role: "assistant", Content: "Yes. " + recall.Summary},
	}
	jres, err := judge.Judge(ctx, query, recall, history)
	if err != nil {
		t.Fatalf("stage 4: judge: %v", err)
	}
	t.Logf("stage 4: judge done in %s: score=%.3f relevant_nodes=%v rationale=%q",
		time.Since(stageStart).Round(time.Millisecond), jres.Score, jres.RelevantNodes, truncateForLog(jres.Rationale, 300))
	if jres.Score < 0 || jres.Score > 1 {
		t.Fatalf("stage 4: judge score %.3f outside [0,1]", jres.Score)
	}

	sink := &fakeRewardSink{rewards: make(map[string]float64)}
	params := mg.DefaultRewardParams()
	composer := mg.NewRewardComposer(sink, params, time.Minute)
	if err := composer.Submit(ctx, recall.TraceID, recall, nil); err != nil {
		t.Fatalf("stage 4: reward submit: %v", err)
	}
	if err := composer.OnJudgeResult(ctx, recall.TraceID, jres); err != nil {
		t.Fatalf("stage 4: reward on-judge-result: %v", err)
	}
	got, ok := sink.rewards[recall.TraceID]
	if !ok {
		t.Fatalf("stage 4: no reward pushed for trace %s", recall.TraceID)
	}
	want := expectedReward(recall, jres.Score, params)
	if math.Abs(got-want) > 1e-9 {
		t.Fatalf("stage 4: reward %.6f does not match formula value %.6f (score=%.3f rounds=%d)", got, want, jres.Score, recall.Rounds)
	}
	if composer.PendingCount() != 0 {
		t.Fatalf("stage 4: composer still has %d pending traces after composition", composer.PendingCount())
	}
	t.Logf("stage 4: reward %.6f pushed for trace %s (formula value %.6f)", got, recall.TraceID, want)

	total := time.Since(testStart)
	if total > e2eWallBudget {
		t.Fatalf("E2E exceeded wall budget: %s > %s", total.Round(time.Second), e2eWallBudget)
	}
	t.Logf("E2E smoke test PASSED in %s (budget %s)", total.Round(time.Second), e2eWallBudget)
}

// e2eExecute runs one one-shot prompt against the backend, draining the
// message stream and returning the final result. It fails the test on
// transport-level errors.
func e2eExecute(t *testing.T, ctx context.Context, b mg.AgentBackend, stage, prompt string, timeout time.Duration) agentpkg.Result {
	t.Helper()
	execCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	session, err := b.Execute(execCtx, prompt, agentpkg.ExecOptions{
		Timeout:          timeout,
		EphemeralSession: true,
	})
	if err != nil {
		t.Fatalf("%s: execute: %v", stage, err)
	}
	for range session.Messages {
	}
	result, ok := <-session.Result
	if !ok {
		t.Fatalf("%s: agent session ended without a result", stage)
	}
	return result
}

// e2eTrajectory returns a small but realistic trajectory export: a user and
// an assistant debugging a Go race condition in a fictional task scheduler,
// with the distinctive e2eKeyword planted throughout.
func e2eTrajectory(t *testing.T) json.RawMessage {
	t.Helper()
	msgs := []map[string]string{
		{"role": "user", "content": "Our " + e2eKeyword + " task scheduler panics intermittently in production with `fatal error: concurrent map writes`. The stack trace points at scheduler.go in the dispatch path. Can you investigate?"},
		{"role": "assistant", "content": "I reproduced it locally with `go test -race ./scheduler`. The " + e2eKeyword + " scheduler's `readyQueue` field is a plain `map[string]*Task`: `(*Scheduler).dispatch()` writes to it while worker goroutines read it from `(*Scheduler).next()`, with no synchronization at all."},
		{"role": "user", "content": "What's the minimal fix?"},
		{"role": "assistant", "content": "I added a `sync.RWMutex` named `queueMu` to the " + e2eKeyword + " Scheduler struct. `dispatch` and `requeue` now take `queueMu.Lock()`, and `next` takes `queueMu.RLock()`. I also added a regression test `TestSchedulerConcurrentDispatch` that runs 64 dispatching goroutines against 4 workers calling next."},
		{"role": "assistant", "content": "Verification: `go test -race ./scheduler -run TestSchedulerConcurrentDispatch -count=20` passes with zero race reports. Root cause: unsynchronized concurrent access to the " + e2eKeyword + " readyQueue map. Fix: guard every readyQueue access with queueMu (sync.RWMutex)."},
	}
	raw, err := json.Marshal(msgs)
	if err != nil {
		t.Fatalf("marshal trajectory: %v", err)
	}
	return raw
}

// fakeRewardSink records pushed rewards by key.
type fakeRewardSink struct {
	rewards map[string]float64
}

func (s *fakeRewardSink) SetReward(_ context.Context, key string, reward float64) error {
	s.rewards[key] = reward
	return nil
}

// expectedReward mirrors RewardComposer.composeReward for the parsed score
// and the recorded runs of the recall (design §2 补充 formula:
// score < τ -> MissPenalty, else mean of Base - WeightRound*rounds over
// non-errored runs).
func expectedReward(recall *mg.RecallResult, score float64, p mg.RewardParams) float64 {
	if score < p.Tau {
		return p.MissPenalty
	}
	if len(recall.AgentRuns) == 0 {
		return p.Base - p.WeightRound*float64(recall.Rounds)
	}
	total := 0.0
	successful := 0
	for _, run := range recall.AgentRuns {
		if run.Error != "" {
			continue
		}
		total += p.Base - p.WeightRound*float64(run.Rounds)
		successful++
	}
	if successful == 0 {
		return p.MissPenalty
	}
	return total / float64(successful)
}

// truncateForLog bounds a string for log output.
func truncateForLog(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return fmt.Sprintf("%s...(%d bytes total)", s[:n], len(s))
}
