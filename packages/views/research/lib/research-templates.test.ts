// @vitest-environment node
import { describe, expect, it } from "vitest";
import {
  RESEARCH_TEMPLATES,
  buildCreateGoal,
  composeTemplateGoal,
  composeTemplateStarter,
  localizeTemplateField,
} from "./research-templates";

const COMMON_ZH_SECTIONS = [
  "任务定义",
  "研究过程",
  "证据规则",
  "停止条件",
  "交付",
  "禁止",
] as const;

const COMMON_EN_SECTIONS = [
  "Frame the task",
  "Research process",
  "Evidence rules",
  "Stop conditions",
  "Deliverable",
  "Do not",
] as const;

const DOMAIN_CONTRACTS = {
  industry: {
    zh: ["研究边界", "相互竞争的假设", "三角验证", "反证", "决策含义"],
    en: [
      "Define scope",
      "competing hypotheses",
      "Triangulate",
      "counterevidence",
      "decision implications",
    ],
  },
  competitor: {
    zh: ["维持现状", "用户任务", "纳入、排除理由", "反证", "失败信号"],
    en: [
      "status quo",
      "user job",
      "inclusion and exclusion reasons",
      "counterevidence",
      "failure signal",
    ],
  },
  tech_selection: {
    zh: ["真实工作负载", "硬门槛", "完整代价", "失败模式和可逆性", "敏感性分析"],
    en: [
      "real workloads",
      "hard gates",
      "full cost",
      "failure modes and reversibility",
      "sensitivity analysis",
    ],
  },
} as const;

function nonEmptyLines(text: string): string[] {
  return text
    .split("\n")
    .map((line) => line.trim())
    .filter(Boolean);
}

describe("research templates", () => {
  it("ships three bounded, non-repetitive research methods with explicit quality contracts", () => {
    expect(RESEARCH_TEMPLATES.map((template) => template.id)).toEqual([
      "industry",
      "competitor",
      "tech_selection",
    ]);

    for (const template of RESEARCH_TEMPLATES) {
      expect(template.title.zh.trim()).not.toBe("");
      expect(template.params.zh.length).toBeGreaterThan(0);

      // The method must be substantial without crowding the 32 KiB goal contract.
      expect(template.goal.zh.length).toBeGreaterThan(800);
      expect(template.goal.en.length).toBeGreaterThan(1_500);
      expect(new TextEncoder().encode(template.goal.zh).byteLength).toBeLessThan(12 << 10);
      expect(new TextEncoder().encode(template.goal.en).byteLength).toBeLessThan(12 << 10);

      for (const section of COMMON_ZH_SECTIONS) {
        expect(template.goal.zh).toContain(`【${section}】`);
      }
      for (const section of COMMON_EN_SECTIONS) {
        expect(template.goal.en).toContain(`[${section}]`);
      }
      for (const marker of DOMAIN_CONTRACTS[template.id as keyof typeof DOMAIN_CONTRACTS].zh) {
        expect(template.goal.zh).toContain(marker);
      }
      for (const marker of DOMAIN_CONTRACTS[template.id as keyof typeof DOMAIN_CONTRACTS].en) {
        expect(template.goal.en).toContain(marker);
      }

      const zhLines = nonEmptyLines(template.goal.zh);
      const enLines = nonEmptyLines(template.goal.en);
      expect(new Set(zhLines).size).toBe(zhLines.length);
      expect(new Set(enLines).size).toBe(enLines.length);
      expect(template.goal.zh).not.toMatch(/深度补强|深度章：|条目\d+|权威 playbook/);
      expect(template.goal.en).not.toMatch(/authoritative playbook|item \d+/i);
    }
  });

  it("keeps each method adaptive instead of forcing generic output quotas", () => {
    for (const template of RESEARCH_TEMPLATES) {
      expect(template.goal.zh).not.toMatch(
        /通常[四4]到[八8]家|优先级使用 P0|优先级使用 P1/,
      );
      expect(template.goal.en).not.toMatch(
        /typically 4.{0,3}8|prioritize P0|prioritize P1/i,
      );
      expect(template.goal.zh).toContain("用户具体目标");
      expect(template.goal.en).toContain("User-specific goal");
    }
  });

  it("composeTemplateGoal appends method-oriented focus hints in the selected language", () => {
    const industry = RESEARCH_TEMPLATES.find((template) => template.id === "industry")!;
    const zh = composeTemplateGoal(industry, "zh-Hans");
    expect(zh.startsWith(industry.goal.zh.trim())).toBe(true);
    expect(zh).toContain("关注维度");
    expect(zh).toContain("研究边界");

    const en = composeTemplateGoal(industry, "en");
    expect(en.startsWith(industry.goal.en.trim())).toBe(true);
    expect(en).toContain("Focus");
    expect(en).toContain("Research scope");
  });

  it("composeTemplateStarter presents a short method summary, not the full prompt", () => {
    const industry = RESEARCH_TEMPLATES.find((template) => template.id === "industry")!;
    const zh = composeTemplateStarter(industry, "zh-Hans");
    expect(zh).toContain("边界、运行机制与变化");
    expect(zh).toContain("行业调研");
    expect(zh).toContain("研究边界");
    expect(zh.length).toBeLessThan(200);

    const en = composeTemplateStarter(industry, "en");
    expect(en).toContain("Industry research");
    expect(en).toContain("Research scope");
    expect(en.length).toBeLessThan(240);
  });

  it("places the user's concrete goal after the method so it remains the task authority", () => {
    const industry = RESEARCH_TEMPLATES.find((template) => template.id === "industry")!;
    expect(buildCreateGoal(null, "  only user  ", "en")).toBe("only user");

    const withTemplate = buildCreateGoal(
      industry,
      "只研究中国县域冷链的进入条件，不做全球市场规模。",
      "zh-Hans",
    );
    expect(withTemplate.startsWith(industry.goal.zh.trim())).toBe(true);
    expect(withTemplate).toContain("用户具体目标");
    expect(withTemplate.endsWith("只研究中国县域冷链的进入条件，不做全球市场规模。"))
      .toBe(true);

    const override = "根据证据建立相互竞争的解释，并报告能推翻结论的信号。";
    expect(buildCreateGoal(industry, "研究目标", "zh-Hans", override)).toBe(
      `${override}\n\n用户具体目标：\n研究目标`,
    );
  });

  it("localizeTemplateField selects Chinese only for Chinese locales", () => {
    const field = { zh: "行业调研", en: "Industry research" };
    expect(localizeTemplateField(field, "zh-CN")).toBe("行业调研");
    expect(localizeTemplateField(field, "en-US")).toBe("Industry research");
  });
});
