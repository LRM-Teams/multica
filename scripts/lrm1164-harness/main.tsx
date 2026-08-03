/**
 * LRM-1164 AC4 gate-shot harness — renders the REAL report reader and the REAL
 * session list row + row skeleton so Playwright can capture the 700 / 767 / 768
 * tiers with live Tailwind media queries (jsdom cannot evaluate `md:`).
 *
 * `?case=report` → ReportReader (outline drawer vs 220px aside)
 * `?case=list`   → ResearchSessionRow ×2 + ResearchSessionRowSkeleton ×2
 *
 * Temporary tooling: delete after the shots are attached to LRM-1164.
 */
import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { I18nextProvider } from "react-i18next";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { createI18n } from "../../packages/core/i18n/create-i18n";
import { ApiClient } from "../../packages/core/api/client";
import { setApiInstance } from "../../packages/core/api";
import { createAuthStore, registerAuthStore } from "../../packages/core/auth";
import { defaultStorage } from "../../packages/core/platform/storage";
import { WorkspaceSlugProvider } from "../../packages/core/paths/hooks";
import { workspaceKeys } from "../../packages/core/workspace/queries";
import type {
  ResearchReport,
  ResearchSession,
  ResearchSource,
} from "../../packages/core/types";
import type { Workspace } from "../../packages/core/types/workspace";
import { NavigationProvider } from "../../packages/views/navigation/context";
import { ReportReader } from "../../packages/views/research/report/report-reader";
import { ResearchSessionRow } from "../../packages/views/research/components/research-session-row";
import {
  ResearchSessionRowSkeleton,
} from "../../packages/views/research/components/research-session-row-skeleton";
import zhResearch from "../../packages/views/locales/zh-Hans/research.json";
import zhCommon from "../../packages/views/locales/zh-Hans/common.json";
import "./harness.css";

const i18n = createI18n("zh-Hans", {
  "zh-Hans": { research: zhResearch, common: zhCommon },
});

// Boot the singletons the real row depends on (row actions read the api client
// and auth store). No network calls are needed for the layout gate — queries
// simply stay in error/idle and the row renders its own DOM.
const api = new ApiClient("", {});
setApiInstance(api);
registerAuthStore(createAuthStore({ api, storage: defaultStorage }));

const workspace: Workspace = {
  id: "11111111-1111-4111-8111-111111111111",
  name: "Demo",
  slug: "demo",
  description: null,
  context: null,
  settings: {},
  repos: [],
  issue_prefix: "LRM",
  avatar_url: null,
  created_at: "2026-07-01T00:00:00Z",
  updated_at: "2026-07-01T00:00:00Z",
};

const queryClient = new QueryClient({
  defaultOptions: { queries: { retry: false, refetchOnWindowFocus: false } },
});
queryClient.setQueryData(workspaceKeys.list(), [workspace]);

const navigation = {
  push: () => {},
  replace: () => {},
  back: () => {},
  pathname: "/demo/research",
  openInNewTab: () => {},
  prefetch: () => {},
} as never;

const report: ResearchReport = {
  id: "r1",
  session_id: "s1",
  revision: 3,
  content_md: [
    "## 结论摘要",
    "",
    "国内小型储能柜市场 2026 上半年出货同比 +38%，价格战主要发生在 100–215kWh 段。",
    "",
    "## 市场格局",
    "",
    "头部三家合计份额 51%，二线厂商靠渠道下沉抢增量。",
    "",
    "### 份额与增速",
    "",
    "增速最快的是工商业侧，年化 45%。",
    "",
    "## 成本结构",
    "",
    "电芯占 BOM 58%，PCS 占 14%，结构件与温控合计 19%。",
    "",
    "## 风险与不确定",
    "",
    "碳酸锂价格波动仍是最大变量；二线厂商现金流承压。",
  ].join("\n"),
  structured: {},
  created_at: "2026-08-01T00:00:00Z",
  updated_at: "2026-08-02T00:00:00Z",
};

const sources: ResearchSource[] = [
  {
    id: "src1",
    session_id: "s1",
    url: "https://example.com/report-a",
    title: "2026H1 储能柜出货追踪",
    source_class: "docs",
    credibility_weight: 0.9,
    stance: "neutral",
    relevance: 0.92,
    summary: "季度出货与价格带拆分。",
    excerpt: "",
    payload: {},
    created_at: "2026-08-01T00:00:00Z",
    updated_at: "2026-08-01T00:00:00Z",
  },
  {
    id: "src2",
    session_id: "s1",
    url: "https://example.com/report-b",
    title: "电芯价格月报",
    source_class: "market",
    credibility_weight: 0.8,
    stance: "neutral",
    relevance: 0.81,
    summary: "碳酸锂与电芯报价。",
    excerpt: "",
    payload: {},
    created_at: "2026-08-01T00:00:00Z",
    updated_at: "2026-08-01T00:00:00Z",
  },
];

function session(overrides: Partial<ResearchSession>): ResearchSession {
  return {
    id: "s1",
    workspace_id: workspace.id,
    fleet_id: "fleet-1",
    created_by: "user-1",
    title: "国内小型储能柜市场格局与成本结构",
    goal: "锁定份额、增速与 BOM 成本",
    status: "running",
    current_stage: "s2_sources",
    project_id: null,
    channel_id: null,
    handoff_summary: null,
    created_at: "2026-08-01T00:00:00Z",
    updated_at: "2026-08-03T03:00:00Z",
    fleet_preview: [
      { agent_id: "agent-1", display_name: "罗纳尔多", is_lead: true },
      { agent_id: "agent-2", display_name: "分析师 A", is_lead: false },
    ],
    ...overrides,
  } as ResearchSession;
}

const rows: ResearchSession[] = [
  session({}),
  session({
    id: "s2",
    title: "竞品定价与渠道策略对比",
    status: "awaiting_user_confirm",
    current_stage: "s3_validation",
    updated_at: "2026-08-03T01:20:00Z",
  }),
  session({
    id: "s3",
    title: "海外工商业储能准入门槛清单",
    status: "completed",
    current_stage: "s4_delivery",
    updated_at: "2026-08-02T09:05:00Z",
  }),
];

function ListCase() {
  return (
    <div className="mx-auto w-full max-w-[1120px] px-3 py-4 md:px-5">
      <h1 className="mb-1 text-sm font-semibold">调研会话列表</h1>
      <p className="mb-3 text-xs text-muted-foreground">
        实行 ×3（下方 2 行为加载骨架）· 断点必须同档翻转
      </p>
      <div className="rounded-xl border" data-case="list">
        <div data-region="rows">
          {rows.map((s) => (
            <ResearchSessionRow key={s.id} session={s} href={`/demo/research/${s.id}`} />
          ))}
        </div>
        <div data-region="skeletons" className="border-t">
          <ResearchSessionRowSkeleton />
          <ResearchSessionRowSkeleton />
        </div>
      </div>
    </div>
  );
}

function ReportCase() {
  return (
    <div data-case="report" className="p-4 text-xs text-muted-foreground">
      交付报告阅读器（下方 dialog 覆盖全屏）
      <ReportReader open onClose={() => {}} report={report} sources={sources} />
    </div>
  );
}

const params = new URLSearchParams(window.location.search);
const which = params.get("case") === "report" ? "report" : "list";

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <QueryClientProvider client={queryClient}>
      <WorkspaceSlugProvider slug="demo">
        <NavigationProvider value={navigation}>
          <I18nextProvider i18n={i18n}>
            {which === "report" ? <ReportCase /> : <ListCase />}
          </I18nextProvider>
        </NavigationProvider>
      </WorkspaceSlugProvider>
    </QueryClientProvider>
  </StrictMode>,
);
