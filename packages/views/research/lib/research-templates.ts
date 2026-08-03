/**
 * LRM-817 / LRM-906 / LRM-1139: homepage quick-start templates.
 * Long professional prompts (zh ≥3000 汉字) live in research-template-prompts.ts.
 * A2: short intent in composer; full prompt editable via expand; submit merges both.
 */
import {
  countHanChars,
  RESEARCH_TEMPLATE_MIN_HAN,
  RESEARCH_TEMPLATE_PROMPTS,
} from "./research-template-prompts";

export { countHanChars, RESEARCH_TEMPLATE_MIN_HAN };

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

/** Compose the professional goal (params appended). Used as default full prompt. */
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

/**
 * LRM-1092 / LRM-1072 / LRM-1140 A2: short starter written into the composer on chip select.
 * Long professional prompts stay in templatePrompt / expand editor; submit via buildCreateGoal.
 */
export function composeTemplateStarter(
  template: ResearchTemplate,
  language: string | undefined,
): string {
  const blurb = localizeTemplateField(template.blurb, language);
  const title = localizeTemplateField(template.title, language);
  const params = localizeTemplateField(template.params, language);
  const focus = params.slice(0, 3);
  const isZh = (language ?? "en").toLowerCase().startsWith("zh");
  if (isZh) {
    return `围绕「${blurb}」做${title}：覆盖${focus.join("、")}，并给出可验证结论。`;
  }
  return `Research around "${blurb}" for ${title}: cover ${focus.join(", ")}, and deliver verifiable conclusions.`;
}

/**
 * Merge full template prompt with the user's short goal line for create.
 * `promptOverride` is the expand-editor draft (defaults to composeTemplateGoal).
 */
export function buildCreateGoal(
  template: ResearchTemplate | null | undefined,
  userGoal: string,
  language: string | undefined,
  promptOverride?: string | null,
): string {
  const user = userGoal.trim();
  if (!template) return user;
  const prompt = (promptOverride ?? composeTemplateGoal(template, language)).trim();
  if (!user) return prompt;
  const label = (language ?? "en").toLowerCase().startsWith("zh")
    ? "用户补充目标"
    : "User goal";
  return `${prompt}\n\n${label}：\n${user}`;
}

/** Locale-aware gate for expand-editor apply (zh: ≥3000 汉字; else length ≥800). */
export function isTemplatePromptAboveMin(
  text: string,
  language: string | undefined,
): boolean {
  const isZh = (language ?? "en").toLowerCase().startsWith("zh");
  if (isZh) return countHanChars(text) >= RESEARCH_TEMPLATE_MIN_HAN;
  return text.trim().length >= 800;
}

/** @deprecated use isTemplatePromptAboveMin — kept for zh-only call sites/tests */
export function isTemplatePromptAboveMinHan(text: string): boolean {
  return countHanChars(text) >= RESEARCH_TEMPLATE_MIN_HAN;
}
