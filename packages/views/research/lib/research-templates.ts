/**
 * LRM-817: homepage quick-start templates.
 * Edit this file to add/retune templates — titles/goals/params stay frontend-local.
 * AC requires Chinese copy; `en` fields keep other locales readable.
 */
export type ResearchTemplate = {
  id: string;
  /** Short card title (zh required by AC). */
  title: { zh: string; en: string };
  /** One-line card blurb. */
  blurb: { zh: string; en: string };
  /** Session title seed (optional create payload). */
  sessionTitle: { zh: string; en: string };
  /** Goal prompt prefilled into the composer. */
  goal: { zh: string; en: string };
  /** Recommended focus params — editable after fill (baked into goal text). */
  params: { zh: string[]; en: string[] };
};

export const RESEARCH_TEMPLATES: readonly ResearchTemplate[] = [
  {
    id: "industry",
    title: { zh: "行业调研", en: "Industry research" },
    blurb: { zh: "市场规模、格局与趋势", en: "Market size, landscape, trends" },
    sessionTitle: { zh: "行业调研", en: "Industry research" },
    goal: {
      zh: "请围绕目标行业做一轮结构化调研：界定市场边界，估算规模与增速，梳理主要玩家与商业模式，并总结近 12 个月的关键趋势与不确定因素。",
      en: "Run a structured industry study: define the market, estimate size and growth, map major players and business models, and summarize key trends and uncertainties over the last 12 months.",
    },
    params: {
      zh: ["市场规模", "竞争格局", "增长驱动", "风险与不确定因素"],
      en: ["Market size", "Competitive landscape", "Growth drivers", "Risks & unknowns"],
    },
  },
  {
    id: "competitor",
    title: { zh: "竞品分析", en: "Competitor analysis" },
    blurb: { zh: "对手对比、差异与机会", en: "Compare rivals, gaps, opportunities" },
    sessionTitle: { zh: "竞品分析", en: "Competitor analysis" },
    goal: {
      zh: "请对目标品类做竞品分析：列出主要竞品，对比功能/定价/获客与口碑，指出差异化机会与我们应优先跟进或避开的点。",
      en: "Analyze competitors in the target category: list major rivals, compare features/pricing/GTM and sentiment, and call out differentiation opportunities we should chase or avoid.",
    },
    params: {
      zh: ["功能对比", "定价策略", "获客渠道", "口碑与评价"],
      en: ["Feature comparison", "Pricing", "Go-to-market", "Sentiment"],
    },
  },
  {
    id: "tech_selection",
    title: { zh: "技术选型", en: "Tech selection" },
    blurb: { zh: "方案对比与落地建议", en: "Options, trade-offs, recommendation" },
    sessionTitle: { zh: "技术选型", en: "Tech selection" },
    goal: {
      zh: "请做一轮技术选型调研：明确问题与约束，对比候选方案的能力、成本、生态与落地风险，并给出可执行的推荐与迁移注意点。",
      en: "Run a tech-selection study: clarify the problem and constraints, compare candidate options on capability, cost, ecosystem and risk, and recommend an actionable path with migration notes.",
    },
    params: {
      zh: ["能力边界", "成本与性能", "生态成熟度", "迁移风险"],
      en: ["Capability fit", "Cost & performance", "Ecosystem maturity", "Migration risk"],
    },
  },
] as const;

export function localizeTemplateField<T>(
  field: { zh: T; en: T },
  language: string | undefined,
): T {
  const lang = (language ?? "en").toLowerCase();
  return lang.startsWith("zh") ? field.zh : field.en;
}

/** Compose an editable goal that includes recommended params. */
export function composeTemplateGoal(
  template: ResearchTemplate,
  language: string | undefined,
): string {
  const goal = localizeTemplateField(template.goal, language).trim();
  const params = localizeTemplateField(template.params, language);
  if (params.length === 0) return goal;
  const label = (language ?? "en").toLowerCase().startsWith("zh") ? "关注维度" : "Focus";
  return `${goal}\n\n${label}：${params.join("、")}`;
}
