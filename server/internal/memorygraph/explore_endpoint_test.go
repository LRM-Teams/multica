package memorygraph

// Tests for the merged /explore endpoint (spec §4): one call returns each
// requested node's body plus inline neighbor edge info, and consumes rounds
// equal to the number of nodes actually served. Reuses the tool-server
// helpers from explore_test.go.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"
)

func exploreNodes(t *testing.T, baseURL, token, traj string, nodeIDs ...string) exploreResponse {
	t.Helper()
	status, body := explorePost(baseURL, token, "/explore", map[string]any{
		"trajectory_id": traj,
		"node_ids":      nodeIDs,
	})
	if status != http.StatusOK {
		t.Fatalf("explore: status = %d body %s", status, body)
	}
	var resp exploreResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("explore decode: %v", err)
	}
	return resp
}

// One requested node returns its body plus inline neighbors, and consumes
// exactly one round (rounds += nodes served).
func TestExploreToolServerExploreReturnsBodyAndNeighbors(t *testing.T) {
	store := newExploreGraphStore(t)
	cfg := testExploreConfig()
	cfg.MaxRounds = 5
	cfg.MaxExpandPerRound = 10
	srv, baseURL, token := startTestToolServer(t, store, cfg)

	resp := exploreNodes(t, baseURL, token, "t1", "b")
	if resp.Round != 1 || resp.BudgetExceeded {
		t.Fatalf("round = %d budget_exceeded=%v, want 1/false", resp.Round, resp.BudgetExceeded)
	}
	if len(resp.Nodes) != 1 {
		t.Fatalf("len(nodes) = %d, want 1", len(resp.Nodes))
	}
	n := resp.Nodes[0]
	if n.NodeID != "b" || n.Level != 1 {
		t.Fatalf("node = %s level %d, want b/1", n.NodeID, n.Level)
	}
	if n.Body != "mid-level summary of dispatch behavior" {
		t.Fatalf("body = %q", n.Body)
	}
	got := make([]string, 0, len(n.Neighbors))
	vias := make(map[string]string)
	for _, c := range n.Neighbors {
		got = append(got, c.NodeID)
		vias[c.NodeID] = c.Via
	}
	want := []string{"a", "c", "e", "d"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("neighbors = %v, want %v", got, want)
	}
	if vias["e"] != EdgeTypeEvidenceFor || vias["d"] != EdgeTypeContradicts {
		t.Fatalf("vias = %v", vias)
	}
	if got := srv.trajectoryRounds("t1"); got != 1 {
		t.Fatalf("server round count = %d, want 1", got)
	}
}

// A batch of N nodes consumes N rounds (rounds += nodes served), not one
// round per call.
func TestExploreToolServerExploreBatchConsumesRoundsPerNode(t *testing.T) {
	store := newExploreGraphStore(t)
	cfg := testExploreConfig()
	cfg.MaxRounds = 5
	cfg.MaxExpandPerRound = 10
	srv, baseURL, token := startTestToolServer(t, store, cfg)

	resp := exploreNodes(t, baseURL, token, "t1", "b", "c", "d")
	if len(resp.Nodes) != 3 {
		t.Fatalf("len(nodes) = %d, want 3", len(resp.Nodes))
	}
	if resp.Round != 3 {
		t.Fatalf("round = %d, want 3 (one per node served)", resp.Round)
	}
	if resp.BudgetExceeded {
		t.Fatalf("budget_exceeded = true, want false (3 of 5 rounds used)")
	}
	if got := srv.trajectoryRounds("t1"); got != 3 {
		t.Fatalf("server round count = %d, want 3", got)
	}
}

// Body truncation (MaxNodeChars) still applies to the merged endpoint.
func TestExploreToolServerExploreTruncatesBody(t *testing.T) {
	store := newExploreGraphStore(t)
	cfg := testExploreConfig()
	cfg.MaxRounds = 5
	cfg.MaxNodeChars = 50
	_, baseURL, token := startTestToolServer(t, store, cfg)

	resp := exploreNodes(t, baseURL, token, "t1", "a")
	if len(resp.Nodes) != 1 {
		t.Fatalf("len(nodes) = %d, want 1", len(resp.Nodes))
	}
	n := resp.Nodes[0]
	if !n.Truncated || len(n.Body) != 50 {
		t.Fatalf("body len = %d truncated=%v, want 50/true", len(n.Body), n.Truncated)
	}
}

// Budget is enforced by nodes-served: with one round left, a 2-node batch
// serves only 1 node, reports budget_exceeded, and — as an over-budget
// request — marks the trajectory budget-blown (forcing Found=false on
// submit). A fully-served request that merely spends the last round does
// not blow (see TestExploreToolServerExactBudgetSubmitKeepsFound).
func TestExploreToolServerExploreServesUpToRemainingBudget(t *testing.T) {
	store := newExploreGraphStore(t)
	cfg := testExploreConfig()
	cfg.MaxRounds = 1
	cfg.MaxExpandPerRound = 10
	srv, baseURL, token := startTestToolServer(t, store, cfg)

	resp := exploreNodes(t, baseURL, token, "t1", "b", "c")
	if len(resp.Nodes) != 1 {
		t.Fatalf("len(nodes) = %d, want 1 (only one round remaining)", len(resp.Nodes))
	}
	if resp.Nodes[0].NodeID != "b" {
		t.Fatalf("served node = %s, want b (first requested)", resp.Nodes[0].NodeID)
	}
	if !resp.BudgetExceeded {
		t.Fatalf("budget_exceeded = false, want true (budget now spent)")
	}
	if got := srv.trajectoryRounds("t1"); got != 1 {
		t.Fatalf("server round count = %d, want 1", got)
	}
	// Asking for two nodes with one round left is an over-budget request
	// (spec §4.2: 超预算 → trajectory Found=false): it got truncated, so the
	// trajectory is blown even though the served part fit the budget.
	if !srv.trajectoryBudgetBlown("t1") {
		t.Fatalf("straddling batch must mark the trajectory budget-blown")
	}
}

// Once the budget is spent, further /explore calls are rejected with 200 +
// budget_exceeded=true and zero nodes, and do not consume more rounds.
func TestExploreToolServerExploreBeyondBudgetRejected(t *testing.T) {
	store := newExploreGraphStore(t)
	cfg := testExploreConfig()
	cfg.MaxRounds = 1
	srv, baseURL, token := startTestToolServer(t, store, cfg)

	// Spend the single round.
	resp := exploreNodes(t, baseURL, token, "t1", "b")
	if len(resp.Nodes) != 1 || !resp.BudgetExceeded {
		t.Fatalf("first explore = %+v, want 1 node served with budget_exceeded", resp)
	}
	// Subsequent calls are rejected with empty nodes.
	for i := 0; i < 2; i++ {
		resp = exploreNodes(t, baseURL, token, "t1", "c")
		if len(resp.Nodes) != 0 || !resp.BudgetExceeded {
			t.Fatalf("over-budget explore %d = %+v, want 0 nodes + budget_exceeded", i+2, resp)
		}
	}
	if got := srv.trajectoryRounds("t1"); got != 1 {
		t.Fatalf("server round count = %d, want 1 (rejected calls do not consume rounds)", got)
	}
}

// An unknown or out-of-view node fails closed with the same 404 shape as a
// missing node (no existence leak).
func TestExploreToolServerExploreUnknownNodeFailsClosed(t *testing.T) {
	store := newExploreGraphStore(t)
	cfg := testExploreConfig()
	_, baseURL, token := startTestToolServer(t, store, cfg)

	status, _ := explorePost(baseURL, token, "/explore", map[string]any{
		"trajectory_id": "t1",
		"node_ids":      []string{"ghost"},
	})
	if status != http.StatusNotFound {
		t.Fatalf("explore unknown node: status = %d, want 404", status)
	}
}

// A batch containing one unknown node fails the whole call closed (404) and
// consumes no rounds.
func TestExploreToolServerExploreBatchWithUnknownFailsClosed(t *testing.T) {
	store := newExploreGraphStore(t)
	cfg := testExploreConfig()
	srv, baseURL, token := startTestToolServer(t, store, cfg)

	status, _ := explorePost(baseURL, token, "/explore", map[string]any{
		"trajectory_id": "t1",
		"node_ids":      []string{"b", "ghost"},
	})
	if status != http.StatusNotFound {
		t.Fatalf("explore batch with unknown: status = %d, want 404", status)
	}
	if got := srv.trajectoryRounds("t1"); got != 0 {
		t.Fatalf("server round count = %d, want 0 (failed call consumes nothing)", got)
	}
}

// Empty node_ids is a bad request.
func TestExploreToolServerExploreEmptyNodeIDs(t *testing.T) {
	store := newExploreGraphStore(t)
	cfg := testExploreConfig()
	_, baseURL, token := startTestToolServer(t, store, cfg)

	status, _ := explorePost(baseURL, token, "/explore", map[string]any{
		"trajectory_id": "t1",
		"node_ids":      []string{},
	})
	if status != http.StatusBadRequest {
		t.Fatalf("explore empty node_ids: status = %d, want 400", status)
	}
}

// Spending exactly the final round is not a budget overrun: the submission
// may still report found=true even though the response marks the budget spent.
func TestExploreToolServerExactBudgetSubmitKeepsFound(t *testing.T) {
	store := newExploreGraphStore(t)
	cfg := testExploreConfig()
	cfg.MaxRounds = 1
	srv, baseURL, token := startTestToolServer(t, store, cfg)

	resp := exploreNodes(t, baseURL, token, "t1", "b")
	if len(resp.Nodes) != 1 || !resp.BudgetExceeded || srv.trajectoryBudgetBlown("t1") {
		t.Fatalf("exact-budget explore = %+v blown=%v, want served/exhausted/not-blown", resp, srv.trajectoryBudgetBlown("t1"))
	}
	status, body := explorePost(baseURL, token, "/submit", map[string]any{
		"trajectory_id": "t1", "found": true, "summary": "s", "node_ids": []string{"b"},
	})
	if status != http.StatusOK {
		t.Fatalf("submit: status = %d body %s", status, body)
	}
	var submitted submitResponse
	if err := json.Unmarshal(body, &submitted); err != nil {
		t.Fatalf("decode submit response: %v", err)
	}
	if len(submitted.Citations) != 1 || submitted.Citations[0].NodeID != "b" || submitted.Citations[0].GraphVersion != storeCurrentVersion(t, store) || submitted.Citations[0].FirstParagraph == "" || submitted.Citations[0].CapturedAt.IsZero() {
		t.Fatalf("citation snapshot = %+v, want immutable metadata for viewed node b", submitted.Citations)
	}
	if sub := srv.trajectorySubmission("t1"); sub == nil || !sub.Found {
		t.Fatalf("stored submission = %+v, want Found=true", sub)
	}
}

// A batch larger than the remaining budget is an overrun even when the
// server returns the prefix it could serve.
func TestExploreToolServerOverBudgetBatchBlowsTrajectory(t *testing.T) {
	store := newExploreGraphStore(t)
	cfg := testExploreConfig()
	cfg.MaxRounds = 1
	srv, baseURL, token := startTestToolServer(t, store, cfg)

	resp := exploreNodes(t, baseURL, token, "t1", "b", "c")
	if len(resp.Nodes) != 1 || !resp.BudgetExceeded || !srv.trajectoryBudgetBlown("t1") {
		t.Fatalf("over-budget batch = %+v blown=%v, want partial/budget-exceeded/blown", resp, srv.trajectoryBudgetBlown("t1"))
	}
}

// A request after exact exhaustion is the overrun: budget_exceeded reports
// the response state, while budgetBlown records the later invalid request.
func TestExploreToolServerRequestAfterExactExhaustionBlowsTrajectory(t *testing.T) {
	store := newExploreGraphStore(t)
	cfg := testExploreConfig()
	cfg.MaxRounds = 1
	srv, baseURL, token := startTestToolServer(t, store, cfg)

	first := exploreNodes(t, baseURL, token, "t1", "b")
	if !first.BudgetExceeded || srv.trajectoryBudgetBlown("t1") {
		t.Fatalf("exact-budget response = %+v blown=%v, want budget-exceeded/not-blown", first, srv.trajectoryBudgetBlown("t1"))
	}
	second := exploreNodes(t, baseURL, token, "t1", "c")
	if len(second.Nodes) != 0 || !second.BudgetExceeded || !srv.trajectoryBudgetBlown("t1") {
		t.Fatalf("post-budget response = %+v blown=%v, want empty/budget-exceeded/blown", second, srv.trajectoryBudgetBlown("t1"))
	}
}

// The merged endpoint reapplies the graph view to both requested nodes and
// inline neighbors, so channel-only nodes cannot leak through a project view.
func TestExploreToolServerExploreAppliesGraphView(t *testing.T) {
	store := newExploreGraphStore(t)
	g, err := LoadGraph(store, 1)
	if err != nil {
		t.Fatalf("LoadGraph: %v", err)
	}
	channelNode := g.Node("c")
	channelNode.Visibility = "channel"
	channelNode.ChannelID = "chan-a"
	if err := store.SaveNode(1, channelNode); err != nil {
		t.Fatalf("SaveNode channel node: %v", err)
	}

	retrieval := DefaultRetrievalConfig()
	retrieval.View = GraphView{AllowProject: true}
	retr := NewHybridRetriever(store, nil, retrieval)
	srv, err := NewExploreToolServer(store, retr, testExploreConfig(), 1)
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

	resp := exploreNodes(t, baseURL, token, "t1", "b")
	for _, neighbor := range resp.Nodes[0].Neighbors {
		if neighbor.NodeID == "c" {
			t.Fatalf("out-of-view channel node leaked through neighbors: %+v", resp.Nodes[0].Neighbors)
		}
	}
	status, _ := explorePost(baseURL, token, "/explore", map[string]any{
		"trajectory_id": "t2",
		"node_ids":      []string{"c"},
	})
	if status != http.StatusNotFound {
		t.Fatalf("explore out-of-view node: status = %d, want 404", status)
	}
}

func TestExploreToolServerNativeStartRedirectCheckpoint(t *testing.T) {
	store := newExploreGraphStore(t)
	retr := newExploreRetriever(t, store)
	cfg := testExploreConfig()
	cfg.MaxRounds = 10
	srv, err := NewExploreToolServer(store, retr, cfg, storeCurrentVersion(t, store))
	if err != nil {
		t.Fatal(err)
	}
	baseURL, token, err := srv.Start(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	})

	status, body := explorePost(baseURL, token, "/start", map[string]any{"query": "dispatch behavior", "idempotency_key": "start-1"})
	if status != http.StatusOK {
		t.Fatalf("start: status=%d body=%s", status, body)
	}
	var started memoryGraphStartResponse
	if err := json.Unmarshal(body, &started); err != nil {
		t.Fatal(err)
	}
	if started.TrajectoryID == "" || started.GraphVersion <= 0 || len(started.Nodes) == 0 {
		t.Fatalf("start response = %+v", started)
	}
	status, body = explorePost(baseURL, token, "/start", map[string]any{"query": "dispatch behavior", "idempotency_key": "start-1"})
	var replayed memoryGraphStartResponse
	if status != http.StatusOK || json.Unmarshal(body, &replayed) != nil || replayed.TrajectoryID != started.TrajectoryID {
		t.Fatalf("idempotent start replay: status=%d body=%s started=%+v", status, body, started)
	}
	status, body = explorePost(baseURL, token, "/start", map[string]any{"query": "different query", "idempotency_key": "start-1"})
	if status != http.StatusConflict || !strings.Contains(string(body), "IDEMPOTENCY_CONFLICT") {
		t.Fatalf("conflicting start replay: status=%d body=%s", status, body)
	}
	status, body = explorePost(baseURL, token, "/redirect", map[string]any{
		"trajectory_id":       started.TrajectoryID,
		"query":               "retry decision",
		"steering_message_id": "message-1",
	})
	if status != http.StatusOK {
		t.Fatalf("redirect: status=%d body=%s", status, body)
	}
	var redirected memoryGraphStartResponse
	if err := json.Unmarshal(body, &redirected); err != nil {
		t.Fatal(err)
	}
	if redirected.TrajectoryID != started.TrajectoryID || redirected.GraphVersion != started.GraphVersion {
		t.Fatalf("redirect changed trajectory/version: start=%+v redirect=%+v", started, redirected)
	}
	status, body = explorePost(baseURL, token, "/checkpoint", map[string]any{
		"trajectory_id": started.TrajectoryID,
		"state":         map[string]any{"objective": "continue retry investigation", "open_questions": []string{"when"}},
	})
	if status != http.StatusOK {
		t.Fatalf("checkpoint: status=%d body=%s", status, body)
	}
	status, body = explorePost(baseURL, token, "/submit", map[string]any{
		"trajectory_id": started.TrajectoryID, "found": false, "summary": "", "node_ids": []string{},
	})
	if status != http.StatusConflict || !strings.Contains(string(body), "TRAJECTORY_TERMINAL") {
		t.Fatalf("submit after checkpoint: status=%d body=%s", status, body)
	}
}

func storeCurrentVersion(t *testing.T, store *Store) int {
	t.Helper()
	version, err := store.CurrentVersion()
	if err != nil {
		t.Fatal(err)
	}
	return version
}

type fakeAgentToolLedgerOperation struct {
	operation string
	request   json.RawMessage
	response  json.RawMessage
	pending   bool
}

type fakeAgentToolLedger struct {
	trajectoryID string
	operations   map[string]*fakeAgentToolLedgerOperation
	viewed       []string
	finished     string
	state        json.RawMessage
	citations    []Citation
}

func newFakeAgentToolLedger(trajectoryID string) *fakeAgentToolLedger {
	return &fakeAgentToolLedger{trajectoryID: trajectoryID, operations: make(map[string]*fakeAgentToolLedgerOperation)}
}

func (l *fakeAgentToolLedger) TrajectoryID() string { return l.trajectoryID }

func (l *fakeAgentToolLedger) Reserve(_ context.Context, key, operation string, request json.RawMessage) (AgentToolOperationReservation, error) {
	if existing := l.operations[key]; existing != nil {
		if existing.operation != operation || string(existing.request) != string(request) {
			return AgentToolOperationReservation{}, errors.New("idempotency conflict")
		}
		return AgentToolOperationReservation{OperationID: key, Replay: !existing.pending, Pending: existing.pending, Response: existing.response}, nil
	}
	l.operations[key] = &fakeAgentToolLedgerOperation{operation: operation, request: append(json.RawMessage(nil), request...), pending: true}
	return AgentToolOperationReservation{OperationID: key}, nil
}

func (l *fakeAgentToolLedger) Complete(_ context.Context, operationID string, response json.RawMessage, _ string) error {
	op := l.operations[operationID]
	if op == nil || !op.pending {
		return errors.New("operation is not pending")
	}
	op.pending = false
	op.response = append(json.RawMessage(nil), response...)
	return nil
}

func (l *fakeAgentToolLedger) RecordViewed(_ context.Context, nodeIDs []string) error {
	l.viewed = append(l.viewed, nodeIDs...)
	return nil
}

func (l *fakeAgentToolLedger) Finish(_ context.Context, operationID, status string, state json.RawMessage, citations []Citation, response json.RawMessage) error {
	if err := l.Complete(context.Background(), operationID, response, ""); err != nil {
		return err
	}
	l.finished = status
	l.state = append(json.RawMessage(nil), state...)
	l.citations = append([]Citation(nil), citations...)
	return nil
}

func TestExploreToolServerDurableAgentLedgerOwnsFiveNativeOperations(t *testing.T) {
	store := newExploreGraphStore(t)
	retr := newExploreRetriever(t, store)
	ledger := newFakeAgentToolLedger("11111111-1111-1111-1111-111111111111")
	srv, err := NewExploreToolServerWithAgentLedger(store, retr, testExploreConfig(), storeCurrentVersion(t, store), nil, ledger)
	if err != nil {
		t.Fatal(err)
	}
	baseURL, token, err := srv.Start(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srv.Shutdown(context.Background()) })

	status, body := explorePost(baseURL, token, "/start", map[string]any{"query": "dispatch", "idempotency_key": "start-1"})
	if status != http.StatusOK {
		t.Fatalf("start status=%d body=%s", status, body)
	}
	var started memoryGraphStartResponse
	if json.Unmarshal(body, &started) != nil || started.TrajectoryID != ledger.trajectoryID || len(started.Nodes) == 0 {
		t.Fatalf("start response=%s", body)
	}
	status, replay := explorePost(baseURL, token, "/start", map[string]any{"query": "dispatch", "idempotency_key": "start-1"})
	var replayed memoryGraphStartResponse
	if status != http.StatusOK || json.Unmarshal(replay, &replayed) != nil || replayed.TrajectoryID != started.TrajectoryID || len(replayed.Nodes) != len(started.Nodes) {
		t.Fatalf("durable start replay status=%d body=%s first=%s", status, replay, body)
	}

	nodeID := started.Nodes[0].NodeID
	status, body = explorePost(baseURL, token, "/explore", map[string]any{
		"trajectory_id": ledger.trajectoryID, "node_ids": []string{nodeID}, "idempotency_key": "explore-1",
	})
	if status != http.StatusOK || len(ledger.viewed) != 1 || ledger.viewed[0] != nodeID {
		t.Fatalf("explore status=%d body=%s viewed=%v", status, body, ledger.viewed)
	}
	status, body = explorePost(baseURL, token, "/redirect", map[string]any{
		"trajectory_id": ledger.trajectoryID, "query": "retry", "steering_message_id": "message-1",
	})
	if status != http.StatusOK || ledger.operations["redirect:message-1"] == nil {
		t.Fatalf("redirect status=%d body=%s operations=%v", status, body, ledger.operations)
	}
	status, body = explorePost(baseURL, token, "/submit", map[string]any{
		"trajectory_id": ledger.trajectoryID, "found": true, "summary": "found", "node_ids": []string{nodeID}, "idempotency_key": "submit-1",
	})
	if status != http.StatusOK || ledger.finished != "submitted" || len(ledger.citations) != 1 || ledger.citations[0].NodeID != nodeID {
		t.Fatalf("submit status=%d body=%s finished=%s citations=%+v", status, body, ledger.finished, ledger.citations)
	}
}

func TestExploreToolServerDurableCheckpointTerminalizesLedger(t *testing.T) {
	store := newExploreGraphStore(t)
	ledger := newFakeAgentToolLedger("22222222-2222-2222-2222-222222222222")
	srv, err := NewExploreToolServerWithAgentLedger(store, newExploreRetriever(t, store), testExploreConfig(), storeCurrentVersion(t, store), nil, ledger)
	if err != nil {
		t.Fatal(err)
	}
	baseURL, token, _ := srv.Start(context.Background())
	t.Cleanup(func() { _ = srv.Shutdown(context.Background()) })
	status, body := explorePost(baseURL, token, "/start", map[string]any{"query": "dispatch", "idempotency_key": "start"})
	if status != http.StatusOK {
		t.Fatalf("start status=%d body=%s", status, body)
	}
	status, body = explorePost(baseURL, token, "/checkpoint", map[string]any{
		"trajectory_id": ledger.trajectoryID, "state": map[string]any{"objective": "resume later"}, "idempotency_key": "checkpoint-1",
	})
	if status != http.StatusOK || ledger.finished != "checkpointed" || !strings.Contains(string(ledger.state), "resume later") {
		t.Fatalf("checkpoint status=%d body=%s finished=%s state=%s", status, body, ledger.finished, ledger.state)
	}
}
