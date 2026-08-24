package memorygraph

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/pkg/agent"
)

// ---------------------------------------------------------------------------
// fixtures
// ---------------------------------------------------------------------------

// newExploreStore returns an initialized store with two topically distinct
// nodes, so hybrid retrieval has a clear top hit for explorer tests.
func newExploreStore(t *testing.T) *Store {
	t.Helper()
	store := NewStore(filepath.Join(t.TempDir(), "memory_graph"))
	if err := store.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	nodes := []*Node{
		{NodeID: "n-target", Body: "the dispatch router retries failed batch jobs with exponential backoff"},
		{NodeID: "n-other", Body: "vector cache eviction policy for stale embeddings and quota accounting"},
	}
	for _, n := range nodes {
		n.CreatedBy = CreatorIngester
		n.CreatedVersion = 1
		n.UpdatedVersion = 1
		n.ObservedAt = time.Now().UTC()
		if err := store.SaveNode(1, n); err != nil {
			t.Fatalf("SaveNode %s: %v", n.NodeID, err)
		}
	}
	return store
}

// newExploreGraphStore builds the expand-ordering fixture: hierarchy chain
// a(L2) -> b(L1) -> c(L0), a contradicts relation b -> d with LevelDelta 2
// (listed first in relations.jsonl), and an evidence_for relation b -> e
// also with LevelDelta 2.
func newExploreGraphStore(t *testing.T) *Store {
	t.Helper()
	store := NewStore(filepath.Join(t.TempDir(), "memory_graph"))
	if err := store.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	now := time.Now().UTC()
	longBody := strings.Repeat("dispatch routing detail ", 20) // > 50 chars
	nodes := []*Node{
		{NodeID: "a", Level: 2, Body: longBody},
		{NodeID: "b", Level: 1, Body: "mid-level summary of dispatch behavior"},
		{NodeID: "c", Level: 0, Body: "leaf statement about dispatch retries"},
		{NodeID: "d", Level: 3, Body: "high-level claim contradicting dispatch behavior"},
		{NodeID: "e", Level: 3, Body: "high-level evidence supporting dispatch behavior"},
	}
	for _, n := range nodes {
		n.CreatedBy = CreatorConsolidator
		n.CreatedVersion = 1
		n.UpdatedVersion = 1
		n.ObservedAt = now
		if err := store.SaveNode(1, n); err != nil {
			t.Fatalf("SaveNode %s: %v", n.NodeID, err)
		}
	}
	hier := []*Edge{
		{EdgeID: "h1", Type: EdgeTypeSummarizes, From: "a", To: "b", CreatedBy: CreatorConsolidator, CreatedVersion: 1},
		{EdgeID: "h2", Type: EdgeTypeSummarizes, From: "b", To: "c", CreatedBy: CreatorConsolidator, CreatedVersion: 1},
	}
	rel := []*Edge{
		{EdgeID: "r1", Type: EdgeTypeContradicts, From: "b", To: "d", Status: StatusContested, Epistemic: EpistemicInferred, SourceLevel: 1, TargetLevel: 3, LevelDelta: 2, CreatedBy: CreatorConsolidator, CreatedVersion: 1},
		{EdgeID: "r2", Type: EdgeTypeEvidenceFor, From: "b", To: "e", Status: StatusSupported, Epistemic: EpistemicInferred, SourceLevel: 1, TargetLevel: 3, LevelDelta: 2, CreatedBy: CreatorConsolidator, CreatedVersion: 1},
	}
	if err := store.SaveEdges(1, hier, rel); err != nil {
		t.Fatalf("SaveEdges: %v", err)
	}
	return store
}

func newExploreRetriever(t *testing.T, store *Store) *HybridRetriever {
	t.Helper()
	r := NewHybridRetriever(store, nil, DefaultRetrievalConfig())
	if err := r.Rebuild(context.Background()); err != nil {
		t.Fatalf("Rebuild: %v", err)
	}
	return r
}

// ---------------------------------------------------------------------------
// fake agent backend
// ---------------------------------------------------------------------------

// fakeExploreBackend plays the explore agent: it extracts the tool-server
// coordinates from the prompt, makes real HTTP calls against the started
// ExploreToolServer, and emits the strict-JSON final response.
type fakeExploreBackend struct {
	t *testing.T

	mu    sync.Mutex
	calls int
	errs  []string

	// expandsPerCall decides how many single-node /explore calls the
	// invocation with the given 1-based call index performs (nil → exactly
	// one). Each call consumes one round, mirroring the old per-expand
	// accounting under the merged protocol.
	expandsPerCall func(callIdx int) int
	// reportRounds rewrites the rounds value reported in the final JSON
	// (nil → report the server-observed round count).
	reportRounds func(serverRounds int) int
	// garbage makes every invocation emit an unparseable final response.
	garbage bool
}

func exploreCompletedSession(output string) *agent.Session {
	return completedSessionWithMessages(output)
}

func (f *fakeExploreBackend) recordErr(format string, args ...any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.errs = append(f.errs, fmt.Sprintf(format, args...))
}

func (f *fakeExploreBackend) Execute(ctx context.Context, prompt string, _ agent.ExecOptions) (*agent.Session, error) {
	f.mu.Lock()
	f.calls++
	idx := f.calls
	f.mu.Unlock()

	if f.garbage {
		return exploreCompletedSession("I explored a bit but have no structured answer for you."), nil
	}

	base := promptField(prompt, "Tool server base URL: ")
	token := promptField(prompt, "Bearer token: ")
	traj := promptField(prompt, "Trajectory ID: ")
	node := firstSeedNode(prompt)
	if base == "" || token == "" || traj == "" || node == "" {
		return nil, fmt.Errorf("prompt missing tool coordinates (base=%q traj=%q node=%q)", base, traj, node)
	}

	expands := 1
	if f.expandsPerCall != nil {
		expands = f.expandsPerCall(idx)
	}
	lastRound := 0
	for i := 0; i < expands; i++ {
		status, body := explorePost(base, token, "/explore", map[string]any{"trajectory_id": traj, "node_ids": []string{node}})
		if status != http.StatusOK {
			f.recordErr("explore %s: status %d body %s", node, status, body)
			break
		}
		var er exploreResponse
		if err := json.Unmarshal(body, &er); err != nil {
			f.recordErr("explore decode: %v", err)
			break
		}
		lastRound = er.Round
	}

	reported := lastRound
	if f.reportRounds != nil {
		reported = f.reportRounds(lastRound)
	}
	if status, _ := explorePost(base, token, "/submit", map[string]any{
		"trajectory_id": traj, "found": true, "summary": "s", "node_ids": []string{node},
	}); status != http.StatusOK {
		f.recordErr("submit: status %d", status)
	}

	out := fmt.Sprintf(`exploration log... {"found":true,"summary":"summary from call %d","node_ids":[%q],"rounds":%d}`, idx, node, reported)
	return exploreCompletedSession(out), nil
}

// promptField extracts "Prefix: value" lines from the trajectory prompt.
func promptField(prompt, prefix string) string {
	for _, line := range strings.Split(prompt, "\n") {
		if strings.HasPrefix(line, prefix) {
			return strings.TrimSpace(strings.TrimPrefix(line, prefix))
		}
	}
	return ""
}

// firstSeedNode extracts the first seed node id from the prompt's initial
// node list ("- <id>: <snippet>" bullet lines).
func firstSeedNode(prompt string) string {
	for _, line := range strings.Split(prompt, "\n") {
		if !strings.HasPrefix(line, "- ") || strings.Contains(line, "(none") {
			continue
		}
		parts := strings.SplitN(strings.TrimPrefix(line, "- "), ": ", 2)
		if len(parts) == 2 {
			return parts[0]
		}
	}
	return ""
}

// explorePost performs one JSON POST against the tool server. An empty token
// omits the Authorization header.
func explorePost(baseURL, token, path string, payload any) (int, []byte) {
	body, err := json.Marshal(payload)
	if err != nil {
		return 0, nil
	}
	req, err := http.NewRequest(http.MethodPost, baseURL+path, bytes.NewReader(body))
	if err != nil {
		return 0, nil
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return 0, nil
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, b
}

func testExploreConfig() ExploreConfig {
	return ExploreConfig{
		Agents:            1,
		MaxRounds:         5,
		MaxExpandPerRound: 5,
		MaxNodeChars:      100,
		Timeout:           15 * time.Second,
	}
}

// ---------------------------------------------------------------------------
// Explorer tests
// ---------------------------------------------------------------------------

func TestDefaultExploreConfig(t *testing.T) {
	cfg := DefaultExploreConfig()
	if cfg.Agents != 1 || cfg.MaxRounds != 6 || cfg.MaxExpandPerRound != 5 || cfg.MaxNodeChars != 2000 || cfg.Timeout != 5*time.Minute {
		t.Fatalf("DefaultExploreConfig = %+v", cfg)
	}
}

func TestExploreUsesServerRounds(t *testing.T) {
	for _, reportedRounds := range []int{1, 99} {
		t.Run(fmt.Sprintf("agent_reports_%d", reportedRounds), func(t *testing.T) {
			store := newExploreStore(t)
			retr := newExploreRetriever(t, store)
			backend := &fakeExploreBackend{
				t:              t,
				expandsPerCall: func(int) int { return 2 },
				reportRounds:   func(int) int { return reportedRounds },
			}
			ex := NewExplorer(store, retr, backend, testExploreConfig(), "pi", nil)

			res, err := ex.Explore(context.Background(), "dispatch router retries")
			if err != nil {
				t.Fatalf("Explore: %v", err)
			}
			if len(backend.errs) > 0 {
				t.Fatalf("fake backend tool errors: %v", backend.errs)
			}
			if !res.Found || res.TraceID == "" || len(res.AgentRuns) != 1 {
				t.Fatalf("Explore result = %+v, want one found run with a trace", res)
			}
			run := res.AgentRuns[0]
			if run.Error != "" || run.Seed != 0 {
				t.Fatalf("run = %+v, want successful seed 0", run)
			}
			if run.Rounds != 2 || res.Rounds != 2 {
				t.Fatalf("rounds = run:%d res:%d, want server count 2", run.Rounds, res.Rounds)
			}
		})
	}
}

// P0 §4.1: persisted seed ids replace the internal hybrid search. The
// internal search for this query hits n-target; explicit n-other seeds
// must win, proving ExploreWithSeeds skipped the search.
func TestExploreWithSeedsUsesProvidedSeeds(t *testing.T) {
	store := newExploreStore(t)
	retr := newExploreRetriever(t, store)
	backend := &fakeExploreBackend{t: t}
	ex := NewExplorer(store, retr, backend, testExploreConfig(), "pi", nil)

	res, err := ex.ExploreWithSeeds(context.Background(), "dispatch router retries", []string{"n-other"})
	if err != nil {
		t.Fatalf("ExploreWithSeeds: %v", err)
	}
	if len(backend.errs) > 0 {
		t.Fatalf("fake backend tool errors: %v", backend.errs)
	}
	// The fake backend explores and submits the FIRST prompt seed node, so
	// an adopted n-other proves the provided seeds reached the trajectory.
	if !res.Found || len(res.NodeIDs) != 1 || res.NodeIDs[0] != "n-other" {
		t.Fatalf("result = %+v, want found run adopting provided seed n-other", res)
	}
}

// Empty seed ids fall back to the internal hybrid search (backtest and
// direct-caller compatibility).
func TestExploreWithSeedsEmptySeedsFallsBackToSearch(t *testing.T) {
	store := newExploreStore(t)
	retr := newExploreRetriever(t, store)
	backend := &fakeExploreBackend{t: t}
	ex := NewExplorer(store, retr, backend, testExploreConfig(), "pi", nil)

	res, err := ex.ExploreWithSeeds(context.Background(), "dispatch router retries", nil)
	if err != nil {
		t.Fatalf("ExploreWithSeeds: %v", err)
	}
	if len(backend.errs) > 0 {
		t.Fatalf("fake backend tool errors: %v", backend.errs)
	}
	if !res.Found || len(res.NodeIDs) != 1 || res.NodeIDs[0] != "n-target" {
		t.Fatalf("result = %+v, want internal-search hit n-target", res)
	}
}

func TestExploreGarbageOutputMarkedFailed(t *testing.T) {
	store := newExploreStore(t)
	retr := newExploreRetriever(t, store)
	backend := &fakeExploreBackend{t: t, garbage: true}
	ex := NewExplorer(store, retr, backend, testExploreConfig(), "pi", nil)

	res, err := ex.Explore(context.Background(), "dispatch router retries")
	if err != nil {
		t.Fatalf("Explore returned Go error for a miss: %v", err)
	}
	if res.Found {
		t.Fatalf("Found = true, want false")
	}
	if res.Summary != "" {
		t.Fatalf("Summary = %q, want empty", res.Summary)
	}
	if len(res.AgentRuns) != 1 || res.AgentRuns[0].Error == "" {
		t.Fatalf("AgentRuns = %+v, want one failed run", res.AgentRuns)
	}
	if res.TraceID == "" {
		t.Fatalf("TraceID is empty even on miss")
	}
}

// Explorer-level budget enforcement (design Q15/A6): a budget-blown run is
// forced to Found=false and never adopted even when its final response
// claims found=true; its within-budget sibling is adopted normally.
func TestExploreBudgetBlownRunNotAdopted(t *testing.T) {
	store := newExploreStore(t)
	retr := newExploreRetriever(t, store)
	backend := &fakeExploreBackend{
		t:              t,
		expandsPerCall: func(callIdx int) int { return callIdx }, // run 1: 1 expand; run 2: 2 expands (blown)
	}
	cfg := testExploreConfig()
	cfg.Agents = 2
	cfg.MaxRounds = 1
	ex := NewExplorer(store, retr, backend, cfg, "pi", nil)

	res, err := ex.Explore(context.Background(), "dispatch router retries")
	if err != nil {
		t.Fatalf("Explore: %v", err)
	}
	if len(backend.errs) > 0 {
		t.Fatalf("fake backend tool errors: %v", backend.errs)
	}
	if len(res.AgentRuns) != 2 {
		t.Fatalf("len(AgentRuns) = %d, want 2", len(res.AgentRuns))
	}
	// The backend call index that performs 2 expands blows the MaxRounds=1
	// budget; which trajectory that is depends on goroutine scheduling, so
	// assert over the pair: exactly one run is server-forced to not-found,
	// exactly one stays found.
	blownRuns, foundRuns := 0, 0
	for _, run := range res.AgentRuns {
		if run.Error != "" {
			t.Fatalf("run error: %s", run.Error)
		}
		if run.Found {
			foundRuns++
		} else {
			blownRuns++
		}
	}
	if blownRuns != 1 || foundRuns != 1 {
		t.Fatalf("runs = %+v, want 1 budget-blown (not-found) + 1 found", res.AgentRuns)
	}
	// The within-budget run is adopted regardless of what the blown run's
	// final response claimed.
	if !res.Found || res.Rounds != 1 {
		t.Fatalf("adopted = found:%v rounds:%d, want true/1 (within-budget run)", res.Found, res.Rounds)
	}
}

func TestExploreParallelAgentsAdoptFewestRounds(t *testing.T) {
	store := newExploreStore(t)
	retr := newExploreRetriever(t, store)
	backend := &fakeExploreBackend{
		t:              t,
		expandsPerCall: func(callIdx int) int { return callIdx }, // 1, 2, 3 rounds
	}
	cfg := testExploreConfig()
	cfg.Agents = 3
	ex := NewExplorer(store, retr, backend, cfg, "pi", nil)

	res, err := ex.Explore(context.Background(), "dispatch router retries")
	if err != nil {
		t.Fatalf("Explore: %v", err)
	}
	if len(backend.errs) > 0 {
		t.Fatalf("fake backend tool errors: %v", backend.errs)
	}
	backend.mu.Lock()
	calls := backend.calls
	backend.mu.Unlock()
	if calls != 3 {
		t.Fatalf("backend invocations = %d, want 3", calls)
	}
	if len(res.AgentRuns) != 3 {
		t.Fatalf("len(AgentRuns) = %d, want 3", len(res.AgentRuns))
	}
	for i, run := range res.AgentRuns {
		if run.Seed != i {
			t.Fatalf("AgentRuns[%d].Seed = %d, want %d", i, run.Seed, i)
		}
		if run.Error != "" {
			t.Fatalf("AgentRuns[%d] error: %s", i, run.Error)
		}
	}
	if !res.Found {
		t.Fatalf("Found = false, want true")
	}
	if res.Rounds != 1 {
		t.Fatalf("adopted Rounds = %d, want 1 (fewest)", res.Rounds)
	}
}

// ---------------------------------------------------------------------------
// ExploreToolServer tests
// ---------------------------------------------------------------------------

func startTestToolServer(t *testing.T, store *Store, cfg ExploreConfig) (*ExploreToolServer, string, string) {
	t.Helper()
	srv, err := NewExploreToolServer(store, nil, cfg, 1)
	if err != nil {
		t.Fatalf("NewExploreToolServer: %v", err)
	}
	baseURL, token, err := srv.Start(context.Background())
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	})
	return srv, baseURL, token
}

func TestExploreToolServerAuth(t *testing.T) {
	store := newExploreGraphStore(t)
	_, baseURL, token := startTestToolServer(t, store, testExploreConfig())
	payload := map[string]any{"trajectory_id": "t1", "node_ids": []string{"a"}}

	if status, _ := explorePost(baseURL, "", "/explore", payload); status != http.StatusUnauthorized {
		t.Fatalf("missing bearer: status = %d, want 401", status)
	}
	if status, _ := explorePost(baseURL, "wrong-token", "/explore", payload); status != http.StatusUnauthorized {
		t.Fatalf("wrong bearer: status = %d, want 401", status)
	}
	if status, _ := explorePost(baseURL, token, "/explore", payload); status != http.StatusOK {
		t.Fatalf("correct bearer: status = %d, want 200", status)
	}
}

// The inline neighbor list of a served node honors the MaxExpandPerRound cap
// (spec §4.1: the per-node neighbor cap is retained as a distinct limit).
func TestExploreToolServerExploreNeighborCap(t *testing.T) {
	store := newExploreGraphStore(t)
	cfg := testExploreConfig()
	cfg.MaxRounds = 1
	cfg.MaxExpandPerRound = 2
	_, baseURL, token := startTestToolServer(t, store, cfg)

	resp := exploreNodes(t, baseURL, token, "t1", "b")
	if len(resp.Nodes) != 1 {
		t.Fatalf("len(nodes) = %d, want 1", len(resp.Nodes))
	}
	if len(resp.Nodes[0].Neighbors) != 2 {
		t.Fatalf("len(neighbors) = %d, want cap 2", len(resp.Nodes[0].Neighbors))
	}
	if !resp.BudgetExceeded {
		t.Fatalf("budget_exceeded = false, want true at MaxRounds=1")
	}
}

// A budget-blown trajectory's /submit is recorded but forced to Found=false
// (design Q15/A6); a well-formed submit cannot override the blown flag. A
// sibling trajectory within budget is unaffected.
func TestExploreToolServerBlownTrajectorySubmitForcedNotFound(t *testing.T) {
	store := newExploreGraphStore(t)
	cfg := testExploreConfig()
	cfg.MaxRounds = 1
	srv, baseURL, token := startTestToolServer(t, store, cfg)

	// t1 blows the budget, then submits found=true with a valid citation.
	for i := 0; i < 2; i++ {
		if status, body := explorePost(baseURL, token, "/explore", map[string]any{"trajectory_id": "t1", "node_ids": []string{"b"}}); status != http.StatusOK {
			t.Fatalf("explore %d: status = %d body %s", i, status, body)
		}
	}
	status, body := explorePost(baseURL, token, "/submit", map[string]any{
		"trajectory_id": "t1", "found": true, "summary": "s", "node_ids": []string{"b"},
	})
	if status != http.StatusOK {
		t.Fatalf("submit: status = %d body %s", status, body)
	}
	var resp submitResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("submit decode: %v", err)
	}
	if len(resp.NodeIDs) != 1 || resp.NodeIDs[0] != "b" {
		t.Fatalf("kept node_ids = %v, want [b] (submission still recorded)", resp.NodeIDs)
	}
	var sawBudgetWarning bool
	for _, w := range resp.Warnings {
		if strings.Contains(w, "budget exceeded") {
			sawBudgetWarning = true
		}
	}
	if !sawBudgetWarning {
		t.Fatalf("warnings = %v, want a budget-exceeded warning", resp.Warnings)
	}
	if sub := srv.trajectorySubmission("t1"); sub == nil || sub.Found {
		t.Fatalf("stored submission = %+v, want Found=false (budget-blown)", sub)
	}

	// The non-blown sibling trajectory submits found=true and keeps it.
	if status, body := explorePost(baseURL, token, "/explore", map[string]any{"trajectory_id": "t2", "node_ids": []string{"b"}}); status != http.StatusOK {
		t.Fatalf("t2 explore: status = %d body %s", status, body)
	}
	status, body = explorePost(baseURL, token, "/submit", map[string]any{
		"trajectory_id": "t2", "found": true, "summary": "s", "node_ids": []string{"b"},
	})
	if status != http.StatusOK {
		t.Fatalf("t2 submit: status = %d body %s", status, body)
	}
	var resp2 submitResponse
	if err := json.Unmarshal(body, &resp2); err != nil {
		t.Fatalf("t2 submit decode: %v", err)
	}
	if len(resp2.Warnings) != 0 {
		t.Fatalf("t2 warnings = %v, want none", resp2.Warnings)
	}
	if sub := srv.trajectorySubmission("t2"); sub == nil || !sub.Found {
		t.Fatalf("t2 stored submission = %+v, want Found=true (sibling unaffected)", sub)
	}
}

func TestExploreToolServerSubmitValidation(t *testing.T) {
	store := newExploreGraphStore(t)
	_, baseURL, token := startTestToolServer(t, store, testExploreConfig())

	if err := store.WriteStagingSegment("s1", []byte("staging summary for dispatch segment")); err != nil {
		t.Fatalf("WriteStagingSegment: %v", err)
	}

	// Register + view graph and staging ids via a real /explore (the merged
	// protocol registers the trajectory and marks the served ids viewed), so
	// submitting exactly the viewed ids is clean.
	status, body := explorePost(baseURL, token, "/explore", map[string]any{
		"trajectory_id": "t1",
		"node_ids":      []string{"a", "seg:s1"},
	})
	if status != http.StatusOK {
		t.Fatalf("explore: status = %d body %s", status, body)
	}

	// Valid graph + staging ids are both kept with no warnings.
	status, body = explorePost(baseURL, token, "/submit", map[string]any{
		"trajectory_id": "t1",
		"found":         true,
		"summary":       "s",
		"node_ids":      []string{"a", "seg:s1"},
	})
	if status != http.StatusOK {
		t.Fatalf("submit: status = %d body %s", status, body)
	}
	var resp submitResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("submit decode: %v", err)
	}
	if len(resp.NodeIDs) != 2 || len(resp.Warnings) != 0 {
		t.Fatalf("kept = %v warnings = %v, want 2 kept / 0 warnings", resp.NodeIDs, resp.Warnings)
	}

	// A citation that was never viewed is a hard conflict rejection, not a
	// silent drop (traversal-state semantics: unviewed ids cannot be claimed).
	status, body = explorePost(baseURL, token, "/submit", map[string]any{
		"trajectory_id": "t1",
		"found":         true,
		"summary":       "s",
		"node_ids":      []string{"a", "ghost"},
	})
	if status != http.StatusConflict {
		t.Fatalf("unviewed submit: status = %d body %s, want 409", status, body)
	}

	// An unregistered trajectory is rejected outright.
	status, body = explorePost(baseURL, token, "/submit", map[string]any{
		"trajectory_id": "t2",
		"found":         true,
		"summary":       "s",
		"node_ids":      []string{"a"},
	})
	if status != http.StatusNotFound {
		t.Fatalf("unregistered submit: status = %d body %s, want 404", status, body)
	}
}

// ---------------------------------------------------------------------------
// Version pinning (design R5/R12)
// ---------------------------------------------------------------------------

// switchingExploreBackend switches the store's current pointer to a fresh
// version — in which the target node no longer exists — between two /explore
// calls of the same trajectory. Under version pinning the in-flight explore
// must keep reading the pinned v1 graph.
type switchingExploreBackend struct {
	t     *testing.T
	store *Store

	mu                  sync.Mutex
	viewedBody          string
	secondExploreStatus int
	submitStatus        int
}

func (b *switchingExploreBackend) Execute(_ context.Context, prompt string, _ agent.ExecOptions) (*agent.Session, error) {
	base := promptField(prompt, "Tool server base URL: ")
	token := promptField(prompt, "Bearer token: ")
	traj := promptField(prompt, "Trajectory ID: ")
	node := firstSeedNode(prompt)
	if base == "" || token == "" || traj == "" || node == "" {
		return nil, fmt.Errorf("prompt missing tool coordinates")
	}

	// First explore of the seed on the pinned version.
	status, body := explorePost(base, token, "/explore", map[string]any{"trajectory_id": traj, "node_ids": []string{node}})
	if status != http.StatusOK {
		return nil, fmt.Errorf("explore %s: status %d", node, status)
	}
	var er exploreResponse
	if err := json.Unmarshal(body, &er); err != nil {
		return nil, fmt.Errorf("explore decode: %v", err)
	}
	if len(er.Nodes) != 1 {
		return nil, fmt.Errorf("explore returned %d nodes, want 1", len(er.Nodes))
	}

	// Mid-flight version switch: v2 drops the target node entirely. An
	// unpinned explore would now 404 on the next /explore and fail /submit.
	v2, err := b.store.CreateVersionFrom(1, "ttt")
	if err != nil {
		return nil, fmt.Errorf("create v2: %v", err)
	}
	if err := os.Remove(filepath.Join(b.store.VersionDir(v2), "nodes", node+".md")); err != nil {
		return nil, fmt.Errorf("remove node from v2: %v", err)
	}
	if err := b.store.SwitchCurrent(v2); err != nil {
		return nil, fmt.Errorf("switch current to v2: %v", err)
	}

	b.mu.Lock()
	b.viewedBody = er.Nodes[0].Body
	b.mu.Unlock()

	secondExploreStatus, _ := explorePost(base, token, "/explore", map[string]any{"trajectory_id": traj, "node_ids": []string{node}})
	submitStatus, _ := explorePost(base, token, "/submit", map[string]any{
		"trajectory_id": traj, "found": true, "summary": "s", "node_ids": []string{node},
	})
	b.mu.Lock()
	b.secondExploreStatus = secondExploreStatus
	b.submitStatus = submitStatus
	b.mu.Unlock()

	out := fmt.Sprintf(`{"found":true,"summary":"pinned summary","node_ids":[%q],"rounds":2}`, node)
	return exploreCompletedSession(out), nil
}

// TestExplorePinsVersionForWholeCall (design R5/R12): a consolidation switch
// in the middle of an explore must not swap the graph under the in-flight
// trajectory — every /explore and /submit reads the version pinned at
// Explore start, and the result records that version.
func TestExplorePinsVersionForWholeCall(t *testing.T) {
	store := newExploreStore(t) // v1 with n-target / n-other
	retr := newExploreRetriever(t, store)
	backend := &switchingExploreBackend{t: t, store: store}

	ex := NewExplorer(store, retr, backend, testExploreConfig(), "pi", nil)
	res, err := ex.Explore(context.Background(), "dispatch router retries")
	if err != nil {
		t.Fatalf("Explore: %v", err)
	}
	if !res.Found {
		t.Fatalf("Found = false (explore=%d submit=%d), want a successful pinned explore", backend.secondExploreStatus, backend.submitStatus)
	}
	if res.Version != 1 {
		t.Fatalf("Version = %d, want 1 (the version pinned at explore start)", res.Version)
	}
	if backend.secondExploreStatus != http.StatusOK {
		t.Fatalf("second explore status = %d, want 200 (pinned v1 still serves the node deleted in v2)", backend.secondExploreStatus)
	}
	if backend.submitStatus != http.StatusOK {
		t.Fatalf("submit status = %d, want 200 (cited node validated against pinned v1)", backend.submitStatus)
	}
	wantBody := "the dispatch router retries failed batch jobs with exponential backoff"
	if backend.viewedBody != wantBody {
		t.Fatalf("viewed body = %q, want the pinned v1 body %q", backend.viewedBody, wantBody)
	}
	if len(res.NodeIDs) != 1 || res.NodeIDs[0] != "n-target" {
		t.Fatalf("NodeIDs = %v, want [n-target]", res.NodeIDs)
	}
}
