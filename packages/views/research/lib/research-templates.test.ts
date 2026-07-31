import { describe, it, expect } from "vitest";
import {
  RESEARCH_TEMPLATES,
  composeTemplateGoal,
  localizeTemplateField,
} from "./research-templates";

describe("research templates (LRM-817)", () => {
  it("ships at least 3 Chinese-titled templates in a frontend constant", () => {
    expect(RESEARCH_TEMPLATES.length).toBeGreaterThanOrEqual(3);
    for (const t of RESEARCH_TEMPLATES) {
      expect(t.title.zh.trim().length).toBeGreaterThan(0);
      expect(t.goal.zh.trim().length).toBeGreaterThan(0);
      expect(t.params.zh.length).toBeGreaterThan(0);
    }
    const titles = RESEARCH_TEMPLATES.map((t) => t.title.zh);
    expect(titles).toEqual(expect.arrayContaining(["行业调研", "竞品分析", "技术选型"]));
  });

  it("composeTemplateGoal includes params and localizes", () => {
    const industry = RESEARCH_TEMPLATES.find((t) => t.id === "industry")!;
    const zh = composeTemplateGoal(industry, "zh-Hans");
    expect(zh).toContain(industry.goal.zh);
    expect(zh).toContain("关注维度");
    expect(zh).toContain("市场规模");
    const en = composeTemplateGoal(industry, "en");
    expect(en).toContain(industry.goal.en);
    expect(en).toContain("Focus");
  });

  it("localizeTemplateField picks zh vs en", () => {
    const field = { zh: "行业调研", en: "Industry research" };
    expect(localizeTemplateField(field, "zh-CN")).toBe("行业调研");
    expect(localizeTemplateField(field, "en-US")).toBe("Industry research");
  });
});
