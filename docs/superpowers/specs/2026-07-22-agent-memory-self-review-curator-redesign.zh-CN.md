# Agent 记忆自审与团队 Curator 闭环改造 Spec

- 创建时间：2026-07-22 06:20 UTC
- 更新时间：2026-07-22 06:20 UTC
- 状态：Draft
- 目标分支：`dev`
- 关联场景：Evolution Center 记忆策展、自审、团队记忆晋升、项目/群 scoped memory、agent daily 写入

## 1. 背景

Multica 现在已经有 agent 本地记忆根目录、daily、REVIEW、项目记忆、用户记忆、团队 curator profile 和定时 memory curation。当前行为能跑通一部分整理流程，但语义上还不够准确。

理想语义应该是：

1. 每个 agent 先审自己的记忆：`memory/`、`daily/`、`REVIEW.md`、`sync_queue/`、项目/用户/群 scoped 文件。
2. 每个 agent 产出自己的本地候选：用户偏好、关系、项目经验、项目状态、通用经验、待审核候选。
3. 团队 curator agent（例如 a-kun）再读取大家产出的候选，做去重、合并、冲突检查、团队经验/共享 skill 凝练。
4. 最后按 profile 模式进入人工 review 或 `auto_safe` 晋升。

当前实现存在偏差：

- `agent_self_review` 阶段由一个 selected curator agent/runtime 代跑所有 target agent 的 self-review prompt。
- UI 上显示的 curator agent（例如 a-kun）容易被理解为“a-kun 在认领策展”，但实际上 self-review 也由它代理执行。
- 如果 selected curator agent 卡住或超时，所有 target agent 的自审都无法产生真实 per-agent 结果。
- 这和“每个 agent 自己醒来审自己，再由 a-kun 团队策展”的用户心智不一致。

这个 Spec 的目标是把记忆系统补成完整闭环：热路径稳定写 daily，agent 自审负责本地提炼，团队 curator 负责跨 agent 治理和共享晋升。

## 2. 当前实现简述

当前核心链路：

1. `StartMemoryCurationRun` 创建一条 `memory_curation_run`。
2. run 里保存：
   - `runtime_id`
   - `curator_agent_id`
   - `target_agent_ids`
   - `stage`
   - `curator_mode`
3. daemon claim 这条 run。
4. daemon 根据 `curator_agent_id` 构造 `AgentStageRunner`。
5. `Engine.Run` 遍历 target agent roots。
6. `runAgentSelfReview` 对每个 target root 调用同一个 `StageAgentRunner`。
7. `team_curation` 也由同一个 runner 读取所有候选并产出团队结果。

问题不在于 target agent roots 没有被遍历；问题在于执行 self-review 的 agent identity/runtime 是同一个 curator，而不是目标 agent 自己。

## 3. 设计目标

### 3.1 产品目标

- 每个 agent 都能形成自己的记忆闭环。
- daily 成为默认热路径流水层。
- 自审从 daily/REVIEW/scoped files 中提炼长期记忆。
- 团队 curator 只处理候选，不代替每个 agent 做自我整理。
- 项目群的项目记忆不混；不同 project/channel scope 保持隔离。
- 人类反馈被高优先级处理。
- 页面上能看清：谁自审、谁团队策展、谁失败、哪个 agent 没写 daily。

### 3.2 工程目标

- 不把 heavy self-review 放在用户响应热路径。
- 不因一个 curator agent 卡住导致所有 agent 自审完全失败。
- 每个 self-review 子任务可独立重试、超时、记录结果。
- team curation 只在必要 target self-review 完成后运行。
- 兼容现有 `memory_curation_run` 和 Evolution Center 页面，逐步迁移。

## 4. 术语

| 术语 | 含义 |
|---|---|
| 热路径 | agent 正在响应用户/任务的当轮执行 |
| Daily | `memory/daily/YYYY-MM-DD.md`，实质工作流水、过程记录、小经验、人类反馈原料 |
| Self-review | 每个 agent 对自己的 daily/REVIEW/scoped files 做整理 |
| Team curator | 团队策展 agent，例如 a-kun，读取多个 agent 候选并做治理 |
| Candidate | self-review 产出的本地候选，可能进入 REVIEW、proposal 或共享审核 |
| Scope | 记忆适用范围：agent/user/project/channel/workspace/team |
| Scoped files | `users/<id>/`、`projects/<id>/`、`channels/<id>/` 下的记忆文件 |

## 5. 目标行为

### 5.1 agent 热路径写入

当 agent 完成实质工作时：

- 默认 append 几行到 `memory/daily/YYYY-MM-DD.md`。
- 明确用户偏好写 `users/<member-id>/USER.md`。
- 人与 agent 的长期协作方式写 `users/<member-id>/RELATIONSHIP.md`。
- 项目经验写 `projects/<project-id>/MEMORY.md`。
- 项目当前状态/阻塞写 `projects/<project-id>/STATE.md`。
- 项目决策写 `projects/<project-id>/DECISIONS.md`。
- 群规则写 `channels/<channel-id>/CONTEXT.md`。
- 不确定、冲突、敏感或不知道放哪的写 `memory/REVIEW.md`。

琐碎经验先进 daily；只有重要、长期、跨项目适用的才进 agent 通用 `memory/MEMORY.md`。

### 5.2 agent self-review

每个 target agent 自己执行 self-review，输入是自己的本地文件和 DB evidence：

- `memory/daily/YYYY-MM-DD.md`
- `memory/REVIEW.md`
- `memory/MEMORY.md`
- `memory/STATE.md`
- `users/<member-id>/USER.md`
- `users/<member-id>/RELATIONSHIP.md`
- `projects/<project-id>/MEMORY.md`
- `projects/<project-id>/STATE.md`
- `projects/<project-id>/DECISIONS.md`
- `channels/<channel-id>/CONTEXT.md`
- `sync_queue/*.jsonl`

self-review 输出：

```json
{
  "agent_id": "...",
  "summary": "...",
  "local_writes": [
    {
      "scope_type": "agent|user|project|channel",
      "scope_id": "...",
      "file": "memory/MEMORY.md|users/<id>/USER.md|projects/<id>/MEMORY.md|...",
      "action": "append|replace|dedupe",
      "reason": "..."
    }
  ],
  "candidates": [
    {
      "candidate_id": "...",
      "type": "memory|preference|relationship|project_fact|project_state|decision|skill|conflict",
      "scope_type": "agent|user|project|channel|workspace|team",
      "scope_id": "...",
      "title": "...",
      "content": "...",
      "confidence": 0.0,
      "sensitivity": "none|unknown|sensitive",
      "evidence_refs": ["kind:id"],
      "applies": {
        "project_ids": [],
        "channel_ids": [],
        "task_types": [],
        "expires_at": "RFC3339"
      }
    }
  ]
}
```

原则：

- daily 是主要原料。
- 人类纠正、返工、确认、不满意是高优先级证据。
- speaker 是 provenance，不自动决定 scope。
- 项目事实保留 project scope，不晋升为 workspace/team。
- user preference 不自动扩大为 team rule。
- 不猜稳定 ID。

### 5.3 team curation

团队 curator（例如 a-kun）只读取 self-review 产出的候选和必要 scoped context。

职责：

- 去重。
- 合并同义候选。
- 检查冲突。
- 保留/修正 scope。
- 凝练团队经验。
- 生成共享 skill draft。
- 将不确定内容送人工 review。
- 按 `auto_safe` 阈值晋升安全候选。

team curator 不应该：

- 代替每个 agent 读完整 daily 并自审。
- 把项目 A 的规则晋升成所有项目通用。
- 把私人偏好晋升成团队制度。
- 为了 fanout 普通群消息而创建共享记忆。

## 6. 改造方案

### 6.1 数据模型：引入 per-agent 子 run

新增表或复用 JSON 字段，推荐新增表：

```sql
CREATE TABLE memory_curation_agent_run (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  parent_run_id UUID NOT NULL REFERENCES memory_curation_run(id) ON DELETE CASCADE,
  workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
  agent_id UUID NOT NULL REFERENCES agent(id) ON DELETE CASCADE,
  runtime_id UUID REFERENCES agent_runtime(id) ON DELETE SET NULL,
  stage TEXT NOT NULL CHECK (stage IN ('agent_self_review')),
  status TEXT NOT NULL DEFAULT 'queued'
    CHECK (status IN ('queued','waiting_runtime','running','succeeded','failed','cancelled','skipped')),
  date_from DATE,
  date_to DATE,
  claim_token UUID,
  attempt INT NOT NULL DEFAULT 0,
  started_at TIMESTAMPTZ,
  finished_at TIMESTAMPTZ,
  error TEXT NOT NULL DEFAULT '',
  stats JSONB NOT NULL DEFAULT '{}'::jsonb,
  output JSONB NOT NULL DEFAULT '{}'::jsonb,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
```

`memory_curation_run` 保留为阶段级父 run：

- `agent_self_review` 父 run 管理 N 个 agent 子 run。
- `team_curation` 父 run 仍由 curator agent 执行。
- `all` 阶段先创建 self-review 子 run，再在满足条件后创建/执行 team curation。

### 6.2 调度：target agent 自己 claim 自己的 self-review

当前：一个 runtime claim 父 run，然后 curator 代审所有 agent。

改造后：

1. scheduler 创建 parent run。
2. server 根据 `target_agent_ids` 创建 N 条 `memory_curation_agent_run`。
3. 每个 online runtime heartbeat 时，server 分配它可执行的 agent 子 run。
4. daemon 以目标 agent 的 identity/root 执行 self-review。
5. 子 run 独立成功/失败。
6. parent run 聚合子 run 状态。

claim 规则：

- agent self-review 子 run 应优先由该 agent 所属 runtime claim。
- 如果一个 agent 有多个 runtime，可以选最近在线、provider 可用、负载较低的 runtime。
- 若 agent 无在线 runtime，子 run 进入 `waiting_runtime`。
- 可设置 fallback：超过一定时间可由 curator 代理审，但 UI 必须标注 `delegated_by_curator`，不能伪装成 self-review。

### 6.3 daemon：区分 self-review runner 和 curator runner

新增 handler：

```text
handleMemorySelfReviewAgentRun
```

执行时：

- `AgentStageRunner.CuratorAgentID` 应设置为当前 target agent id 或新增 `RunnerAgentID`。
- `Cwd` 使用 target agent root。
- prompt 明确：“你正在审自己的记忆”。
- 只允许写自己的 root。

team curation 仍使用：

```text
handleMemoryCuration
```

但仅消费 self-review 候选，不代跑 self-review。

### 6.4 阶段依赖

`all` 阶段：

```text
parent run: all
  -> create self-review child runs for each target agent
  -> wait until child runs finished / skipped / failed with threshold
  -> create team_curation run if enough self-review inputs exist
```

team curation 启动条件：

- 所有可运行 target agent 子 run succeeded/skipped；或
- 达到超时，且至少有部分 succeeded；或
- 手动 force。

如果所有 self-review 都失败，team curation 应明确 skipped/failed：

```text
team curation skipped: no successful self-review candidates
```

而不是笼统 `one or more agents failed`。

## 7. Closeout Guard 与 Daily 闭环

### 7.1 closeout guard 不是 agent

closeout guard 是 daemon 中的 Go 逻辑，不调用 LLM，不启动 Cursor/Pi/Codex。

任务 completed 后：

1. 判断是否有实质工作信号。
2. 检查今天 daily 是否在本轮变化。
3. 如果实质工作且 daily 没变，append 一条保守 auto closeout stub。
4. 上报 memory write event。

### 7.2 实质工作判断

采用白名单强信号，不枚举所有寒暄。

默认不补；只有强信号才补。

强信号包括：

- issue task 完成。
- result 有 branch/action/PR/comment。
- 运行时长超过阈值。
- 有明显工具调用/平台操作统计。
- 有 memory 文件写入但没有 daily。
- 有人类明确反馈或纠正。

弱消息如 `hi`、`hello`、`谢谢`、贴纸、纯寒暄，因没有强信号而跳过。

### 7.3 memory write telemetry 补齐

当前需要纳入写入统计的文件：

- `memory/daily/*.md` -> `agent_daily / DAILY`
- `users/*/RELATIONSHIP.md` -> `user / RELATIONSHIP`
- `projects/*/STATE.md` -> `project / STATE`
- `notes/agents.md` -> `agent_notes / AGENTS`
- `notes/relationship-map.md` -> `agent_notes / RELATIONSHIP_MAP`
- `notes/work-log.md` -> `agent_notes / WORK_LOG`

已有继续保留：

- `memory/MEMORY.md`
- `memory/STATE.md`
- `users/*/USER.md`
- `channels/*/CONTEXT.md`
- `projects/*/MEMORY.md`
- `projects/*/DECISIONS.md`

## 8. UI 改造

Evolution Center 应区分：

### 8.1 父 run 信息

- 阶段：agent self-review / team curation / all
- 运行时：谁 claim 父任务
- 团队 curator：a-kun
- target agents：列表
- 成功/失败/跳过计数

### 8.2 agent self-review 子 run 列表

每个 target agent 一行：

- agent 名称
- runtime
- status
- attempt
- duration
- daily writes
- candidates produced
- error

### 8.3 team curation 信息

- curator agent
- input candidate count
- promoted count
- merged count
- conflict count
- skipped reason

### 8.4 记忆健康度

每个 agent 显示：

- 今日 daily 写入数
- USER 写入数
- RELATIONSHIP 写入数
- project 写入数
- channel 写入数
- auto closeout 次数
- 最近一次 self-review 时间
- 最近一次 curator 晋升时间

## 9. 兼容与迁移

### 9.1 第一阶段兼容

保留现有 parent `memory_curation_run` API。

新增子 run 后：

- 老 UI 仍可看 parent run status。
- 新 UI 可展开 child runs。
- 老的 `stats.agent_results` 仍可从 child runs 聚合生成。

### 9.2 Fallback 策略

为了不一次性破坏现有策展：

- 默认启用 true self-review。
- 如果 target agent runtime 不在线，可标记 `waiting_runtime`。
- 可提供 profile 配置：
  - `self_review_execution = own_agent | curator_delegated | hybrid`
- `curator_delegated` 作为兼容/应急模式，但 UI 必须明确展示“curator delegated review”。

## 10. 分阶段实施计划

### Phase 1：Daily 和写入可观测闭环

目标：先知道 agent 到底有没有写。

- 扩展 memory write diff 白名单。
- 增加 daily closeout guard。
- 写入事件聚合增加 daily/relationship/project state。
- 测试覆盖 greeting 不写、实质任务写、已有 daily 不重复写。

### Phase 2：per-agent self-review 子 run

目标：self-review 真正按 agent 拆分。

- 新增 `memory_curation_agent_run`。
- scheduler/backfill 创建子 run。
- daemon claim agent 自己的 self-review。
- parent run 聚合状态。
- UI 展示 per-agent 进度。

### Phase 3：team curator 消费候选

目标：a-kun 只做团队治理。

- team curation 只读取 self-review candidate/proposal。
- 不再代跑 target roots self-review。
- 保留 project/channel/user scope。
- 加冲突和去重测试。

### Phase 4：Memory Write CLI/API

目标：降低 agent 拼路径错误。

- `multica memory write --scope daily|user|project|channel|agent|review ...`
- 根据 env 自动定位 scoped dir。
- 服务端/daemon 记录审计。

### Phase 5：UI 记忆健康度

目标：用户能看到每个 agent 学得怎么样。

- 写入统计卡片。
- self-review 子 run 列表。
- curator 晋升详情。
- scope 分布和冲突展示。

## 11. 验收标准

### 11.1 自审语义

- 配置 5 个 target agents 后，会产生 5 条 self-review 子 run。
- 每条子 run 使用对应 target agent root。
- a-kun 不再代跑每个 agent 的 self-review，除非显式 fallback。
- 一个 agent self-review 失败不阻断其他 agent self-review。

### 11.2 daily 闭环

- 实质任务完成且 agent 没写 daily 时，daemon 自动 append auto closeout。
- `hi` / 纯感谢 / 贴纸不写 daily。
- agent 手动写过 daily 时，daemon 不重复写。
- daily 写入能出现在 memory write event 中。

### 11.3 scope 正确性

- Multica 项目群产生的项目经验进入 Multica project scope。
- River 项目群产生的项目经验进入 River project scope。
- 单聊默认不加载/写项目 scope，除非有明确 project context。
- 用户偏好进入 user scope，不自动变 team rule。
- 团队 curator 不把项目规则错误晋升为 workspace/team。

### 11.4 UI 可解释性

- 页面能区分 curator agent 和 target agents。
- 页面能看到每个 agent 的 self-review 成功/失败。
- 页面能看到 team curation 是否因 self-review 失败而 skipped。
- 页面能看到写入健康度。

## 12. 风险与取舍

### 12.1 成本

per-agent self-review 会比 curator 代审更分散，调度复杂度更高。但好处是：

- 语义正确。
- 单点失败减少。
- 每个 agent 更像自己在学习。
- UI 更可解释。

### 12.2 延迟

self-review 是后台冷路径，不应影响用户热路径。

closeout guard 是本地 Go 文件检查，不调用模型，通常毫秒级。

### 12.3 兼容

短期保留 curator delegated fallback，避免部分 agent runtime 离线时完全无法策展。

### 12.4 噪音

daily closeout 必须默认 skip，只有强实质工作信号才写。宁可漏写，不要污染 daily。

## 13. 一句话总结

目标不是让 a-kun 代所有 agent 看记忆，而是让每个 agent 先整理自己的工作日记和本地记忆；a-kun 再像团队图书管理员一样审核大家交上来的候选，做去重、冲突检查、团队经验和共享 skill 凝练。daily 是热路径原料，自审是本地提炼，curator 是团队治理，三者互补而不是互相替代。

## 14. 实现清单

### 14.1 记忆写入闭环

- [ ] 扩展 `server/internal/daemon/memory_write.go` 的白名单，让 `memory/daily/*.md`、`users/*/RELATIONSHIP.md`、`projects/*/STATE.md`、`notes/agents.md`、`notes/relationship-map.md`、`notes/work-log.md` 都进入写入 diff / telemetry。
- [ ] 在 `server/internal/daemon/daemon.go` 的 task result closeout 路径增加轻量 guard：只对“实质工作”补 daily stub，`hi/hello/谢谢/贴纸` 直接跳过。
- [ ] 在 `server/internal/daemon/execenv/runtime_config.go` 继续强化 Memory Operating Guide，明确 daily 先写、长期经验再晋升、project scope 与 user scope 不能串。
- [ ] 为 daily closeout、user preference、relationship、project state 增加幂等写入测试。

### 14.2 自审/团队策展语义

- [ ] 把 `agent_self_review` 拆成 per-agent 子 run 或等价执行单元，确保每个 target agent 用自己的 root / identity 自审。
- [ ] 保留 `memory_curation_run` 作为父 run，新增子 run 记录每个 agent 的自审状态、结果和错误。
- [ ] 让 `team_curation` 只消费 self-review 产出的候选，不再代跑所有 target agent 的 self-review。
- [ ] 在 `server/internal/memorycuration/l3_reviewer.go` 和 `server/internal/memorycuration/engine.go` 中保留 scope / applies 信息，避免项目/群/用户内容被错误晋升。

### 14.3 数据与接口

- [ ] 为 per-agent self-review 增加子 run schema 或等价字段，至少包含 `parent_run_id`、`agent_id`、`runtime_id`、`status`、`output`、`error`、`attempt`、`started_at`、`finished_at`。
- [ ] 更新 `server/internal/handler/memory_curation.go`、`server/internal/handler/memory_curation_daemon.go`、`server/internal/daemon/memory_curation.go` 的调度/claim/执行链路。
- [ ] 保持现有 parent run API 兼容，旧 UI 仍能读取 parent 状态，新 UI 再展开 child runs。

### 14.4 验证与回归

- [ ] 自审：5 个 target agents 时应出现 5 条 self-review 结果；其中单个失败不应拖死其他 agent。
- [ ] curator：a-kun 只做团队治理时，应清楚看到输入候选数、合并数、冲突数、晋升数。
- [ ] daily：实质工作完成但 agent 没写时，应自动补 closeout；纯寒暄不补。
- [ ] scope：Multica / River 等不同项目群的项目记忆不混，单聊默认不加载项目记忆。
- [ ] 可观测：页面和写入事件都能看见 daily / user / relationship / project / channel 的写入计数。
