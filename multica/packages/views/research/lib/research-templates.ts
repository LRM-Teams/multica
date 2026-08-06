/**
 * LRM-817 / LRM-906: homepage quick-start templates.
 * Long professional prompts (≥800 chars) live in research-template-prompts.ts.
 * Cards never dump the full prompt into the composer — chip + hidden goal (T2).
 */
import { RESEARCH_TEMPLATE_PROMPTS } from "./research-template-prompts";

export type ResearchTemplate = {
  id: string;
  /** Short card title (zh required by AC). */
  title: { zh: string; en: string };
  /** One-line card blurb. */
  blurb: { zh: string; en: string };
  /** Session title seed (optional create payload). */
  sessionTitle: { zh: string; en: string };
  /** Goal prompt prefilled into the composer (hidden behind chip). */
  goal: { zh: string; en: string };
  /** Recommended focus params — baked into the hidden goal text. */
  params: { zh: string[]; en: string[] };
};

export const RESEARCH_TEMPLATES: readonly ResearchTemplate[] = [
  {
    id: "industry",
    title: { zh: "行业调研", en: "Industry research" },
    blurb: { zh: "市场规模、格局与趋势", en: "Market size, landscape, trends" },
    sessionTitle: { zh: "行业调研", en: "Industry research" },
    goal: RESEARCH_TEMPLATE_PROMPTS.industry,
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
    goal: RESEARCH_TEMPLATE_PROMPTS.competitor,
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
    goal: RESEARCH_TEMPLATE_PROMPTS.tech_selection,
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

/** Compose the hidden professional goal (params appended). Never dump into the textarea. */
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

/** Merge hidden template prompt with the user's short goal line for create. */
export function buildCreateGoal(
  template: ResearchTemplate | null | undefined,
  userGoal: string,
  language: string | undefined,
): string {
  const user = userGoal.trim();
  if (!template) return user;
  const prompt = composeTemplateGoal(template, language);
  if (!user) return prompt;
  const label = (language ?? "en").toLowerCase().startsWith("zh")
    ? "用户补充目标"
    : "User goal";
  return `${prompt}\n\n${label}：\n${user}`;
}
