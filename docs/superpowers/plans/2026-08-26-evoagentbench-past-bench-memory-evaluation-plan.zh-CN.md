# EvoAgentBench / PAST-Bench API-only Memory 评测 Harness 实施计划

- 日期：2026-08-26
- 依据：`../specs/2026-08-25-evoagentbench-past-bench-memory-evaluation-spec.zh-CN.md`
- Harness：`/home/zhoujie22/river2_0/memory_bench`
- 状态：已实施并完成本地验证；真实 Pilot/Full 仍由 capability gate 阻止

## 实施结果（2026-08-26）

- 外部 `memory_bench` Harness 已补全；未修改 Multica Server、Daemon、schema 或 UI。
- capability gate 固定记录当前为 `system-comparison-only`，`fresh_session=approximate`，`execute_allowed=false`。
- 已实现不可变 run manifest、严格 onboarding 关联、精确 transient retry、正式 manifest/pin 校验、unknown usage 语义、按 benchmark 分离的 macro/bootstrap/WTL 和最终 Markdown 报告。
- 验证：47 个 unittest 通过，`compileall` 通过，24-run synthetic dry-run 明确无网络调用，六类核心产物及门禁断言通过。
- 当前禁止 Pilot/Full；恢复 directed delivery 后也只能先重跑最小 capability Smoke。严格 Evo transfer、官方 PAST persistence gap、等价 Graph preseed 和完整 Graph cost 仍需另行批准的产品/API能力。

## 1. 当前基线

`memory_bench` 已有可复用的 stdlib-only Python 3.11+ 实现，不从零重建：

- EvoAgentBench / PAST-Bench JSON/JSONL manifest 归一化；
- Graph/Legacy paired、交错、全局串行调度；
- 默认无网络 dry-run 与付费执行多重确认门禁；
- Multica 公共 HTTP API client；
- Graph profile CAS、channel Agent mode、Legacy inactive、session reset、barrier 和主 Agent usage 前后差分；
- 基础设施错误最多重试 2 次，任务错误不重试；
- `attempts.jsonl`、`summary.csv`、`preflight.md`；
- 29 个本地单元测试和真实 capability Smoke 证据。

真实 Smoke 已确认：Graph profile CAS、managed Agent active/runtime/model pin、Legacy inactive 可用；但 directed channel delivery 在 0/9 daemon WebSocket connected 时无法可靠执行，普通消息又允许 `no_reply`。此外公开 API 缺少 Evo test read-only/frozen snapshot、matched persistence-off、Graph canonical import、owner-readable managed Memory Agent/background usage 和 evaluation control。因此当前禁止 Pilot/Full，兼容性只能是 `system-comparison-only`。

## 2. 本轮目标与边界

本轮只增强外部 Harness，不修改 Multica Server、Daemon、数据库 schema 或 UI，不直写数据库，不运行网络请求或付费模型。目标是：

1. 把 capability 结论变成机器可执行的 preflight gate，而不是仅存在于研究报告；
2. 修复真实执行链中 onboarding 等待、错误分类和重试边界；
3. 完善不可变 run manifest、协议兼容性和 deviation 数据合同；
4. 补齐 Evo/PAST 离线 adapter 元数据验证和描述性统计/报告；
5. 用 fixture、fake client 和本地 mock smoke 验证，明确 Pilot/Full 门禁仍关闭。

不在本轮实现：官方 benchmark 下载、官方 grader 执行、Parquet、真实 Pilot/Full、严格 read-only/off、Graph 等价预置、完整 Graph usage closure。以上能力若需要产品支持，必须另起 API 规格并获显式批准。

## 3. 实施步骤

### Phase A：协议与运行合同

修改 `src/memory_bench/models.py`、`config.py`、`reporting.py`，新增：

- `protocol_compatibility`：`official-compatible | partially-compatible | system-comparison-only`；
- capability 状态：`pass | approximate | fail | unsupported | unknown`；
- deviations、blocked claims、usage completeness；
- run-level manifest，固定 harness 版本、配置 hash、输入 manifest hash、上游 repo/commit、模型/runtime pin、seed、backend、track、资源 ID 和开始/结束时间；
- 缺失 usage 保持 `null/unknown`，禁止由聚合逻辑转为 0。

配置必须显式固定 Evo/PAST 上游 commit 和主 Agent model；fixture dry-run 可使用明确的 synthetic 标记，但不得伪装成正式数据。

### Phase B：Capability gate

新增独立的 capability evaluator：

- strict Evo 必须具备 fresh session、frozen/read-only test state、model pin 和 usage closure；
- official PAST persistence gap 必须具备 fresh session、matched persistence-off、ready barrier 和 usage closure；
- Graph equivalent-preseed 必须具备 canonical import 与 provenance；
- 任一硬缺口自动降级标签并列出 blocked claims；
- 当前已知 API 缺口应稳定得到 `system-comparison-only`，并阻止 `pilot/full` execute；
- synthetic dry-run 可继续生成计划与报告，但必须醒目标注不产生 benchmark 结论。

### Phase C：执行可靠性

修改 `src/memory_bench/cli.py`、`multica_client.py`、`scheduler.py`：

- `add_channel_agent` 后等待 onboarding task 进入终态，不要求可选的 visible introduction；
- onboarding 未完成、无 task、超时均作为基础设施错误；完成的 `no_reply` onboarding 可继续；
- 仅网络错误、5xx、429、明确 transient 409（如 `graph_not_ready`）可重试；鉴权、校验和普通 4xx 不重试；
- 任务错误和错误答案不重试；同一基础设施重试保留幂等 `client_message_id`；
- 所有失败 attempt 的 wall time、request count 和可观测 token 均保留。

### Phase D：Adapter 与统计报告

修改 `adapters/evoagentbench.py`、`adapters/past_bench.py`、`telemetry.py`、`reporting.py`：

- Evo 记录 split、domain、task ID、禁止在线使用 test Ability label；正式 manifest 校验 528 train / 267 test 和四领域，仅 synthetic fixture 可跳过规模校验；
- PAST 记录 capability、family、episode role/order；正式 manifest 校验 26 family / 204 episode 及四类规模，仅 synthetic fixture 可跳过；
- 输出 domain/family/capability macro-average、paired difference、win/tie/loss、确定性 paired bootstrap 95% CI；
- 输出端到端 latency p50/p95、failure rate、timeout rate 和 usage unknown rate；
- 不生成 Evo+PAST 统一总分，不设置胜负阈值；
- 未接 grader 时 task/mechanism score 必须是 missing，不从 expected 文本伪造分数。

### Phase E：文档与门禁产物

更新 `memory_bench/README.md` 和 example config：

- 明确 fixture、正式 manifest、capability report、run manifest 和 report 用法；
- 列出当前 API hard/approximate gaps；
- 保留 Smoke → Pilot → Full 两次人工确认；
- Pilot/Full 前必须有预计 token、费用、耗时、失败率且 capability gate 允许；
- 当前状态明确为禁止 Pilot/Full。

## 4. 测试计划

新增或扩展测试：

1. capability matrix 到 compatibility label、blocked claims 和 execute gate；
2. run manifest hash 稳定性及敏感 token 不落盘；
3. onboarding completed/no_reply、missing task、timeout；
4. 429/5xx/transient 409 重试，普通 4xx/TaskError 不重试；
5. Evo/PAST 正式规模与分类校验、synthetic fixture 例外；
6. macro-average、paired bootstrap、WTL、p50/p95、missing/crash/timeout；
7. missing usage 始终为 unknown；
8. dry-run 生成 JSONL/CSV/Markdown/run manifest，且不触发网络。

验证命令：

```bash
cd /home/zhoujie22/river2_0/memory_bench
PYTHONPATH=src python3 -m unittest discover -v
PYTHONPATH=src python3 -m compileall -q src smoke tests
PYTHONPATH=src python3 -m memory_bench --config configs/smoke.example.json
```

如环境提供 Ruff/Mypy，可额外运行；本项目不为此新增依赖。

## 5. 完成标准

- 所有本地测试、compileall 和 dry-run smoke 通过；
- 无 Multica 产品代码修改、无网络或付费模型调用；
- 产物包含不可变 run manifest、capability gate、compatibility/deviation、描述性统计和 unknown usage；
- 当前真实 capability 结论仍阻止 Pilot/Full，并明确不得宣称 official PAST persistence gap 或 strict Evo ability transfer；
- 若后续恢复 daemon directed delivery，只允许重新执行最小真实 Smoke；仍需用户另行确认，不能自动进入 Pilot。
