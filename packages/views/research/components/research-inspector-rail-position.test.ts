// @vitest-environment node

import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";

const css = readFileSync(
  new URL("./research-d5-layout.css", import.meta.url),
  "utf8",
);

describe("D5 Agent inspector rail-safe position", () => {
  it("tracks closed, compact, and wide context-rail widths", () => {
    expect(css).toMatch(
      /\.d5-workspace\s*\{[^}]*--d5-inspector-right:\s*24px/s,
    );
    expect(css).toMatch(
      /\.d5-workspace\[data-d5-rail-open="true"\]\s*\{[^}]*--d5-inspector-right:\s*376px/s,
    );
    expect(css).toMatch(
      /@media \(min-width: 768px\) and \(max-width: 1199px\)[\s\S]*?\.d5-workspace\[data-d5-rail-open="true"\]\s*\{[^}]*--d5-inspector-right:\s*336px/s,
    );
    expect(css).toMatch(
      /\.research-agent-inspector\s*\{[^}]*right:\s*var\(--d5-inspector-right\)/s,
    );
  });
});
