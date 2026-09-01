# Handoff：Graph Memory Agent 合并与 CI #8110 调查（已结案）

> 更新时间：2026-08-25 11:20 UTC
> 仓库：`/home/zhoujie22/river2_0/multica`
> 状态：**CI #8110 根因已完全查清并本地验证**。失败与 Graph Memory 合并无关。上游团队已开 PR #3822 处理，但该 PR 经本地验证仍不完整。前向修复已在干净 worktree 准备并验证通过，等用户决定是否提交。

## 1. 最终结论（2026-08-25 11:20 UTC）

1. 非 TTT 的 **Graph Memory Agent mode v1 已实现、验证并合入上游 `dev`**（集成点 `c0acd5cd9`）。
2. **CI #8110（run 32834403516，`03bde6117` push）的失败与 Graph 合并无关**：失败步骤是 backend job 的 `Test`（`go test -p 1`，9 个包），失败测试全部位于 `internal/researchrun` 的 V6 research 契约测试。
3. **根因提交是 `9d23ad851`（PR #3815 "fix(research): restore V6 insight convergence"，le-czs，2026-08-25 09:48 UTC）**，不是 Graph 集成点。
4. 该失败为什么漏网（三层防线全部失效）：
   - PR 描述明说 "local runtime/tests intentionally not run per request; CI is the verification gate"；
   - PR #3815 从创建到合并只有 12 秒，PR CI 没有起到门禁作用；
   - 它触发的 push CI run #8106 被 concurrency 取消（后续 dev push 优先级更高）；
   - 触发 #8110 的 PR #3816 自己的 PR CI run #8109 因 scope 竞争（fetch origin/dev 时 dev 已推进到合并提交，PR diff 变空集）**完全跳过了 backend 测试**，是假绿。
5. 交接文档早前怀疑的 `TestPermanentAgentDeleteNonCascadingFKInventory` 失败是**本地数据库 schema 漂移**，与 CI 无关（见第 7 节）。
6. 上游 `me-frankan` 已开 **PR #3822**（restore frozen V6 director schema）处理同一问题，但方向是"回退 schema、保留实现"；本地验证其仍失败（见第 9 节），其 CI run #8119 也已确认 backend Test 失败。
7. **前向修复**（保留功能、同步契约）已在 `/tmp/multica-ci-8110-forwardfix` 准备完毕并通过验证（见第 10 节）。未提交未推送，等待用户决策。

## 2. CI #8110 失败的确切事实

- Run：<https://github.com/LRM-Teams/multica/actions/runs/32834403516>（backend job id 97760070183）。
- 失败步骤：step 14 `Test`（`cd server && go test -p 1 <packages>`）。Build/vet/migrate/research DB 准备全部成功。
- 实际测试的 package 列表（`./internal/researchrun` 的反向依赖闭包，因该 push 唯一变更文件是 `server/internal/researchrun/director_proposal_preflight_v6_test.go`）：
  ```
  ./cmd/materialize-promoted ./cmd/rescan-suggestions ./cmd/server ./internal/handler
  ./internal/integrations/lark ./internal/researcheval ./internal/researchrun
  ./internal/scheduler ./internal/service
  ```
- 在全新 pgvector/pg17 + redis:7-alpine 容器（与 CI 相同配置）上完整复现，失败测试（全部在 `internal/researchrun`）：
  - `TestRonaldoV6EmbeddedSchemaMatchesCanonicalDocument`：embedded schema 与 `docs/contracts/research-run-v6.schema.json` 漂移。
  - `TestV6DirectorBriefIncludesAtomicResultFrontier`：frontier node id 断言失败（artifact id vs content id）。
  - `TestV6DesignContractIsFrozenAndNotProductionEnabled`：V6 契约哈希 `23b49b97…` ≠ 冻结值 `2ce8b8af…`。
- 其余 8 个包（含 `internal/handler`，95s）全部通过。

## 3. 根因：`9d23ad851` 的三处遗漏

该提交给 V6 `work_manifest` 增加 `task_context`、`input_nodes` 两个字段，并新增 integration.create.v1 动作、讨论/集成提示词、frontier 查询改用 content id 等实现。它同步更新了：
- `docs/contracts/research-run-v6-director.schema.json`（冻结哈希覆盖的文档）
- `server/internal/researchrun/research_run_v6_director.schema.json`（go:embed 内嵌副本）

但漏了三处：
1. `server/internal/researchrun/v6_contract_test.go` 的 `frozenV6ContractSHA256` 常量没有重新钉住。
2. `docs/contracts/research-run-v6.schema.json`（第三份 canonical 副本，`TestRonaldoV6…` 的比对对象）没有同步。
3. `TestV6DirectorBriefIncludesAtomicResultFrontier` 仍断言旧语义（node id = artifact id），而 `loadV6BranchFrontierBrief` 已改为 `content.content_id`（result node / insight version 的内容身份）。

## 4. PR #8109（假绿）机制

PR #3816 的 PR CI run #8109 于 09:53:50 创建，其 backend job 里 `Setup Go`/`Expand Go packages`/`Test` 全部 skipped、只有 "Skip unaffected backend tests" 运行。原因：workflow 在 job 开始时 `git fetch origin dev`，此刻 dev 已被 09:54:00 的合并推送推进到 `03bde6117`；PR checkout（refs/pull/3816/merge）与之 merge-base 即 `0eccae8c0`，`git diff origin/dev...HEAD` 为空 → `go_mode=skip`。同因，交接文档第 9.2 节此前算出 `go_mode=skip` 却与 #8110 实际跑了测试矛盾——那次重算对 push 事件是对的，但 #8110 是 push 事件（`event.before=c0acd5cd9`，diff 非空、scope 为 researchrun 闭包），矛盾本身源于混淆了两种事件的 BASE_REF 语义。

## 5. 提交、分支与远端状态（Graph 部分，仍然有效）

```text
e4b515a8e  feat(graph-memory): add managed memory agent mode
f155ee206  initial merge onto then-current dev
fcc17298f  fix(graph-memory): isolate agent mode on latest dev
0210d134c  sync newer dev
c0acd5cd973f521e812e0b30d4fb3f1aa9fd5641  final Graph integration point（已入 dev）
```

Fork 留档：`ketsuzhou/multica` 分支 `graph-memory-agent-dev-20260825` = `c0acd5cd9`。

上游 dev 已继续推进（2026-08-25 11:00 UTC 前后为 `39733ca9f`，含 PR #3817 notes 功能）；`03bde6117..39733ca9f` 之间 `internal/researchrun` 与 `docs/contracts` 无任何变化，故本 handoff 的失败分析对当前 dev 依然成立。

Graph migration 编号：451/452/453；`450_channel_message_evaluation` 属用户并发 WIP，保持隔离（见第 6 节）。

## 6. 主工作树：必须保护的用户 WIP（不变）

主工作树仍在 `merge/graph-memory-agent-dev-20260825`（`c0acd5cd9`），相对 `lrm/dev` 落后。存在大量未提交的 evaluation/daemon/450 migration WIP 与根目录 `handoff.md`（另一项 TTT 设计文档）。**不要清理、覆盖、暂存或 rebase**；保留 stash@{0..3}（preserve-* 系列）除非用户明确确认。

## 7. 已排除的假线索：本地 FK 库存测试失败

此前在本地库复现的 `TestPermanentAgentDeleteNonCascadingFKInventory`（`issue_derived_agent_assignment_source_agent_id_fkey` 缺 teardown）：

- migration `307_goal_gated_continuous_loop.up.sql` 创建该 FK 时就是 `ON DELETE CASCADE`，仓库中没有任何 migration 把它改为非级联。
- 在全新迁移的 CI 同构库上实测 `confdeltype='c'`（CASCADE），该测试**通过**（handler 整包在 CI 命令下 95s ok）。
- 本地失败是因为本地库应用过旧版 307（当时为非级联），之后文件被改为 CASCADE；migration 按文件名记录版本、编辑不重跑，于是本地库永久漂移。`ensureHandlerTestSchema` 只补缺失的 migration，不修漂移。
- 结论：与 CI #8110 无关。若要修本地环境，需手动 `ALTER TABLE … DROP CONSTRAINT` 后按当前文件重建该约束，或重建本地测试库。

## 8. 复现与验证环境（本次调查）

- 干净 worktree：`/tmp/multica-ci-8110`（detached `03bde6117`）；PR #3822 worktree：`/tmp/multica-ci-8110-pr3822`；前向修复 worktree：`/tmp/multica-ci-8110-forwardfix`（基于 `lrm/dev`=`39733ca9f`）。
- Docker 容器（与 CI 相同配置，可复用/清理：`docker rm -f multica-ci8110-pg multica-ci8110-redis`）：
  - `multica-ci8110-pg`：pgvector/pgvector:pg17，端口 55432，DB `multica` 与 `multica_research` 均已完整迁移。
  - `multica-ci8110-redis`：redis:7-alpine，端口 56379。
- 复现命令（即 CI Test 步骤）：
  ```bash
  cd <worktree>/server
  export DATABASE_URL='postgres://multica:multica@localhost:55432/multica?sslmode=disable' \
         TEST_DATABASE_URL='postgres://multica:multica@localhost:55432/multica_research?sslmode=disable' \
         REDIS_TEST_URL='redis://localhost:56379/1' \
         PATH=/home/zhoujie22/go-native/go/bin:$PATH \
         GOCACHE=/home/zhoujie22/.gocache-zcode GOPROXY=https://goproxy.cn,direct
  go test -p 1 -count=1 ./cmd/materialize-promoted ./cmd/rescan-suggestions ./cmd/server \
    ./internal/handler ./internal/integrations/lark ./internal/researcheval \
    ./internal/researchrun ./internal/scheduler ./internal/service
  ```
- GitHub API 匿名可读 job/step 结论（`/actions/runs/{id}/jobs`），但日志下载需要 admin 权限；本次调查未使用任何凭证。

## 9. 上游 PR #3822 的状态与缺口（截至 11:20 UTC）

- 作者 me-frankan，分支 `fix/release-v6-frozen-contract`，动机：v0.4.25 release verify 在 main 上因同样三个测试失败被挡。
- 内容：把两份 director schema 副本回退到冻结内容（删 `task_context`/`input_nodes`），**保留** 9d23ad851 的全部 Go 实现；新提交 `9048b81b15` 又把 frontier 查询回退为 `v.artifact_id`。
- 本地验证（等价状态）：`TestV6DirectorBriefIncludesAtomicResultFrontier` 恢复通过，但
  - `TestBuildV6WorkDispatchPromptMakesDiscussionExecutable`：`$.task_context: unknown field`
  - `TestBuildV6WorkDispatchPromptMakesIntegrationExecutable`：`$.task_context: unknown field`
  - 原因：实现（`compileV6WorkManifestTx`、`BuildV6WorkDispatchPrompt` 走 `DecodeV6Contract` 校验）仍产出/依赖这两个字段，而回退后的 schema `additionalProperties:false` 直接拒绝。其 CI run #8119 已确认 backend Test 失败，作者应会继续迭代。
- 结构性矛盾：schema 回退方向若要彻底，必须同时移除实现中产出这两个字段的路径（`compileV6WorkManifestTx` 的 `input_nodes`/`task_context` 注入），这会实质废掉 PR #3815 的 discussion/integration 派发功能；只回退 schema 不动实现不可行。
- 另一个 9d23ad851 的潜在不一致（无论哪个方向都值得让 feature 作者知道）：frontier CTE 对 insight 的 content id 用 `research_insight_version.id`，而 `compileV6WorkManifestTx` 的 `input_nodes` 对 insight 用 `iv.insight_id`——insight 路径两处身份不一致。

## 10. 前向修复（已验证，未提交）

位置：`/tmp/multica-ci-8110-forwardfix`（基于当前 dev `39733ca9f`，未建分支、未提交）。3 文件 +11/-2：

1. `docs/contracts/research-run-v6.schema.json`：补上与 director 文档相同的 2 行（`task_context`、`input_nodes`），三份副本恢复字节一致。
2. `server/internal/researchrun/v6_contract_test.go`：`frozenV6ContractSHA256` 重钉为 `23b49b97c4db0884b085ef8859a914b1582c6ce2c3e1c61de5f7fd6da9692464`，附英文注释说明出处。
3. `server/internal/researchrun/director_v6_test.go`：frontier 断言改为 `node["id"] == resultNodeID`（内容身份），与 `compileV6WorkManifestTx` 冻结进 `input_nodes` 的引用一致，附英文注释。

验证：`internal/researchrun` 整包 `-count=1` 通过（217s）；CI 精确 9 包命令（见第 8 节）**全部通过、exit 0**：

- [x] researchrun 整包：ok 217.132s
- [x] 9 包 CI 命令：cmd/server 5.1s、internal/handler 114.0s、internal/integrations/lark 3.4s、internal/researcheval 0.1s、internal/researchrun 225.8s、internal/scheduler 3.0s、internal/service 13.8s（cmd/materialize-promoted、cmd/rescan-suggestions 无测试文件）

含义与代价：该修复**承认对 V6 冻结契约的修订**（测试注释里写明下次语义变更必须等 research-run-v7），保留 le-czs 的功能；与 PR #3822 的回退方向互斥，两者只能取一。是否接受契约修订属于团队治理决策。

## 11. 下一步（按优先级）

1. **用户/团队决策**：二选一——
   - 前向：采用本 handoff 第 10 节的修复（可让 me-frankan 在 PR #3822 里反向调整，或另开 PR）；
   - 回退：完整 revert `9d23ad851`（schema + 实现 + 其新增测试），冻结契约优先，功能作废重做。
2. 若用户要求提交前向修复：在 `/tmp/multica-ci-8110-forwardfix` 建分支提交（不要动主工作树），并先 `git fetch lrm dev` 确认无新冲突（PR #3822 若先合入会冲突）。
3. 与 me-frankan/le-czs 沟通第 9 节的验证结论，避免 PR #3822 反复试错。
4. 环境清理（可选）：`docker rm -f multica-ci8110-pg multica-ci8110-redis`；worktree `/tmp/multica-ci-8110*` 可保留供复查。
5. 只有用户明确要求时才 commit/push；不要直接 push main/master。

## 12. 历史：合并前 Graph 验证记录（仍然有效）

合并前在干净 Graph 验证 worktree 通过：`internal/memorygraph`、`internal/service`、`internal/scheduler`、`internal/daemon`、`pkg/agent`、`cmd/server` 与 `internal/handler -run 'Test(GraphMemory|ManagedGraphMemory)'`；前端 Core/Views TS 通过、测试 94 passed 2 skipped；`git diff --check lrm/dev...HEAD` 干净且不含 evaluation/memory_policy 差异。
