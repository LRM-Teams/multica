/**
 * Homepage quick-start templates.
 * Decision-oriented methods live in research-template-prompts.ts.
 * The composer holds the concrete intent; submit merges it with the editable method.
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
    blurb: {
      zh: "边界、运行机制与变化",
      en: "Boundaries, mechanisms, and change",
    },
    sessionTitle: { zh: "行业调研", en: "Industry research" },
    goal: RESEARCH_TEMPLATE_PROMPTS.industry,
    params: {
      zh: ["研究边界", "驱动机制", "证据与反证", "决策含义"],
      en: ["Research scope", "Causal mechanisms", "Evidence and counterevidence", "Decision implications"],
    },
  },
  {
    id: "competitor",
    title: { zh: "竞品分析", en: "Competitor analysis" },
    blurb: {
      zh: "用户选择、替代方案与差异",
      en: "User choice, alternatives, and differentiation",
    },
    sessionTitle: { zh: "竞品分析", en: "Competitor analysis" },
    goal: RESEARCH_TEMPLATE_PROMPTS.competitor,
    params: {
      zh: ["替代集合", "用户任务", "可比证据", "差异化验证"],
      en: ["Alternative set", "User jobs", "Comparable evidence", "Differentiation tests"],
    },
  },
  {
    id: "tech_selection",
    title: { zh: "技术选型", en: "Tech selection" },
    blurb: {
      zh: "约束、场景验证与可逆决策",
      en: "Constraints, scenario tests, and reversibility",
    },
    sessionTitle: { zh: "技术选型", en: "Tech selection" },
    goal: RESEARCH_TEMPLATE_PROMPTS.tech_selection,
    params: {
      zh: ["真实工作负载", "硬约束", "失败模式", "迁移与可逆性"],
      en: ["Real workloads", "Hard constraints", "Failure modes", "Migration and reversibility"],
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
    ? "用户具体目标"
    : "User-specific goal";
  return `${prompt}\n\n${label}：\n${user}`;
}
