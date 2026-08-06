# 贝克汉姆（Beckham）群管理 Agent — 实施计划

> 依赖已确认的设计：`docs/superpowers/specs/2026-07-14-beckham-group-manager-design.md`。
> **开工前置**：需先确认 spec §3 的 D1–D6（尤其 D2 角色标记形态、D3 切绑定）。本计划按推荐方案（D1 单例、D2(b) 新绑定表、D3 切绑定）编写；若改选，Phase 0/1 相应调整。
> 基线分支：`origin/dev`（含 ambient 硬化 PR #570）。每个 Phase 一个独立 PR。

## Phase 0 — 角色骨架（不改运行时行为）

分支 `feat/beckham-role-skeleton`。

- [ ] 迁移：`workspace_group_manager_state`(workspace_id PK, group_manager_agent_id, enabled, timestamps)（D2(b)）。
- [ ] sqlc：`UpsertGroupManagerState` / `GetGroupManagerAgentID` / `ClearGroupManagerState`。
- [ ] `EnsureGroupManager(ctx, workspaceID)`：创建/复用受管贝克汉姆 agent（镜像 `EnsureWindy` 的供给：runtime、visibility、display_name="贝克汉姆"），写入绑定表。
- [ ] 单测：EnsureGroupManager 幂等；GetGroupManagerAgentID 返回绑定。
- [ ] 验证：`go vet` + 相关包 `go test`；`go build ./...`。

产出：能创建/识别贝克汉姆，但无人调用——零行为变化。

## Phase 1 — 自动加入 + 回填 + 授权改判

分支 `feat/beckham-autojoin-backfill`。

- [ ] 建 group 钩子：group 创建后 `EnsureGroupManager` 并把贝克汉姆加为 agent 成员（respect 后续人工移除）。
- [ ] 回填迁移/启动任务：每工作区 EnsureGroupManager；加入所有现有 group；把监督绑定从 Wendy 切到贝克汉姆（写 `workspace_group_manager_state`；若保留 `workspace_radar_state` 则改指贝克汉姆——按 D3 最终形态）。
- [ ] `workspace_radar_task_is_authorized`：supervisor 来源改判群管理绑定；**保留** ambient event(`wendy_ambient:%`) 分支与频道成员校验，仅替换绑定判定来源。带迁移测试（ambient event 任务仍被授权）。
- [ ] 测试：建 group → 贝克汉姆成为成员；回填后现有 group 含贝克汉姆；授权函数对贝克汉姆 ambient/handoff 放行、对旧 Wendy 绑定不再必需。
- [ ] 验证同上。

产出：贝克汉姆已就位并被授权，但主动能力仍由 Wendy 解析触发（下阶段切）。

## Phase 2 — 迁移主动能力到贝克汉姆

分支 `feat/beckham-owns-proactive`。

- [ ] ambient 解析：`resolveWendyAmbientAgentForChannel` → `resolveGroupManagerAgentForChannel`（按角色绑定，非名字）。`ingestWendyHumanGroupMessage`/`ingestWendyAgentGroupMessage` 改用之。
- [ ] handoff dispatch：`DispatchDueWendyHandoffs` 的 supervisor 解析改群管理绑定；发言者=贝克汉姆。
- [ ] prompt 人设：`BuildAmbientChannelPrompt`（及相关 radar prompt）"You are Wendy, the workspace supervisor" → 贝克汉姆群管理人设。
- [ ] 复用 PR #570 的 ambient 生命周期（max-wait 上限 / running→reconcile / stale reclaim），无需重写。
- [ ] 测试（回放式，复用 handler harness）：
  - 建 group + 消息 → 贝克汉姆产生「主动发现」；Wendy 群内 0 主动消息。
  - issue 依赖解锁 → 贝克汉姆 @ 对应 agent。
  - 忙碌群 max-wait 触发仍成立（继承 #1）。
- [ ] 验证同上。

产出：群内所有主动行为由贝克汉姆产生。

## Phase 3 — Wendy 收敛为 HR

分支 `feat/wendy-hr-only`。

- [ ] `windyInstructions`：删除"监督群聊/主动发现/换手"表述，明确 Wendy 仅私聊 HR（组队、招聘、人事），群里被动。
- [ ] 移除/关闭 Wendy 的群 auto-watch 残留路径（若 Phase 2 未全摘）。
- [ ] 贝克汉姆 persona 文档化（群管理：协调/监控/主动 @/不做具体活）。
- [ ] 回归：Wendy 群内静默、私聊 HR 正常；贝克汉姆全面接管。
- [ ] 验证同上。

## 跨阶段验证

- 每阶段：`go vet ./internal/...`、相关包 `go test`（用干净的 `multica_test` DB：`DATABASE_URL=postgres://multica:multica@localhost:5432/multica_test?sslmode=disable`）、`go build ./...`。
- 上线顺序建议：Phase 1 与 Phase 2 紧邻发布，缩短"双督导"窗口（见 spec §7）。

## 覆盖对照

| 需求 | Phase |
|------|-------|
| 建群默认加入群管理 agent | 1 |
| 主动发现/主动交流归贝克汉姆 | 2 |
| 换手/协调归贝克汉姆 | 2 |
| Wendy 回归 HR | 3 |
| 角色标记取代名字识别（根治 #3） | 0–2 |
| 存量工作区回填 | 1 |
