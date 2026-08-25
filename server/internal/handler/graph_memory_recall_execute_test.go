// SPDX-License-Identifier: Apache-2.0

package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/multica-ai/multica/server/internal/memorygraph"
	"github.com/multica-ai/multica/server/internal/service"
	"github.com/multica-ai/multica/server/internal/util"
	"github.com/multica-ai/multica/server/pkg/agent"
)

// scriptedRecallBackend uses the Explorer's loopback tool protocol so this
// endpoint test exercises server-side rounds, views, submissions, and citation
// qualification without launching a provider binary.
type scriptedRecallBackend struct {
	mu      sync.Mutex
	calls   int
	found   bool
	summary string
	err     error
}

func (b *scriptedRecallBackend) Execute(_ context.Context, prompt string, _ agent.ExecOptions) (*agent.Session, error) {
	b.mu.Lock()
	b.calls++
	b.mu.Unlock()
	if b.err != nil {
		return nil, b.err
	}
	base := recallPromptField(prompt, "Tool server base URL: ")
	token := recallPromptField(prompt, "Bearer token: ")
	trajectory := recallPromptField(prompt, "Trajectory ID: ")
	nodeID := recallFirstSeedNode(prompt)
	if base == "" || token == "" || trajectory == "" || nodeID == "" {
		return nil, fmt.Errorf("missing explore tool coordinates")
	}
	if status := recallToolPost(base, token, "/explore", map[string]any{
		"trajectory_id": trajectory, "node_ids": []string{nodeID},
	}); status != http.StatusOK {
		return nil, fmt.Errorf("explore status %d", status)
	}
	if status := recallToolPost(base, token, "/submit", map[string]any{
		"trajectory_id": trajectory, "found": b.found, "summary": b.summary, "node_ids": []string{nodeID},
	}); status != http.StatusOK {
		return nil, fmt.Errorf("submit status %d", status)
	}
	return recallCompletedSession(fmt.Sprintf(`{"found":%t,"summary":%q,"node_ids":[%q],"rounds":1}`, b.found, b.summary, nodeID)), nil
}

func (b *scriptedRecallBackend) count() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.calls
}

func recallCompletedSession(output string) *agent.Session {
	messages := make(chan agent.Message)
	close(messages)
	result := make(chan agent.Result, 1)
	result <- agent.Result{Status: "completed", Output: output}
	close(result)
	return &agent.Session{Messages: messages, Result: result}
}

func recallPromptField(prompt, prefix string) string {
	for _, line := range strings.Split(prompt, "\n") {
		if strings.HasPrefix(line, prefix) {
			return strings.TrimSpace(strings.TrimPrefix(line, prefix))
		}
	}
	return ""
}

func recallFirstSeedNode(prompt string) string {
	for _, line := range strings.Split(prompt, "\n") {
		if !strings.HasPrefix(line, "- ") || strings.Contains(line, "(none") {
			continue
		}
		if parts := strings.SplitN(strings.TrimPrefix(line, "- "), ": ", 2); len(parts) == 2 {
			return parts[0]
		}
	}
	return ""
}

func recallToolPost(base, token, path string, payload any) int {
	body, err := json.Marshal(payload)
	if err != nil {
		return 0
	}
	req, err := http.NewRequest(http.MethodPost, base+path, bytes.NewReader(body))
	if err != nil {
		return 0
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	if err != nil {
		return 0
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.StatusCode
}

func TestGraphMemoryRecallExecutionHappyPath(t *testing.T) {
	if testPool == nil {
		t.Skip("handler test database unavailable")
	}
	root := t.TempDir()
	fx := mustGraphMemoryRecallEndpointFixture(t, root)
	dir := mustGraphMemoryGraphDir(t, root, util.UUIDToString(fx.workspaceID), memorygraph.GraphDirKindProject, fx.projectID)
	store := memorygraph.NewStore(dir)
	if err := store.SaveNode(1, &memorygraph.Node{
		NodeID: "recall-node", Body: "dispatch retries use exponential backoff", Level: 2,
		CreatedBy: memorygraph.CreatorIngester, CreatedVersion: 1, UpdatedVersion: 1, ObservedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	backend := &scriptedRecallBackend{found: true, summary: "bounded server-side recall summary"}
	h := newRecallEndpointHandler(root, &countingRecallSeeder{ids: []string{"recall-node"}})
	h.GraphMemoryRecallExecutor = service.NewGraphMemoryRecallExecutor(
		testPool, service.NewGraphMemoryDiveService(testPool),
		func(context.Context, *service.GraphMemoryRecallPlan) (memorygraph.AgentBackend, error) {
			return backend, nil
		},
		nil, nil, "test-model",
	)
	traceID := "trace-exec-" + uuid.NewString()[:8]
	rec := doGraphMemoryRecall(t, h, util.UUIDToString(fx.workspaceID), fx.daemonID, graphMemoryRecallBody(fx, traceID, "dispatch retries"))
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d body %s, want 202", rec.Code, rec.Body.String())
	}
	resp := decodeRecallResponse(t, rec)
	if resp["found"] != true || resp["injection"] == "" || !strings.Contains(resp["injection"].(string), "cited nodes:") {
		t.Fatalf("response = %v, want found bounded injection", resp)
	}
	recallID, _ := resp["recall_id"].(string)
	var trajectoryStatus, recallStatus string
	var rounds int
	var terminalAt *time.Time
	if err := testPool.QueryRow(context.Background(), `
		SELECT t.status, t.rounds, t.terminal_at, r.status
		FROM graph_memory_trajectory t JOIN graph_memory_recall r ON r.id = t.recall_id
		WHERE t.recall_id = $1 AND t.seed_index = 0
	`, recallID).Scan(&trajectoryStatus, &rounds, &terminalAt, &recallStatus); err != nil {
		t.Fatal(err)
	}
	if trajectoryStatus != "found" || rounds < 1 || terminalAt == nil || recallStatus != "dive_queued" {
		t.Fatalf("ledger = trajectory(%s rounds=%d terminal=%v), recall=%s", trajectoryStatus, rounds, terminalAt, recallStatus)
	}
	var jobs int
	if err := testPool.QueryRow(context.Background(), `SELECT count(*) FROM graph_memory_dive_job WHERE recall_id = $1`, recallID).Scan(&jobs); err != nil {
		t.Fatal(err)
	}
	if jobs != 1 || backend.count() != 1 {
		t.Fatalf("dive jobs = %d backend calls = %d, want 1 each", jobs, backend.count())
	}
	entries, err := store.ReadQueryLog("daemon")
	if err != nil {
		t.Fatalf("ReadQueryLog: %v", err)
	}
	if len(entries) != 1 || entries[0].TraceID != traceID || entries[0].Query != "dispatch retries" || entries[0].Version != 1 || !entries[0].Found || entries[0].Rounds < 1 || len(entries[0].NodeIDs) != 1 || entries[0].NodeIDs[0] != "recall-node" {
		t.Fatalf("query log entries = %+v, want persisted recall", entries)
	}
	replay := doGraphMemoryRecall(t, h, util.UUIDToString(fx.workspaceID), fx.daemonID, graphMemoryRecallBody(fx, traceID, "dispatch retries"))
	if replay.Code != http.StatusOK {
		t.Fatalf("replay status = %d body %s, want 200", replay.Code, replay.Body.String())
	}
	replayResponse := decodeRecallResponse(t, replay)
	if replayResponse["injection"] != resp["injection"] || backend.count() != 1 {
		t.Fatalf("replay = %v backend calls = %d, want same injection and no provider call", replayResponse, backend.count())
	}
}

func TestGraphMemoryRecallExecutionMissFailureAndReplay(t *testing.T) {
	if testPool == nil {
		t.Skip("handler test database unavailable")
	}
	t.Run("miss queues dive", func(t *testing.T) {
		root := t.TempDir()
		fx := mustGraphMemoryRecallEndpointFixture(t, root)
		dir := mustGraphMemoryGraphDir(t, root, util.UUIDToString(fx.workspaceID), memorygraph.GraphDirKindProject, fx.projectID)
		store := memorygraph.NewStore(dir)
		if err := store.SaveNode(1, &memorygraph.Node{NodeID: "miss-node", Body: "nothing useful", CreatedBy: memorygraph.CreatorIngester, CreatedVersion: 1, UpdatedVersion: 1, ObservedAt: time.Now().UTC()}); err != nil {
			t.Fatal(err)
		}
		backend := &scriptedRecallBackend{found: false, summary: "no match"}
		h := newRecallEndpointHandler(root, &countingRecallSeeder{ids: []string{"miss-node"}})
		h.GraphMemoryRecallExecutor = service.NewGraphMemoryRecallExecutor(testPool, service.NewGraphMemoryDiveService(testPool), func(context.Context, *service.GraphMemoryRecallPlan) (memorygraph.AgentBackend, error) {
			return backend, nil
		}, nil, nil, "test-model")
		rec := doGraphMemoryRecall(t, h, util.UUIDToString(fx.workspaceID), fx.daemonID, graphMemoryRecallBody(fx, "trace-miss-"+uuid.NewString()[:8], "nothing"))
		if rec.Code != http.StatusAccepted {
			t.Fatalf("status = %d body %s", rec.Code, rec.Body.String())
		}
		resp := decodeRecallResponse(t, rec)
		if resp["found"] != false || resp["injection"] != "" {
			t.Fatalf("response = %v, want bounded miss", resp)
		}
		var trajectoryStatus, recallStatus string
		if err := testPool.QueryRow(context.Background(), `SELECT t.status, r.status FROM graph_memory_trajectory t JOIN graph_memory_recall r ON r.id = t.recall_id WHERE t.recall_id = $1`, resp["recall_id"]).Scan(&trajectoryStatus, &recallStatus); err != nil {
			t.Fatal(err)
		}
		if trajectoryStatus != "miss" || recallStatus != "dive_queued" {
			t.Fatalf("statuses = trajectory %s recall %s", trajectoryStatus, recallStatus)
		}
	})

	t.Run("backend failure is data and replay does not re-execute", func(t *testing.T) {
		root := t.TempDir()
		fx := mustGraphMemoryRecallEndpointFixture(t, root)
		h := newRecallEndpointHandler(root, &countingRecallSeeder{})
		factoryCalls := 0
		h.GraphMemoryRecallExecutor = service.NewGraphMemoryRecallExecutor(testPool, service.NewGraphMemoryDiveService(testPool), func(context.Context, *service.GraphMemoryRecallPlan) (memorygraph.AgentBackend, error) {
			factoryCalls++
			return nil, fmt.Errorf("provider unavailable")
		}, nil, nil, "test-model")
		body := graphMemoryRecallBody(fx, "trace-failure-"+uuid.NewString()[:8], "q")
		first := doGraphMemoryRecall(t, h, util.UUIDToString(fx.workspaceID), fx.daemonID, body)
		if first.Code != http.StatusAccepted {
			t.Fatalf("first status = %d body %s", first.Code, first.Body.String())
		}
		firstResponse := decodeRecallResponse(t, first)
		if firstResponse["injection"] != "" {
			t.Fatalf("failure response = %v, want no injection", firstResponse)
		}
		second := doGraphMemoryRecall(t, h, util.UUIDToString(fx.workspaceID), fx.daemonID, body)
		if second.Code != http.StatusOK || factoryCalls != 1 {
			t.Fatalf("replay status = %d calls = %d, want 200 and one factory call", second.Code, factoryCalls)
		}
		var trajectoryStatus, recallStatus string
		if err := testPool.QueryRow(context.Background(), `SELECT t.status, r.status FROM graph_memory_trajectory t JOIN graph_memory_recall r ON r.id = t.recall_id WHERE t.recall_id = $1`, firstResponse["recall_id"]).Scan(&trajectoryStatus, &recallStatus); err != nil {
			t.Fatal(err)
		}
		if trajectoryStatus != "error" || recallStatus != "dive_queued" {
			t.Fatalf("statuses = trajectory %s recall %s", trajectoryStatus, recallStatus)
		}
	})
}

func TestGraphMemoryRecallExecutionKAndMissingPinnedVersion(t *testing.T) {
	if testPool == nil {
		t.Skip("handler test database unavailable")
	}
	t.Run("all K trajectories become terminal", func(t *testing.T) {
		root := t.TempDir()
		fx := mustGraphMemoryRecallEndpointFixture(t, root)
		mustGraphMemoryGraphProfile(t, fx.workspaceID, true, 3)
		dir := mustGraphMemoryGraphDir(t, root, util.UUIDToString(fx.workspaceID), memorygraph.GraphDirKindProject, fx.projectID)
		if err := memorygraph.NewStore(dir).SaveNode(1, &memorygraph.Node{NodeID: "k-node", Body: "parallel exploration", CreatedBy: memorygraph.CreatorIngester, CreatedVersion: 1, UpdatedVersion: 1, ObservedAt: time.Now().UTC()}); err != nil {
			t.Fatal(err)
		}
		backend := &scriptedRecallBackend{found: true, summary: "parallel result"}
		h := newRecallEndpointHandler(root, &countingRecallSeeder{ids: []string{"k-node"}})
		h.GraphMemoryRecallExecutor = service.NewGraphMemoryRecallExecutor(testPool, service.NewGraphMemoryDiveService(testPool), func(context.Context, *service.GraphMemoryRecallPlan) (memorygraph.AgentBackend, error) {
			return backend, nil
		}, nil, nil, "test-model")
		rec := doGraphMemoryRecall(t, h, util.UUIDToString(fx.workspaceID), fx.daemonID, graphMemoryRecallBody(fx, "trace-k-"+uuid.NewString()[:8], "parallel"))
		if rec.Code != http.StatusAccepted {
			t.Fatalf("status = %d body %s", rec.Code, rec.Body.String())
		}
		recallID := decodeRecallResponse(t, rec)["recall_id"]
		var total, terminal int
		if err := testPool.QueryRow(context.Background(), `SELECT count(*), count(*) FILTER (WHERE status <> 'running') FROM graph_memory_trajectory WHERE recall_id = $1`, recallID).Scan(&total, &terminal); err != nil {
			t.Fatal(err)
		}
		if total != 3 || terminal != 3 || backend.count() != 3 {
			t.Fatalf("trajectories total=%d terminal=%d backend calls=%d, want 3", total, terminal, backend.count())
		}
	})

	t.Run("missing pinned version becomes error trajectories", func(t *testing.T) {
		root := t.TempDir()
		fx := mustGraphMemoryRecallEndpointFixture(t, root)
		dir := mustGraphMemoryGraphDir(t, root, util.UUIDToString(fx.workspaceID), memorygraph.GraphDirKindProject, fx.projectID)
		svc := newRecallEndpointHandler(root, &countingRecallSeeder{}).GraphMemoryRecall
		plan, err := svc.Begin(context.Background(), service.GraphMemoryRecallRequest{WorkspaceID: util.UUIDToString(fx.workspaceID), DaemonID: fx.daemonID, TaskID: util.UUIDToString(fx.taskID), RuntimeID: util.UUIDToString(fx.runtimeID), Query: "q", TraceID: "trace-missing-" + uuid.NewString()[:8]})
		if err != nil {
			t.Fatal(err)
		}
		if err := os.RemoveAll(memorygraph.NewStore(dir).VersionDir(plan.GraphVersion)); err != nil {
			t.Fatal(err)
		}
		executor := service.NewGraphMemoryRecallExecutor(testPool, service.NewGraphMemoryDiveService(testPool), func(context.Context, *service.GraphMemoryRecallPlan) (memorygraph.AgentBackend, error) {
			t.Fatal("backend must not run")
			return nil, nil
		}, nil, nil, "test-model")
		injection, err := executor.Execute(context.Background(), plan)
		if err != nil || injection != nil {
			t.Fatalf("Execute = (%v, %v), want (nil, nil)", injection, err)
		}
		var trajectoryStatus, recallStatus string
		if err := testPool.QueryRow(context.Background(), `SELECT t.status, r.status FROM graph_memory_trajectory t JOIN graph_memory_recall r ON r.id = t.recall_id WHERE t.recall_id = $1`, plan.RecallID).Scan(&trajectoryStatus, &recallStatus); err != nil {
			t.Fatal(err)
		}
		if trajectoryStatus != "error" || recallStatus != "dive_queued" {
			t.Fatalf("statuses = trajectory %s recall %s", trajectoryStatus, recallStatus)
		}
	})
}

func TestGraphMemoryRecallExecutionBoundsSummary(t *testing.T) {
	if testPool == nil {
		t.Skip("handler test database unavailable")
	}
	root := t.TempDir()
	fx := mustGraphMemoryRecallEndpointFixture(t, root)
	dir := mustGraphMemoryGraphDir(t, root, util.UUIDToString(fx.workspaceID), memorygraph.GraphDirKindProject, fx.projectID)
	if err := memorygraph.NewStore(dir).SaveNode(1, &memorygraph.Node{NodeID: "bound-node", Body: "bounded summary source", CreatedBy: memorygraph.CreatorIngester, CreatedVersion: 1, UpdatedVersion: 1, ObservedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	backend := &scriptedRecallBackend{found: true, summary: strings.Repeat("界", 4001)}
	h := newRecallEndpointHandler(root, &countingRecallSeeder{ids: []string{"bound-node"}})
	h.GraphMemoryRecallExecutor = service.NewGraphMemoryRecallExecutor(testPool, service.NewGraphMemoryDiveService(testPool), func(context.Context, *service.GraphMemoryRecallPlan) (memorygraph.AgentBackend, error) {
		return backend, nil
	}, nil, nil, "test-model")
	rec := doGraphMemoryRecall(t, h, util.UUIDToString(fx.workspaceID), fx.daemonID, graphMemoryRecallBody(fx, "trace-bounds-"+uuid.NewString()[:8], "bounded"))
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d body %s", rec.Code, rec.Body.String())
	}
	injection, _ := decodeRecallResponse(t, rec)["injection"].(string)
	parts := strings.SplitN(injection, "\n\ncited nodes:", 2)
	summary := strings.TrimPrefix(parts[0], "## Graph Memory Recall\n")
	if len(parts) != 2 || !strings.HasSuffix(summary, "…[truncated]") || len([]rune(strings.TrimSuffix(summary, "…[truncated]"))) != 4000 {
		t.Fatalf("injection did not apply the exact bounded-summary marker: %q", injection)
	}
}
