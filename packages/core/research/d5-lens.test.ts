import { describe, expect, it } from "vitest";
import {
  DEFAULT_RESEARCH_D5_LENS,
  RESEARCH_D5_LENSES,
  isResearchD5Lens,
} from "./d5-lens";

describe("d5-lens", () => {
  it("defaults to relations", () => {
    expect(DEFAULT_RESEARCH_D5_LENS).toBe("relations");
  });

  it("validates known lens ids", () => {
    for (const lens of RESEARCH_D5_LENSES) {
      expect(isResearchD5Lens(lens)).toBe(true);
    }
    expect(isResearchD5Lens("nope")).toBe(false);
  });
});
