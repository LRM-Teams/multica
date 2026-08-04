/**
 * LRM-1339 gate-shot harness — mounts the REAL `ResearchProductRoundCardView`
 * so the 11px / 10px summary spans get their true rendered color.
 *
 * Why a browser is required: the defect is an alpha stack on top of a semantic
 * tone. `decisionTone` gives the button `text-brand` / `text-success` /
 * `text-warning` / `text-muted-foreground` plus the matching low-alpha wash, and
 * the small spans then multiplied that tone by `opacity-80` / `opacity-70`.
 * jsdom resolves neither `oklch()` tokens nor composited alpha, so only live
 * Chromium can produce the color the user actually sees.
 *
 * `?theme=dark` puts `.dark` on <html>; default light.
 * `?case=summary` (default) renders the four decision tones as compact summary
 * rows; `?case=detail` opens the dialog with a `goal_patch` + old goal so the
 * struck-through old-goal line and the budget-capped note can be measured.
 *
 * Temporary tooling: delete after the shots are attached to LRM-1339.
 */
import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { I18nextProvider } from "react-i18next";
import { createI18n } from "../../packages/core/i18n/create-i18n";
import { ResearchProductRoundCardView } from "../../packages/views/research/components/research-product-round-card";
import type { ResearchProductRoundCard } from "../../packages/core/types";
import zhResearch from "../../packages/views/locales/zh-Hans/research.json";
import zhCommon from "../../packages/views/locales/zh-Hans/common.json";
import "./harness.css";

const i18n = createI18n("zh-Hans", {
  "zh-Hans": { research: zhResearch, common: zhCommon },
});

const base: ResearchProductRoundCard = {
  id: "c1",
  session_id: "s1",
  round_number: 2,
  decision: "continue",
  coverage_gaps: ["单位算力成本区间未定"],
  confidence_note: "证据偏弱：仅两家公开定价，缺自建成本口径",
  budget_used: 2,
  budget_remaining: 3,
  goal_patch_proposal: "收窄到成本对比",
  next_round_focus: "补成本证据",
  decided_by_agent_id: "agent-1",
  created_at: "2026-07-31T08:00:00Z",
};

/**
 * All four `decisionTone` branches, because each one is a different foreground
 * token on a matching wash — the contrast number is per-tone, not global.
 * `default` is reached with an unknown decision string, which is exactly what
 * the component's `switch` default arm renders in production.
 */
const cases: { key: string; card: ResearchProductRoundCard }[] = [
  { key: "continue", card: { ...base, decision: "continue" } },
  { key: "stop_enough", card: { ...base, decision: "stop_enough" } },
  { key: "stop_budget", card: { ...base, decision: "stop_budget" } },
  {
    key: "default",
    card: {
      ...base,
      decision: "needs_review" as ResearchProductRoundCard["decision"],
    },
  },
];

const params = new URLSearchParams(window.location.search);
if (params.get("theme") === "dark") {
  document.documentElement.classList.add("dark");
} else {
  document.documentElement.classList.add("light");
}
const detail = params.get("case") === "detail";

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <I18nextProvider i18n={i18n}>
      <div className="flex min-h-screen w-full flex-col gap-3 bg-background p-4">
        {detail ? (
          <div data-case="detail">
            <ResearchProductRoundCardView
              card={{ ...base, decision: "stop_budget" }}
              currentGoal="对比国内外全部同类方案的成本与合规差异"
              autoAdoptSeconds={0}
            />
          </div>
        ) : (
          cases.map(({ key, card }) => (
            // `compact` is the production path that renders the summary row and
            // keeps the dialog closed — i.e. the defect surface itself.
            <div key={key} data-case={key} className="max-w-[520px]">
              <ResearchProductRoundCardView card={card} compact autoAdoptSeconds={30} />
            </div>
          ))
        )}
      </div>
    </I18nextProvider>
  </StrictMode>,
);
