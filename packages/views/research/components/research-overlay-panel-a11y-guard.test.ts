/**
 * LRM-1290 — [巡检] source guard: desktop bare overlays must reuse
 * `useOverlayPanelA11y` (no second Esc/focus trap implementation).
 */
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

const here = path.dirname(fileURLToPath(import.meta.url));

function read(name: string) {
  return fs.readFileSync(path.join(here, name), "utf8");
}

describe("LRM-1290 overlay panel a11y reuse guard", () => {
  it("aux + chat desktop asides import and call useOverlayPanelA11y", () => {
    for (const file of [
      "research-aux-drawer.tsx",
      "research-chat-drawer.tsx",
    ] as const) {
      const src = read(file);
      expect(src, file).toMatch(/useOverlayPanelA11y/);
      expect(src, file).toMatch(/<aside[\s\S]*ref=\{bindPanel\}/);
      expect(src, file).not.toMatch(/focus-trap|FocusTrap|createFocusTrap/);
    }
  });

  it("node-detail overlay-card reuses the same hook (non-modal dialog)", () => {
    const src = read("research-node-detail.tsx");
    expect(src).toMatch(/useOverlayPanelA11y/);
    expect(src).toMatch(/mode === "overlay-card"/);
    expect(src).toMatch(/ref=\{bindPanel\}/);
    expect(src).not.toMatch(/focus-trap|FocusTrap|createFocusTrap/);
  });

  it("aux drawer close lucide is decorative (aria-hidden) under a labeled button", () => {
    const src = read("research-aux-drawer.tsx");
    // LRM-1329: close label is panel-specific (sources → aux_close_sources).
    expect(src).toMatch(/aria-label=\{closeLabel\}/);
    expect(src).toMatch(/panel\.aux_close/);
    expect(src).toMatch(/<X className="size-4" aria-hidden/);
  });
});
