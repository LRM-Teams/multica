import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";

const source = readFileSync(
  new URL("./research-constellation-workspace.tsx", import.meta.url),
  "utf8",
);

describe("D5 rail toggle visibility", () => {
  it("does not overlay a duplicate toggle on an open desktop rail", () => {
    const toggle = source.slice(
      source.indexOf("{isMobile || !railOpen ? ("),
      source.indexOf("{showDesktopRail ? ("),
    );
    expect(toggle).toContain('data-testid="research-d5-rail-toggle"');
    expect(toggle).toContain("isMobile || !railOpen");
    expect(toggle).toContain("railToggleRef");
  });
});
