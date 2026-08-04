# Claim 级证据适配实施记录

状态：代码与本地验证完成，等待前置 PR #2245 合并后更新 `dev` 并提交 PR

日期：2026-08-04

前置 PR：[#2245](https://github.com/LRM-Teams/multica/pull/2245)

## 目标

让交付 Gate 按每个 Claim 的判断类型和已接受 Research Method 检查证据，不再用深度等级推导全局来源数量，也不把论文、官网、新闻或社区的类别当成固定信用分。

## 已完成：现状核对

- [x] `sourceClassWeight` 只写入旧 `research_source` 投影，不参与 Durable Run 交付 Gate。
- [x] Gate 当前按 shallow/standard/deep 硬编码要求 2/3/5 个全局独立来源。
- [x] 重大 Claim 当前按 shallow/standard/deep 硬编码要求 1/2/3 个独立来源。
- [x] 现有 Gate 只检查 verified 和 `independence_key`，不知道来源对具体 Claim 的直接性、方法适配度和证据用途。
- [x] 旧 adaptive kickoff/playbook 仍包含 S1–S4、固定来源数和通用来源层级；它不能作为 Durable Run 的方法真相。

## 协议设计

`research-run-v4` 在 v3 方法蓝图上增加：

1. `EvidenceStandard`：稳定 key、用途、最小独立来源数、来源特征、最低证据强度、直接性和方法适配度，以及是否要求反证搜索。
2. Claim 必须引用一个已接受的 `evidence_standard_key`。
3. Source Snapshot 记录可验证的 `evidence_traits`，例如 direct_measurement、official_record、first_party_statement、independent_evaluation、reproducible_artifact、expert_interview。
4. Claim–Observation Evidence Link 记录 `directness` 和 `method_fit`；验证任务接受后才会进入 Gate。
5. v4 Gate 对每个必答 Claim 和报告 Claim 按它的标准检查。允许方法明确规定“单一控制记录即可”，也允许对高风险判断要求多类特征和多个独立来源。
6. 要求反证的标准按 Claim 检查任务覆盖：必答 Claim 由绑定同一问题的 `counter_search` 证明，其他报告 Claim 必须出现在反证任务的持久化结果中。无关反证任务不能通过 Gate。

## 版本边界

- v1–v3 结果、Prompt 和 Gate 保持原行为。
- v4 字段不得被 v1–v3 结果接受，避免在旧运行中静默改变协议。
- 来源类别保留为描述与 UI 字段；v4 旧投影统一写中性 `0.5`，不再由类别生成看似精确的全局信用分。

## 实施步骤

- [x] 增加 v4 Result 与 Evidence Standard 验证，锁定 v1–v3。
- [x] 增加 Claim、Source Snapshot 和 Evidence Link 的持久化字段与迁移。
- [x] 实现 v4 Claim 级 Evidence Fitness Gate 和可操作的 finding metadata。
- [x] 把反证完成条件从“任意任务成功”改为 Claim 定向覆盖。
- [x] 更新 v4 任务 Prompt、新建 Fleet Prompt、内置 Skill 和 source map。
- [x] 重写 adaptive playbook，删除固定 S1–S4、来源数和通用来源层级。
- [x] 修正 legacy stage gate 的固定三来源、来源类别多样性、0.7 权重和统一人机边界要求。
- [x] 增加 `general` adaptation profile；未知领域不再默认套用 tech+market。
- [x] 增加单元、API schema、Prompt 和 PostgreSQL 集成测试。
- [ ] 合并最新 `dev`，提交非草稿 PR。

## 参考取舍

- 采用参考项目的 question→method 约束、纳入/排除、反证、停止条件、证据出处与失败记录。
- 采用参考图中的任务图、证据图和评估反馈关系；现有 Fleet role/capability 继续负责执行路由。
- 不采用 FINER、PRISMA、IRB、期刊格式、固定论文阶段或 DOI/arXiv 层级作为通用默认。
- 不在 Research Run 内复制组织权限图、CI/CD 健康图或自修改 Prompt；这些属于其他产品域，且缺少可验证的升级协议。

## 实现完成记录

1. `research-run-v4` 成为新运行默认；v1–v3 解码器、Result kind 和 Prompt 继续按版本固定。
2. migration 284 增加 `research_source_snapshot.evidence_traits`、`research_claim.evidence_standard_key`、`research_claim_evidence.directness/method_fit` 及查询索引。
3. Plan/Replan 持久化 Evidence Standards；Claim 只能引用当前 goal/plan 已接受标准，跨版本或未知 key 拒绝。
4. verified 结果可替换早期未验证的 link scores，避免用 `GREATEST` 永久保留虚高评分。
5. v4 Gate 不再运行全局 2/3/5 来源和重大 Claim 1/2/3 来源检查；它计算满足阈值的来源、独立 family 和 trait 覆盖。
6. Gate finding 带 Claim key、standard key、实际/要求独立来源数、缺失 traits 和三个阈值，remediation 可直接生成 evidence task。
7. legacy adaptive profile 改为候选方法与失败测试；domain 检测结果明确是可替换假设，seed 可以合并、删除或重写。
8. 人机责任分析只在用户目标显式包含人工审核、人机分工或自动化边界时成为要求；“开发/制作”不再自动制造该结论。
9. 前端只做向前兼容解析：Method standards、source traits、Claim standard 和 link scores 均为 optional；旧服务响应不会让已安装客户端崩溃。

## 途中发现并处理的问题

- PostgreSQL 真实回归发现旧版结果的 nil `evidence_traits` 会显式写 SQL NULL，违反 migration 284 的 NOT NULL。写入边界改为空数组；不补造 traits，v1–v3 行为不变。
- 初版反证 Gate 只数任意成功的 `counter_search`，无关任务也可通过。改为每个要求反证的目标 Claim 分别检查。
- legacy stage gate 仍硬编码三来源、两类别、0.7 权重和 delivery-like 人机边界；这些会让单一控制记录、非技术通用调研和普通交付型目标误失败，已删除。
- 全量 `go test ./...` 的既有测试数据库夹具在迁移 251 前后混用新 schema，出现 `voice_call_session`、`workspace_role`、`agent_inbox_event.issue_id` 等无关缺列；研究包的全量真实数据库测试通过，本次未用产品代码掩盖测试夹具问题。

## 验证记录

- `go test ./internal/researchrun ./internal/handler ./internal/metrics`：通过。
- migration 284 在 worktree PostgreSQL 数据库应用成功。
- `TEST_DATABASE_URL=... go test ./internal/researchrun -count=1`：全包通过。
- v4 完整 plan→evidence→report→独立 quality/citation→Gate→confirm→complete：通过。
- deep tier 单一控制记录标准：通过，且未出现旧全局来源 Gate。
- Claim 定向 counter-search：缺失时失败，目标任务成功后通过。
- Core schema 7 tests、node detail 9 tests：通过。
- `pnpm typecheck`：通过。
- React Doctor：0 issues。
