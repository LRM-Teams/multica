import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

/**
 * LRM-1374 — same defect class as LRM-1368 / LRM-1339 / LRM-1252.
 *
 * Archived channel sidebar rows used whole-button `opacity-60 hover:opacity-100`
 * while the name was already `text-muted-foreground`. Alpha multiplies through
 * the title → WCAG AA fail. Softening must be solid muted text only.
 *
 * jsdom cannot composite ancestor opacity against theme tokens, so this guard
 * is a source contract on the archived row block.
 */
describe("channels-page archived sidebar contrast (LRM-1374)", () => {
  const src = readFileSync(
    join(dirname(fileURLToPath(import.meta.url)), "channels-page.tsx"),
    "utf8",
  );

  const archivedBlock = (() => {
    const marker = 'data-testid="channel-sidebar-archived-row"';
    const start = src.indexOf(marker);
    expect(start).toBeGreaterThan(-1);
    // Capture the button opening tag through the muted title span.
    const slice = src.slice(start, start + 700);
    return slice;
  })();

  it("keeps the archived row free of row-level opacity-*", () => {
    expect(archivedBlock).not.toMatch(/\bopacity-\d+\b/);
    expect(archivedBlock).not.toMatch(/hover:opacity-/);
  });

  it("softens archived names with solid muted text", () => {
    expect(archivedBlock).toMatch(/text-muted-foreground/);
  });
});
