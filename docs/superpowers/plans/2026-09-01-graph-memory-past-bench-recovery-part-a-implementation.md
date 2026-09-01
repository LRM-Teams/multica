# Graph Memory PAST-Bench 恢复 — Part A 实施记录

- 日期：2026-09-01
- 规格：`docs/superpowers/specs/2026-09-01-graph-memory-past-bench-recovery-spec.zh-CN.md`（Part A = merge-independent 项，落 main 工作区，未提交——规格 §8 不授权 commit/部署）
- 状态：A1/A2 已实现并通过测试；A3（复测）等 worktree 部署，未开始（按规格 §D）

## A1 — recall 404 服务端修复（生产 ~3.2 万次/天 → 目标 ~0）

### 改动文件

| 文件 | 改动 |
|---|---|
| `server/migrations/474_graph_memory_recall_dual_shape_task.{up,down}.sql` | 新增。`task_id` 放宽为双形状（drop 单表 FK）；`task_shape` 记账列（`agent_inbox_event`\|`channel_message`，默认前者）；身份触发器改按 task_shape 在对应表内校验 workspace 归属（runtime/graph-owner 校验不变） |
| `server/internal/service/graph_memory_recall.go` | `Begin` 的任务加载抽为 `resolveRecallTask`：先按 `agent_inbox_event.id` 查，miss 则按 `channel_message.id` 反查（message → channel → workspace 归属校验）。双形状 miss = identity 拒绝。plan 增加 `TaskShape`，INSERT/回放 SELECT 均记账。channel-message 形状：agent 作者是唯一可用 invoking agent（`resolveTrainingMode` 无行默认 offline_capture），issue 恒空、scope 走 channel route |
| `server/internal/handler/graph_memory_recall.go` | 改走 `TryBegin`（每次 skip 带 machine-readable reason 的服务端日志——A1-4 观测面）；executor 空结果补 `graph memory recall empty` 日志（graph miss = 合法空结果，A1-3 语义） |
| `server/internal/service/graph_memory_recall_dual_shape_test.go` | 新增 5 个集成测试（DATABASE_URL 门控） |

### 语义要点（评审关注）

1. **门禁顺序（A1-2）**：mode 门禁在形状解析之后。agent-mode workspace 的 resident 路径请求现在返回 200/`{"status":"disabled"}` 而非 404；legacy memory_type 同理。真 unknown id 仍是 404（不掩盖跨租户探测）。
2. **严格分离（A1-3）**：graph workspace 的 miss 走零注入 + ledger 记录；daemon 侧本就无 graph→legacy 回退（`graphExecutionMemories` 注释明确的合约），本次未引入任何运行时回退。legacy→graph 迁移工具按规格挂 follow-up（§E2）。
3. **观测（A1-4）**：reason 分布 = `TryBegin` 的 `graph memory recall skipped`（identity/disabled/no_scope/conflict/finalized）+ handler 的 `graph memory recall empty`（empty）。成功 recall 的形状在 ledger `task_shape` 列按天聚合。
4. **迁移编号 474 的原因**：worktree `feat/universal-interaction-dag-20260827` 已占用 466-473（其 466 = atom projection）。本仓 migrate 工具按版本号去重，同号双文件会静默跳过一个；474 高于 worktree 现最大值，worktree 后续合并时按序补应用，无冲突。

### 测试证据

- 迁移：隔离库（multica_a1_test@本地 pg17 容器）全新 schema `migrate up` 全量通过（含 474）；`migrate down` 实际回滚 474 成功（task_shape 列移除、FK 恢复），停在无关的 445 不可逆迁移属预期。
- `go test ./internal/service/ ./internal/handler/`（真实 Postgres）：全部通过，含新增 5 测试——双形状解析与 agent 归属、channel-message 形状端到端落 ledger + 幂等回放、inbox 形状记账、门禁顺序（agent-mode/legacy → disabled 非 404）、触发器拒绝伪造形状。

## A2 — driver 改造（areal skill `graph-memory-regression/scripts/gm_official2.py`）

1. **harness gate（A2-1/P0-4，双形态）**：每个 learn/update_or_stabilize episode 后 barrier + 断言。pre-merge：staging 段非空、无 "No trajectory messages were available" 标记、版本推进；post-merge（`to_regclass('graph_memory_atom')` 探测）：episode 窗口内 ≥1 已发布未隔离 atom。失败→`-gateN` 后缀新 client_message_id 重发 ≤2 次→仍失败抛 `HARNESS_GATE_BLOCKED`（族标 error 不进评分）。pre-merge 无写尝试（无 submit/checkpoint）判行为缺失不 BLOCKED（missing-learning cap 评分）。
2. **硬归因（A2-2/P4-15）**：`memory_injection` 信用 = learn 写入 typed refs ∩ eval start seed refs 非空。写入 refs：pre-merge 版本节点差集（`graph_node:<id>`）+ post-merge atoms（`staging_atom:<atom_id>`，排除 quarantined）。seed refs：start 响应 v1 `nodes[].id` / v2 `seeds[].ref`（SQL 单查询双形状，已对真实 pg17 验证五种响应形态）。旧 state 无 seed 捕获回退图非空代理（`injection_basis` 标注）。`state.learn_window` 运行时捕获（anchor 回退前）；regrade 磁盘漂移复用运行时捕获；版本目录不可读标记 `node_diff_unavailable`（不制造假 miss/假 hit）。
3. **口径（A2-3/Q9）**：`--business-graph-search on|off` 配置项进每族 state 与 batch results（B8 上线后 off 臂必须 off）；report 输出 `PRIMARY_METRIC ablation_outcome_delta`（同 channel 协议偏差标注）、`HARD_SIGNAL`、`CAPABILITY_SPLIT`（混合部署分列）、BLOCKED 族单列。
4. **capability（A2-4）**：start 响应 `protocol_generation` → `mech.memory_capability`（v1_node_only/memory_explore_v2），family 众数 + mixed 标记。
5. **零 explore submit（A2-5）**：`mech.zero_explore_submit`——优先读服务端 B3 列（存在性探测），否则本地推导（start_populated ∧ 0 explore ∧ submit）。顺带修复 `start_populated` 从未赋值的存量缺口。

### 测试证据

- `py_compile` + 离线逻辑套件（假 SSH/psql）：归因命中/未命中/代理回退、磁盘漂移复用、不可读标记、双 gate 谓词（真实/空/标记段、版本不推进、atom 0/2/无窗口）、capability 聚合与 mixed、门禁 summary——全过。
- seed 提取 SQL 对本地 pg17 实测：v1 双节点、v2 混合 typed refs、空 nodes、错误响应、非 start op 五形态输出正确（修复过 v2 分支漏 string_agg 的 bug）。
- SKILL.md 已补 A2 改造章节（用法与坑位）。

## 未做（按规格本就不在本轮）

- Part B（B1-B10 worktree amendments）：随 universal pipeline merge 评审，未动 worktree。
- A3/D 复测：等 worktree 部署窗口；冒烟门（PG02/PG03/SM03/PC01）协议已具备（gate + seed 命中断言 + capability 分列）。
- A1 的生产验证（404 速率观测）：部署后按 reason 分布确认。

## 环境备注

- 本地验证用隔离库 `multica_a1_test`（复用既有 `multica-ci8110-pg` 容器，55432 端口，已迁移到 474）；未触碰运行中的 backend/compose 栈。
- Go 工具链装在 `/home/zhoujie22/go`（GOROOT），GOPATH/GOCACHE 隔离在 `~/gopath`。
