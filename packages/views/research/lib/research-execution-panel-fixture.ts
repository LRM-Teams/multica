export type ResearchExecutionStatus =
  | "queued"
  | "running"
  | "done"
  | "failed"
  | "stale"
  | "idle";

export type ResearchExecutionActionKey =
  | "waiting"
  | "working"
  | "recent_done"
  | "recent_failed"
  | "stale"
  | "idle";

export type ResearchExecutionTimeKey =
  | "queued"
  | "running"
  | "recent"
  | "failed"
  | "stale"
  | "idle";

export type ResearchExecutionFailureKey = "failed";

// `action` / `actionDetail` carry live activity text (server-provided and
// already locale-appropriate). The static chrome (fallback action, update
// time, failure reason) is carried as semantic `*Key` codes and translated by
// the ResearchExecutionPanel component against the active locale.
export type ResearchExecutionAgent = {
  id: string;
  name: string;
  role: string;
  avatarUrl?: string;
  status: ResearchExecutionStatus;
  action?: string;
  actionKey?: ResearchExecutionActionKey;
  actionDetail?: string;
  failureReasonKey?: ResearchExecutionFailureKey;
  timeKey: ResearchExecutionTimeKey;
  currentNodeId?: string;
  locationLabel?: string;
};

export const researchExecutionPanelFixture = [
  {
    id: "queued",
    name: "Ada",
    role: "资料检索",
    status: "queued",
    action: "等待行业数据库检索名额",
    actionDetail: "将在竞品定价核验完成后开始检索。",
    timeKey: "queued",
    locationLabel: "资料分支",
  },
  {
    id: "running",
    name: "Lin",
    role: "交叉验证",
    status: "running",
    action: "核验 2026 年企业版定价与合同限制",
    actionDetail:
      "正在对照官方定价页、服务条款和三份公开采购合同，标记口径差异与生效日期。",
    timeKey: "running",
    locationLabel: "证据节点 12",
  },
  {
    id: "done",
    name: "Mina",
    role: "访谈归纳",
    status: "done",
    action: "整理 14 份用户访谈中的迁移阻力",
    actionDetail: "已归并为权限、数据迁移和培训成本三个主题。",
    timeKey: "recent",
    locationLabel: "洞察分支",
  },
  {
    id: "failed",
    name: "Owen",
    role: "数据分析",
    status: "failed",
    action: "计算样本留存率与置信区间",
    actionDetail: "原始 CSV 包含重复表头，解析在第 482 行停止。",
    failureReasonKey: "failed",
    timeKey: "failed",
    locationLabel: "分析节点 7",
  },
  {
    id: "stale",
    name: "Ravi",
    role: "网页研究",
    status: "stale",
    action:
      "Review the complete multilingual compliance documentation and verify every regional data-residency exception",
    actionDetail:
      "The source page stopped responding while the agent was checking the APAC hosting matrix.",
    timeKey: "stale",
    locationLabel: "合规分支",
  },
  {
    id: "idle",
    name: "苏澄",
    role: "报告编辑",
    status: "idle",
    action: "等待可编辑的报告提纲",
    actionDetail: "当前没有可领取的小任务。",
    timeKey: "idle",
  },
] satisfies readonly ResearchExecutionAgent[];
