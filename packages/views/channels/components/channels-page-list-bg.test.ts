// @vitest-environment node
import { readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

/**
 * Messages list pane must stay on the product surface so it does not merge
 * with app-sidebar chrome. Search stays an explicit field on that plane.
 */
const here = dirname(fileURLToPath(import.meta.url));

describe("channels-page listPane bg", () => {
  const src = readFileSync(resolve(here, "./channels-page.tsx"), "utf8");

  it("listPane uses CONVERSATION_LIST_PANE_BG + border-border, not sidebar chrome", () => {
    expect(src).toMatch(
      /flex flex-1 min-h-0 flex-col",\s*CONVERSATION_LIST_PANE_BG,/,
    );
    expect(src).toContain("border-r border-border");
    expect(src).not.toMatch(/flex flex-1 min-h-0 flex-col bg-sidebar/);
    expect(src).not.toMatch(/flex flex-1 min-h-0 flex-col bg-muted\/20/);
  });

  it("sidebar search Input is explicit bg-background (inset on chrome)", () => {
    expect(src).toMatch(/className="h-9 bg-background pl-8"/);
  });

  it("main conversation pane stays bg-background", () => {
    expect(src).toMatch(
      /className="relative flex flex-1 min-h-0 min-w-0 flex-col bg-background"/,
    );
  });
});
