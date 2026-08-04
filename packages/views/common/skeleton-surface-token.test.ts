/**
 * LRM-1366 — a loading skeleton must be visible on every surface it ships on.
 *
 * Root cause of the reported "DM sidebar is blank after a hard refresh"
 * (LRM-1364 screenshot: 私信 heading + `+` and ~3 rows of empty space): the
 * shared `Skeleton` filled with `bg-muted`, and in the light theme
 * `--muted: var(--page-bg)` is the *same* `#f6f6f4` as `--sidebar`. Contrast
 * 1.00:1 — `DmListSkeleton` painted three rows of literally invisible
 * placeholders, so a normal pending `GET /api/dm` read as a blank region.
 * On white surfaces the same fill measured 1.08:1, i.e. barely better.
 *
 * Fix shape mirrors LRM-1328 / LRM-1353 / LRM-1369: no per-surface override at
 * call sites, one dedicated token. `--skeleton` points at the frozen
 * `--line-strong` (#d1d1d1) in light so no new colour enters the palette, and
 * at oklch(0.32) in dark, one step above the shipped `--muted` oklch(0.274),
 * which measured 1.19:1 against the dark sidebar in Chromium.
 *
 * Placeholder floor is 1.25:1, not WCAG 4.5: skeletons are `aria-hidden`
 * decoration, so the bar is "perceivable as a shape", which is what the
 * ubiquitous gray-200-on-white shimmer (1.25:1) delivers.
 *
 * Source scan + pure math only; the real-Chromium counterpart lives in
 * `scripts/lrm1366-gate-shots.mjs`.
 */
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

const here = path.dirname(fileURLToPath(import.meta.url));
const packagesRoot = path.resolve(here, "../..");
const tokensPath = path.resolve(packagesRoot, "ui/styles/tokens.css");
const tokensSrc = fs.readFileSync(tokensPath, "utf8");
const skeletonSrc = fs.readFileSync(
  path.resolve(packagesRoot, "ui/components/ui/skeleton.tsx"),
  "utf8",
);

/** Minimum perceivable placeholder contrast (gray-200 on white ≈ 1.25). */
const MIN_PLACEHOLDER = 1.25;

/** tokens.css keeps light under `:root, .light` and dark under `.dark`. */
function blockFor(selector: "light" | "dark"): string {
  const opener = selector === "light" ? ":root,\n.light {" : ".dark {";
  const start = tokensSrc.indexOf(opener);
  expect(start, `${selector} block present`).toBeGreaterThanOrEqual(0);
  const end = tokensSrc.indexOf("\n}", start);
  expect(end, `${selector} block terminated`).toBeGreaterThan(start);
  return tokensSrc.slice(start, end);
}

/** Custom-property declarations of one theme block, `--name` → raw value. */
function declarations(block: string): Map<string, string> {
  const out = new Map<string, string>();
  for (const match of block.matchAll(/^\s*(--[\w-]+):\s*([^;]+);/gm)) {
    out.set(match[1]!, match[2]!.trim());
  }
  return out;
}

/** Follow a `var(--x)` chain to the literal value it resolves to. */
function resolve(decls: Map<string, string>, name: string, depth = 0): string {
  const raw = decls.get(name);
  expect(raw, `${name} declared`).toBeDefined();
  const chained = /^var\((--[\w-]+)\)$/.exec(raw!.trim());
  if (!chained) return raw!.trim();
  expect(depth, `${name} var() chain terminates`).toBeLessThan(8);
  return resolve(decls, chained[1]!, depth + 1);
}

function contrast(aHex: string, bHex: string): number {
  const lum = (hex: string) => {
    const h = hex.replace("#", "");
    const [r = 0, g = 0, b = 0] = [0, 2, 4]
      .map((i) => Number.parseInt(h.slice(i, i + 2), 16) / 255)
      .map((v) => (v <= 0.04045 ? v / 12.92 : ((v + 0.055) / 1.055) ** 2.4));
    return 0.2126 * r + 0.7152 * g + 0.0722 * b;
  };
  const la = lum(aHex);
  const lb = lum(bHex);
  return (Math.max(la, lb) + 0.05) / (Math.min(la, lb) + 0.05);
}

describe("LRM-1366 skeleton surface token", () => {
  it("declares --skeleton in both themes and exposes it to Tailwind", () => {
    expect(blockFor("light")).toContain("--skeleton: var(--line-strong);");
    // Dark gets its own step: `--muted` oklch(0.274) measured 1.19:1 against the
    // dark sidebar (below the placeholder floor) in `lrm1366-gate-shots.mjs`.
    expect(blockFor("dark")).toContain("--skeleton: oklch(0.32 0.006 286.033);");
    expect(blockFor("dark")).not.toContain("--skeleton: var(--muted);");
    expect(tokensSrc).toContain("--color-skeleton: var(--skeleton);");
  });

  it("fills the shared Skeleton from the token, not from --muted", () => {
    const fill = /cn\(\s*"([^"]+)"/.exec(skeletonSrc)?.[1] ?? "";
    expect(fill, "Skeleton base classes").toMatch(/\bbg-skeleton\b/);
    expect(fill).not.toMatch(/\bbg-muted\b/);
  });

  it("keeps the light placeholder perceivable on sidebar chrome and on white", () => {
    const light = declarations(blockFor("light"));
    const fill = resolve(light, "--skeleton");
    const sidebar = resolve(light, "--sidebar");
    const surface = resolve(light, "--background");

    expect(fill).toMatch(/^#[0-9a-f]{6}$/i);
    for (const [base, label] of [
      [sidebar, "--sidebar (conversation list pane)"],
      [surface, "--background (cards / stream)"],
    ] as const) {
      expect(
        contrast(fill, base),
        `--skeleton on ${label} (${fill} vs ${base})`,
      ).toBeGreaterThanOrEqual(MIN_PLACEHOLDER);
    }

    // The shipped defect: bg-muted was byte-identical to the sidebar plane and
    // only 1.08:1 on white, which is why the DM region read as blank.
    const muted = resolve(light, "--muted");
    expect(muted).toBe(sidebar);
    expect(contrast(muted, sidebar)).toBeCloseTo(1, 5);
    expect(contrast(muted, surface)).toBeLessThan(1.1);
  });
});
