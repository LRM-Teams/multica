/**
 * LRM-1282 gate-shot harness — mounts the REAL SourceStrategyStrip +
 * HumanBoundaryCard inside a 360px drawer chrome (desktop) or full-width sheet
 * (narrow). Query params:
 *   ?mode=loading|empty|ready
 *   ?theme=light|dark
 *
 * Temporary tooling: delete after the shots are attached to LRM-1282.
 */
import { StrictMode, type ReactNode } from "react";
import { createRoot } from "react-dom/client";
import { I18nextProvider } from "react-i18next";
import { createI18n } from "../../packages/core/i18n/create-i18n";
import type {
  HumanBoundaryModel,
  SourceStrategyModel,
} from "../../packages/views/research/lib/m2-visibility";
import { HumanBoundaryCard } from "../../packages/views/research/components/human-boundary-card";
import { SourceStrategyStrip } from "../../packages/views/research/components/source-strategy-strip";
import zhResearch from "../../packages/views/locales/zh-Hans/research.json";
import zhCommon from "../../packages/views/locales/zh-Hans/common.json";
import "./harness.css";

const i18n = createI18n("zh-Hans", {
  "zh-Hans": { research: zhResearch, common: zhCommon },
});

const params = new URLSearchParams(window.location.search);
const theme = params.get("theme") === "dark" ? "dark" : "light";
const mode = (params.get("mode") || "ready") as "loading" | "empty" | "ready";
document.documentElement.classList.add(theme);

const EMPTY_STRATEGY: SourceStrategyModel = {
  chips: [],
  whyLine: "",
  empty: true,
};

const READY_STRATEGY: SourceStrategyModel = {
  empty: false,
  whyLine: "先采信通用权威基线，再交叉领域供给数据。",
  chips: [
    {
      id: "docs",
      label: "docs",
      layer: "general",
      why: "官方文档提供可复核的规范定义。",
      samples: [
        { id: "s1", title: "RFC 9110", url: "https://www.rfc-editor.org/rfc/rfc9110" },
        { id: "s2", title: "MDN Fetch", url: "https://developer.mozilla.org/docs/Web/API/Fetch_API" },
      ],
    },
    {
      id: "marketplace",
      label: "marketplace",
      layer: "domain",
      why: "领域供给与定价只能从交易侧数据读出。",
      samples: [
        { id: "s3", title: "SteamDB charts", url: "https://steamdb.info/" },
      ],
    },
  ],
};

const EMPTY_BOUNDARY: HumanBoundaryModel = {
  aiCeiling: "",
  mustHuman: "",
  matrix: [],
  empty: true,
};

const READY_BOUNDARY: HumanBoundaryModel = {
  empty: false,
  aiCeiling: "可检索公开资料并起草对照表，但不能出具持牌合规结论。",
  mustHuman: "最终采纳、对外承诺与合规终审必须由人确认。",
  matrix: [
    { human: "锁定验收标准与风险阈值", ai: "汇总候选证据并标注冲突" },
    { human: "签署对外表述", ai: "生成可编辑的初稿段落" },
  ],
};

const strategy =
  mode === "ready" ? READY_STRATEGY : EMPTY_STRATEGY;
const boundary =
  mode === "ready" ? READY_BOUNDARY : EMPTY_BOUNDARY;
const sessionStatus =
  mode === "loading" ? "running" : mode === "empty" ? "drafting" : "done";

function DrawerChrome({ children }: { children: ReactNode }) {
  return (
    <div
      className="flex h-dvh flex-col bg-background text-foreground"
      data-testid="lrm1282-shell"
      data-mode={mode}
      data-theme={theme}
    >
      <header className="flex h-11 shrink-0 items-center border-b border-border/60 px-3 text-sm font-medium">
        调研会话 · 辅抽屉
      </header>
      <div className="relative min-h-0 flex-1 bg-muted/20">
        <div className="absolute inset-0 opacity-40" aria-hidden>
          <div className="m-4 h-24 rounded-xl border border-dashed border-border/60" />
          <div className="mx-4 h-40 rounded-xl border border-dashed border-border/60" />
        </div>
        <aside
          data-testid="research-aux-drawer-chrome"
          className="absolute inset-y-0 right-0 flex w-full max-w-[360px] flex-col border-l border-border/60 bg-background shadow-xl sm:w-[360px]"
        >
          <div className="flex h-11 shrink-0 items-center border-b border-border/55 px-3 text-sm font-semibold">
            调研依据与协作分工
          </div>
          <div className="min-h-0 flex-1 space-y-3 overflow-y-auto p-0">
            {children}
          </div>
        </aside>
      </div>
    </div>
  );
}

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <I18nextProvider i18n={i18n}>
      <DrawerChrome>
        <SourceStrategyStrip
          model={strategy}
          sessionStatus={sessionStatus}
          className="!border-b"
        />
        <div className="px-0 pb-3">
          <HumanBoundaryCard
            model={boundary}
            sessionStatus={sessionStatus}
            className="!rounded-none !border-x-0 !shadow-none"
          />
        </div>
      </DrawerChrome>
    </I18nextProvider>
  </StrictMode>,
);
