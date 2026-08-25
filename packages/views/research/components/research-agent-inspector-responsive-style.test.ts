// @vitest-environment node

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
    const inspectorRule = css.match(/\.research-agent-inspector\s*\{[^}]*\}/s)?.[0];

    expect(component).toContain(
      'data-testid="research-agent-inspector-content"',
    );
    expect(component).toContain('className="min-w-0 [overflow-wrap:anywhere]"');

    expect(component).toContain('showCloseButton={false}');
    expect(component).toContain('$.d5.inspector.execution_details');
    expect(inspectorRule).toMatch(/width:\s*min\(24rem, calc\(100% - 2rem\)\)/);
    expect(inspectorRule).toMatch(/max-height:\s*calc\(100% - 8\.5rem\)/);
    expect(inspectorRule).not.toMatch(/linear-gradient|box-shadow/);
    expect(css).not.toMatch(/\.research-agent-inspector-content \.agent-/);
    expect(css).not.toMatch(/\.research-agent-inspector-content \.work-/);
  });
});
