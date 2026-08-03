/**
 * LRM-1242 — gallery × bubble-shell R1–R4 CSS contract (SoT LRM-1238).
 * Source scan only; does not touch resolveGalleryLayout thresholds.
 */
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

const here = path.dirname(fileURLToPath(import.meta.url));
const css = fs.readFileSync(path.join(here, "attachment.css"), "utf8");

describe("LRM-1242 attachment.css gallery shell R1–R4", () => {
  it("R1: gallery-layout-grid max-width is min(100%, 22.5rem), not 28rem", () => {
    expect(css).toMatch(
      /\.message-attachment-gallery\.gallery-layout-grid\s*\{[^}]*max-width:\s*min\(100%,\s*22\.5rem\)/s,
    );
    expect(css).not.toMatch(
      /\.message-attachment-gallery\.gallery-layout-grid\s*\{[^}]*max-width:\s*min\(100%,\s*28rem\)/s,
    );
  });

  it("R2: grid cell + message image + unavailable use var(--radius-md)", () => {
    expect(css).toMatch(
      /\.message-attachment-gallery\.gallery-layout-grid\s+\.gallery-cell\s*\{[^}]*border-radius:\s*var\(--radius-md\)/s,
    );
    expect(css).toMatch(
      /\.message-surface\s+\.image-content\s*\{[^}]*border-radius:\s*var\(--radius-md\)/s,
    );
    expect(css).toMatch(
      /\[data-testid=["']attachment-unavailable["']\]\s*\{[^}]*border-radius:\s*var\(--radius-md\)/s,
    );
  });

  it("R3: message image uses inset ring (light + dark), not outer border", () => {
    const surfaceBlock = css.match(
      /\.message-surface\s+\.image-content\s*\{[^}]+\}/s,
    )?.[0];
    expect(surfaceBlock).toBeTruthy();
    expect(surfaceBlock).toMatch(/border:\s*0/);
    expect(surfaceBlock).toMatch(
      /box-shadow:\s*inset\s+0\s+0\s+0\s+1px\s+color-mix\(in\s+srgb,\s*var\(--foreground\)\s+14%,\s*transparent\)/,
    );
    expect(surfaceBlock).not.toMatch(/border:\s*1px\s+solid/);

    expect(css).toMatch(
      /\.dark\s+\.message-surface\s+\.image-content\s*\{[^}]*box-shadow:\s*inset\s+0\s+0\s+0\s+1px\s+color-mix\(in\s+srgb,\s*#fff\s+16%,\s*transparent\)/s,
    );
  });

  it("R4: data-count=3 first cell spans 2 with 16/9 and max-height:none", () => {
    expect(css).toMatch(
      /\.message-attachment-gallery\.gallery-layout-grid\[data-count=["']3["']\]\s*\.gallery-cell:first-child\s*\{[^}]*grid-column:\s*span\s+2[^}]*aspect-ratio:\s*16\s*\/\s*9[^}]*max-height:\s*none/s,
    );
  });
});
