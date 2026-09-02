# Graph Memory Recall P0 去重实施计划（Phase 1）

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 消除 graph memory recall 的两类重复计算--单次 recall 内部的双重 hybrid 检索、resident message batch 内逐消息的重复 recall。

**Architecture:** `Begin` 的 seeder 成为 round-0 seed 的唯一权威计算点，`GraphMemoryRecallPlan` 携带 seeds、`Execute` 通过新 API `Explorer.ExploreWithSeeds` 消费之（空列表回退内部检索以兼容 backtest）；daemon 侧在 `prepareResidentMessageBatch` 内以规范化 query 为 key memo 化 `graphExecutionMemories` 结果。

**Tech Stack:** Go（module `github.com/multica-ai/multica/server`）、`net/http/httptest`、标准 `testing`。

**Spec:** `docs/superpowers/specs/2026-08-24-graph-memory-recall-continuation-spec.zh-CN.md` §4（Phase 1）。本计划**仅覆盖 Phase 1**；Phase 2（continuation）依据 spec §5 另立计划，启动门槛为 Phase 1 量测基线（共识 D12）。

## Global Constraints

- 所有命令在 `multica/server/` 目录下执行（Go module 根）。
- 不修改 explore 工具协议（`/explore`、`/submit`）、adoption 规则、GraphView 过滤语义、consolidation 与版本 pinning（spec §2 非目标）。
- `Found=false` 是数据不是错误；一切 recall 失败保持非致命注入空（现有语义）。
- 回归判据：现有 `internal/memorygraph`、`internal/service`、`internal/daemon` 测试套件全绿；rounds / found / 注入内容不变。
- 提交信息格式：`feat(<scope>): ...` / `test(<scope>): ...`。

---

### Task 1: `Explorer.ExploreWithSeeds` 与 `seedsFromIDs`（memorygraph 包）

**Files:**
- Modify: `internal/memorygraph/explore.go`
- Test: `internal/memorygraph/explore_test.go`

**Interfaces:**
- Consumes: 现有 `NewExplorer(store, retr, backend, cfg, provider, traces)`、`exploreSeed{id, snippet}`、测试基建 `newExploreStore` / `newExploreRetriever` / `fakeExploreBackend` / `firstSeedNode`。
- Produces: `func (e *Explorer) ExploreWithSeeds(ctx context.Context, query string, seedIDs []string) (*RecallResult, error)`；`Explore(ctx, query)` 变为其薄包装。Task 2 依赖此签名。

- [ ] **Step 1: Write the failing test**

追加到 `internal/memorygraph/explore_test.go`（放在 `TestExploreUsesServerRounds` 之后）：

```go
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
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test ./internal/memorygraph/ -run 'TestExploreWithSeeds' -v
```

Expected: 编译失败 `undefined: (*Explorer).ExploreWithSeeds`。

- [ ] **Step 3: Implement ExploreWithSeeds + seedsFromIDs**

3a. 将 `explore.go` 中现有 `Explore` 的**函数头与种子段**替换为（注释 `// Explore recalls...` 起至 `seeds := e.seedSnippets(hits, version)` 行止，函数其余部分原样保留在新函数体内）：

```go
// Explore recalls memory relevant to query, computing hybrid-retrieval
// seeds internally. It is ExploreWithSeeds with a nil seed list.
func (e *Explorer) Explore(ctx context.Context, query string) (*RecallResult, error) {
	return e.ExploreWithSeeds(ctx, query, nil)
}

// ExploreWithSeeds runs Explore against server-persisted round-0 seed node
// ids, skipping the internal hybrid search when they are present: the
// recall Begin already computed the authoritative seed batch for this
// pinned version (spec P0 §4.1). An empty list falls back to the internal
// search so direct callers (backtests) keep their behavior. A miss (no
// trajectory found relevant information, or every trajectory failed) is
// data, not an error: it returns Found=false with a nil error.
func (e *Explorer) ExploreWithSeeds(ctx context.Context, query string, seedIDs []string) (*RecallResult, error) {
	if e.backend == nil {
		return nil, fmt.Errorf("explore: agent backend not configured")
	}
	if e.retr == nil {
		return nil, fmt.Errorf("explore: retriever not configured")
	}

	// Version pinning (design R5/R12): resolve the graph version ONCE - the
	// pinned version when set, else the current pointer - and serve the
	// whole call (seed hydration, /explore, /submit validation) from that
	// version, so a mid-explore consolidation switch never swaps the graph
	// under an in-flight trajectory.
	version := e.pinnedVersion
	if version <= 0 {
		var err error
		version, err = e.store.CurrentVersion()
		if err != nil {
			return nil, fmt.Errorf("explore: current version: %w", err)
		}
	}
	retr, err := e.retr.ForkForVersion(ctx, version)
	if err != nil {
		return nil, fmt.Errorf("explore: pin retriever to v%d: %w", version, err)
	}

	// (a) Round-0 seeds: the persisted batch when provided, else internal
	// hybrid retrieval.
	var seeds []exploreSeed
	if len(seedIDs) > 0 {
		seeds = e.seedsFromIDs(seedIDs, version)
	} else {
		hits, err := retr.Search(ctx, query)
		if err != nil {
			return nil, fmt.Errorf("explore: seed retrieval: %w", err)
		}
		seeds = e.seedSnippets(hits, version)
	}
```

（函数从 `// Tool server shared by all trajectories...` 起的其余部分、直到 adoption 段结束，原样保留。）

3b. 将现有 `seedSnippets`（`explore.go:344-370`）整体替换为：

```go
func (e *Explorer) seedSnippets(hits []ScoredDoc, version int) []exploreSeed {
	ids := make([]string, 0, len(hits))
	for _, h := range hits {
		ids = append(ids, h.ID)
	}
	return e.seedsFromIDs(ids, version)
}

// seedsFromIDs hydrates seed node ids into prompt seeds by reading each
// node's body from the pinned version (staging segments included), so a
// persisted seed batch and a fresh hybrid batch produce identical seeds.
func (e *Explorer) seedsFromIDs(ids []string, version int) []exploreSeed {
	var g *Graph
	if e.store != nil {
		if loaded, err := LoadGraph(e.store, version); err == nil {
			g = loaded
		}
	}
	seeds := make([]exploreSeed, 0, len(ids))
	for _, id := range ids {
		var body string
		switch {
		case IsStagingID(id) && e.store != nil:
			if b, err := e.store.ReadStagingSegment(strings.TrimPrefix(id, stagingDocPrefix)); err == nil {
				body = string(b)
			}
		case g != nil:
			if n := g.Node(id); n != nil {
				body = n.Body
			}
		}
		if len(body) > expandSnippetChars {
			body = body[:expandSnippetChars] + "..."
		}
		seeds = append(seeds, exploreSeed{id: id, snippet: body})
	}
	return seeds
}
```

- [ ] **Step 4: Run tests to verify pass**

```bash
go test ./internal/memorygraph/ -run 'TestExploreWithSeeds' -v
go test ./internal/memorygraph/
```

Expected: 两个新测试 PASS，包内全部测试 PASS（`TestExplorePinsVersionForWholeCall`、`TestExploreParallelAgentsAdoptFewestRounds` 等不回归）。

- [ ] **Step 5: Commit**

```bash
git add internal/memorygraph/explore.go internal/memorygraph/explore_test.go
git commit -m "feat(memorygraph): ExploreWithSeeds consumes persisted seeds, single search per recall"
```

---

### Task 2: plan 携带 Seeds 并由 Executor 传递（service 包，wiring）

**Files:**
- Modify: `internal/service/graph_memory_recall.go`（`GraphMemoryRecallPlan` 结构体 + `Begin`）
- Modify: `internal/service/graph_memory_recall_execute.go:94`

**Interfaces:**
- Consumes: Task 1 的 `Explorer.ExploreWithSeeds(ctx, query, seedIDs)`；`Begin` 现有局部变量 `seeds []string`（`graph_memory_recall.go:267-273`）。
- Produces: `GraphMemoryRecallPlan.Seeds []string`（Task 3 与 Phase 2 的 prior record 读取同一字段的先声）。

**测试策略说明：** 本任务是三行 wiring（结构体字段、plan 字面量一项、Execute 一处调用），行为已由 Task 1 的 `ExploreWithSeeds` 测试与 replay 路径（`LoadReplayInjection` 不经过 Execute）覆盖；service 包无 recall Begin/Execute 的 DB 测试基建（`graph_memory_recall_execute_test.go` 仅纯函数单测），本任务不新增单测，回归依赖现有套件全绿。

- [ ] **Step 1: Add the Seeds field to GraphMemoryRecallPlan**

`internal/service/graph_memory_recall.go`，结构体中 `TraceID` 字段之后追加：

```go
	Query        string
	TraceID      string
	// Seeds are the authoritative round-0 hybrid hit node ids computed by
	// Begin's seeder against GraphVersion and persisted to the ledger; the
	// executor hands them to Explorer.ExploreWithSeeds so the hybrid search
	// runs exactly once per recall (spec P0 §4.1).
	Seeds        []string
```

- [ ] **Step 2: Populate it in Begin**

`Begin` 的 plan 字面量（`graph_memory_recall.go:275-291`）中 `TraceID:      req.TraceID,` 之后追加一行：

```go
		TraceID:      req.TraceID,
		Seeds:        seeds,
```

（`persistPlan(ctx, plan, wsUUID, taskUUID, rtUUID, graphOwnerID, seeds)` 维持不变--ledger 写入语义不动。）

- [ ] **Step 3: Consume it in Execute**

`internal/service/graph_memory_recall_execute.go:94`：

```go
	result, err := explorer.ExploreWithSeeds(ctx, plan.Query, plan.Seeds)
```

（replayed plan 或 seeder 未接线时 `plan.Seeds` 为空，`ExploreWithSeeds` 回退内部检索，行为与现状一致。）

- [ ] **Step 4: Build and run the regression suites**

```bash
go build ./...
go test ./internal/service/ ./internal/memorygraph/ ./internal/daemon/
```

Expected: 编译通过；三个包现有测试全绿。

- [ ] **Step 5: Commit**

```bash
git add internal/service/graph_memory_recall.go internal/service/graph_memory_recall_execute.go
git commit -m "feat(service): pass persisted seed batch from Begin to Explore, drop duplicate search"
```

---

### Task 3: batch 内 query 级合并（daemon 包）

**Files:**
- Modify: `internal/daemon/graph_memory.go`（新增 memo 助手）
- Modify: `internal/daemon/message_runtime.go`（`prepareResidentMessageBatch` 循环）
- Test: `internal/daemon/message_runtime_memory_test.go`

**Interfaces:**
- Consumes: 现有 `(d *WorkspaceDaemonCore) graphExecutionMemories(ctx, task, log)`、`graphRecallQuery(task)`、`NewClient(baseURL)`（`client.go:152`）。
- Produces: `(d *WorkspaceDaemonCore) memoizedGraphExecutionMemories(ctx, task, memo, log)`、`normalizeGraphRecallKey(query) string`（Phase 2 的 brief 去重复用同一规范化规则）。

- [ ] **Step 1: Write the failing test**

追加到 `internal/daemon/message_runtime_memory_test.go`：

```go
// P0 §4.2: identical recall queries within one resident message batch
// coalesce into a single server recall; whitespace/case variants share the
// normalized key, distinct queries do not.
func TestPrepareResidentMessageBatchCoalescesIdenticalGraphRecalls(t *testing.T) {
	var recalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/daemon/graph-memory/recalls" {
			http.NotFound(w, r)
			return
		}
		recalls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"found":true,"injection":"## Graph Memory Recall\ndispatch retries use exponential backoff","status":"explore_terminal"}`))
	}))
	defer server.Close()

	root := t.TempDir()
	d := New(Config{WorkspacesRoot: root, MemoryType: MemoryTypeGraph}, nil)
	d.client = NewClient(server.URL)
	d.runtimeIndex["runtime-1"] = Runtime{ID: "runtime-1", WorkspaceID: "workspace-1"}

	msg := func(id, content string) protocol.AgentMessageProjection {
		return protocol.AgentMessageProjection{
			ID: id, Target: "channel:group-1", Seq: 1, Content: content,
			ChannelID: "channel-group", ChannelKind: "group",
			InitiatorType: "member", InitiatorID: "member-1", InitiatorName: "JHP",
		}
	}
	messages := []protocol.AgentMessageProjection{
		msg("m-1", "总结一下当前进度"),
		msg("m-2", "总结一下当前进度"),
		msg("m-3", "总结一下  当前进度"), // 空白差异：同一 key
		msg("m-4", "列出当前的风险项"),   // 不同 query：独立 recall
	}

	prepared, _, err := d.prepareResidentMessageBatch(context.Background(), "agent-1", "runtime-1", messages)
	if err != nil {
		t.Fatal(err)
	}
	if len(prepared) != 4 {
		t.Fatalf("prepared messages = %d, want 4", len(prepared))
	}
	if got := recalls.Load(); got != 2 {
		t.Fatalf("recall calls = %d, want 2 (identical queries coalesced)", got)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test ./internal/daemon/ -run TestPrepareResidentMessageBatchCoalescesIdenticalGraphRecalls -v
```

Expected: FAIL，`recall calls = 4, want 2`。

- [ ] **Step 3: Implement the memo layer**

3a. `internal/daemon/graph_memory.go` 末尾（`graphRecallQuery` 之后）追加：

```go
// memoizedGraphExecutionMemories coalesces identical graph recall queries
// within one resident message batch: the first message pays the recall and
// later messages whose normalized query matches reuse the result (spec P0
// §4.2). nil results are memoized too - a recall failure is non-fatal
// data, not a retryable error, within a near-simultaneous batch.
func (d *WorkspaceDaemonCore) memoizedGraphExecutionMemories(
	ctx context.Context, task Task,
	memo map[string][]execenv.MemoryContextForEnv, log *slog.Logger,
) []execenv.MemoryContextForEnv {
	key := normalizeGraphRecallKey(graphRecallQuery(task))
	if key == "" {
		return nil
	}
	if cached, ok := memo[key]; ok {
		if log != nil {
			log.Info("graph memory recall coalesced within batch", "task_id", task.ID)
		}
		return cached
	}
	memories := d.graphExecutionMemories(ctx, task, log)
	memo[key] = memories
	return memories
}

// normalizeGraphRecallKey canonicalizes a recall query for exact-match
// coalescing: case-folding with whitespace runs collapsed.
func normalizeGraphRecallKey(query string) string {
	return strings.Join(strings.Fields(strings.ToLower(query)), " ")
}
```

3b. `internal/daemon/message_runtime.go` 的 `prepareResidentMessageBatch` 中，`sessionKey := residentTurnScopeSessionKey(agentID, runtimeID)` 之后新增一行：

```go
	sessionKey := residentTurnScopeSessionKey(agentID, runtimeID)
	graphRecallMemo := map[string][]execenv.MemoryContextForEnv{}
```

3c. 同函数循环内（`message_runtime.go:425`）替换调用：

```go
			combined := mergeGraphModeExecutionMemory(
				agentRoot, messageTask, serverMemories,
				d.memoizedGraphExecutionMemories(ctx, messageTask, graphRecallMemo, d.logger),
			)
```

（`daemon.go:2505` 的 runTask 路径不改--单任务单调用，无重复可合并，spec §2 非目标。）

- [ ] **Step 4: Run tests to verify pass**

```bash
go test ./internal/daemon/ -run TestPrepareResidentMessageBatchCoalescesIdenticalGraphRecalls -v
go test ./internal/daemon/
```

Expected: 新测试 PASS；daemon 包全部测试 PASS（`TestPrepareResidentMessageBatchScopesIdentityAndUserMemoryPerMessage` 等不回归）。

- [ ] **Step 5: Commit**

```bash
git add internal/daemon/graph_memory.go internal/daemon/message_runtime.go internal/daemon/message_runtime_memory_test.go
git commit -m "feat(daemon): coalesce identical graph recall queries within a resident message batch"
```

---

## 收尾：全量回归与基线记录

- [ ] `cd multica/server && go test ./...` 全绿。
- [ ] 部署后观测（Phase 2 启动门槛，共识 D12）：
  - `graph memory recall coalesced within batch` 日志频次（batch 合并收益）；
  - query log（`RecordRecall`）中 rounds / found 分布与 Phase 1 之前对比（行为等价确认）；
  - 该基线数据落盘后，Phase 2（continuation，spec §5）计划启动。

## Self-Review（已执行）

- **Spec 覆盖**：spec §4.1 -> Task 1+2；§4.2 -> Task 3；§7 验收 1-3 -> Task 1/2，4-6 -> Task 3 + 收尾回归。
- **占位符扫描**：无 TBD/TODO；所有代码步骤给出完整代码或精确到行的替换区域。
- **类型一致性**：`ExploreWithSeeds` 签名在 Task 1 定义、Task 2 消费一致；`memoizedGraphExecutionMemories` 的 memo 类型与 `graphExecutionMemories` 返回类型一致（`[]execenv.MemoryContextForEnv`）。
