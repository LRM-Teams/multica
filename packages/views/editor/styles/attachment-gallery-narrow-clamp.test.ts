/**
 * LRM-1247 — narrow (<768) gallery clamp: aspect×max-height must not force 144-wide cells.
 * Companion to LRM-1242 R1–R4 shell contract; source-scan only.
 */
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

const here = path.dirname(fileURLToPath(import.meta.url));
const css = fs.readFileSync(path.join(here, "attachment.css"), "utf8");

function narrowGalleryMediaBlock(): string {
  const match = css.match(
    /@media\s*\(\s*max-width:\s*767px\s*\)\s*\{[\s\S]*?\.message-attachment-gallery\.gallery-layout-grid\s*\{[\s\S]*?\.message-attachment-gallery\.gallery-layout-stack\s*\{[\s\S]*?\}\s*\}/,
  );
  expect(match?.[0]).toBeTruthy();
  return match![0];
}

describe("LRM-1247 attachment.css gallery narrow clamp", () => {
  it("narrow grid cells use height:9rem + aspect-ratio:auto (not max-height+square)", () => {
    const block = narrowGalleryMediaBlock();
    expect(block).toMatch(
      /\.message-attachment-gallery\.gallery-layout-grid\s+\.gallery-cell\s*\{[^}]*aspect-ratio:\s*auto[^}]*height:\s*9rem/s,
    );
    expect(block).not.toMatch(
      /\.message-attachment-gallery\.gallery-layout-grid\s+\.gallery-cell\s*\{[^}]*max-height:\s*9rem/s,
    );
  });

  it("narrow data-count=3 first cell keeps height:auto (R4 not clamped)", () => {
    const block = narrowGalleryMediaBlock();
    expect(block).toMatch(
      /\.message-attachment-gallery\.gallery-layout-grid\[data-count=["']3["']\]\s*\.gallery-cell:first-child\s*\{[^}]*height:\s*auto/s,
    );
  });
});
