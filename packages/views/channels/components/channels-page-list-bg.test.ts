// @vitest-environment node
import { readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

/**
 * LRM-551 — Messages list chrome plane (lock A from LRM-545).
 * Source contract so listPane / search stay on sidebar tokens without
 * regressing to muted/20 or transparent search on chrome.
 */
const here = dirname(fileURLToPath(import.meta.url));

describe("channels-page listPane bg (LRM-551)", () => {
  const src = readFileSync(resolve(here, "./channels-page.tsx"), "utf8");

  it("listPane uses bg-sidebar + border-border, not muted/20", () => {
    expect(src).toMatch(
      /flex flex-1 min-h-0 flex-col bg-sidebar[\s\S]*?border-r border-border/,
    );
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
