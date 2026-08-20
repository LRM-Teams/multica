package memorygraph

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/multica-ai/multica/server/pkg/agent"
)

// Spec §4, acceptance A2/A3/A24: server-side traversal state. /view requires
// trajectory+expansion+node with batch-membership and graph-view checks; the
// per-batch distinct-view quota is atomic; re-view is idempotent; /expand
// requires a previously viewed anchor and an idempotency request key; /submit
// is one immutable server-side record per trajectory.

// registerSeeds registers a trajectory with the given round-0 seed
// candidates and returns the seed expansion id.
func registerSeeds(t *testing.T, srv *ExploreToolServer, trajectoryID string, seeds []string) string {
	t.Helper()
	expansionID, err := srv.RegisterTrajectory(context.Background(), trajectoryID, seeds)
	if err != nil {
		t.Fatalf("RegisterTrajectory: %v", err)
	}
	if expansionID == "" {
		t.Fatal("RegisterTrajectory returned an empty seed expansion id")
	}
	return expansionID
}

func viewNode(t *testing.T, baseURL, token, trajectoryID, expansionID, nodeID string) (int, []byte) {
	t.Helper()
	return explorePost(baseURL, token, "/view", map[string]any{
		"trajectory_id": trajectoryID, "expansion_id": expansionID, "node_id": nodeID,
	})
}

func expandNode(baseURL, token, trajectoryID, anchor, requestKey string) (int, []byte) {
	return explorePost(baseURL, token, "/expand", map[string]any{
		"trajectory_id": trajectoryID, "node_id": anchor, "request_key": requestKey,
	})
}

func expandOK(t *testing.T, baseURL, token, trajectoryID, anchor, requestKey string) expandResponse {
	t.Helper()
	status, body := expandNode(baseURL, token, trajectoryID, anchor, requestKey)
	if status != http.StatusOK {
		t.Fatalf("expand %s: status = %d body %s", anchor, status, body)
	}
	var resp expandResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("expand decode: %v", err)
	}
	return resp
}

func errorCode(t *testing.T, body []byte) string {
	t.Helper()
	var e struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(body, &e); err != nil {
		t.Fatalf("error decode: %v (body %s)", err, body)
	}
	return e.Error
}

// A3: /view binds node to batch to trajectory.
func TestViewRequiresBatchMembership(t *testing.T) {
	store := newExploreGraphStore(t)
	srv, baseURL, token := startTestToolServer(t, store, testExploreConfig())
	seedExp := registerSeeds(t, srv, "t1", []string{"a"})

	// Unknown expansion id: not found.
	if status, _ := viewNode(t, baseURL, token, "t1", "no-such-expansion", "a"); status != http.StatusNotFound {
		t.Fatalf("view with unknown expansion: status = %d, want 404", status)
	}
	// Node outside the batch is rejected even though it exists in the graph.
	status, body := viewNode(t, baseURL, token, "t1", seedExp, "c")
	if status != http.StatusConflict || errorCode(t, body) != "VIEW_NOT_IN_BATCH" {
		t.Fatalf("view outside batch: status = %d code %s, want 409 VIEW_NOT_IN_BATCH", status, body)
	}
	// Member view succeeds.
	if status, body := viewNode(t, baseURL, token, "t1", seedExp, "a"); status != http.StatusOK {
		t.Fatalf("member view: status = %d body %s, want 200", status, body)
	}
}

// A2: the per-batch distinct-view quota is exact; re-viewing the same node is
// idempotent and consumes no extra slot.
func TestViewDistinctQuotaAndIdempotentReview(t *testing.T) {
	store := newExploreGraphStore(t)
	cfg := testExploreConfig()
	cfg.ViewsPerExpansion = 1
	srv, baseURL, token := startTestToolServer(t, store, cfg)
	seedExp := registerSeeds(t, srv, "t1", []string{"a", "b"})

	if status, body := viewNode(t, baseURL, token, "t1", seedExp, "a"); status != http.StatusOK {
		t.Fatalf("first view: status = %d body %s", status, body)
	}
	if status, body := viewNode(t, baseURL, token, "t1", seedExp, "a"); status != http.StatusOK {
		t.Fatalf("idempotent re-view: status = %d body %s, want 200", status, body)
	}
	status, body := viewNode(t, baseURL, token, "t1", seedExp, "b")
	if status != http.StatusConflict || errorCode(t, body) != "VIEW_QUOTA_EXCEEDED" {
		t.Fatalf("second distinct view: status = %d code %s, want 409 VIEW_QUOTA_EXCEEDED", status, body)
	}
}

// A2/A24: a failed node load releases the reservation — it must not burn a
// distinct-view slot.
func TestViewFailedLoadReleasesSlot(t *testing.T) {
	store := newExploreGraphStore(t)
	cfg := testExploreConfig()
	cfg.ViewsPerExpansion = 1
	srv, baseURL, token := startTestToolServer(t, store, cfg)
	seedExp := registerSeeds(t, srv, "t1", []string{"seg:missing", "a"})

	if status, _ := viewNode(t, baseURL, token, "t1", seedExp, "seg:missing"); status != http.StatusNotFound {
		t.Fatalf("failed load: status = %d, want 404", status)
	}
	if status, body := viewNode(t, baseURL, token, "t1", seedExp, "a"); status != http.StatusOK {
		t.Fatalf("view after failed load: status = %d body %s, want 200 (slot released)", status, body)
	}
}

// A2/A24: racing first views of different nodes for the last slot admit at
// most one winner.
func TestViewRacingLastSlotAdmitsOne(t *testing.T) {
	store := newExploreGraphStore(t)
	cfg := testExploreConfig()
	cfg.ViewsPerExpansion = 1
	srv, baseURL, token := startTestToolServer(t, store, cfg)
	seedExp := registerSeeds(t, srv, "t1", []string{"a", "b", "c", "d", "e"})

	var wg sync.WaitGroup
	statuses := make([]int, 5)
	nodes := []string{"a", "b", "c", "d", "e"}
	for i, n := range nodes {
		wg.Add(1)
		go func(i int, n string) {
			defer wg.Done()
			status, _ := viewNode(t, baseURL, token, "t1", seedExp, n)
			statuses[i] = status
		}(i, n)
	}
	wg.Wait()
	ok := 0
	for _, s := range statuses {
		if s == http.StatusOK {
			ok++
		} else if s != http.StatusConflict {
			t.Fatalf("racing view status = %d, want 200 or 409", s)
		}
	}
	if ok != 1 {
		t.Fatalf("racing views admitted %d, want exactly 1", ok)
	}
}

// A3: only a previously viewed node is a valid expansion anchor.
func TestExpandAnchorMustBeViewed(t *testing.T) {
	store := newExploreGraphStore(t)
	srv, baseURL, token := startTestToolServer(t, store, testExploreConfig())
	seedExp := registerSeeds(t, srv, "t1", []string{"b"})

	status, body := expandNode(baseURL, token, "t1", "b", "k1")
	if status != http.StatusConflict || errorCode(t, body) != "ANCHOR_NOT_VIEWED" {
		t.Fatalf("expand unviewed anchor: status = %d code %s, want 409 ANCHOR_NOT_VIEWED", status, body)
	}
	if status, body := viewNode(t, baseURL, token, "t1", seedExp, "b"); status != http.StatusOK {
		t.Fatalf("view seed: status = %d body %s", status, body)
	}
	resp := expandOK(t, baseURL, token, "t1", "b", "k1")
	if resp.ExpansionID == "" || resp.Round != 1 {
		t.Fatalf("expand viewed anchor = %+v, want expansion id + round 1", resp)
	}
}

// A3: any previously viewed node stays eligible as an anchor, so a
// trajectory branches even when the view width is one.
func TestExpandBranchesFromAnyViewedNode(t *testing.T) {
	store := newExploreGraphStore(t)
	cfg := testExploreConfig()
	cfg.ViewsPerExpansion = 1
	srv, baseURL, token := startTestToolServer(t, store, cfg)
	seedExp := registerSeeds(t, srv, "t1", []string{"b"})

	if status, body := viewNode(t, baseURL, token, "t1", seedExp, "b"); status != http.StatusOK {
		t.Fatalf("view b: status = %d body %s", status, body)
	}
	batch1 := expandOK(t, baseURL, token, "t1", "b", "k1")
	if status, body := viewNode(t, baseURL, token, "t1", batch1.ExpansionID, "c"); status != http.StatusOK {
		t.Fatalf("view candidate c: status = %d body %s", status, body)
	}
	// Branch from c (viewed in batch 1), while the older anchor b also stays
	// expandable.
	branch := expandOK(t, baseURL, token, "t1", "c", "k2")
	if branch.Round != 2 {
		t.Fatalf("branch round = %d, want 2", branch.Round)
	}
	again := expandOK(t, baseURL, token, "t1", "b", "k3")
	if again.Round != 3 {
		t.Fatalf("re-expand earlier anchor round = %d, want 3", again.Round)
	}
}

// A24: the /expand request key replays the original batch without consuming
// a round; conflicting reuse of the key is rejected.
func TestExpandRequestKeyIdempotency(t *testing.T) {
	store := newExploreGraphStore(t)
	srv, baseURL, token := startTestToolServer(t, store, testExploreConfig())
	seedExp := registerSeeds(t, srv, "t1", []string{"b"})
	if status, body := viewNode(t, baseURL, token, "t1", seedExp, "b"); status != http.StatusOK {
		t.Fatalf("view b: status = %d body %s", status, body)
	}

	first := expandOK(t, baseURL, token, "t1", "b", "k1")
	replay := expandOK(t, baseURL, token, "t1", "b", "k1")
	if replay.ExpansionID != first.ExpansionID || replay.Round != first.Round {
		t.Fatalf("replay = %+v, want original batch %+v", replay, first)
	}
	if got := srv.trajectoryRounds("t1"); got != 1 {
		t.Fatalf("rounds after replay = %d, want 1 (replay consumes no round)", got)
	}
	// Same key, different anchor: conflict, even though c is unviewed (the
	// key check fires first).
	status, body := expandNode(baseURL, token, "t1", "c", "k1")
	if status != http.StatusConflict || errorCode(t, body) != "REQUEST_KEY_CONFLICT" {
		t.Fatalf("conflicting key reuse: status = %d code %s, want 409 REQUEST_KEY_CONFLICT", status, body)
	}
	if got := srv.trajectoryRounds("t1"); got != 1 {
		t.Fatalf("rounds after conflict = %d, want 1", got)
	}
}

// Spec §14: the final allowed expansion is served with budget_exceeded=true
// but is not a violation; only a further expand marks the trajectory
// budget-blown.
func TestExpandFinalRoundIsNotBudgetViolation(t *testing.T) {
	store := newExploreGraphStore(t)
	cfg := testExploreConfig()
	cfg.MaxRounds = 1
	srv, baseURL, token := startTestToolServer(t, store, cfg)
	seedExp := registerSeeds(t, srv, "t1", []string{"b"})
	if status, body := viewNode(t, baseURL, token, "t1", seedExp, "b"); status != http.StatusOK {
		t.Fatalf("view b: status = %d body %s", status, body)
	}

	final := expandOK(t, baseURL, token, "t1", "b", "k1")
	if !final.BudgetExceeded || len(final.Candidates) == 0 {
		t.Fatalf("final allowed expand = %+v, want served candidates with budget_exceeded", final)
	}
	if srv.trajectoryBudgetBlown("t1") {
		t.Fatal("final allowed expansion must not mark the trajectory budget-blown")
	}
	over := expandOK(t, baseURL, token, "t1", "b", "k2")
	if !over.BudgetExceeded || len(over.Candidates) != 0 {
		t.Fatalf("over-budget expand = %+v, want empty candidates", over)
	}
	if !srv.trajectoryBudgetBlown("t1") {
		t.Fatal("expanding past the budget must mark the trajectory budget-blown")
	}
}

// 2.4: exactly one immutable submission per trajectory; identical replay is
// idempotent, conflicting replay rejected; cited ids must be unique and a
// subset of successfully viewed nodes.
func TestSubmitAuthority(t *testing.T) {
	store := newExploreGraphStore(t)
	srv, baseURL, token := startTestToolServer(t, store, testExploreConfig())
	seedExp := registerSeeds(t, srv, "t1", []string{"a", "b"})
	if status, body := viewNode(t, baseURL, token, "t1", seedExp, "a"); status != http.StatusOK {
		t.Fatalf("view a: status = %d body %s", status, body)
	}

	submit := func(traj string, found bool, ids []string) (int, []byte) {
		return explorePost(baseURL, token, "/submit", map[string]any{
			"trajectory_id": traj, "found": found, "summary": "s", "node_ids": ids,
		})
	}

	status, body := submit("t1", true, []string{"a"})
	if status != http.StatusOK {
		t.Fatalf("first submit: status = %d body %s", status, body)
	}
	// Identical replay: idempotent 200.
	if status, body := submit("t1", true, []string{"a"}); status != http.StatusOK {
		t.Fatalf("identical replay: status = %d body %s, want 200", status, body)
	}
	// Conflicting replay (different outcome over the same viewed node):
	// rejected.
	status, body = submit("t1", false, []string{"a"})
	if status != http.StatusConflict || errorCode(t, body) != "SUBMIT_CONFLICT" {
		t.Fatalf("conflicting replay: status = %d code %s, want 409 SUBMIT_CONFLICT", status, body)
	}
	// The recorded submission is unchanged.
	if sub := srv.trajectorySubmission("t1"); sub == nil || len(sub.NodeIDs) != 1 || sub.NodeIDs[0] != "a" {
		t.Fatalf("stored submission = %+v, want [a]", sub)
	}

	// Citing a node the trajectory never viewed is rejected.
	seedExp2 := registerSeeds(t, srv, "t2", []string{"a", "b"})
	_ = seedExp2
	status, body = submit("t2", true, []string{"b"})
	if status != http.StatusConflict || errorCode(t, body) != "SUBMIT_NODE_NOT_VIEWED" {
		t.Fatalf("unviewed citation: status = %d code %s, want 409 SUBMIT_NODE_NOT_VIEWED", status, body)
	}
	// Duplicate ids are rejected.
	seedExp3 := registerSeeds(t, srv, "t3", []string{"a"})
	if status, body := viewNode(t, baseURL, token, "t3", seedExp3, "a"); status != http.StatusOK {
		t.Fatalf("t3 view a: status = %d body %s", status, body)
	}
	status, body = submit("t3", true, []string{"a", "a"})
	if status != http.StatusBadRequest || errorCode(t, body) != "DUPLICATE_NODE_IDS" {
		t.Fatalf("duplicate ids: status = %d code %s, want 400 DUPLICATE_NODE_IDS", status, body)
	}
}

// ---------------------------------------------------------------------------
// Explorer-level submission authority (2.4)
// ---------------------------------------------------------------------------

// scriptedTraversalBackend plays the new traversal protocol against the tool
// server and returns the given final JSON.
type scriptedTraversalBackend struct {
	t *testing.T

	viewSeed    bool
	expandTimes int
	submit      bool
	submitFound bool
	finalJSON   string

	errs []string
	mu   sync.Mutex
}

func (b *scriptedTraversalBackend) recordErr(format string, args ...any) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.errs = append(b.errs, fmt.Sprintf(format, args...))
}

func (b *scriptedTraversalBackend) Execute(_ context.Context, prompt string, _ agent.ExecOptions) (*agent.Session, error) {
	base := promptField(prompt, "Tool server base URL: ")
	token := promptField(prompt, "Bearer token: ")
	traj := promptField(prompt, "Trajectory ID: ")
	seedExp := promptField(prompt, "Seed expansion ID: ")
	node := firstSeedNode(prompt)
	if base == "" || token == "" || traj == "" || seedExp == "" || node == "" {
		return nil, fmt.Errorf("prompt missing tool coordinates (base=%q traj=%q seedExp=%q node=%q)", base, traj, seedExp, node)
	}
	if b.viewSeed {
		if status, body := explorePost(base, token, "/view", map[string]any{
			"trajectory_id": traj, "expansion_id": seedExp, "node_id": node,
		}); status != http.StatusOK {
			b.recordErr("view %s: status %d body %s", node, status, body)
		}
	}
	for i := 0; i < b.expandTimes; i++ {
		if status, body := explorePost(base, token, "/expand", map[string]any{
			"trajectory_id": traj, "node_id": node, "request_key": fmt.Sprintf("rk-%d", i),
		}); status != http.StatusOK {
			b.recordErr("expand %d: status %d body %s", i, status, body)
		}
	}
	if b.submit {
		if status, body := explorePost(base, token, "/submit", map[string]any{
			"trajectory_id": traj, "found": b.submitFound, "summary": "s", "node_ids": []string{node},
		}); status != http.StatusOK {
			b.recordErr("submit: status %d body %s", status, body)
		}
	}
	return exploreCompletedSession(b.finalJSON), nil
}

// 2.4: a trajectory that ends without a tool-server submission is an
// execution failure, whatever the model's final JSON claims.
func TestRunTrajectoryMissingSubmissionIsExecutionFailure(t *testing.T) {
	store := newExploreStore(t)
	retr := newExploreRetriever(t, store)
	backend := &scriptedTraversalBackend{
		t:         t,
		viewSeed:  true,
		finalJSON: `{"found":true,"summary":"claims a find","node_ids":["n-target"],"rounds":0}`,
	}
	ex := NewExplorer(store, retr, backend, testExploreConfig(), "pi", nil)
	res, err := ex.Explore(context.Background(), "dispatch router retries")
	if err != nil {
		t.Fatalf("Explore: %v", err)
	}
	if res.Found {
		t.Fatal("a trajectory without submission must never be adopted")
	}
	if len(res.AgentRuns) != 1 || !strings.Contains(res.AgentRuns[0].Error, "submission") {
		t.Fatalf("run = %+v, want an execution failure naming the missing submission", res.AgentRuns)
	}
}

// 2.4: the tool-server submission is authoritative; the model's final JSON
// is audit-only and cannot override it. Viewed and submitted node sets are
// persisted separately.
func TestRunTrajectoryModelJSONIsAuditOnly(t *testing.T) {
	store := newExploreStore(t)
	retr := newExploreRetriever(t, store)
	backend := &scriptedTraversalBackend{
		t:           t,
		viewSeed:    true,
		submit:      true,
		submitFound: false,
		finalJSON:   `{"found":true,"summary":"overriding claim","node_ids":["n-other"],"rounds":9}`,
	}
	ex := NewExplorer(store, retr, backend, testExploreConfig(), "pi", nil)
	res, err := ex.Explore(context.Background(), "dispatch router retries")
	if err != nil {
		t.Fatalf("Explore: %v", err)
	}
	if res.Found {
		t.Fatal("model final JSON must not override the found=false tool submission")
	}
	run := res.AgentRuns[0]
	if run.Error != "" {
		t.Fatalf("run error = %q (backend errs %v)", run.Error, backend.errs)
	}
	if run.Found {
		t.Fatal("run.Found must come from the tool submission (false)")
	}
	if len(run.NodeIDs) != 1 || run.NodeIDs[0] != "n-target" {
		t.Fatalf("run.NodeIDs = %v, want the submitted [n-target]", run.NodeIDs)
	}
	if len(run.ViewedNodeIDs) != 1 || run.ViewedNodeIDs[0] != "n-target" {
		t.Fatalf("run.ViewedNodeIDs = %v, want the viewed [n-target]", run.ViewedNodeIDs)
	}
	if run.Rounds != 0 {
		t.Fatalf("run.Rounds = %d, want the server-counted 0 (model-reported 9 ignored)", run.Rounds)
	}
}
