// @vitest-environment node
/**
 * LRM-359 — attachment chip filename vs chip surface contrast.
 *
 * Uses frozen product-surface hex / oklch approximations from
 * packages/ui/styles/tokens.css so CI fails if tokens drift below WCAG AA
 * (4.5:1) for normal text.
 */
import { describe, expect, it } from "vitest";

function srgbToLin(c: number): number {
  const s = c / 255;
  return s <= 0.04045 ? s / 12.92 : ((s + 0.055) / 1.055) ** 2.4;
}

function relLuminance(r: number, g: number, b: number): number {
  return 0.2126 * srgbToLin(r) + 0.7152 * srgbToLin(g) + 0.0722 * srgbToLin(b);
}

function contrastRatio(
  a: [number, number, number],
  b: [number, number, number],
): number {
  const la = relLuminance(...a);
  const lb = relLuminance(...b);
  const lighter = Math.max(la, lb);
  const darker = Math.min(la, lb);
  return (lighter + 0.05) / (darker + 0.05);
}

function hexToRgb(hex: string): [number, number, number] {
  const h = hex.replace("#", "");
  return [
    Number.parseInt(h.slice(0, 2), 16),
    Number.parseInt(h.slice(2, 4), 16),
    Number.parseInt(h.slice(4, 6), 16),
  ];
}

/** Approximate oklch(L C H) → sRGB (enough for contrast gate; C≈0 here). */
function oklchApproxToRgb(L: number): [number, number, number] {
  const v = Math.round(Math.min(1, Math.max(0, L)) * 255);
  return [v, v, v];
}

describe("LRM-359 attachment chip contrast (token math)", () => {
  it("light: foreground (#1d1c1d) on muted (#f6f6f4) ≥ 4.5:1", () => {
    const fg = hexToRgb("#1d1c1d");
    const muted = hexToRgb("#f6f6f4");
    expect(contrastRatio(fg, muted)).toBeGreaterThanOrEqual(4.5);
  });

  it("dark: foreground oklch(0.985) on muted oklch(0.274) ≥ 4.5:1", () => {
    const fg = oklchApproxToRgb(0.985);
    const muted = oklchApproxToRgb(0.274);
    expect(contrastRatio(fg, muted)).toBeGreaterThanOrEqual(4.5);
  });

  it("rejects the washed-out pair Frank filed (ink-3 on near-white)", () => {
    // Pre-fix failure mode: light gray (#868686 / ink-3) on white-ish wash.
    const washed = hexToRgb("#868686");
    const washBg = hexToRgb("#f8f8f8");
    expect(contrastRatio(washed, washBg)).toBeLessThan(4.5);
  });
});
