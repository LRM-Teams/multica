// @vitest-environment node

import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";

const source = readFileSync(
  new URL("./research-constellation-workspace.tsx", import.meta.url),
  "utf8",
);

describe("D5 canvas rail-safe region", () => {
  it("does not subtract the sibling context rail twice", () => {
    expect(source).toContain("hostRef observes the canvas flex child");
    expect(source).toContain("rightPanelWidth: 0");
    expect(source).toContain("rightPanelWidth={0}");
    expect(source).not.toContain("effectiveRailWidth");
  });
});
