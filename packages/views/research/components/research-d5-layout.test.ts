import { describe, expect, it } from "vitest";
import { readFileSync } from "node:fs";
import { join } from "node:path";

describe("research-d5-layout local theme", () => {
  it("defines scoped semantic tokens and canvas host background", () => {
    const css = readFileSync(
      join(import.meta.dirname, "research-d5-layout.css"),
      "utf8",
    );
    expect(css).toContain(".research-d5-theme");
    expect(css).toContain("--foreground: #e8f1f7");
    expect(css).toContain(".d5-canvas-host");
    expect(css).toMatch(/\.d5-canvas-host[\s\S]*background:\s*var\(--background\)/);
  });
});
