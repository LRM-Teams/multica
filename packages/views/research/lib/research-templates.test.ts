// @vitest-environment node
import { describe, it, expect } from "vitest";
import {
  RESEARCH_TEMPLATES,
  RESEARCH_TEMPLATE_MIN_HAN,
  buildCreateGoal,
  composeTemplateGoal,
  composeTemplateStarter,
  countHanChars,
  isTemplatePromptAboveMinHan,
  localizeTemplateField,
} from "./research-templates";

describe("research templates (LRM-817 / LRM-906 / LRM-1139)", () => {
  it("ships 3 Chinese-titled templates with ≥3000-han zh playbooks", () => {
    expect(RESEARCH_TEMPLATES.length).toBeGreaterThanOrEqual(3);
    expect(RESEARCH_TEMPLATE_MIN_HAN).toBe(3000);
    for (const t of RESEARCH_TEMPLATES) {
      expect(t.title.zh.trim().length).toBeGreaterThan(0);
      expect(countHanChars(t.goal.zh)).toBeGreaterThanOrEqual(
        RESEARCH_TEMPLATE_MIN_HAN,
      );
      expect(isTemplatePromptAboveMinHan(t.goal.zh)).toBe(true);
      expect(t.goal.en.trim().length).toBeGreaterThanOrEqual(800);
      expect(t.params.zh.length).toBeGreaterThan(0);
      // Structure gates from LRM-1140 SoT
      for (const section of ["角色", "目标", "方法", "证据", "输出", "边界"]) {
        expect(t.goal.zh).toContain(`【${section}】`);
      }
      // No filler loops
      expect(t.goal.zh.includes("补充要求：保持结构清晰")).toBe(false);
    }
    const titles = RESEARCH_TEMPLATES.map((t) => t.title.zh);
    expect(titles).toEqual(expect.arrayContaining(["行业调研", "竞品分析", "技术选型"]));
  });

  it("composeTemplateGoal includes params and localizes", () => {
    const industry = RESEARCH_TEMPLATES.find((t) => t.id === "industry")!;
    const zh = composeTemplateGoal(industry, "zh-Hans");
    expect(zh.startsWith(industry.goal.zh.trim())).toBe(true);
    expect(zh).toContain("关注维度");
    expect(zh).toContain("市场规模");
    expect(countHanChars(zh)).toBeGreaterThanOrEqual(RESEARCH_TEMPLATE_MIN_HAN);
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
    expect(countHanChars(zh)).toBeLessThan(RESEARCH_TEMPLATE_MIN_HAN);
    const en = composeTemplateStarter(industry, "en");
    expect(en).toContain("Industry research");
    expect(en.length).toBeLessThan(200);
  });

  it("buildCreateGoal merges template prompt (or override) with user supplement", () => {
    const industry = RESEARCH_TEMPLATES.find((t) => t.id === "industry")!;
    expect(buildCreateGoal(null, "  only user  ", "en")).toBe("only user");
    const withTpl = buildCreateGoal(industry, "页游可行性", "zh-Hans");
    expect(withTpl.startsWith(industry.goal.zh.trim())).toBe(true);
    expect(withTpl).toContain("用户补充目标");
    expect(withTpl).toContain("页游可行性");
    expect(countHanChars(buildCreateGoal(industry, "", "zh-Hans"))).toBeGreaterThanOrEqual(
      RESEARCH_TEMPLATE_MIN_HAN,
    );
    const overridden = buildCreateGoal(
      industry,
      "短意图",
      "zh-Hans",
      `${industry.goal.zh}\n\n【用户改写】自定义完整模板段落。`,
    );
    expect(overridden).toContain("【用户改写】");
    expect(overridden).toContain("短意图");
  });

  it("localizeTemplateField picks zh vs en", () => {
    const field = { zh: "行业调研", en: "Industry research" };
    expect(localizeTemplateField(field, "zh-CN")).toBe("行业调研");
    expect(localizeTemplateField(field, "en-US")).toBe("Industry research");
  });
});
