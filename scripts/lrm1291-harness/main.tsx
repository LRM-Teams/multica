/**
 * LRM-1291 gate-shot harness — mounts the REAL `ResearchStageTimeline` so the
 * stage energy track gets its true rendered colors, geometry and animation.
 *
 * Why a browser is required: every claim in this slice is unobservable in jsdom.
 * The band colors come from `--research-stage-*` (authored `oklch()` in dark)
 * and `color-mix()`; the "one moving part" rule is a computed `animation-name`;
 * the "no overflow at 360" rule is layout; the reduced-motion downgrade is a
 * media query. jsdom resolves none of those, so a unit test can only guard
 * class names — the numbers have to come from live Chromium.
 *
 * `?theme=dark` puts `.dark` on <html>; default is light.
 *
 * Three cases are mounted at once so one shot covers the AC state matrix:
 *   first-current  → S1 current, S2–S4 upcoming (fresh session)
 *   mid-running    → S1 done, S2 current, S3/S4 upcoming
 *   all-done       → completed session, all four done, zero animation
 *
 * Temporary tooling: delete after the shots are attached to LRM-1291/1271.
 */
import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { I18nextProvider } from "react-i18next";
import { createI18n } from "../../packages/core/i18n/create-i18n";
import { ResearchStageTimeline } from "../../packages/views/research/components/research-stage-timeline";
import zhResearch from "../../packages/views/locales/zh-Hans/research.json";
import zhCommon from "../../packages/views/locales/zh-Hans/common.json";
import "./harness.css";

const i18n = createI18n("zh-Hans", {
  "zh-Hans": { research: zhResearch, common: zhCommon },
});

const params = new URLSearchParams(window.location.search);
if (params.get("theme") === "dark") {
  document.documentElement.classList.add("dark");
} else {
  document.documentElement.classList.add("light");
}

const cases = [
  { id: "first-current", currentStage: "s1_plan", sessionStatus: "running" },
  { id: "mid-running", currentStage: "s2_sources", sessionStatus: "running" },
  { id: "all-done", currentStage: "s4_delivery", sessionStatus: "completed" },
] as const;

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <I18nextProvider i18n={i18n}>
      <div className="flex min-h-screen w-full flex-col bg-background">
        {cases.map((c) => (
          <div key={c.id} data-case={c.id} className="mb-6">
            <p className="px-3 pt-3 pb-1 font-mono text-[10px] text-muted-foreground">
              {c.id}
            </p>
            <ResearchStageTimeline
              currentStage={c.currentStage}
              sessionStatus={c.sessionStatus}
              onSelectStage={() => {}}
            />
          </div>
        ))}
      </div>
    </I18nextProvider>
  </StrictMode>,
);
