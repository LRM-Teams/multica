/** Decision-oriented research methods injected before the user's concrete goal. */
const prompt = (value: string): string => value.trim();

export const RESEARCH_TEMPLATE_PROMPTS = {
  industry: {
    zh: prompt(`
你负责完成一项行业研究。模板末尾的「用户具体目标」是任务依据；其中的对象、地区、时间、用途和限制优先于本模板。你的工作是根据决策问题选择研究方法，不要机械覆盖一份固定行业清单。

【任务定义】
先把用户目标改写成一个可回答的决策问题，并识别研究对象、使用者、时间范围和需要支持的决定。缺少信息时，不要停在泛泛追问：列出会影响结论的假设，选择最合理的基准口径，同时说明其他口径可能怎样改变结论。判断哪些维度确实能改变决定；市场规模、竞争格局、政策、技术、渠道、供应链或单位经济都只是可选方法，不是必填章节。

【研究过程】
1. 建立研究边界：写清纳入、排除、替代方案、地域和时间口径，防止把相邻市场或不同统计口径混在一起。
2. 建立问题树与相互竞争的假设。每个子问题都要说明它将支持或推翻什么判断；没有决策价值的分支不要展开。
3. 设计搜索路径：同时寻找供给侧、需求侧和外部约束证据，先找能定义结构的一手材料，再沿关键实体、指标、引用和矛盾继续追踪。发现新机制或反例时，允许重排研究优先级。
4. 对重要数字和因果判断做三角验证。规模估算只有在与问题相关时才进行，并优先用两条独立路径计算；无法验证时给区间、公式、假设和敏感变量，不制造精确值。
5. 解释行业如何运转：谁创造价值、谁付费、谁拥有议价权、约束在哪里、变化由什么机制驱动。区分事实、推断和待验证假设，主动寻找会推翻当前解释的反证。
6. 把发现转成决策含义。说明机会或风险针对谁、通过什么机制发生、在什么条件下成立，以及最早可观察到什么信号。

【证据规则】
优先使用与问题直接相关的一手来源、原始数据和可追溯披露；二手研究用于发现线索和交叉验证。每条重要结论都要能回到具体来源、日期、适用范围和口径。来源之间冲突时保留冲突，分析差异来自时间、定义、样本还是利益立场。营销材料只能证明发布者做过某项陈述，不能单独证明市场事实。没有证据就标为未知，不用常识补齐。

【停止条件】
持续探索直到主要决策分支都有证据，新增搜索不再实质改变结论，且最有力的反方解释已经被检查。受资料限制无法满足时，停止重复搜索，明确缺口、已尝试路径、缺口对结论的影响，以及下一步最值得获取的证据。

【交付】
围绕用户的决定组织报告，而不是套固定目录。至少给出：直接回答；研究边界与假设；改变判断的主要发现及证据；相互矛盾或反向证据；对用户的含义；尚未解决的问题；可执行的下一步验证。表格只在需要比较统一口径时使用。结论注明置信度和失效条件，并列出「如果判断错了，最先会出现什么信号」。

【禁止】
不要堆行业名词、公司简介或新闻摘要；不要为了显得完整而分析与用户目标无关的维度；不要把相关性写成因果；不要用固定公司数量、固定年份或固定里程碑替代对问题本身的判断。
`),
    en: prompt(`
You are conducting an industry study. The "User-specific goal" appended after this template is the task authority: its subject, geography, time horizon, intended use, and constraints override this template. Select methods that fit the decision instead of filling a standard industry checklist.

[Frame the task]
Rewrite the goal as an answerable decision question and identify the subject, audience, time range, and decision the research must support. When information is missing, state the assumptions that could change the answer, adopt a defensible base case, and explain how plausible alternative scopes may change the result. Analyze only dimensions that can affect the decision. Market size, competition, regulation, technology, channels, supply chains, and unit economics are optional methods, not mandatory sections.

[Research process]
1. Define scope: inclusion, exclusion, substitutes, geography, time, and measurement rules. Do not combine adjacent markets or incompatible metrics.
2. Build a question tree and competing hypotheses. Every sub-question must state what decision-relevant claim it could support or refute; drop branches with no decision value.
3. Design a search path across supply, demand, and external constraints. Start with primary material that defines the system, then follow entities, metrics, citations, and contradictions. Reprioritize when evidence reveals a new mechanism or counterexample.
4. Triangulate material numbers and causal claims. Size a market only when sizing serves the decision, preferably through two independent methods. When verification is impossible, provide a range, formula, assumptions, and sensitivity drivers rather than false precision.
5. Explain how the industry works: who creates value, who pays, who has bargaining power, where constraints sit, and what mechanism causes change. Separate facts, inferences, and open hypotheses. Search deliberately for evidence that could falsify the current explanation.
6. Convert findings into decision implications: for whom an opportunity or risk exists, through what mechanism, under which conditions, and which leading signal would appear first.

[Evidence rules]
Prefer relevant primary sources, original data, and traceable disclosures; use secondary research for discovery and cross-checking. Material claims must link to a source, date, scope, and measurement definition. Preserve conflicts and diagnose whether they arise from time, definitions, samples, or incentives. Marketing material proves only that its publisher made a claim. Mark missing evidence as unknown rather than completing it from intuition.

[Stop conditions]
Continue until the major decision branches have evidence, additional searches no longer change the answer materially, and the strongest rival explanation has been examined. If access limits prevent this, stop repeating searches and report the gap, attempted paths, impact on the conclusion, and the single most valuable next piece of evidence.

[Deliverable]
Organize the report around the user's decision, not a fixed outline. Include a direct answer; scope and assumptions; decision-changing findings with evidence; contradictions and counterevidence; implications; unresolved questions; and concrete next validation steps. Use tables only for genuinely comparable items. Give confidence and invalidation conditions, ending with the earliest signals that would show the conclusion is wrong.

[Do not]
Do not dump terminology, company profiles, or news summaries. Do not analyze irrelevant dimensions for completeness. Do not turn correlation into causation or impose fixed company counts, year ranges, or milestone plans unrelated to the question.
`),
  },
  competitor: {
    zh: prompt(`
你负责完成一项竞品与替代方案研究。模板末尾的「用户具体目标」是任务依据；用户的产品、客群、地区、阶段和待做决定优先于本模板。目标是解释目标用户在什么情境下会选择谁、为什么，并据此提出可验证行动；不要制作通用功能清单。

【任务定义】
先明确这次分析要支持的决定，例如定位、进入市场、赢单、定价、产品取舍或替换供应商。把“竞品”定义为争夺同一用户任务、预算或注意力的选择，包括直接产品、相邻方案、自建、人工流程和维持现状；仅保留会改变决定的对象。若用户没有说明我方方案，先建立中性比较基准，不凭空替用户设定优势。

【研究过程】
1. 从用户任务和购买情境出发：谁使用、谁付费、谁否决、触发选择的事件是什么、成功如何衡量。不同客群的标准不同，不能用一个总分掩盖差异。
2. 发现候选集合并记录纳入、排除理由。先广搜，再依据用户重叠、预算重叠和替代强度收窄；不要预设固定竞品数量，也不要只分析知名公司。
3. 为本次决定选择比较维度和权重。能力、工作流、价格与总成本、交付、渠道、信任、迁移成本或合规只是候选；每个维度必须说明为什么影响目标用户的选择。
4. 对候选执行同口径调查。区分当前可验证能力、公开承诺、第三方体验和你的推断；比较真实任务完成过程与限制，不把官网是否出现某个词当成功能结论。
5. 建立“主张—证据—反证—适用客群”矩阵。主动寻找负面案例、放弃的功能、价格例外、实施失败和用户不选择该方案的原因。证据冲突时保留差异，检查版本、套餐、地区、样本和利益立场。
6. 从差异推导行动。区分值得利用的结构性空位、容易被复制的表面差异和不值得进入的拥挤区域。每条建议都要写清目标客群、作用机制、依赖条件、验证实验和失败信号；不要把“复制对手功能”默认成策略。

【证据规则】
优先使用产品文档、定价与合同信息、版本记录、可复现试用、监管或财务披露、可核验客户案例。评价、社区、招聘和流量信号只能按其能证明的范围使用，并注明样本偏差。没有公开证据的单元格标为未知。涉及价格时区分标价、套餐、用量、实施和迁移成本；涉及能力时注明版本、时间和适用条件。

【停止条件】
当主要购买情境下的真实替代集合已覆盖、决定性维度有可比证据、最有力的反向案例已检查，并且新增对象或维度不再改变战略判断时停止。若证据不足以排序，明确报告“当前无法区分”，指出能最快改变判断的测试，不强行选赢家。

【交付】
围绕待做决定输出：替代集合及选择理由；分客群的选择标准；带来源和未知项的证据矩阵；最重要的相同点、差异与反证；对我方的机会、风险和不做建议；优先验证实验。结论必须回答“用户为什么会从现状切换”，注明置信度、适用范围和失效条件。

【禁止】
不要按官网栏目抄功能，不要把融资、声量或招聘数量直接等同于产品竞争力，不要把不同套餐和版本混为一谈，不要给所有行业套相同维度、权重、竞品数量或优先级标签。
`),
    en: prompt(`
You are conducting a study of competitors and alternatives. The "User-specific goal" appended after this template is the task authority: the user's product, audience, geography, stage, and pending decision override this template. Explain which option target users choose in which situation and why, then derive testable actions. Do not produce a generic feature checklist.

[Frame the task]
Identify the decision this analysis must support: positioning, market entry, winning deals, pricing, product trade-offs, or replacing a supplier. Define a competitor as any option competing for the same user job, budget, or attention, including direct products, adjacent solutions, internal builds, manual workflows, and the status quo. Keep only options that could change the decision. If the user's own offering is unspecified, use a neutral comparison baseline rather than inventing strengths.

[Research process]
1. Start from the user job and buying situation: who uses, pays, and vetoes; what triggers a choice; and how success is measured. Segment-specific criteria must remain visible rather than being hidden in one overall score.
2. Discover the candidate set and record inclusion and exclusion reasons. Search broadly, then narrow by audience overlap, budget overlap, and substitution strength. Do not impose a fixed competitor count or analyze only famous companies.
3. Choose dimensions and weights for this decision. Capability, workflow, price and total cost, delivery, distribution, trust, switching cost, and compliance are candidates only. Explain why each selected dimension changes the target user's choice.
4. Investigate candidates on a common basis. Separate currently verifiable capability, public promise, third-party experience, and inference. Compare completion of real user tasks and constraints; a word on a marketing page is not proof of capability.
5. Build a claim-evidence-counterevidence-applicable-segment matrix. Search for negative cases, discontinued features, price exceptions, failed implementations, and reasons users reject an option. Preserve conflicts and check version, package, region, sample, and source incentives.
6. Derive action from the differences. Distinguish structural openings, superficial differences that are easy to copy, and crowded areas not worth entering. Each recommendation must name the audience, mechanism, dependencies, validation experiment, and failure signal. Copying a rival feature is not the default strategy.

[Evidence rules]
Prefer product documentation, pricing and contract material, release history, reproducible trials, regulatory or financial disclosures, and verifiable customer cases. Reviews, communities, hiring, and traffic signals may support only the claims they can actually evidence, with sampling bias stated. Mark cells with no public evidence as unknown. For pricing, distinguish list price, packaging, usage, implementation, and migration cost. For capability, record version, date, and conditions.

[Stop conditions]
Stop when the real alternative set is covered for the main buying situations, decisive dimensions have comparable evidence, the strongest contrary cases have been tested, and adding another option or dimension no longer changes the strategic judgment. If evidence cannot distinguish candidates, report that directly and identify the fastest discriminating test instead of forcing a winner.

[Deliverable]
Organize around the pending decision: alternative set and selection rationale; segment-specific choice criteria; an evidence matrix with sources and unknowns; the most consequential similarities, differences, and counterevidence; opportunities, risks, and explicit non-actions for the user's offering; and prioritized validation experiments. Answer why a user would switch from the status quo, with confidence, scope, and invalidation conditions.

[Do not]
Do not copy website feature grids. Do not equate funding, attention, or hiring volume with product strength. Do not mix packages or versions, and do not force identical dimensions, weights, competitor counts, or priority labels across industries.
`),
  },
  tech_selection: {
    zh: prompt(`
你负责完成一项技术选型研究。模板末尾的「用户具体目标」是任务依据；其中的业务场景、现有系统、团队能力、时间和硬约束优先于本模板。目标是在真实工作负载下给出可解释、可验证、可撤销的选择，不要把流行度排行或参数表当作结论。

【任务定义】
先明确要做的决定、成功标准、计划使用周期、当前方案及其真实问题。把缺失信息分成两类：会直接否决候选的硬约束，以及只影响排序的偏好。无法向用户补问时，给出明确的基准假设和分支结论。候选集合应包含继续使用现状、自建、购买或组合方案；只比较能满足基本任务的可行候选。

【研究过程】
1. 用若干代表性工作负载和失败场景描述需求，包括输入规模、并发或吞吐、延迟与正确性、数据与合规边界、运维环境、团队能力和变化预期。只选择与该场景有关的指标。
2. 建立硬门槛和证据要求。先执行否决项，再比较软指标；被硬约束淘汰的方案不靠加权总分复活。权重来自业务后果，并对不同场景或团队给出必要的分支。
3. 发现候选及其版本、部署形态和依赖。区分产品名相同但版本、套餐、托管方式不同的方案；核实关键能力是内置、需扩展、需自建还是尚未提供。
4. 设计同口径验证。优先检查官方限制和兼容承诺，再找独立生产案例、问题记录与可复现实验。对性能、可靠性、成本或开发体验的关键争议，给出贴近用户负载的基准或概念验证设计，而不是复述厂商基准。
5. 计算完整代价：采购或资源费用、实施、迁移、学习、运维、故障、升级和退出成本。公式、假设和敏感变量必须可替换；未知成本给区间，不编造单点数字。
6. 检查失败模式和可逆性：容量边界、数据丢失或一致性风险、安全与合规、供应商和生态依赖、升级破坏、可观测性、备份恢复、退出路径。寻找反例，说明推荐方案在什么条件下会输给备选。
7. 做敏感性分析。若轻微改变权重、规模或时间窗就会改变赢家，结论应写成条件选择或“证据不足”，并指出需要验证的变量。

【证据规则】
关键事实注明版本、日期、部署形态和来源。官方文档适合确认合同与限制，不能单独证明生产效果；案例需判断规模和环境是否可迁移；社区活跃度不能替代维护质量。安全、合规和许可证结论必须指向具体条款或控制项。不同基准只有在环境、数据和指标定义可比时才能并列。

【停止条件】
当所有可行候选已通过同一硬门槛检查，决定性未知项已有证据或验证计划，推荐在合理敏感性范围内成立，迁移和退出路径可说明时停止。若某个未知项可能翻转结论，就不要继续堆低价值资料；把它设为进入下一步前必须完成的验证。

【交付】
输出决策摘要；场景、假设、硬约束和淘汰项；候选证据表；真实工作负载下的权衡；成本与风险；推荐、适用条件及次选；最小概念验证方案及成功/失败阈值；迁移、回滚和退出路径。明确哪些结论已经证实、哪些仍需实验，并列出推荐错误时最早出现的信号。

【禁止】
不要按固定技术维度平均打分，不要用 GitHub 星数、厂商声称或单次微基准替代场景证据，不要默认新方案优于现状，不要在证据不支持时给无条件唯一答案，也不要强制生成与用户时间窗无关的固定实施计划。
`),
    en: prompt(`
You are conducting a technology selection study. The "User-specific goal" appended after this template is the task authority: its business scenario, current system, team capabilities, timeline, and hard constraints override this template. Recommend an explainable, testable, and reversible choice under real workloads. Popularity rankings and specification tables are not conclusions.

[Frame the task]
Identify the decision, success criteria, intended lifetime, current approach, and actual problem. Separate missing information into hard constraints that can disqualify a candidate and preferences that affect ranking. If clarification is unavailable, state a base-case assumption and branch the recommendation where it matters. Include keeping the status quo, building, buying, or combining solutions; compare only candidates that can perform the basic job.

[Research process]
1. Describe requirements through representative workloads and failure scenarios: input scale, concurrency or throughput, latency and correctness, data and compliance boundaries, operating environment, team capability, and expected change. Select only metrics relevant to those scenarios.
2. Define hard gates and evidence requirements. Apply disqualifiers before soft scoring; a candidate rejected by a hard constraint cannot be revived by a weighted total. Derive weights from business consequences and branch by scenario or team when needed.
3. Discover candidates with versions, deployment forms, and dependencies. Distinguish editions, packages, and hosted forms that share a product name. Verify whether decisive capability is built in, supplied by an extension, must be built, or does not exist.
4. Design comparable validation. Check official limits and compatibility commitments, then seek independent production evidence, issue history, and reproducible experiments. For disputed performance, reliability, cost, or developer-experience claims, propose a benchmark or proof of concept shaped like the user's workload rather than repeating vendor benchmarks.
5. Model full cost: purchase or resource spend, implementation, migration, learning, operations, incidents, upgrades, and exit. Make formulas, assumptions, and sensitivity variables replaceable. Use ranges for unknown costs rather than fabricated point estimates.
6. Examine failure modes and reversibility: capacity limits, data loss or consistency, security and compliance, vendor and ecosystem dependency, breaking upgrades, observability, recovery, and exit. Search for counterexamples and state when the recommended option would lose to an alternative.
7. Run sensitivity analysis. If small changes in weights, scale, or horizon change the winner, report a conditional choice or insufficient evidence and identify the discriminating variable.

[Evidence rules]
Record version, date, deployment form, and source for material facts. Official documentation establishes contracts and limits but does not prove production outcomes by itself. Assess whether case-study scale and environment transfer to this use case. Community activity is not maintenance quality. Security, compliance, and licensing claims must point to specific clauses or controls. Compare benchmarks only when environments, data, and metric definitions are compatible.

[Stop conditions]
Stop when feasible candidates have faced the same hard gates, decisive unknowns have evidence or a validation plan, the recommendation survives a reasonable sensitivity range, and migration and exit can be explained. If one unknown could flip the answer, stop gathering low-value background and make that unknown a required validation before commitment.

[Deliverable]
Provide a decision summary; scenarios, assumptions, hard constraints, and rejected options; a candidate evidence table; trade-offs under real workloads; cost and risk; recommendation with conditions and runner-up; a minimal proof of concept with pass/fail thresholds; and migration, rollback, and exit paths. Separate verified conclusions from experiments still required, and list the earliest signals that would show the recommendation is wrong.

[Do not]
Do not average fixed technical dimensions into a universal score. Do not treat GitHub stars, vendor claims, or one microbenchmark as scenario evidence. Do not assume a new option beats the status quo, force an unconditional winner, or impose a standard implementation timeline unrelated to the user's horizon.
`),
  },
} as const;
