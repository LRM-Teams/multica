# Graph Memory Agent Mode 实施计划

> 本计划直接执行，不使用子智能体。每个slice严格red → green → refactor；不创建commit，除非用户另行明确要求。

- 日期：2026-08-25
- Spec：`docs/superpowers/specs/2026-08-25-graph-memory-agent-mode-spec.zh-CN.md`
- Goal：交付Graph Memory `agent` mode非TTT首版，包括配置、受管频道智能体、PostgreSQL状态机、五个Graph tools、Pi directed steering、lease/quota、citation、恢复和完整UI。
- Architecture：以`GraphMemoryAgentControlPlane`深模块为单一状态机边界；handler负责HTTP/auth，daemon负责resident执行，memorygraph负责Graph读取，PostgreSQL负责identity/state/ledger，现有channel completion负责公开输出。

## 全局约束

- 保持`memory_type=legacy|graph`，新增`graph_memory_mode=inject|agent`；有效Agent mode绝不fallback inject。
- 只支持group channel和Pi resident directed steering；首版K=1。
- 所有数据库迁移双向；当前checkout最新migration是447，新迁移从448开始。运行`make sqlc`更新generated code，禁止手改generated SQL。
- API wire保持snake_case，daemon/computer JSON新字段保持camelCase，TS内部camelCase。
- 代码注释只用英文；不新增dependency。
- 不修改当前用户未提交的continuation spec/plan、`handoff.md`及`multi-agent-collaboration-self-evolution-spec.zh-CN.md`。

## Phase 1：配置、Schema 与 Provisioning

### Slice 1.1 — Profile模式、TTT拆分和Channel override

**Test first**

1. 在`server/internal/handler/graph_memory_profile_test.go`新增handler+test DB用例：旧row/default返回`agent`；update拒绝非法mode；`recall_ttt_enabled`和`consolidation_ttt_enabled`独立持久化；CAS冲突不覆盖。
2. 在`server/internal/handler/channel_test.go`新增频道override权限、`inherit|inject|agent`校验和effective mode precedence用例。
3. 在`packages/core` schema测试新增旧API response缺省解析、snake_case→camelCase以及mutation payload测试。
4. 在`packages/views/evolution/components/graph-memory-cards.test.tsx`新增Workspace mode/runtime/model/quota和split TTT交互测试；新建`channel-graph-memory-settings.test.tsx`。
5. 运行目标测试确认编译/断言失败。

**Implement**

- 新建`server/migrations/451_graph_memory_agent_mode.up.sql/.down.sql`：
  - 扩展`graph_memory_profile`：mode、runtime/model/thinking、recall/consolidation TTT、idle/node/token/turn limits。
  - 为channel添加override，默认inherit。
  - 新增managed channel identity/status/state核心表及必要enum/check/index；既有Graph profile迁移为agent。
- 更新`server/pkg/db/queries/graph_memory.sql`及channel/agent queries；运行`make sqlc`。
- 更新`graph_memory_profile.go` response/request/validation；新增effective mode resolver。
- 更新`packages/core/types/evolution.ts`、`api/schemas.ts`、`api/client.ts`。
- 更新Graph Memory cards并新建channel override card；英文和中文locale按术语表同步。

**Validate**

```bash
make sqlc
cd server && go test ./internal/handler -run 'Test.*GraphMemory(Profile|Mode|Override)'
pnpm --filter @multica/core test -- graph-memory
pnpm --filter @multica/views exec vitest run packages/views/evolution/components/graph-memory-cards.test.tsx packages/views/channels/components/channel-graph-memory-settings.test.tsx
```

### Slice 1.2 — Provisioning reconciler与blocked/recovered

**Test first**

- 在`server/internal/service/graph_memory_agent_control_test.go`通过公开interface验证：group channel一Agent、DM忽略、stable handle、可共享runtime、无Pi/capability时blocked、partial success、幂等reconcile、停用移出membership但保留state、重新启用cursor跳到max seq。
- handler测试有效Agent mode下普通remove member返回冲突。
- channel delivery测试blocked directed `@`产生明确system failure；blocked/recovered状态消息按transition去重。

**Implement**

- 新建`server/internal/service/graph_memory_agent_control.go`及Postgres adapter，定义`ReconcileWorkspace/Channel`、`ObserveActivity`、`ResetState`。
- 在`server/internal/handler/graph_memory_profile.go`、channel create/update/delete、owner transfer后触发事务外reconcile。
- 沿用Agent/AgentRuntime实体创建`managed_role=graph_memory_channel`成员；生成delegated capability记录而非复制owner credential。
- 新增status/reset API和router wiring；owner transfer轮换delegation并短暂停止新run。

**Validate**

```bash
cd server && go test ./internal/service -run TestGraphMemoryAgent
cd server && go test ./internal/handler -run 'Test.*GraphMemoryAgent|Test.*ManagedMember'
```

## Phase 2：运行时、Tools、State 与 Steering

### Slice 2.1 — Run/trajectory/state数据库状态机

**Test first**

- Control-plane interface测试：单active writer、lease renewal/filter、120秒idle、cursor只在成功提交后推进、ambient/directed failure差异、第一target占有、跨target排队、token/node/time quota暂停、reset fence、version change revalidation。

**Implement**

- 扩展448迁移或新增449（若Phase 1已落地）：run、trajectory、steering event、tool operation、checkpoint/submission、candidate/viewed node、target queue、hour token bucket和audit ledger表。
- 新建`graph_memory_agent_state.go`、`graph_memory_agent_scheduler.go`、`graph_memory_agent_store.go`；所有mutation带request id+run/turn fencing。
- 在canonical channel delivery和agent-work lifecycle事件中调用`ObserveActivity`；排除self/reaction/sticker/system noise。

### Slice 2.2 — Native Graph tools

**Test first**

- 在`server/internal/memorygraph/explore_tools_test.go`或新`graph_memory_agent_tools_test.go`跨HTTP/module接口测试start、explore、redirect、submit、checkpoint。
- 断言Agent传入的workspace/channel/version被忽略；不可见node fail-closed；edge direction/type投影和20条cursor分页；每call 4 nodes；submit只接受trajectory实际viewed nodes；重复idempotency key返回同结果。

**Implement**

- 重构`explore_tools.go`，生产mux只暴露五个native动作；删除dead `view/expand` handler和prompt引用。
- 将`Explorer`的seed/hydrate/GraphView逻辑抽为server-authoritative service；start固定route/view/version，redirect保持trajectory/version。
- submit进入现有query log/Dive/Judge/reward并生成pending citation snapshot；checkpoint只持久state不Judge。

**Validate**

```bash
cd server && go test ./internal/memorygraph -run 'TestMemoryGraph(Start|Explore|Redirect|Submit|Checkpoint)'
cd server && go test ./internal/service -run TestGraphMemoryAgent
```

### Slice 2.3 — Pi resident directed steering和daemon scheduler

**Test first**

- `server/pkg/agent/pi_rpc_test.go`：新增完整directed input使用`streamingBehavior:"steer"`，保留现有content-free pending notice行为。
- `server/internal/daemon/agent_runtime_pool_test.go`：busy Memory Agent在safe boundary接受同turn steering；stale turn fence被拒；不同target排队不注入当前turn。
- scheduler测试：bounded initial context优先级、thread回复位置、checkpoint滚动新turn、不无限resume transcript、明确miss有输出。

**Implement**

- 在`pkg/agent/agent.go`定义`ResidentDirectedMessageInput`和capability interface；包含run/turn ID、canonical message、parts、actor、target和bounded context。
- 在`pi_rpc.go`实现native steer；其他resident backend返回unsupported。
- 在runtime pool新增fenced delivery方法；在channel mention调度中识别managed Memory Agent并调用control plane，而不是普通pending notice。
- 新建daemon Memory Agent loop：claim lease → compose约16k context/state →运行resident turn →持久token/cost/tool事件 →terminal/checkpoint →按activity决定下一turn。
- Prompt明确：自主发布门槛、directed必须回复、无方向不随机探索、citation只来自submit。

**Validate**

```bash
cd server && go test ./pkg/agent -run 'TestPiRPC.*(Directed|Pending)'
cd server && go test ./internal/daemon -run TestGraphMemoryAgent
```

## Phase 3：Citation、UI 与 Recovery

### Slice 3.1 — Citation与derivative ingest guard

**Test first**

- handler/channel message integration测试：submitted node生成immutable citation；未viewed node拒绝；历史snapshot不随current node变化；Memory AgentGraph复述标记derivative且ingest过滤；其他参与者回复仍正常ingest。
- views测试citation badge、popover历史snapshot及可选current version loading/error。

**Implement**

- 新增citation/message join schema和query；snapshot包含node/version/metadata/title/first paragraph/excerpt≤2000/hash/time。
- channel completion支持structured citation和`graph_memory_derivative` metadata。
- Graph ingest入口默认过滤derivative内容。
- core schema/client和views message renderer展示citation。

### Slice 3.2 — 状态、blocked/recovery与reset UI

**Test first**

- Workspace card显示provisioning/active/blocked channel counts和原因；channel settings显示effective mode/override/Memory Agent status；reset需要确认且保留public artifacts；retry/recovered UI更新。

**Implement**

- 新增profile/channel status query endpoints与reset mutation。
- 更新Graph Memory cards、channel settings/member row、status badges和locale。
- reset将active run标记`state_reset`、fence旧写入、清除semantic state、cursor置max seq，保留messages/citations/audit。

## End-to-End 与回归验证

### E2E场景

1. Workspace从inject切到Agent：新inject停止；两个group channel分别active/blocked；active出现managed member。
2. ambient消息启动lease，start/explore/checkpoint后继续；idle 120秒停止。
3. active explore期间`@Memory`：safe-boundary steering，trajectory/version不变，pre-steering结果保留。
4. submit后普通completion发布并带snapshot citation；derivative消息不ingest。
5. runtime恢复后blocked→active只发一次recovered消息；cursor跳过inactive backlog。
6. Agent→inject：Agent inactive且membership移除，state保留；inject continuation独立。

### Validation commands

```bash
make sqlc
cd server && go test ./internal/memorygraph ./internal/service ./internal/handler ./internal/daemon ./pkg/agent
cd server && go test ./...
pnpm --filter @multica/core test
pnpm --filter @multica/views exec vitest run
pnpm typecheck
pnpm lint
make test
```

若full suite因仓库既有失败无法通过，记录精确命令、失败文件以及为何与本变更无关；所有目标测试必须green。

## Review Checklist

- [ ] Agent-supplied scope/version无信任路径。
- [ ] 模式互斥且无blocked→inject fallback。
- [ ] 每频道单active run和所有写入fencing成立。
- [ ] Directed steering保留same trajectory/version，stale input不能落入later turn。
- [ ] Citation只来自当前trajectory validated submissions。
- [ ] Memory Agent derivative ingest guard生效。
- [ ] 状态切换、retry、reset和owner transfer幂等可审计。
- [ ] UI覆盖配置、effective status、blocked/recovery、citation和reset。
- [ ] Agent mode K固定1，inject/consolidation TTT互不干扰。
- [ ] 未触碰用户已有未提交文件；无commit。
