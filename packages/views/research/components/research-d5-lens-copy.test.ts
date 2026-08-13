import { describe, expect, it } from "vitest";
import enResearch from "../../locales/en/research.json";
import zhResearch from "../../locales/zh-Hans/research.json";

describe("D5 lens product copy", () => {
  it("names the lineage lens as round lineage in both supported locales", () => {
    expect(enResearch.d5.lens.lineage).toBe("Round lineage");
    expect(zhResearch.d5.lens.lineage).toBe("轮次谱系");
  });
});
