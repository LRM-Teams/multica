/**
 * LRM-1170 gate-shot harness — renders the REAL ResearchClarificationCard
 * (no hand-drawn mock) in every state the AC cares about, so Playwright can
 * capture pixel-true desktop/375 shots without the app login gate.
 *
 * Temporary tooling: delete after the gate shots are attached to LRM-1170.
 */
import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { I18nextProvider } from "react-i18next";
import { createI18n } from "../../packages/core/i18n/create-i18n";
import type { ResearchClarificationQuestion } from "../../packages/core/types";
import { ResearchClarificationCard } from "../../packages/views/research/components/research-clarification-card";
import zhResearch from "../../packages/views/locales/zh-Hans/research.json";
import "./harness.css";

const i18n = createI18n("zh-Hans", {
  "zh-Hans": { research: zhResearch },
});

const optionQuestion: ResearchClarificationQuestion = {
  question_id: "q-scope",
  prompt: "这次调研先锁定哪个方向？",
  layout: "options",
  options: [
    { id: "cost", label: "成本结构", description: "拆价格构成与单位经济性" },
    { id: "market", label: "市场格局", description: "玩家、份额与增速" },
    { id: "tech", label: "技术路线", description: "关键路径与成熟度" },
  ],
  fields: [],
  allow_skip: true,
};

const formQuestion: ResearchClarificationQuestion = {
  question_id: "q-brief",
  prompt: "补充两条约束，我就开工。",
  layout: "form",
  options: [],
  fields: [
    { id: "region", label: "目标区域", type: "text", placeholder: "如：中国大陆", required: true },
    { id: "note", label: "补充说明", type: "textarea", placeholder: "可选", required: false },
  ],
  allow_skip: true,
};

const cases = [
  {
    id: "options-pending",
    title: "options · pending（可交互，无 opacity）",
    node: <ResearchClarificationCard question={optionQuestion} resolution={{ status: "pending" }} />,
  },
  {
    id: "options-inflight",
    title: "options · in-flight（pending 提交中 → opacity-60）",
    node: (
      <ResearchClarificationCard
        question={optionQuestion}
        resolution={{ status: "pending" }}
        pending
      />
    ),
  },
  {
    id: "options-resolved-answered",
    title: "options · resolved/answered（选中项不灰，其余单一 opacity-50，skip 已移除）",
    node: (
      <ResearchClarificationCard
        question={optionQuestion}
        resolution={{
          status: "answered",
          replyMessageId: "u1",
          optionId: "cost",
          optionLabel: "成本结构",
        }}
      />
    ),
  },
  {
    id: "options-resolved-skipped",
    title: "options · resolved/skipped（全部 opacity-50，skip 已移除）",
    node: (
      <ResearchClarificationCard
        question={optionQuestion}
        resolution={{ status: "skipped", replyMessageId: "u1" }}
      />
    ),
  },
  {
    id: "form-pending",
    title: "form · pending（提交 + 跳过并排）",
    node: <ResearchClarificationCard question={formQuestion} resolution={{ status: "pending" }} />,
  },
  {
    id: "form-resolved-answered",
    title: "form · resolved/answered（skip 移除，提交独占整行）",
    node: (
      <ResearchClarificationCard
        question={formQuestion}
        resolution={{ status: "answered", replyMessageId: "u1" }}
      />
    ),
  },
];

function Harness() {
  return (
    <div className="mx-auto flex w-full max-w-[720px] flex-col gap-5 p-4">
      {cases.map((c) => (
        <section key={c.id} data-case={c.id} className="flex flex-col gap-1.5">
          <h2 className="text-xs font-semibold uppercase tracking-wide text-muted-foreground">
            {c.title}
          </h2>
          {/* Message-column width so the shot matches the real chat surface. */}
          <div className="w-full">{c.node}</div>
        </section>
      ))}
    </div>
  );
}

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <I18nextProvider i18n={i18n}>
      <Harness />
    </I18nextProvider>
  </StrictMode>,
);
