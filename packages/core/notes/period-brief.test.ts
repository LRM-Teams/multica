import { describe, expect, it } from "vitest";
import {
  PERIOD_BRIEF_FIXTURE_MARKDOWN,
  periodBriefLooksStructured,
} from "./period-brief";

describe("periodBriefLooksStructured", () => {
  it("accepts the fixture Brief with reporting headings", () => {
    expect(periodBriefLooksStructured(PERIOD_BRIEF_FIXTURE_MARKDOWN)).toBe(true);
  });

  it("rejects a raw commit dump", () => {
    const dump = [
      "a1b2c3d wire SSO",
      "e4f5a6b fix digest",
      "1122334 add denylist",
      "5566778 harvest git",
      "99aabb0 owner switch",
      "ccddeef scope remotes",
    ].join("\n");
    expect(periodBriefLooksStructured(dump)).toBe(false);
  });
});
