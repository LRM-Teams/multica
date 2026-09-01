// SPDX-License-Identifier: Apache-2.0

package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/multica-ai/multica/server/internal/memorygraph"
	"github.com/multica-ai/multica/server/internal/middleware"
	"github.com/multica-ai/multica/server/internal/service"
	"github.com/multica-ai/multica/server/internal/util"
)

// Spec §14/A23: the daemon recall endpoint is authenticated with a scoped
// daemon capability, answers 202 only after the ledger commit is durable,
// and every unknown/mismatched/stale/conflicting/finalized replay fails
// closed with zero provider calls and zero ledger mutations.

// countingRecallSeeder counts provider-side seed retrievals so the replay
// matrix can assert zero provider calls on replay/error paths.
type countingRecallSeeder struct {
	mu    sync.Mutex
	calls int
	ids   []string
}

func (c *countingRecallSeeder) Seeds(_ context.Context, _, _ string, _ int, _ string, _ memorygraph.GraphView) ([]string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls++
	return c.ids, nil
}

func (c *countingRecallSeeder) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls
}

// mustGraphMemoryRecallEndpointFixture builds the channel→project-routed
// fixture: initialized project graph on disk, channel bound to the project,
// channel-scoped task, graph-mode profile.
func mustGraphMemoryRecallEndpointFixture(t *testing.T, root string) recallLedgerFixture {
	t.Helper()
	if testPool == nil {
		t.Skip("handler test database unavailable")
	}
	fx := mustGraphMemoryRecallFixture(t)
	mustGraphMemoryGraphDir(t, root, util.UUIDToString(fx.workspaceID), memorygraph.GraphDirKindProject, fx.projectID)
	bindChannelProject(t, context.Background(), fx.channelID, fx.projectID)
	mustGraphMemoryTaskScope(t, fx.taskID, "channel_id", fx.channelID)
	mustGraphMemoryGraphProfile(t, fx.workspaceID, false, 4)
	return fx
}

func newRecallEndpointHandler(root string, seeder *countingRecallSeeder) *Handler {
	return &Handler{GraphMemoryRecall: service.NewGraphMemoryRecallService(
		testPool, service.LoadGraphMemoryLimits(func(string) string { return "" }),
		root, "legacy", seeder,
	)}
}

func doGraphMemoryRecall(t *testing.T, h *Handler, workspaceID, daemonID string, body map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	payload, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/daemon/graph-memory/recalls", strings.NewReader(string(payload)))
	if workspaceID != "" {
		req = req.WithContext(middleware.WithDaemonContext(req.Context(), workspaceID, daemonID))
	}
	rec := httptest.NewRecorder()
	h.RequestGraphMemoryRecall(rec, req)
	return rec
}

func graphMemoryRecallBody(fx recallLedgerFixture, traceID, query string) map[string]any {
	return map[string]any{
		"trace_id":   traceID,
		"task_id":    util.UUIDToString(fx.taskID),
		"runtime_id": util.UUIDToString(fx.runtimeID),
		"query":      query,
	}
}

func decodeRecallResponse(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response %q: %v", rec.Body.String(), err)
	}
	return resp
}

func TestGraphMemoryRecallEndpointAcceptedAfterDurableCommit(t *testing.T) {
	root := t.TempDir()
	fx := mustGraphMemoryRecallEndpointFixture(t, root)
	seeder := &countingRecallSeeder{ids: []string{"n1", "n2"}}
	h := newRecallEndpointHandler(root, seeder)

	traceID := "trace-ep-" + uuid.NewString()[:8]
	rec := doGraphMemoryRecall(t, h, util.UUIDToString(fx.workspaceID), fx.daemonID, graphMemoryRecallBody(fx, traceID, "q"))
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d body %s, want 202", rec.Code, rec.Body.String())
	}
	resp := decodeRecallResponse(t, rec)
	recallID, _ := resp["recall_id"].(string)
	if recallID == "" || resp["trace_id"] != traceID || resp["status"] != "accepted" || resp["replayed"] != false {
		t.Fatalf("response = %v, want accepted new recall", resp)
	}

	// A25 (first half): the 202 is issued only after the ledger commit is
	// durable — the rows must be visible once the response returns.
	var status string
	var k int32
	if err := testPool.QueryRow(context.Background(), `
		SELECT status, k FROM graph_memory_recall WHERE id = $1
	`, recallID).Scan(&status, &k); err != nil {
		t.Fatalf("recall row not durable at response time: %v", err)
	}
	if status != "accepted" || k != 1 {
		t.Fatalf("recall row = (%s, k=%d), want (accepted, 1)", status, k)
	}
	var trajCount int
	if err := testPool.QueryRow(context.Background(), `
		SELECT count(*) FROM graph_memory_trajectory WHERE recall_id = $1
	`, recallID).Scan(&trajCount); err != nil {
		t.Fatal(err)
	}
	if trajCount != 1 {
		t.Fatalf("trajectories = %d, want 1", trajCount)
	}
	if seeder.count() != 1 {
		t.Fatalf("seed retrievals = %d, want 1", seeder.count())
	}
}

func TestGraphMemoryRecallEndpointReplayMatrix(t *testing.T) {
	ctx := context.Background()

	t.Run("identical replay is idempotent with zero provider calls", func(t *testing.T) {
		root := t.TempDir()
		fx := mustGraphMemoryRecallEndpointFixture(t, root)
		seeder := &countingRecallSeeder{ids: []string{"n1"}}
		h := newRecallEndpointHandler(root, seeder)
		ws := util.UUIDToString(fx.workspaceID)
		traceID := "trace-rp-" + uuid.NewString()[:8]

		first := doGraphMemoryRecall(t, h, ws, fx.daemonID, graphMemoryRecallBody(fx, traceID, "q"))
		if first.Code != http.StatusAccepted {
			t.Fatalf("first status = %d body %s", first.Code, first.Body.String())
		}
		recallID, _ := decodeRecallResponse(t, first)["recall_id"].(string)

		second := doGraphMemoryRecall(t, h, ws, fx.daemonID, graphMemoryRecallBody(fx, traceID, "q"))
		if second.Code != http.StatusOK {
			t.Fatalf("replay status = %d body %s, want 200", second.Code, second.Body.String())
		}
		resp := decodeRecallResponse(t, second)
		if resp["recall_id"] != recallID || resp["replayed"] != true {
			t.Fatalf("replay response = %v, want same recall id with replayed=true", resp)
		}
		if seeder.count() != 1 {
			t.Fatalf("seed retrievals = %d, want 1 (replay must not call the provider)", seeder.count())
		}
		var trajCount int
		if err := testPool.QueryRow(ctx, `
			SELECT count(*) FROM graph_memory_trajectory WHERE recall_id = $1
		`, recallID).Scan(&trajCount); err != nil {
			t.Fatal(err)
		}
		if trajCount != 1 {
			t.Fatalf("trajectories after replay = %d, want 1 (no duplicate ledger rows)", trajCount)
		}
	})

	t.Run("conflicting replay is rejected with zero side effects", func(t *testing.T) {
		root := t.TempDir()
		fx := mustGraphMemoryRecallEndpointFixture(t, root)
		seeder := &countingRecallSeeder{ids: []string{"n1"}}
		h := newRecallEndpointHandler(root, seeder)
		ws := util.UUIDToString(fx.workspaceID)
		traceID := "trace-cf-" + uuid.NewString()[:8]

		if rec := doGraphMemoryRecall(t, h, ws, fx.daemonID, graphMemoryRecallBody(fx, traceID, "q")); rec.Code != http.StatusAccepted {
			t.Fatalf("first status = %d body %s", rec.Code, rec.Body.String())
		}
		conflict := doGraphMemoryRecall(t, h, ws, fx.daemonID, graphMemoryRecallBody(fx, traceID, "a different query"))
		if conflict.Code != http.StatusConflict {
			t.Fatalf("conflicting replay status = %d body %s, want 409", conflict.Code, conflict.Body.String())
		}
		if seeder.count() != 1 {
			t.Fatalf("seed retrievals = %d, want 1 (conflict must not call the provider)", seeder.count())
		}
		var recalls int
		if err := testPool.QueryRow(ctx, `
			SELECT count(*) FROM graph_memory_recall WHERE workspace_id = $1
		`, fx.workspaceID).Scan(&recalls); err != nil {
			t.Fatal(err)
		}
		if recalls != 1 {
			t.Fatalf("recall rows = %d, want 1 (no mutation on conflict)", recalls)
		}
	})

	t.Run("finalized replay fails closed", func(t *testing.T) {
		root := t.TempDir()
		fx := mustGraphMemoryRecallEndpointFixture(t, root)
		seeder := &countingRecallSeeder{ids: []string{"n1"}}
		h := newRecallEndpointHandler(root, seeder)
		ws := util.UUIDToString(fx.workspaceID)
		traceID := "trace-fn-" + uuid.NewString()[:8]

		first := doGraphMemoryRecall(t, h, ws, fx.daemonID, graphMemoryRecallBody(fx, traceID, "q"))
		if first.Code != http.StatusAccepted {
			t.Fatalf("first status = %d body %s", first.Code, first.Body.String())
		}
		recallID, _ := decodeRecallResponse(t, first)["recall_id"].(string)
		if _, err := testPool.Exec(ctx, `
			UPDATE graph_memory_recall SET status = 'completed', terminal_at = now() WHERE id = $1
		`, recallID); err != nil {
			t.Fatal(err)
		}
		replay := doGraphMemoryRecall(t, h, ws, fx.daemonID, graphMemoryRecallBody(fx, traceID, "q"))
		if replay.Code != http.StatusConflict {
			t.Fatalf("finalized replay status = %d body %s, want 409", replay.Code, replay.Body.String())
		}
		if seeder.count() != 1 {
			t.Fatalf("seed retrievals = %d, want 1 (finalized replay must not call the provider)", seeder.count())
		}
	})

	t.Run("stale replay fails closed", func(t *testing.T) {
		root := t.TempDir()
		fx := mustGraphMemoryRecallEndpointFixture(t, root)
		seeder := &countingRecallSeeder{ids: []string{"n1"}}
		h := newRecallEndpointHandler(root, seeder)
		ws := util.UUIDToString(fx.workspaceID)
		traceID := "trace-st-" + uuid.NewString()[:8]

		first := doGraphMemoryRecall(t, h, ws, fx.daemonID, graphMemoryRecallBody(fx, traceID, "q"))
		if first.Code != http.StatusAccepted {
			t.Fatalf("first status = %d body %s", first.Code, first.Body.String())
		}
		recallID, _ := decodeRecallResponse(t, first)["recall_id"].(string)
		// The runtime was deregistered after the recall committed (FK
		// ON DELETE SET NULL): the replay's identity no longer matches the
		// durable record.
		if _, err := testPool.Exec(ctx, `
			UPDATE graph_memory_recall SET runtime_id = NULL WHERE id = $1
		`, recallID); err != nil {
			t.Fatal(err)
		}
		replay := doGraphMemoryRecall(t, h, ws, fx.daemonID, graphMemoryRecallBody(fx, traceID, "q"))
		if replay.Code != http.StatusConflict {
			t.Fatalf("stale replay status = %d body %s, want 409", replay.Code, replay.Body.String())
		}
		if seeder.count() != 1 {
			t.Fatalf("seed retrievals = %d, want 1 (stale replay must not call the provider)", seeder.count())
		}
	})

	t.Run("unknown task fails closed", func(t *testing.T) {
		root := t.TempDir()
		fx := mustGraphMemoryRecallEndpointFixture(t, root)
		seeder := &countingRecallSeeder{ids: []string{"n1"}}
		h := newRecallEndpointHandler(root, seeder)
		body := graphMemoryRecallBody(fx, "trace-un-"+uuid.NewString()[:8], "q")
		body["task_id"] = uuid.NewString()
		rec := doGraphMemoryRecall(t, h, util.UUIDToString(fx.workspaceID), fx.daemonID, body)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("unknown task status = %d body %s, want 404", rec.Code, rec.Body.String())
		}
		if seeder.count() != 0 {
			t.Fatalf("seed retrievals = %d, want 0", seeder.count())
		}
	})

	t.Run("mismatched runtime fails closed", func(t *testing.T) {
		root := t.TempDir()
		fx := mustGraphMemoryRecallEndpointFixture(t, root)
		other := mustGraphMemoryRecallFixture(t)
		seeder := &countingRecallSeeder{ids: []string{"n1"}}
		h := newRecallEndpointHandler(root, seeder)
		body := graphMemoryRecallBody(fx, "trace-mm-"+uuid.NewString()[:8], "q")
		body["runtime_id"] = util.UUIDToString(other.runtimeID)
		rec := doGraphMemoryRecall(t, h, util.UUIDToString(fx.workspaceID), fx.daemonID, body)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("foreign runtime status = %d body %s, want 404", rec.Code, rec.Body.String())
		}
		if seeder.count() != 0 {
			t.Fatalf("seed retrievals = %d, want 0", seeder.count())
		}
	})

	t.Run("missing daemon capability is forbidden", func(t *testing.T) {
		root := t.TempDir()
		fx := mustGraphMemoryRecallEndpointFixture(t, root)
		seeder := &countingRecallSeeder{ids: []string{"n1"}}
		h := newRecallEndpointHandler(root, seeder)
		rec := doGraphMemoryRecall(t, h, "", "", graphMemoryRecallBody(fx, "trace-nd-"+uuid.NewString()[:8], "q"))
		if rec.Code != http.StatusForbidden {
			t.Fatalf("no-capability status = %d body %s, want 403", rec.Code, rec.Body.String())
		}
		if seeder.count() != 0 {
			t.Fatalf("seed retrievals = %d, want 0", seeder.count())
		}
	})

	t.Run("missing required fields are a client error", func(t *testing.T) {
		root := t.TempDir()
		fx := mustGraphMemoryRecallEndpointFixture(t, root)
		seeder := &countingRecallSeeder{ids: []string{"n1"}}
		h := newRecallEndpointHandler(root, seeder)
		body := graphMemoryRecallBody(fx, "trace-bf-"+uuid.NewString()[:8], "")
		rec := doGraphMemoryRecall(t, h, util.UUIDToString(fx.workspaceID), fx.daemonID, body)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("empty query status = %d body %s, want 400", rec.Code, rec.Body.String())
		}
		if seeder.count() != 0 {
			t.Fatalf("seed retrievals = %d, want 0", seeder.count())
		}
	})
}

// The daemon may attach caller-side hints; they are diagnostics only and
// never steer resolution (A14).
func TestGraphMemoryRecallEndpointCallerHintsAreDiagnostics(t *testing.T) {
	root := t.TempDir()
	fx := mustGraphMemoryRecallEndpointFixture(t, root)
	seeder := &countingRecallSeeder{ids: []string{"n1"}}
	h := newRecallEndpointHandler(root, seeder)

	body := graphMemoryRecallBody(fx, "trace-ch-"+uuid.NewString()[:8], "q")
	body["graph_kind"] = "channel"
	body["graph_owner_id"] = util.UUIDToString(fx.channelID)
	body["graph_version"] = 99
	body["training_mode"] = "online_rl"
	body["k"] = 42
	rec := doGraphMemoryRecall(t, h, util.UUIDToString(fx.workspaceID), fx.daemonID, body)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d body %s, want 202", rec.Code, rec.Body.String())
	}
	resp := decodeRecallResponse(t, rec)
	if resp["k"] != float64(1) || resp["graph_version"] != float64(1) {
		t.Fatalf("response = %v, want server-resolved k=1 version=1 (caller hints ignored)", resp)
	}
}
