// @vitest-environment node
import { describe, it, expect } from "vitest";
import {
  RESEARCH_TEMPLATES,
  buildCreateGoal,
  composeTemplateGoal,
  composeTemplateStarter,
  localizeTemplateField,
} from "./research-templates";

describe("research templates (LRM-817 / LRM-906)", () => {
  it("ships at least 3 Chinese-titled templates with ≥800-char zh prompts", () => {
    expect(RESEARCH_TEMPLATES.length).toBeGreaterThanOrEqual(3);
    for (const t of RESEARCH_TEMPLATES) {
      expect(t.title.zh.trim().length).toBeGreaterThan(0);
      expect(t.goal.zh.trim().length).toBeGreaterThanOrEqual(800);
      expect(t.goal.en.trim().length).toBeGreaterThanOrEqual(800);
      expect(t.params.zh.length).toBeGreaterThan(0);
    }
    const titles = RESEARCH_TEMPLATES.map((t) => t.title.zh);
    expect(titles).toEqual(expect.arrayContaining(["行业调研", "竞品分析", "技术选型"]));
  });

  it("does not pad long prompts by repeating instructions", () => {
    for (const template of RESEARCH_TEMPLATES) {
      for (const prompt of [template.goal.zh, template.goal.en]) {
        const sentences = prompt
          .split(/[。！？.!?]+/u)
          .map((sentence) => sentence.trim())
          .filter((sentence) => sentence.length >= 12);
        const uniqueSentences = new Set(sentences);
        expect(uniqueSentences.size, `${template.id} contains repeated sentences`).toBe(
          sentences.length,
        );
      }
    }
  });

  it("composeTemplateGoal includes params and localizes", () => {
    const industry = RESEARCH_TEMPLATES.find((t) => t.id === "industry")!;
    const zh = composeTemplateGoal(industry, "zh-Hans");
    expect(zh.startsWith(industry.goal.zh.trim())).toBe(true);
    expect(zh).toContain("关注维度");
    expect(zh).toContain("市场规模");
    const en = composeTemplateGoal(industry, "en");
    expect(en.startsWith(industry.goal.en.trim())).toBe(true);
    expect(en).toContain("Focus");
  });

  it("composeTemplateStarter writes short prefill (LRM-1092), not the long prompt", () => {
    const industry = RESEARCH_TEMPLATES.find((t) => t.id === "industry")!;
    const zh = composeTemplateStarter(industry, "zh-Hans");
    expect(zh).toContain("市场规模、格局与趋势");
    expect(zh).toContain("行业调研");
    expect(zh.length).toBeLessThan(200);
    const en = composeTemplateStarter(industry, "en");
    expect(en).toContain("Industry research");
    expect(en.length).toBeLessThan(200);
  });

  it("buildCreateGoal merges template prompt with user supplement without dumping into UI", () => {
    const industry = RESEARCH_TEMPLATES.find((t) => t.id === "industry")!;
    expect(buildCreateGoal(null, "  only user  ", "en")).toBe("only user");
    const withTpl = buildCreateGoal(industry, "页游可行性", "zh-Hans");
    expect(withTpl.startsWith(industry.goal.zh.trim())).toBe(true);
    expect(withTpl).toContain("用户补充目标");
    expect(withTpl).toContain("页游可行性");
    expect(buildCreateGoal(industry, "", "en").length).toBeGreaterThan(800);
  });

  it("localizeTemplateField picks zh vs en", () => {
    const field = { zh: "行业调研", en: "Industry research" };
    expect(localizeTemplateField(field, "zh-CN")).toBe("行业调研");
    expect(localizeTemplateField(field, "en-US")).toBe("Industry research");
  });
});
