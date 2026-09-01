// SPDX-License-Identifier: Apache-2.0

package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/multica-ai/multica/server/internal/memorygraph"
	"github.com/multica-ai/multica/server/internal/util"
	"github.com/multica-ai/multica/server/pkg/agent"
)

func TestGraphMemoryRecallInjectionContentBoundsCitations(t *testing.T) {
	citations := make([]memorygraph.Citation, graphMemoryRecallMaxCitationCount+1)
	for i := range citations {
		citations[i] = memorygraph.Citation{NodeID: fmt.Sprintf("n%d", i), Level: i, Epistemic: "observed"}
	}
	content := graphMemoryRecallInjectionContent(strings.Repeat("x", graphMemoryRecallMaxSummaryChars+1), citations)
	if !strings.Contains(content, graphMemoryRecallTruncationMarker) {
		t.Fatalf("content missing summary truncation marker: %q", content)
	}
	if strings.Count(content, "\n- n") != graphMemoryRecallMaxCitationCount || !strings.Contains(content, "\n- …and 1 more") {
		t.Fatalf("content did not cap citations: %q", content)
	}
}

// replayAgentBackend hands every Execute the same completed session.
type replayAgentBackend struct{ output string }

func (b *replayAgentBackend) Execute(_ context.Context, _ string, _ agent.ExecOptions) (*agent.Session, error) {
	msgs := make(chan agent.Message)
	close(msgs)
	results := make(chan agent.Result, 1)
	results <- agent.Result{Status: "completed", Output: b.output}
	close(results)
	return &agent.Session{Messages: msgs, Result: results}, nil
}

func testPriorPlan(query string) *GraphMemoryRecallPlan {
	return &GraphMemoryRecallPlan{
		Query: query, GraphVersion: 1,
		WorkspaceID: "ws-1", GraphKind: "graph", GraphOwnerID: "owner-1",
		GraphView: memorygraph.GraphView{ChannelID: "chan-1"},
	}
}

// A pre-populated brief for the normalized query is reused as-is; a nil
// backend proves no provider work happens on the cache-hit path.
func TestPriorBriefCacheHitSkipsCompression(t *testing.T) {
	e := &GraphMemoryRecallExecutor{}
	store := memorygraph.NewPriorRecordStore(t.TempDir())
	rec := &memorygraph.PriorRecord{GraphVersion: 1, Query: "old", Briefs: map[string]memorygraph.PriorBrief{
		memorygraph.NormalizeRecallKey("Query B"): {Summary: "cached"},
	}}
	plan := testPriorPlan("Query B")

	brief := e.priorBrief(context.Background(), plan, rec, store, graphPriorOwnerKey(plan), nil, "m")
	if brief == nil || brief.Summary != "cached" {
		t.Fatalf("brief = %+v, want cached", brief)
	}
}

// Cache miss: compression runs against the backend and the parsed brief is
// written back under the normalized query key for the next recall.
func TestPriorBriefCompressMissWritesBack(t *testing.T) {
	e := &GraphMemoryRecallExecutor{}
	dir := t.TempDir()
	store := memorygraph.NewPriorRecordStore(dir)
	rec := &memorygraph.PriorRecord{GraphVersion: 1, Query: "old", Transcript: []memorygraph.TraceMessage{
		{Kind: "message", Sequence: 0, Type: "text", Content: "explored n-a"},
	}}
	backend := &replayAgentBackend{output: `{"summary":"fresh","node_ids":["n-a"],"observations":["o"],"rejected":[],"open_questions":[]}`}
	plan := testPriorPlan("Query B")

	brief := e.priorBrief(context.Background(), plan, rec, store, graphPriorOwnerKey(plan), backend, "m")
	if brief == nil || brief.Summary != "fresh" || len(brief.NodeIDs) != 1 {
		t.Fatalf("brief = %+v, want compressed", brief)
	}
	reloaded, err := store.Load(graphPriorOwnerKey(plan))
	if err != nil || reloaded == nil {
		t.Fatalf("Load after write-back: %v %v", reloaded, err)
	}
	if got := reloaded.Briefs[memorygraph.NormalizeRecallKey("Query B")].Summary; got != "fresh" {
		t.Fatalf("written-back brief = %q, want fresh", got)
	}
}

type recordingRecallBackend struct {
	calls atomic.Int32
	model atomic.Value
}

func (b *recordingRecallBackend) Execute(_ context.Context, _ string, opts agent.ExecOptions) (*agent.Session, error) {
	b.calls.Add(1)
	b.model.Store(opts.Model)
	return nil, errors.New("recording backend stopped")
}

func recallExecutorTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	ctx := context.Background()
	databaseURL := strings.TrimSpace(os.Getenv("DATABASE_URL"))
	if databaseURL == "" {
		databaseURL = "postgres://multica:multica@localhost:5432/multica?sslmode=disable"
	}
	admin, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatalf("connect PostgreSQL: %v", err)
	}
	if err := admin.Ping(ctx); err != nil {
		admin.Close()
		t.Fatalf("ping PostgreSQL: %v", err)
	}
	schema := fmt.Sprintf("graph_memory_recall_execute_test_%d", time.Now().UnixNano())
	quotedSchema := pgx.Identifier{schema}.Sanitize()
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+quotedSchema); err != nil {
		admin.Close()
		t.Fatalf("create private schema: %v", err)
	}

	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		_, _ = admin.Exec(ctx, "DROP SCHEMA IF EXISTS "+quotedSchema+" CASCADE")
		admin.Close()
		t.Fatalf("parse PostgreSQL config: %v", err)
	}
	config.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		_, _ = admin.Exec(ctx, "DROP SCHEMA IF EXISTS "+quotedSchema+" CASCADE")
		admin.Close()
		t.Fatalf("open private-schema pool: %v", err)
	}
	t.Cleanup(func() {
		pool.Close()
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		if _, err := admin.Exec(cleanupCtx, "DROP SCHEMA IF EXISTS "+quotedSchema+" CASCADE"); err != nil {
			t.Logf("drop private schema %s: %v", schema, err)
		}
		admin.Close()
	})
	if _, err := pool.Exec(ctx, `
		CREATE TABLE graph_memory_recall (
			id uuid PRIMARY KEY,
			status text NOT NULL,
			updated_at timestamptz NOT NULL DEFAULT now()
		);
		CREATE TABLE graph_memory_trajectory (
			recall_id uuid NOT NULL,
			seed_index integer NOT NULL,
			status text NOT NULL,
			error_kind text NOT NULL DEFAULT '',
			summary text NOT NULL DEFAULT '',
			viewed_node_ids jsonb NOT NULL DEFAULT '[]'::jsonb,
			submitted_node_ids jsonb NOT NULL DEFAULT '[]'::jsonb,
			rounds integer NOT NULL DEFAULT 0,
			model text NOT NULL DEFAULT '',
			terminal_at timestamptz,
			updated_at timestamptz NOT NULL DEFAULT now(),
			PRIMARY KEY (recall_id, seed_index)
		)
	`); err != nil {
		t.Fatalf("create private recall tables: %v", err)
	}
	return pool
}

func recallExecutorTestPlan(t *testing.T, pool *pgxpool.Pool, workspaceID string) *GraphMemoryRecallPlan {
	t.Helper()
	graphDir := filepath.Join(t.TempDir(), "memory_graph")
	store := memorygraph.NewStore(graphDir)
	if err := store.Init(); err != nil {
		t.Fatalf("init graph store: %v", err)
	}
	recallID := uuid.NewString()
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO graph_memory_recall (id, status) VALUES ($1, 'accepted')
	`, recallID); err != nil {
		t.Fatalf("insert recall fixture: %v", err)
	}
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO graph_memory_trajectory (recall_id, seed_index, status) VALUES ($1, 0, 'running')
	`, recallID); err != nil {
		t.Fatalf("insert trajectory fixture: %v", err)
	}
	return &GraphMemoryRecallPlan{
		RecallID: recallID, WorkspaceID: workspaceID, GraphDir: graphDir, GraphVersion: 1, K: 1,
	}
}

func assertRecallExecutorTrajectory(t *testing.T, pool *pgxpool.Pool, recallID, wantStatus, wantModel string) {
	t.Helper()
	var status, model string
	if err := pool.QueryRow(context.Background(), `
		SELECT status, model FROM graph_memory_trajectory WHERE recall_id = $1 AND seed_index = 0
	`, recallID).Scan(&status, &model); err != nil {
		t.Fatalf("load trajectory state: %v", err)
	}
	if status != wantStatus || model != wantModel {
		t.Fatalf("trajectory status/model = %q/%q, want %q/%q", status, model, wantStatus, wantModel)
	}
}

func TestGraphMemoryRecallExecutorPolicyFailureSkipsBackend(t *testing.T) {
	pool := recallExecutorTestPool(t)
	workspaceID := "10000000-0000-4000-8000-000000000021"
	plan := recallExecutorTestPlan(t, pool, workspaceID)
	resolver := NewMemoryProviderPolicyResolver(fakeMemoryProviderWorkspaceReader{settings: map[string][]byte{}}, testProviderPolicyConfig(nil))
	backend := &recordingRecallBackend{}
	factoryCalls := 0
	executor := NewGraphMemoryRecallExecutor(pool, nil, resolver, func(_ context.Context, _ ResolvedMemoryProvider) (memorygraph.AgentBackend, error) {
		factoryCalls++
		return backend, nil
	}, nil, nil)

	injection, err := executor.Execute(context.Background(), plan)
	if injection != nil || err == nil {
		t.Fatalf("Execute = (%v, %v), want nil injection and durable-path configuration error", injection, err)
	}
	if factoryCalls != 0 || backend.calls.Load() != 0 {
		t.Fatalf("backend factory/execution calls = %d/%d, want 0/0", factoryCalls, backend.calls.Load())
	}
	assertRecallExecutorTrajectory(t, pool, plan.RecallID, "error", "")
}

func TestGraphMemoryRecallExecutorInvalidWorkspaceSkipsBackend(t *testing.T) {
	pool := recallExecutorTestPool(t)
	plan := recallExecutorTestPlan(t, pool, "not-a-workspace-uuid")
	backend := &recordingRecallBackend{}
	factoryCalls := 0
	executor := NewGraphMemoryRecallExecutor(pool, nil, nil, func(_ context.Context, _ ResolvedMemoryProvider) (memorygraph.AgentBackend, error) {
		factoryCalls++
		return backend, nil
	}, nil, nil)

	injection, err := executor.Execute(context.Background(), plan)
	if injection != nil || err == nil {
		t.Fatalf("Execute = (%v, %v), want nil injection and durable-path configuration error", injection, err)
	}
	if factoryCalls != 0 || backend.calls.Load() != 0 {
		t.Fatalf("backend factory/execution calls = %d/%d, want 0/0", factoryCalls, backend.calls.Load())
	}
	assertRecallExecutorTrajectory(t, pool, plan.RecallID, "error", "")
}

func TestGraphMemoryRecallExecutorUsesResolvedProviderAndModel(t *testing.T) {
	pool := recallExecutorTestPool(t)
	workspaceUUID := util.MustParseUUID("10000000-0000-4000-8000-000000000022")
	workspaceID := util.UUIDToString(workspaceUUID)
	plan := recallExecutorTestPlan(t, pool, workspaceID)
	resolved := ResolvedMemoryProvider{
		Provider: "approved", Model: "resolved-model", Region: "eu-central-1", PolicyVersion: "policy-recall",
	}
	resolver := NewMemoryProviderPolicyResolver(fakeMemoryProviderWorkspaceReader{settings: map[string][]byte{
		workspaceID: testMemoryProviderSettings(resolved.PolicyVersion, `"dive":{"enabled":true,"provider":"approved","model":"resolved-model","region":"eu-central-1"}`),
	}}, testProviderPolicyConfig(nil))
	backend := &recordingRecallBackend{}
	factoryCalls := 0
	var factoryPolicy ResolvedMemoryProvider
	executor := NewGraphMemoryRecallExecutor(pool, nil, resolver, func(_ context.Context, policy ResolvedMemoryProvider) (memorygraph.AgentBackend, error) {
		factoryCalls++
		factoryPolicy = policy
		return backend, nil
	}, nil, nil)

	injection, err := executor.Execute(context.Background(), plan)
	if injection != nil || err == nil {
		t.Fatalf("Execute = (%v, %v), want nil injection and durable-path configuration error", injection, err)
	}
	if factoryCalls != 1 || backend.calls.Load() != 1 {
		t.Fatalf("backend factory/execution calls = %d/%d, want 1/1", factoryCalls, backend.calls.Load())
	}
	if factoryPolicy != resolved {
		t.Fatalf("backend factory policy = %+v, want %+v", factoryPolicy, resolved)
	}
	if got, _ := backend.model.Load().(string); got != resolved.Model {
		t.Fatalf("backend model = %q, want %q", got, resolved.Model)
	}
	assertRecallExecutorTrajectory(t, pool, plan.RecallID, "error", resolved.Model)
}
