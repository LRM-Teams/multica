# Graph Memory Agent Mode 规格

- 日期：2026-08-25
- 状态：设计已确认；非 TTT 第一版待实现
- 范围：为 Graph Memory 增加 `inject | agent` 交付模式；Agent mode 中每个群聊频道拥有一个真实、常驻语义但事件驱动执行的 Memory Agent
- 前置规格：`2026-08-24-graph-memory-recall-continuation-spec.zh-CN.md`、`2026-08-21-graph-memory-backtest-explore-redesign-spec.zh-CN.md`、`2026-08-17-graph-memory-scope-design.md`

## Problem Statement

当前 Graph Memory 只有 inject 交付形态：每个业务智能体 turn 开始前，守护进程以当前消息为 query 同步请求一次 server-authoritative recall，Server 运行临时 Explore trajectory，然后把 adopted summary 作为 workspace memory 注入该业务智能体。相邻调用可以通过 per-channel continuation 继承压缩先验，但每次 recall 仍由业务 turn 被动触发，Explore agent 看不到完整持续群聊语境，也不能作为频道成员主动决定检索方向或在发现重要长期记忆时发言。

团队需要另一种交付形态：Memory Agent 是群聊中的真实智能体成员。它持续观察当前频道的人类与其他智能体消息，在频道活跃期间异步推进 Graph Explore；未被 `@` 时自行决定当前语境是否需要检索、检索什么及是否公开结果；被 `@` 时在同一个运行中 turn 接收完整定向消息并调整检索方向。该模式必须避免与 inject 双重检索、跨频道权限泄漏、无限资源消耗、Graph 自我复制及不可恢复的 provider session 状态。

## Solution

在 `memory_type=graph` 下新增独立的 `graph_memory_mode=inject|agent`。Workspace 提供默认值，频道提供 `inherit|inject|agent` override。两种交付模式严格互斥。

Agent mode 为每个 group channel provision 一个 `managed_role=graph_memory_channel` 的真实 Memory Agent。每个 Agent 具有独立身份、resident session、频道 cursor、结构化状态和 trajectory，但可以共享同一运行时。它通过受限 delegated credential 使用 Server-backed 原生 Graph tools和当前频道消息读取/搜索；不持有 shell、文件、Issue mutation或外部 MCP能力。

Memory Agent 使用可续期 exploration lease：有效频道消息、其他频道智能体的 pending/running 工作或明确 `@Memory` 会启动/续期；频道静默且无其他活跃智能体后 checkpoint并休眠。没有固定 Explore round 终止条件，但受单次节点数、节点速率、连续 turn 时长、并发和 Workspace token配额约束。

每个逻辑 turn只拥有一个 trajectory。`submit` 或 `checkpoint` 终结 trajectory；频道仍活跃时，以 PostgreSQL 中的结构化 state和 bounded channel context滚动到新 turn。明确 `@` 不取消 turn，而是在当前工具调用安全边界向同一个 resident turn 注入完整 directed steering，保留已读节点、trajectory和pinned graph version；明显换题时由 Agent调用 `memory_graph_redirect` 获取同版本 fresh hybrid seeds。

## User Stories

1. 作为 Workspace owner，我希望在 Graph Memory 中选择 inject 或 Agent mode，以便决定长期记忆是隐式注入还是由频道成员主动协作。
2. 作为频道 manager，我希望为自己的频道选择 `inherit|inject|agent`，以便在不改变整个 Workspace 的情况下调整交付形态。
3. 作为现有 Graph Workspace owner，我希望升级后默认进入 Agent mode，并自动复用合格的 owner/onboarding Pi运行时，以便无需逐频道手工配置。
4. 作为频道成员，我希望看到一个真实可辨识的 Memory Agent，以便知道长期记忆消息由谁提供并可直接 `@` 它。
5. 作为频道成员，我希望 Memory Agent读取当前群聊上下文，以便检索方向来自持续讨论而不是孤立消息。
6. 作为业务智能体，我希望 Memory Agent观察我的频道消息和运行状态，以便我工作期间它能并行准备相关长期记忆。
7. 作为频道成员，我希望 Memory Agent未被 `@` 时只在内容相关、新颖、可行动且有记忆依据时主动发言，以减少噪声。
8. 作为频道成员，我希望明确 `@Memory` 后总能获得可见结果或“未找到”回复，以便知道请求已完成。
9. 作为频道成员，我希望正在探索的 Memory Agent能接收新的 `@` 并调整方向，而不是丢失已有探索或重新开始。
10. 作为 thread参与者，我希望 Memory Agent以thread为主要上下文并在原thread回复，以免答案出现在错误位置。
11. 作为另一thread中的请求者，我希望我的 `@` 在当前target被占用时进入高优先队列，以免被最新请求覆盖。
12. 作为安全管理员，我希望 Memory Agent只能访问当前频道和当前GraphView允许的节点，以免通过边遍历或owner身份泄漏其他频道数据。
13. 作为安全管理员，我希望 owner只是sponsor，Agent只获得channel-scoped delegated credential，以免暴露owner完整权限。
14. 作为运行时管理员，我希望只有支持 `directed_message_steering` 的Pi resident runtime可承载首版Memory Agent，以保证provider行为一致。
15. 作为 Workspace owner，我希望看到 provisioning、active、blocked和inactive状态，以便诊断为什么频道没有长期记忆协作。
16. 作为频道成员，我希望blocked状态只在状态变化时通知、被 `@` 时明确失败，以免刷屏或无限等待。
17. 作为审计人员，我希望每个start、explore、redirect、steering、submit和checkpoint都持久化且幂等，以便恢复和追责。
18. 作为审计人员，我希望初始query不可覆盖，方向变化记录为有序steering events，以便重建最终答案的因果链。
19. 作为管理员，我希望Agent state在停用后归档，并在重新启用时恢复但不回放停用期间的全部消息，以免产生历史检索风暴。
20. 作为管理员，我希望可以清除内部state而不删除频道公开消息、citation和审计ledger，以便安全重置。
21. 作为频道成员，我希望Memory Agent消息附带可点击的Graph citation，以便检查当时证据。
22. 作为审计人员，我希望citation固定历史版本excerpt和hash，以免当前节点修改后冒充旧证据。
23. 作为Graph维护者，我希望Memory Agent的复述标记为derivative且默认不重新ingest，以免Graph自我复制。
24. 作为Graph维护者，我希望Memory Agent只读Graph，新事实仍通过interaction ingest、staging和consolidation进入，以保持单一写入权威。
25. 作为成本管理员，我希望持续探索没有任意固定轮数但受速率、时长和token ceiling约束，以兼顾持续性和资源安全。
26. 作为成本管理员，我希望达到速率限制时暂停而不是记为miss，达到token窗口时checkpoint并等待恢复，以免污染质量指标。
27. 作为客户端用户，我希望Graph profile和channel settings都能配置并展示有效模式，以免配置只存在于隐藏API。
28. 作为inject用户，我希望inject模式继续保持现有recall、continuation、TTT和failure-nonfatal语义，以免新增模式造成回归。
29. 作为TTT用户，我希望recall TTT和consolidation TTT拆分，以便独立控制高频检索和低频图优化成本。
30. 作为未来Agent-TTT用户，我希望首版明确固定单trajectory且保存偏好，以便后续扩展内部K候选而不增加可见频道成员。

## Acceptance Criteria

1. `memory_type=graph` 时有效交付模式按 `channel override ?? workspace default` 计算；其他memory type不启动Graph Memory Agent。
2. Agent mode下普通业务智能体的新turn不调用inject recall；inject mode下不调度Memory Agent。
3. Workspace和channel模式均使用CAS/事务更新；旧Graph profile迁移默认Agent mode，旧channel默认inherit。
4. 切换Agent mode前预检Pi runtime、model及directed steering capability；配置提交后每个group channel独立reconcile。
5. 每个group channel最多一个专属managed Memory Agent；DM不provision；handle稳定且成员管理入口不能删除有效Agent。
6. 首次启用和重新启用均从当前最大seq开始，不逐条回放历史；新turn获得bounded recent context。
7. Memory Agent自己发出的消息不会重新唤醒自身；人类和其他智能体消息可唤醒；噪声事件不会续期lease。
8. 频道有非Memory Agent pending/running工作或有效新消息时lease可续期；无方向时不随机遍历。
9. 一个turn只有一个active trajectory；每个trajectory只有一个terminal `submit|checkpoint`。
10. `memory_graph_start`固定scope/view/version并返回fresh seed nodes及边投影；Agent不能自报owner、channel或version。
11. `memory_graph_explore`是唯一节点读取interface；不暴露`view/expand`；边默认分页20条并每步重验GraphView。
12. `memory_graph_redirect`在同一trajectory/version记录steering并返回新query的fresh seeds。
13. Directed steering在当前tool call安全边界携带完整消息和bounded context进入同一个Pi turn；迟到输入由active turn fence拒绝。
14. 第一个directed target占有当前turn；同target可继续steer；其他target排队下一turn。
15. version切换只影响新turn；旧state nodes重验，旧observations在重读前tentative。
16. state、run、trajectory、steering、checkpoint、submission和citation以PostgreSQL为authority；trace文件不是可用性依赖。
17. ambient失败不推进cursor或state并bounded retry；directed失败保留target，耗尽后系统提示；所有mutation有幂等键。
18. Prompt决定主动发言；Server不做语义阻断但记录policy violation。明确 `@` 的miss必须可见回复。
19. 发布消息的citation只能来自该trajectory服务端验证过的submitted node IDs，并固化有限历史snapshot。
20. derivative Memory Agent消息不进入Graph evidence ingest；其他参与者的新决定正常ingest。
21. Profile UI、channel override UI、Agent status、blocked/recovery和citation viewer可用，具备loading/error/empty状态。
22. 现有inject路径及其tests保持通过；Agent mode首版强制K=1；consolidation TTT独立工作。

## Implementation Decisions

### Configuration and precedence

- `graph_memory_profile`新增Workspace默认mode、Memory runtime/model/thinking、lease及quota字段，并把现有`ttt_enabled`拆为`recall_ttt_enabled`和`consolidation_ttt_enabled`。
- 频道新增三态override。Graph Workspace默认`agent`；既有Graph rows迁移为Agent mode。迁移本身不执行side effects，Server reconciler负责provisioning。
- 合格runtime优先取显式配置，其次取Workspace onboarding/owner Agent所在Pi runtime；找不到则blocked且不fallback inject。

### Managed Agent provisioning

- 每频道一个真实Agent，`managed_role=graph_memory_channel`，稳定handle `memory-<channel-id-prefix>`，默认display name `Memory · #<channel-name>`。
- Agent可以共享runtime但拥有独立resident slot/session/state。owner是sponsor，执行使用channel-scoped delegated credential。
- 有效Agent mode时普通成员删除被拒绝；停用时移出频道并inactive，state归档；频道永久删除时级联清理专属内部状态。

### Control-plane module

- 建立深模块 `GraphMemoryAgentControlPlane`，外部interface覆盖：reconcile effective mode、observe activity、begin/continue turn、apply directed steering、executeGraphTool、terminalize、reset。复杂的lease、cursor、state、target queue、quota和恢复都隐藏在该interface后。
- PostgreSQL adapter是权威实现；handler和daemon只跨该seam，不分别复制状态机。
- start/redirect/explore/submit/checkpoint使用run/trajectory-scoped idempotency和fencing token。

### Graph tool interface

- 原生工具为`memory_graph_start`、`memory_graph_explore`、`memory_graph_redirect`、`memory_graph_submit`、`memory_graph_checkpoint`。
- 物理node/edge保持分离；节点响应携带visible edge projection及cursor。删除不可达的legacy view/expand handler和相应prompt文案。
- Fresh retrieval永远优先；state candidate IDs只有在当前version/view可hydrate后追加。

### Resident steering

- 新增与content-free pending notice不同的`ResidentDirectedMessageInput` capability。输入包含fenced active turn identity、canonical target和bounded directed context。
- Pi adapter使用native steer，在当前工具调用结束后、下一模型调用前插入；首版只有Pi声明capability。
- Steering不切换pinned version，不删除pre-steering结果；跨target请求由control plane排队。

### Lease and quotas

- 默认idle grace 120秒、每call 4 nodes、每频道30 nodes/min、连续turn 600秒、Workspace 200k tokens/hour；美元成本只记录不阻断。
- 无fixed MaxRounds；速率耗尽进入paused，时长/context/token边界checkpoint。无open question/目标时保持lease但不调用Graph工具。

### Context, output, citations

- 初始上下文约16k tokens，优先directed、trigger、thread、recent channel、structured state；旧消息通过当前频道search。
- 附件只注入已授权提取文本/transcript和元数据。
- 最终文本走现有channel completion/no-reply路径；prompt-only speech policy，Server只审计。
- Citation保存node/version/level/epistemic/tags/title、最多2000字符excerpt、content hash和captured time；UI默认历史snapshot，可选当前节点。

### Mode isolation and TTT

- inject continuation与Agent state完全隔离，不导入、不回写。
- Agent mode首版单trajectory；`recall_ttt_enabled`仅对inject生效，`consolidation_ttt_enabled`独立生效。

## Testing Decisions

测试只跨已确认seam观察外部行为，不断言private helper或SQL实现细节：

1. **Profile/channel handler + DB integration seam**：验证默认/override precedence、CAS、迁移默认Agent、权限、provisioning/blocked、mode互斥和成员保护。沿用现有`graph_memory_profile_test.go`、`channel_test.go`测试数据库fixture。
2. **Graph Memory Agent control-plane interface seam**：使用PostgreSQL adapter和Graph fixture验证lease、cursor、target queue、idempotency、trajectory terminal、version revalidation、quota、retry和reset。测试通过module interface，不直接查询内部表作为主断言。
3. **Graph tool HTTP/native interface seam**：验证Server重算scope/view/version、start/explore/redirect/submit/checkpoint、边分页、不可见节点fail-closed及submitted node约束。沿用现有Explore endpoint和recall handler风格。
4. **Resident directed-steering seam**：daemon pool以fake resident adapter验证active-turn fencing、safe-boundary delivery、跨target queue；Pi adapter测试native steer payload携带完整canonical message而非content-free notice。
5. **Core API/schema + Views seam**：验证旧response解析默认值、profile和channel mutation payload、loading/error/blocked UI、权限及citation snapshot展示。测试落在`packages/core`和`packages/views`，不mock Next.js。
6. **回归**：现有memorygraph、recall、message runtime、channel membership、provider pending notice、Graph Memory card测试全部通过。

## Out of Scope

- Agent mode中的K条并行TTT trajectory、多个可见Memory Agent或内部候选辩论。
- 非Pi provider的完整directed-message steering。
- DM中的Memory Agent。
- Graph写入工具、直接consolidation或绕过interaction ingest的记忆编辑。
- 跨频道消息搜索、Graph prior共享或owner权限扩张。
- 美元成本硬阻断（首版只记录）。
- Agent与inject continuation之间的状态迁移。
- 将旧provider transcript作为长期authority或无限resume。

## Further Notes

- 这是一次高影响默认值变更：已有Graph Workspace升级后进入Agent mode；没有合格Pi runtime时会blocked且inject停止。UI和状态通知是交付必需部分，不是后续优化。
- `docs/engineering-principles.md`被仓库规范引用，但当前checkout不存在；实现过程中若恢复该文件，应将本规格中的新持久契约登记到其索引。
- 当前工作树已有用户未提交的Graph continuation文档；本规格是独立新文件，不修改或覆盖它们。
