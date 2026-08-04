/**
 * LRM-1252 gate-shot harness — mounts the REAL `ResearchStageTimeline` and
 * `ExplorationRail` so the 11px labels get their true rendered color.
 *
 * Why a browser is required: the defect was `text-muted-foreground/80` sitting
 * under an ancestor `opacity-75` (effective alpha 0.60). jsdom neither resolves
 * the `--muted-foreground` token nor composites ancestor opacity, so only live
 * Chromium can produce the color the user actually sees.
 *
 * `?theme=dark` puts `.dark` on <html>; default is light.
 *
 * Temporary tooling: delete after the shots are attached to LRM-1252.
 */
import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { I18nextProvider } from "react-i18next";
import { createI18n } from "../../packages/core/i18n/create-i18n";
import { ResearchStageTimeline } from "../../packages/views/research/components/research-stage-timeline";
import { ExplorationRail } from "../../packages/views/research/components/exploration-rail";
import type { ExplorationDimension } from "../../packages/views/research/lib/m2-visibility";
import zhResearch from "../../packages/views/locales/zh-Hans/research.json";
import zhCommon from "../../packages/views/locales/zh-Hans/common.json";
import "./harness.css";

const i18n = createI18n("zh-Hans", {
  "zh-Hans": { research: zhResearch, common: zhCommon },
});

/**
 * `s2_sources` on a running session is the real product state that produces all
 * three step states at once: S1 done · S2 current · S3/S4 upcoming.
 */
const CURRENT_STAGE = "s2_sources";

/**
 * One dimension WITHOUT `findingSummary` renders the pending-summary branch
 * (the second defect site); one WITH a summary keeps the solid-text control in
 * the same shot so the weakening hierarchy stays visible.
 */
const dimensions: ExplorationDimension[] = [
  {
    family: "cost",
    title: "成本与资源估算",
    status: "open",
    required: true,
    questions: [
      { id: "q-cost-1", title: "单位算力成本区间", nodeType: "question" },
      { id: "q-cost-2", title: "人力投入下限", nodeType: "question" },
    ],
  },
  {
    family: "market",
    title: "市场与竞品",
    status: "covered",
    questions: [{ id: "q-market-1", title: "头部玩家定价带", nodeType: "question" }],
    findingSummary:
      "三家头部公开定价集中在 19–49 美元/席位，折扣主要出现在年付与 100 席以上。",
  },
];

const params = new URLSearchParams(window.location.search);
if (params.get("theme") === "dark") {
  document.documentElement.classList.add("dark");
} else {
  document.documentElement.classList.add("light");
}

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <I18nextProvider i18n={i18n}>
      <div className="flex min-h-screen w-full flex-col bg-background">
        <div data-case="timeline">
          <ResearchStageTimeline
            currentStage={CURRENT_STAGE}
            sessionStatus="running"
            onSelectStage={() => {}}
          />
        </div>
        <div className="flex-1 p-4" data-case="rail">
          <ExplorationRail
            dimensions={dimensions}
            sessionStatus="running"
            selectedFamily="cost"
            className="max-w-[380px]"
          />
        </div>
      </div>
    </I18nextProvider>
  </StrictMode>,
);
