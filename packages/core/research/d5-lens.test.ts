import { describe, expect, it } from "vitest";
import {
  DEFAULT_RESEARCH_D5_LENS,
  RESEARCH_D5_LENSES,
  isResearchD5Lens,
} from "./d5-lens";

describe("d5-lens", () => {
  it("defaults to agent", () => {
    expect(DEFAULT_RESEARCH_D5_LENS).toBe("agent");
  });

  it("validates known lens ids", () => {
    expect(RESEARCH_D5_LENSES).toEqual(["agent", "lineage"]);
    for (const lens of RESEARCH_D5_LENSES) {
      expect(isResearchD5Lens(lens)).toBe(true);
    }
    expect(isResearchD5Lens("relations")).toBe(false);
    expect(isResearchD5Lens("confidence")).toBe(false);
    expect(isResearchD5Lens("nope")).toBe(false);
  });
});
