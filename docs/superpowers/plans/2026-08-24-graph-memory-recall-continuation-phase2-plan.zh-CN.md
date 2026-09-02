# Graph Memory Recall Continuation（Phase 2）实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 实现相邻 recall 间的证据接续（evidence continuation）--同 channel 的下一次 recall 复用上一轮 adopted 探索的 query-aware 压缩简报，注入 explore prompt 作为先验证据。

**Architecture:** memorygraph 包新增四个构件：导出的 `TraceMessage`（白名单清洗后的消息记录）、`PriorCompressor`（一次性 LLM 压缩调用）、`PriorRecordStore`（graph 目录下按 channel key 的文件式 per-channel prior record）、`ExploreWithPrior`（prior 节点并入种子集 + prompt 双区块注入）。service 层 Executor 装配：加载 prior -> 版本匹配则压缩（规范化 query 精确缓存 + singleflight）-> 注入 explore -> found 后以 adopted transcript 覆盖 prior 指针。

**Tech Stack:** Go（module `github.com/multica-ai/multica/server`）、`golang.org/x/sync/singleflight`（go.mod 已有 v0.20.0）、标准 `testing`。

**Spec:** `docs/superpowers/specs/2026-08-24-graph-memory-recall-continuation-spec.zh-CN.md` §5（设计决定 D1、D4–D11）。

**注意（共识 D12 的门控调整）：** 原计划 Phase 2 以 Phase 1 基线数据为启动门槛；用户指示立即启动。因此本计划把 D9 的遥测做成「上线后同时采集 prior_on / prior_off 对比数据」--`PriorUsed` 已入 query log，回退判据（净开销不降则回退）以线上数据裁决，不等离线基线。

## Global Constraints

- 所有命令在 `multica/server/` 下执行；Go 工具链 `/home/zhoujie22/go-native/go/bin/go`，需 `GOCACHE=/home/zhoujie22/.gocache-zcode GOPROXY=https://goproxy.cn,direct`（默认 proxy 不可达）。
- 一切 prior/compression 失败**非致命降级**为无 prior 路径（与现有 recall 非致命语义一致，spec §5.2）。
- brief 只含图证据（节点 ID、发现、被否定分支、未决问题），**不含**原始 transcript、凭证、会话内部信息（D6）。
- 注入的 prior 节点必须过 `GraphView` 可见性检查（D6）；fresh seeds 永远保留且排在 prior 之前（D1）。
- prompt 中 prior 区块必须带 tentative 措辞（"MAY BE STALE OR WRONG - this round's query always wins"）--D7 之下这是唯一的锚定缓解，是承重墙。
- 不改：explore 工具协议、adoption 规则、MaxRounds（保持 6，D9）、consolidation、版本 pinning。
- prior 失效边界仅 graph version（D8）：`rec.GraphVersion != plan.GraphVersion` -> 视为无 prior，不做 hash 复验、无年龄上限。
- 提交信息格式 `feat(<scope>): ...` / `test(<scope>): ...`；不 push。
- 代码注释仅英文（仓库规范）。

---

### Task 1: 导出 `TraceMessage` 并捕获 adopted transcript（memorygraph 包）

**Files:**
- Modify: `internal/memorygraph/trace_writer.go`（`traceMessage` 重命名为导出的 `TraceMessage`；`TraceDrain.Messages()` 幂等化）
- Modify: `internal/memorygraph/types.go`（`ExploreRun`/`RecallResult` 增字段）
- Modify: `internal/memorygraph/explore.go`（adoption 处清洗并挂载）
- Test: `internal/memorygraph/explore_test.go`、`internal/memorygraph/trace_writer_test.go`（追加）

**关键事实（已核实，勿改动既有语义）：**
- `TraceRecorder.Drain` 在 nil recorder 下也真实缓冲（trace_writer.go:53-67），因此 transcript 捕获**不依赖** traces 是否启用。
- 现有 `TraceDrain.Messages()` 是一次性信道接收（`return <-d.done`）。`runTrajectory` 捕获 transcript 与（recorder 启用时）`writeTrace` 内的 `drain.Messages()` 会构成**两次接收，第二次永久阻塞**--必须先做幂等化，这是本 Task 的前置步骤。
- 成功路径要求 backend 真实调用 tool server 的 `/explore` + `/submit`（`srv.trajectorySubmission` 为空则 run 失败）。已有 `traceFakeBackend`（trace_writer_test.go:42-66）同时做真实提交和流式消息，直接复用；不要新造只回放 session 的 backend。
- `serializeTraceMessage(seq, m)` 的 Sequence 是 0-based（trace_writer.go:273）。

**Interfaces:**
- Produces: `type TraceMessage struct {...}`（7 个白名单字段，json 标签不变）；`RecallResult.AdoptedIndex int`、`RecallResult.AdoptedTranscript []TraceMessage`；`ExploreRun.Messages []agent.Message \`json:"-"\``。Task 2/3/5 消费。

- [ ] **Step 1: Write the failing tests**

追加到 `explore_test.go`：

```go
// Phase 2 §5.1: the adopted run's message stream is captured, sanitized to
// the allowlisted TraceMessage shape, and exposed on the result for the
// per-channel prior record. Non-adopted runs drop their buffered streams.
func TestExploreCapturesAdoptedTranscript(t *testing.T) {
	store := newExploreStore(t)
	retr := newExploreRetriever(t, store)
	finalJSON := `{"found":true,"summary":"s","node_ids":["n-target"],"rounds":1}`
	backend := &traceFakeBackend{
		output: finalJSON,
		msgs: []agent.Message{
			{Type: agent.MessageText, Content: "exploring the graph"},
			{Type: agent.MessageDiagnostic, Title: "diag", Diagnostic: "provider-internal", Content: "diag content"},
		},
	}
	ex := NewExplorer(store, retr, backend, testExploreConfig(), "pi", nil)

	res, err := ex.Explore(context.Background(), "dispatch router retries")
	if err != nil {
		t.Fatalf("Explore: %v", err)
	}
	if !res.Found || res.AdoptedIndex != 0 {
		t.Fatalf("result found=%v adopted=%d, want found run 0 adopted", res.Found, res.AdoptedIndex)
	}
	if len(res.AdoptedTranscript) != 2 {
		t.Fatalf("AdoptedTranscript = %d records, want 2", len(res.AdoptedTranscript))
	}
	if res.AdoptedTranscript[0].Content != "exploring the graph" || res.AdoptedTranscript[0].Sequence != 0 {
		t.Fatalf("transcript[0] = %+v", res.AdoptedTranscript[0])
	}
	// Diagnostic internals stay out: the record carries only the allowlisted
	// Content column, never Title/Diagnostic/SessionID.
	if res.AdoptedTranscript[1].Content != "diag content" || res.AdoptedTranscript[1].Type != "diagnostic" {
		t.Fatalf("transcript[1] = %+v", res.AdoptedTranscript[1])
	}
	if len(res.AgentRuns) != 1 || res.AgentRuns[0].Messages != nil {
		t.Fatalf("run message buffers must be cleared after adoption: %+v", res.AgentRuns[0])
	}
}
```

追加到 `trace_writer_test.go`：

```go
// Phase 2: transcript capture in runTrajectory and the trace write both
// consume the same drain; Messages must be idempotent or the second call
// blocks forever on the one-shot channel.
func TestTraceDrainMessagesIdempotent(t *testing.T) {
	msgs := make(chan agent.Message, 1)
	msgs <- agent.Message{Type: agent.MessageText, Content: "m"}
	close(msgs)
	drain := (*TraceRecorder)(nil).Drain(msgs)
	first := drain.Messages()
	second := drain.Messages()
	if len(first) != 1 || len(second) != 1 || first[0].Content != second[0].Content {
		t.Fatalf("idempotent Messages: first=%v second=%v", first, second)
	}
	if (*TraceDrain)(nil).Messages() != nil {
		t.Fatalf("nil drain must yield nil")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
export PATH=/home/zhoujie22/go-native/go/bin:$PATH GOCACHE=/home/zhoujie22/.gocache-zcode GOPROXY=https://goproxy.cn,direct
go test ./internal/memorygraph/ -run 'TestExploreCapturesAdoptedTranscript|TestTraceDrainMessagesIdempotent' -v
```

Expected: 编译失败 `res.AdoptedIndex undefined` / `run.Messages undefined`。

- [ ] **Step 3: Implement**

3a. `trace_writer.go` -- `TraceDrain` 幂等化：

```go
type TraceDrain struct {
	once sync.Once
	done chan []agent.Message
	msgs []agent.Message
}
```

```go
// Messages returns the drained messages in arrival order, blocking until the
// session's message channel closed. Safe to call repeatedly (the buffered
// slice is handed out once); a nil drain yields nil.
func (d *TraceDrain) Messages() []agent.Message {
	if d == nil {
		return nil
	}
	d.once.Do(func() { d.msgs = <-d.done })
	return d.msgs
}
```

（import 补 `sync`；`Drain` 构造处不变。）

3b. `trace_writer.go` -- `type traceMessage struct` 重命名为 `type TraceMessage`（json 标签原样），`serializeTraceMessage` 返回类型同步改；包内引用点同步。

3c. `types.go` -- `ExploreRun` 增字段（import 补 `pkg/agent`）、`RecallResult` 增字段：

```go
	// Messages is the run's drained message stream, captured for every run
	// and cleared on all runs after adoption (only the sanitized adopted
	// transcript travels on). Transport for the prior record, never
	// persisted.
	Messages []agent.Message `json:"-"`
```

```go
	// AdoptedIndex points into AgentRuns at the adopted trajectory (-1 on
	// miss). AdoptedTranscript is the adopted run's message stream
	// sanitized to the allowlisted TraceMessage shape (Phase 2 prior
	// record input).
	AdoptedIndex      int            `json:"adopted_index,omitempty"`
	AdoptedTranscript []TraceMessage `json:"adopted_transcript,omitempty"`
```

3d. `explore.go` `runTrajectory` -- `<-session.Result` 的 `!ok` 检查之后、status 检查之前：

```go
	run.Messages = drain.Messages()
```

3e. `explore.go` adoption 段（`if adopted >= 0 {` 块内，`result.Citations` 赋值之后）追加，并在两个 return 之前清空全部 run 缓冲：

```go
		result.AdoptedIndex = adopted
		result.AdoptedTranscript = sanitizeTranscript(a.Messages)
	}
	for i := range runs {
		runs[i].Messages = nil
	}
```

新增（0-based sequence，与 writeTrace 一致）：

```go
// sanitizeTranscript maps the adopted run's message stream onto the
// allowlisted TraceMessage shape (same columns as the trace writer).
func sanitizeTranscript(msgs []agent.Message) []TraceMessage {
	out := make([]TraceMessage, 0, len(msgs))
	for i, m := range msgs {
		out = append(out, serializeTraceMessage(i, m))
	}
	return out
}
```

- [ ] **Step 4: Run tests**

```bash
go test ./internal/memorygraph/ -run 'TestExploreCapturesAdoptedTranscript|TestTraceDrainMessagesIdempotent' -v
go test ./internal/memorygraph/
```

Expected: PASS，包内全绿（含既有 trace/explore 测试--幂等化对它们透明）。

- [ ] **Step 5: Commit**

```bash
git add internal/memorygraph/
git commit -m "feat(memorygraph): capture adopted run transcript as sanitized TraceMessage"
```

---

### Task 2: `PriorBrief` 与 `PriorCompressor`（memorygraph 包，新文件 `prior.go`）

**Files:**
- Create: `internal/memorygraph/prior.go`
- Test: `internal/memorygraph/prior_test.go`

**关键事实（已核实）：**
- `agent.ExecOptions{Model, ThreadName, Timeout, EphemeralSession}`（explore.go:281-286 的用法）。
- `agent.Result{Status: "completed", Output: string}`；`Session.Messages` 在 Result resolve 前关闭（channel 语义），`Session{Messages, Result}` 字段可在外部构造。
- `exploreCompletedSession(output)`（explore_test.go:125-127）返回无消息、Result completed 的 session，本 Task 的压缩测试直接可用。

**Interfaces:**
- Consumes: Task 1 的 `TraceMessage`；`AgentBackend`（explore.go:18）。
- Produces: `type PriorBrief struct{...}`、`func NewPriorCompressor(backend AgentBackend, model string, timeout time.Duration) *PriorCompressor`、`(c *PriorCompressor) Compress(ctx context.Context, query string, transcript []TraceMessage) (*PriorBrief, error)`、`const DefaultPriorCompressionTimeout`。Task 4/5 消费。

- [ ] **Step 1: Write the failing test**

`internal/memorygraph/prior_test.go`：

```go
package memorygraph

import (
	"context"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/pkg/agent"
)

// replayCompressorBackend hands every Execute the same completed session.
type replayCompressorBackend struct{ output string }

func (b *replayCompressorBackend) Execute(context.Context, string, agent.ExecOptions) (*agent.Session, error) {
	return exploreCompletedSession(b.output), nil
}

func TestPriorCompressorParsesBrief(t *testing.T) {
	out := `{"summary":"prior work","node_ids":["n-a","n-b"],"observations":["a and b relate"],"rejected":["c irrelevant"],"open_questions":["timeline of d"]}`
	comp := NewPriorCompressor(&replayCompressorBackend{output: out}, "m", time.Second)

	brief, err := comp.Compress(context.Background(), "query B", []TraceMessage{
		{Kind: "message", Sequence: 0, Type: "text", Content: "explored n-a"},
	})
	if err != nil {
		t.Fatalf("Compress: %v", err)
	}
	if brief.Summary != "prior work" || len(brief.NodeIDs) != 2 || brief.NodeIDs[0] != "n-a" {
		t.Fatalf("brief = %+v", brief)
	}
	if len(brief.Observations) != 1 || len(brief.Rejected) != 1 || len(brief.OpenQuestions) != 1 {
		t.Fatalf("brief evidence fields = %+v", brief)
	}
}

func TestPriorCompressorFailures(t *testing.T) {
	transcript := []TraceMessage{{Kind: "message", Sequence: 0, Type: "text", Content: "x"}}
	if _, err := NewPriorCompressor(nil, "m", time.Second).Compress(context.Background(), "q", transcript); err == nil {
		t.Fatalf("want error for nil backend")
	}
	if _, err := NewPriorCompressor(&replayCompressorBackend{output: "ok"}, "m", time.Second).Compress(context.Background(), "q", nil); err == nil {
		t.Fatalf("want error for empty transcript")
	}
	if _, err := NewPriorCompressor(&replayCompressorBackend{output: "no json here"}, "m", time.Second).Compress(context.Background(), "q", transcript); err == nil {
		t.Fatalf("want error for unparseable compressor output")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test ./internal/memorygraph/ -run TestPriorCompressor -v
```

Expected: 编译失败 `undefined: NewPriorCompressor`。

- [ ] **Step 3: Implement `internal/memorygraph/prior.go`**

```go
package memorygraph

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/multica-ai/multica/server/pkg/agent"
)

// DefaultPriorCompressionTimeout bounds the one-shot continuation
// compression call; a slower compressor degrades to a prior-less recall.
const DefaultPriorCompressionTimeout = 60 * time.Second

// PriorBrief is the query-aware, graph-evidence-only digest of a previous
// recall's adopted exploration (spec §5.3): node ids and findings, never
// raw transcripts, credentials, or session internals.
type PriorBrief struct {
	Summary       string   `json:"summary"`
	NodeIDs       []string `json:"node_ids"`
	Observations  []string `json:"observations,omitempty"`
	Rejected      []string `json:"rejected,omitempty"`
	OpenQuestions []string `json:"open_questions,omitempty"`
}

// PriorCompressor distills one adopted transcript into a PriorBrief for
// the next recall's query (spec §5.2: lazy per-B, query-aware).
type PriorCompressor struct {
	backend AgentBackend
	model   string
	timeout time.Duration
}

func NewPriorCompressor(backend AgentBackend, model string, timeout time.Duration) *PriorCompressor {
	if timeout <= 0 {
		timeout = DefaultPriorCompressionTimeout
	}
	return &PriorCompressor{backend: backend, model: model, timeout: timeout}
}

// Compress runs the single LLM call. Any failure is returned as an error;
// the caller degrades to a prior-less recall.
func (c *PriorCompressor) Compress(ctx context.Context, query string, transcript []TraceMessage) (*PriorBrief, error) {
	if c.backend == nil {
		return nil, fmt.Errorf("prior compress: backend not configured")
	}
	if len(transcript) == 0 {
		return nil, fmt.Errorf("prior compress: empty transcript")
	}
	raw, err := json.Marshal(transcript)
	if err != nil {
		return nil, fmt.Errorf("prior compress: transcript: %w", err)
	}
	var b strings.Builder
	b.WriteString("You compress the sanitized exploration transcript of a PREVIOUS memory-graph recall into prior evidence for the CURRENT query.\n\n")
	fmt.Fprintf(&b, "Current query: %s\n\n", query)
	b.WriteString("Rules:\n")
	b.WriteString("- Output graph evidence only: node ids seen in the transcript, findings, rejected branches, open questions.\n")
	b.WriteString("- Never invent node ids; every node id must appear in the transcript.\n")
	b.WriteString("- If nothing in the transcript is relevant to the current query, return empty evidence.\n\n")
	b.WriteString("Transcript (allowlisted message records):\n")
	b.Write(raw)
	b.WriteString("\n\nYour FINAL response must be exactly one JSON object and nothing else:\n")
	b.WriteString(`{"summary":string,"node_ids":[string],"observations":[string],"rejected":[string],"open_questions":[string]}`)

	execCtx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	session, err := c.backend.Execute(execCtx, b.String(), agent.ExecOptions{
		Model: c.model, ThreadName: "memorygraph-prior-compress", Timeout: c.timeout, EphemeralSession: true,
	})
	if err != nil {
		return nil, fmt.Errorf("prior compress: execute: %w", err)
	}
	// Session.Messages is a 256-cap buffered channel; keep it flowing until
	// it closes (it closes before Result resolves) so long outputs stall
	// nobody. The compressor itself only needs the final result.
	go func() {
		for range session.Messages {
		}
	}()
	result, ok := <-session.Result
	if !ok {
		return nil, fmt.Errorf("prior compress: session ended without a result")
	}
	if result.Status != "completed" {
		return nil, fmt.Errorf("prior compress: session status %q", result.Status)
	}
	var brief PriorBrief
	start, end := strings.Index(result.Output, "{"), strings.LastIndex(result.Output, "}")
	if start < 0 || end < start || json.Unmarshal([]byte(result.Output[start:end+1]), &brief) != nil {
		return nil, fmt.Errorf("prior compress: unparseable output")
	}
	return &brief, nil
}
```

- [ ] **Step 4: Run tests**（同上命令）Expected: PASS。

- [ ] **Step 5: Commit**

```bash
git add internal/memorygraph/prior.go internal/memorygraph/prior_test.go
git commit -m "feat(memorygraph): PriorCompressor distills adopted transcript into graph-evidence brief"
```

---

### Task 3: `PriorRecordStore`（memorygraph 包，文件式 per-channel prior record）

**Files:**
- Modify: `internal/memorygraph/prior.go`（追加）
- Test: `internal/memorygraph/prior_test.go`（追加）

**Interfaces:**
- Produces: `type PriorRecord struct{ GraphVersion int; Query string; CreatedAt time.Time; Transcript []TraceMessage; Briefs map[string]PriorBrief }`、`NewPriorRecordStore(dir string) *PriorRecordStore`、`(s) Save(key string, rec PriorRecord) error`（sha1 文件名 + temp/rename 原子写）、`(s) Load(key string) (*PriorRecord, error)`（缺失返回 nil, nil）、`NormalizeRecallKey(query string) string`。Task 5 消费。

- [ ] **Step 1: Write the failing test**

```go
func TestPriorRecordStoreRoundtrip(t *testing.T) {
	dir := t.TempDir()
	s := NewPriorRecordStore(dir)
	if rec, err := s.Load("ws|channel|c1"); err != nil || rec != nil {
		t.Fatalf("Load missing = (%v, %v), want (nil, nil)", rec, err)
	}
	in := PriorRecord{
		GraphVersion: 3, Query: "q1", CreatedAt: time.Unix(1000, 0).UTC(),
		Transcript: []TraceMessage{{Kind: "message", Sequence: 0, Type: "text", Content: "hello"}},
		Briefs:     map[string]PriorBrief{"q b": {Summary: "s"}},
	}
	if err := s.Save("ws|channel|c1", in); err != nil {
		t.Fatalf("Save: %v", err)
	}
	out, err := s.Load("ws|channel|c1")
	if err != nil || out == nil {
		t.Fatalf("Load: %v %v", out, err)
	}
	if out.GraphVersion != 3 || out.Query != "q1" || len(out.Transcript) != 1 || out.Briefs["q b"].Summary != "s" {
		t.Fatalf("roundtrip = %+v", out)
	}
	in.GraphVersion = 4 // overwrite: the newest found recall replaces wholesale
	if err := s.Save("ws|channel|c1", in); err != nil {
		t.Fatalf("Save overwrite: %v", err)
	}
	if out, _ := s.Load("ws|channel|c1"); out.GraphVersion != 4 {
		t.Fatalf("overwrite failed: %+v", out)
	}
}

func TestNormalizeRecallKey(t *testing.T) {
	if NormalizeRecallKey("  Foo   Bar ") != "foo bar" {
		t.Fatalf("NormalizeRecallKey = %q", NormalizeRecallKey("  Foo   Bar "))
	}
}
```

- [ ] **Step 2: Run test to verify it fails**（`-run TestPriorRecordStore`，编译失败 `undefined: NewPriorRecordStore`）

- [ ] **Step 3: Implement**（追加到 `prior.go`；import 补 `crypto/sha1`、`errors`、`os`、`path/filepath`）

```go
// PriorRecord is the per-channel continuation state (spec §5.1): the
// latest found recall's sanitized adopted transcript plus the brief cache.
// It lives under <graph_dir>/continuation/<sha1(key)>.json and is replaced
// wholesale by each new found recall; a graph version change invalidates
// it (caller compares GraphVersion, spec D8).
type PriorRecord struct {
	GraphVersion int                   `json:"graph_version"`
	Query        string                `json:"query"`
	CreatedAt    time.Time             `json:"created_at"`
	Transcript   []TraceMessage        `json:"transcript"`
	Briefs       map[string]PriorBrief `json:"briefs,omitempty"`
}

type PriorRecordStore struct{ dir string }

func NewPriorRecordStore(dir string) *PriorRecordStore { return &PriorRecordStore{dir: dir} }

func priorRecordPath(dir, key string) string {
	sum := sha1.Sum([]byte(key))
	return filepath.Join(dir, fmt.Sprintf("%x.json", sum))
}

// Save writes the record atomically (temp file + rename). Errors are the
// caller's to log; continuation is best-effort by design.
func (s *PriorRecordStore) Save(key string, rec PriorRecord) error {
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return err
	}
	data, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	path := priorRecordPath(s.dir, key)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Load returns nil, nil when no record exists for the key.
func (s *PriorRecordStore) Load(key string) (*PriorRecord, error) {
	data, err := os.ReadFile(priorRecordPath(s.dir, key))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var rec PriorRecord
	if err := json.Unmarshal(data, &rec); err != nil {
		return nil, err
	}
	return &rec, nil
}

// NormalizeRecallKey canonicalizes a query for exact-match brief caching
// (spec D11): case-folding with whitespace runs collapsed. Mirrors the
// daemon-side batch coalescing key.
func NormalizeRecallKey(query string) string {
	return strings.Join(strings.Fields(strings.ToLower(query)), " ")
}
```

- [ ] **Step 4: Run tests**（PASS）
- [ ] **Step 5: Commit**：`feat(memorygraph): file-backed per-channel PriorRecordStore with brief cache`

---

### Task 4: `ExploreWithPrior`（种子合并 + View 过滤 + prompt 双区块）

**Files:**
- Modify: `internal/memorygraph/explore.go`、`internal/memorygraph/retriever.go`
- Test: `internal/memorygraph/explore_test.go`（追加）

**关键事实（已核实，测试据此设计）：**
- Store fixture 只有两个节点 `n-target`（dispatch 主题）与 `n-other`（vector cache 主题）（explore_test.go:32-34）。**不要用 nil fresh seeds 测 prior 合并**--空 seeds 会回退 hybrid search，TopK=10 会把 fixture 两个节点全部返回，prior 的 `n-other` 被去重掉。必须显式传 fresh seeds，靠 prompt 断言合并结果。
- retriever.go:196-199 的过滤规则：`viewActive()` 为假全放行；为真时 `nodeForDoc(id) == nil`（staging/未知 id）放行，否则 `View.Allows(n)`。`AllowsNodeID` 必须逐字等价。
- `seedsFromIDs` 对未知 id 也追加（snippet 为空串）--合并时以 `snippet != ""` 作为"水化成功"判据，未知 id 被跳过。
- prompt 种子行格式 `- %s: %s\n`（explore.go:417）；prior 区块插在种子循环之后、`"Tool server base URL"` 行之前。
- `fakeExploreBackend` 只探索 prompt 中的**第一个**种子（`firstSeedNode`）并提交它--它验证整条链路，合并顺序由 prompt 捕获测试证明。

**Interfaces:**
- Consumes: Task 2 的 `PriorBrief`。
- Produces: `(e *Explorer) ExploreWithPrior(ctx, query string, seedIDs []string, prior *PriorBrief) (*RecallResult, error)`（`ExploreWithSeeds` 变为其 nil-prior 薄包装）；`(r *HybridRetriever) AllowsNodeID(id string) bool`。Task 5 消费。

- [ ] **Step 1: Write the failing tests**

```go
type promptCaptureBackend struct{ prompt string }

func (b *promptCaptureBackend) Execute(_ context.Context, prompt string, _ agent.ExecOptions) (*agent.Session, error) {
	b.prompt = prompt
	return exploreCompletedSession(`{"found":false,"summary":"","node_ids":[],"rounds":0}`), nil
}

// Phase 2 §5.4: prior node ids merge AFTER the fresh seeds (fresh always
// first, D1), duplicates are skipped, unknown ids (empty hydration) are
// dropped, and the tentative evidence block carries the full brief.
func TestExploreWithPriorPromptCarriesTentativeBlock(t *testing.T) {
	store := newExploreStore(t)
	retr := newExploreRetriever(t, store)
	capture := &promptCaptureBackend{}
	ex := NewExplorer(store, retr, capture, testExploreConfig(), "pi", nil)

	prior := &PriorBrief{
		Summary: "S", NodeIDs: []string{"n-ghost", "n-other", "n-target"}, // dup + unknown + mergeable
		Observations: []string{"obs-1"}, Rejected: []string{"rej-1"}, OpenQuestions: []string{"oq-1"},
	}
	if _, err := ex.ExploreWithPrior(context.Background(), "dispatch router retries", []string{"n-target"}, prior); err != nil {
		t.Fatalf("ExploreWithPrior: %v", err)
	}
	for _, want := range []string{
		"MAY BE STALE OR WRONG", "prior summary: S",
		"observation: obs-1", "rejected branch: rej-1", "open question: oq-1",
	} {
		if !strings.Contains(capture.prompt, want) {
			t.Fatalf("prompt missing %q", want)
		}
	}
	freshAt := strings.Index(capture.prompt, "- n-target:")
	priorAt := strings.Index(capture.prompt, "- n-other:")
	if freshAt < 0 || priorAt < 0 || freshAt > priorAt {
		t.Fatalf("fresh seed must precede merged prior seed (fresh=%d prior=%d)", freshAt, priorAt)
	}
	if strings.Count(capture.prompt, "- n-other:") != 1 {
		t.Fatalf("prior node must appear exactly once (dedup)")
	}
	if strings.Contains(capture.prompt, "n-ghost") {
		t.Fatalf("unknown prior node id must be dropped from seeds")
	}
}

// The merged seed list must keep the whole tool-server flow healthy: the
// fake backend explores and submits the first seed end to end.
func TestExploreWithPriorToolServerPath(t *testing.T) {
	store := newExploreStore(t)
	retr := newExploreRetriever(t, store)
	backend := &fakeExploreBackend{t: t}
	ex := NewExplorer(store, retr, backend, testExploreConfig(), "pi", nil)

	prior := &PriorBrief{Summary: "prior summary", NodeIDs: []string{"n-other"}}
	res, err := ex.ExploreWithPrior(context.Background(), "dispatch router retries", []string{"n-target"}, prior)
	if err != nil {
		t.Fatalf("ExploreWithPrior: %v", err)
	}
	if len(backend.errs) > 0 {
		t.Fatalf("fake backend tool errors: %v", backend.errs)
	}
	if !res.Found || len(res.NodeIDs) != 1 || res.NodeIDs[0] != "n-target" {
		t.Fatalf("result = %+v, want first seed n-target explored and adopted", res)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**（`-run TestExploreWithPrior`，`undefined: ExploreWithPrior`）

- [ ] **Step 3: Implement**

3a. `retriever.go` 追加（与 Search 的放行规则逐字等价）：

```go
// AllowsNodeID reports whether id may be surfaced to this retriever's
// caller: staging docs and unknown ids pass (same rule as Search), graph
// nodes must satisfy the active view. The continuation seed merge uses
// this so prior node ids can never bypass channel visibility (spec §5.3).
func (r *HybridRetriever) AllowsNodeID(id string) bool {
	n := r.nodeForDoc(id)
	return n == nil || !r.viewActive() || r.cfg.View.Allows(n)
}
```

3b. `explore.go` -- `ExploreWithSeeds` 整体改为薄包装，原主体变成 `ExploreWithPrior`（签名增加 `prior *PriorBrief`，doc comment 说明 prior 语义）：

```go
// ExploreWithSeeds is ExploreWithPrior without prior evidence.
func (e *Explorer) ExploreWithSeeds(ctx context.Context, query string, seedIDs []string) (*RecallResult, error) {
	return e.ExploreWithPrior(ctx, query, seedIDs, nil)
}
```

种子段变为：

```go
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
	if prior != nil {
		seeds = e.mergePriorSeeds(retr, seeds, prior, version)
	}
```

```go
// mergePriorSeeds appends prior node ids that are view-visible and hydrate
// to a non-empty body at the pinned version (fresh seeds always come
// first; duplicates and unknown ids are skipped). Spec §5.4.
func (e *Explorer) mergePriorSeeds(retr *HybridRetriever, seeds []exploreSeed, prior *PriorBrief, version int) []exploreSeed {
	seen := make(map[string]bool, len(seeds))
	for _, s := range seeds {
		seen[s.id] = true
	}
	for _, id := range prior.NodeIDs {
		if seen[id] || !retr.AllowsNodeID(id) {
			continue
		}
		merged := e.seedsFromIDs([]string{id}, version)
		if len(merged) == 1 && merged[0].snippet != "" {
			seeds = append(seeds, merged[0])
			seen[id] = true
		}
	}
	return seeds
}
```

3c. `runTrajectory` 与 `buildPrompt` 增加 `prior *PriorBrief` 参数并穿透（goroutine 调用处同步）。`buildPrompt` 种子循环之后、`fmt.Fprintf(&b, "\nTool server base URL: %s\n", baseURL)` 之前插入：

```go
	if prior != nil {
		b.WriteString("\nPrior exploration evidence from the previous recall in this channel (MAY BE STALE OR WRONG - this round's query always wins; re-read any node via /explore to verify before relying on it):\n")
		fmt.Fprintf(&b, "- prior summary: %s\n", prior.Summary)
		if len(prior.NodeIDs) > 0 {
			fmt.Fprintf(&b, "- prior node ids: %s\n", strings.Join(prior.NodeIDs, ", "))
		}
		for _, o := range prior.Observations {
			fmt.Fprintf(&b, "- observation: %s\n", o)
		}
		for _, rj := range prior.Rejected {
			fmt.Fprintf(&b, "- rejected branch: %s\n", rj)
		}
		for _, q := range prior.OpenQuestions {
			fmt.Fprintf(&b, "- open question: %s\n", q)
		}
	}
```

- [ ] **Step 4: Run tests**

```bash
go test ./internal/memorygraph/ -run TestExploreWithPrior -v
go test ./internal/memorygraph/
```

Expected: PASS，全包回归绿（Task 1 旧测试与 `Explore`/`ExploreWithSeeds` 薄包装等价性由此保证）。

- [ ] **Step 5: Commit**：`feat(memorygraph): ExploreWithPrior injects view-filtered prior evidence block into trajectories`

---

### Task 5: Executor 装配（service 包）--加载/压缩/去重/注入/回写 + 遥测

**Files:**
- Modify: `internal/service/graph_memory_recall_execute.go`
- Modify: `internal/memorygraph/types.go`（`QueryLogEntry` 增 `PriorUsed bool \`json:"prior_used,omitempty"\``）
- Test: `internal/service/graph_memory_recall_execute_test.go`（追加）

**关键事实（已核实）：**
- `Execute` 在 explore 调用**之前**已创建 `backend`（graph_memory_recall_execute.go:78）--压缩复用该 backend，不再调 factory。
- `GraphMemoryRecallExecutor` 字段：`pool, dive, backendFactory, embedder, traces, model`（同包测试可直接构造）。
- `GraphMemoryRecallPlan` 有 `WorkspaceID, GraphKind, GraphOwnerID, GraphDir, GraphVersion, GraphView, Query, Seeds`。
- `agent.Session{Messages, Result}` 与 `agent.Result{Status, Output}` 均为导出字段，service 测试可直接构造。

**Interfaces:**
- Consumes: Task 1–4 全部产物；`golang.org/x/sync/singleflight`。
- Produces: `(e *GraphMemoryRecallExecutor) priorBrief(ctx, plan, rec, store, ownerKey string, backend memorygraph.AgentBackend) *memorygraph.PriorBrief`（可单测）；`graphPriorOwnerKey(plan)`。

- [ ] **Step 1: Write the failing tests**

追加到 `graph_memory_recall_execute_test.go`（import 按需补 `pkg/agent`）：

```go
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
	e := &GraphMemoryRecallExecutor{model: "m"}
	store := memorygraph.NewPriorRecordStore(t.TempDir())
	rec := &memorygraph.PriorRecord{GraphVersion: 1, Query: "old", Briefs: map[string]memorygraph.PriorBrief{
		memorygraph.NormalizeRecallKey("Query B"): {Summary: "cached"},
	}}
	plan := testPriorPlan("Query B")

	brief := e.priorBrief(context.Background(), plan, rec, store, graphPriorOwnerKey(plan), nil)
	if brief == nil || brief.Summary != "cached" {
		t.Fatalf("brief = %+v, want cached", brief)
	}
}

// Cache miss: compression runs against the backend and the parsed brief is
// written back under the normalized query key for the next recall.
func TestPriorBriefCompressMissWritesBack(t *testing.T) {
	e := &GraphMemoryRecallExecutor{model: "m"}
	dir := t.TempDir()
	store := memorygraph.NewPriorRecordStore(dir)
	rec := &memorygraph.PriorRecord{GraphVersion: 1, Query: "old", Transcript: []memorygraph.TraceMessage{
		{Kind: "message", Sequence: 0, Type: "text", Content: "explored n-a"},
	}}
	backend := &replayAgentBackend{output: `{"summary":"fresh","node_ids":["n-a"],"observations":["o"],"rejected":[],"open_questions":[]}`}
	plan := testPriorPlan("Query B")

	brief := e.priorBrief(context.Background(), plan, rec, store, graphPriorOwnerKey(plan), backend)
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
```

- [ ] **Step 2: Run tests to verify they fail**（`go test ./internal/service/ -run TestPriorBrief -v`，`e.priorBrief undefined`）

- [ ] **Step 3: Implement**

3a. `types.go`：`QueryLogEntry` 增 `PriorUsed bool \`json:"prior_used,omitempty"\``。

3b. `graph_memory_recall_execute.go`：import 增 `path/filepath`、`strconv`、`golang.org/x/sync/singleflight`；`GraphMemoryRecallExecutor` 增字段 `priorFlights singleflight.Group`。

3c. `Execute` 中 `explorer.PinVersion(plan.GraphVersion)` 与 explore 调用之间插入，并把 explore 调用替换：

```go
	priorStore := memorygraph.NewPriorRecordStore(filepath.Join(plan.GraphDir, "continuation"))
	ownerKey := graphPriorOwnerKey(plan)
	var brief *memorygraph.PriorBrief
	if rec, err := priorStore.Load(ownerKey); err != nil {
		slog.Warn("graph memory recall: prior record load failed; continuing without prior", "recall_id", plan.RecallID, "error", err)
	} else if rec != nil && rec.GraphVersion == plan.GraphVersion { // D8: version is the only expiry boundary
		brief = e.priorBrief(ctx, plan, rec, priorStore, ownerKey, backend)
	}
	result, err := explorer.ExploreWithPrior(ctx, plan.Query, plan.Seeds, brief)
```

3d. 新增方法：

```go
// graphPriorOwnerKey is the per-channel continuation key: workspace +
// graph identity + view channel (spec §5.3). Cross-workspace or
// cross-channel reuse is impossible by construction.
func graphPriorOwnerKey(plan *GraphMemoryRecallPlan) string {
	return strings.Join([]string{
		plan.WorkspaceID, plan.GraphKind, plan.GraphOwnerID, plan.GraphView.ChannelID,
	}, "|")
}

// priorBrief resolves the query-aware brief for this recall: exact-match
// cache on the normalized query, then a singleflight-compressed miss (spec
// §5.2/§5.5). Every failure degrades to nil (prior-less recall).
func (e *GraphMemoryRecallExecutor) priorBrief(ctx context.Context, plan *GraphMemoryRecallPlan, rec *memorygraph.PriorRecord, store *memorygraph.PriorRecordStore, ownerKey string, backend memorygraph.AgentBackend) *memorygraph.PriorBrief {
	key := memorygraph.NormalizeRecallKey(plan.Query)
	if key == "" {
		return nil
	}
	if cached, ok := rec.Briefs[key]; ok {
		slog.Info("graph memory recall: prior brief cache hit", "recall_id", plan.RecallID)
		return &cached
	}
	flightKey := ownerKey + "|" + strconv.Itoa(rec.GraphVersion) + "|" + key
	started := time.Now()
	v, err, _ := e.priorFlights.Do(flightKey, func() (any, error) {
		return memorygraph.NewPriorCompressor(backend, e.model, memorygraph.DefaultPriorCompressionTimeout).
			Compress(ctx, plan.Query, rec.Transcript)
	})
	if err != nil {
		slog.Warn("graph memory recall: prior compression failed; continuing without prior", "recall_id", plan.RecallID, "error", err)
		return nil
	}
	brief, _ := v.(*memorygraph.PriorBrief)
	if brief == nil {
		return nil
	}
	slog.Info("graph memory recall: prior brief compressed",
		"recall_id", plan.RecallID, "ms", time.Since(started).Milliseconds(),
		"transcript_msgs", len(rec.Transcript), "brief_nodes", len(brief.NodeIDs))
	if rec.Briefs == nil {
		rec.Briefs = map[string]memorygraph.PriorBrief{}
	}
	rec.Briefs[key] = *brief
	if err := store.Save(ownerKey, *rec); err != nil { // best-effort write-back
		slog.Warn("graph memory recall: prior brief write-back failed", "recall_id", plan.RecallID, "error", err)
	}
	return brief
}
```

3e. `RecordRecall` 的 entry 增 `PriorUsed: brief != nil`。

3f. found 后回写 prior 指针（`enqueueDive` 成功之后、`if !result.Found` return 之前）：

```go
	if result.Found {
		if err := priorStore.Save(ownerKey, memorygraph.PriorRecord{
			GraphVersion: plan.GraphVersion,
			Query:        plan.Query,
			CreatedAt:    time.Now().UTC(),
			Transcript:   result.AdoptedTranscript,
		}); err != nil {
			slog.Warn("graph memory recall: prior record save failed", "recall_id", plan.RecallID, "error", err)
		}
	}
```

（新 found recall 整体覆盖旧 record、Briefs 清零--spec §5.1「被覆盖」。`Found=false` 不写--无 adopted run。）

- [ ] **Step 4: Run tests**

```bash
go test ./internal/service/ -run TestPriorBrief -v
go test ./internal/service/ ./internal/memorygraph/ ./internal/daemon/
```

Expected: PASS，三包全绿。

- [ ] **Step 5: Commit**：`feat(service): wire per-channel continuation - prior record, dedup compression, tentative injection`

---

## 收尾：全量回归与观测就绪

- [ ] `go build ./...` + `go test ./...`（DB 依赖的既有失败按 Phase 1 报告口径记录，不算回归；已知无关失败：workgraph 的 `issue_decompose_child` 缺表与 cleanup deadlock、共享库 migration 系列失败）。
- [ ] 观测面确认：query log 新增 `prior_used` 字段落盘；`prior brief cache hit` / `prior brief compressed`（含 ms、transcript_msgs、brief_nodes）/ `prior compression failed` 日志可检索。
- [ ] D9 回退判据就绪：上线后对比 `prior_used=true/false` 两组的 rounds 分布、found rate 与压缩延迟，净开销不降或 found rate 恶化则回退本特性（保留 Phase 1）。

## Self-Review（已执行）

- **Spec 覆盖**：§5.1 prior record -> Task 3+5f；§5.2 压缩 -> Task 2+5d；§5.3 跨 agent+GraphView -> Task 4a+5d ownerKey；§5.4 注入与无条件附着 -> Task 4（无门控，只要 version 匹配即注入）；§5.5 失效+去重 -> Task 5（版本比较 + exact-match + singleflight）；§5.6 预算不动 + 遥测 -> 全计划无 MaxRounds 改动 + Task 5e/收尾。
- **上游事实核实**：`TraceDrain` nil-recorder 仍缓冲（捕获不依赖 traces）；`Messages()` 一次性信道需幂等化（Task 1 前置）；成功 run 必须真实 /submit（测试复用 `traceFakeBackend`）；store fixture 双节点 + TopK=10 使 nil-seeds 回退测试不可行（Task 4 改用显式 seeds + prompt 断言）；`viewActive`/`nodeForDoc` 放行规则与 `AllowsNodeID` 逐字等价；plan 身份字段齐备；Execute 已有 backend 可复用于压缩。
- **占位符扫描**：全部代码步骤含完整代码，无 TODO/占位符。
- **类型一致性**：`TraceMessage`/`PriorBrief`/`PriorRecord`/`ExploreWithPrior`/`AllowsNodeID`/`priorBrief` 的签名在定义与消费任务间一致；`NormalizeRecallKey` 与 daemon 侧 `normalizeGraphRecallKey` 语义相同（D11 同一规范化规则）。
