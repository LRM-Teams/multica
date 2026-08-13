import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";

const component = readFileSync(
  new URL("./research-agent-inspector.tsx", import.meta.url),
  "utf8",
);
const css = readFileSync(
  new URL("./research-d5-layout.css", import.meta.url),
  "utf8",
);

describe("D5 Agent inspector shared responsive styling", () => {
  it("uses one content boundary for desktop overlay and mobile sheet", () => {
    expect(component).toContain(
      'data-testid="research-agent-inspector-content"',
    );
    expect(component).toContain('className="min-w-0 [overflow-wrap:anywhere]"');

    for (const selector of [
      "agent-head",
      "agent-body",
      "agent-objective",
      "work-item",
      "agent-foot",
    ]) {
      expect(css).toContain(`.research-agent-inspector-content .${selector}`);
    }
    expect(css).not.toMatch(/\.research-agent-inspector \.agent-/);
    expect(css).not.toMatch(/\.research-agent-inspector \.work-/);
  });
});
