import { describe, expect, it } from "vitest";
import { readFileSync } from "node:fs";
import { join } from "node:path";

describe("research-d5-layout local theme", () => {
  it("defines scoped semantic tokens and canvas host background", () => {
    const css = readFileSync(
      join(import.meta.dirname, "research-d5-layout.css"),
      "utf8",
    );
    expect(css).toMatch(
      /\.research-d5-theme\s*\{[^}]*--foreground:\s*#[0-9a-f]{6};/is,
    );
    expect(css).toContain(".d5-canvas-host");
    expect(css).toMatch(/\.d5-canvas-host[\s\S]*background:\s*var\(--background\)/);
    expect(css).toMatch(/\.d5-lens-btn[\s\S]*color:\s*var\(--muted-foreground\)/);
    expect(css).toMatch(/\.research-agent-inspector[\s\S]*color:\s*var\(--foreground\)/);
  });

  it("lets the star graph stretch to the canvas host height", () => {
    const css = readFileSync(
      join(import.meta.dirname, "research-d5-layout.css"),
      "utf8",
    );

    expect(css).toMatch(
      /\.d5-canvas-host\s*\{[^}]*display:\s*flex;[^}]*flex-direction:\s*column;/s,
    );
  });
});
