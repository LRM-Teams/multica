/**
 * LRM-1369 — success wash labels must resolve through `--success-strong`.
 *
 * Why a second token instead of deepening `--success` (the LRM-1353 warning
 * route): `--success` also paints presence dots (`bg-success`), the issue-board
 * divider and the solid add-people button, so moving it would darken frozen
 * product surfaces. This slice mirrors LRM-1328's `--destructive-strong`
 * instead: the wash, the dot and the fill keep `--success`; only text sitting
 * on `bg-success/*` switches to `--success-strong`.
 *
 * Measured before the fix (live Chromium, `scripts/lrm1369-gate-shots.mjs`):
 * light `/15` on card 4.32, light `/15` on muted 4.15, dark `/15` on muted
 * 4.49 — all below WCAG 1.4.3's 4.5:1. After: light 5.13–6.15, dark 7.28–9.47.
 *
 * Source scan + pure math only; no DOM, no routes. File scope is mutually
 * exclusive with LRM-1359 / LRM-1368 (`opacity-*` family) and with
 * LRM-1328 / LRM-1353 (destructive / warning tokens).
 */
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

const here = path.dirname(fileURLToPath(import.meta.url));
const packagesRoot = path.resolve(here, "../..");
const tokensPath = path.resolve(packagesRoot, "ui/styles/tokens.css");
const tokensSrc = fs.readFileSync(tokensPath, "utf8");

/** Frozen product-surface green — dots, solid fills, dividers, plain text. */
const FROZEN_OK = "#007a5a";
const LIGHT_SUCCESS_STRONG = "#026a4f";
const DARK_SUCCESS_STRONG = "oklch(0.78 0.15 150)";
const MIN_CONTRAST = 4.5;

/** tokens.css keeps light under `:root, .light` and dark under `.dark`. */
function blockFor(selector: "light" | "dark"): string {
  const opener = selector === "light" ? ":root,\n.light {" : ".dark {";
  const start = tokensSrc.indexOf(opener);
  expect(start, `${selector} block present`).toBeGreaterThanOrEqual(0);
  const end = tokensSrc.indexOf("\n}", start);
  expect(end, `${selector} block terminated`).toBeGreaterThan(start);
  return tokensSrc.slice(start, end);
}

const SCAN_ROOTS = ["views", "core", "ui"];
const SKIP_DIR = new Set(["node_modules", "dist", "build", ".next"]);

function sourceFiles(): string[] {
  const out: string[] = [];
  const walk = (dir: string) => {
    for (const entry of fs.readdirSync(dir, { withFileTypes: true })) {
      if (entry.isDirectory()) {
        if (!SKIP_DIR.has(entry.name)) walk(path.join(dir, entry.name));
        continue;
      }
      const file = path.join(dir, entry.name);
      if (!/\.tsx?$/.test(file) || /\.test\.tsx?$/.test(file)) continue;
      out.push(file);
    }
  };
  for (const root of SCAN_ROOTS) walk(path.resolve(packagesRoot, root));
  return out;
}

/**
 * A persistent `bg-success/<alpha>` wash inside the SAME class string as the
 * label. Matching per string (not per line) keeps config objects that keep the
 * icon colour and the column wash in sibling fields out of scope — there the
 * icon never paints on that wash. `hover:` washes are transient states.
 */
const WASH_PAIR = /(?<!hover:)\bbg-success\/\d/;
/** Quoted class strings and template literals on one source line. */
const CLASS_STRINGS = /(["'`])((?:\\.|(?!\1)[^\\])*)\1/g;
/** Bare `text-success` — `text-success-strong` must not match. */
const BARE_LABEL = /\btext-success(?!-strong)\b/;

function contrast(fgHex: string, bgHex: string, alpha = 1, baseHex = "#ffffff") {
  const toRgb = (hex: string) => {
    const h = hex.replace("#", "");
    return [0, 2, 4].map((i) => Number.parseInt(h.slice(i, i + 2), 16));
  };
  const over = (fg: number[], bg: number[], a: number) =>
    fg.map((c, i) => c * a + (bg[i] ?? 0) * (1 - a));
  const lum = (rgb: number[]) => {
    const [r = 0, g = 0, b = 0] = rgb.map((c) => {
      const v = c / 255;
      return v <= 0.04045 ? v / 12.92 : ((v + 0.055) / 1.055) ** 2.4;
    });
    return 0.2126 * r + 0.7152 * g + 0.0722 * b;
  };
  const wash = over(toRgb(bgHex), toRgb(baseHex), alpha);
  const la = lum(toRgb(fgHex));
  const lb = lum(wash);
  return (Math.max(la, lb) + 0.05) / (Math.min(la, lb) + 0.05);
}

describe("LRM-1369 success wash token", () => {
  it("declares --success-strong in both themes without moving --success", () => {
    const light = blockFor("light");
    const dark = blockFor("dark");

    expect(light).toContain(`--success-strong: ${LIGHT_SUCCESS_STRONG};`);
    expect(dark).toContain(`--success-strong: ${DARK_SUCCESS_STRONG};`);

    // The wash / dot / fill source of truth must stay frozen.
    expect(light).toContain(`--ok: ${FROZEN_OK};`);
    expect(light).toContain("--success: var(--ok);");
    expect(dark).toContain("--success: var(--ok);");
    expect(light).not.toMatch(/--success: (?!var\(--ok\))/);
  });

  it("exposes the token to Tailwind as text-success-strong", () => {
    expect(tokensSrc).toContain(
      "--color-success-strong: var(--success-strong);",
    );
  });

  it("keeps the label above 4.5:1 on the deepest wash and base", () => {
    // Worst shipped stack: bg-success/15 over the muted page base.
    for (const [alpha, base] of [
      [0.15, "#f6f6f4"],
      [0.15, "#ffffff"],
      [0.1, "#f6f6f4"],
    ] as const) {
      expect(
        contrast(LIGHT_SUCCESS_STRONG, FROZEN_OK, alpha, base),
        `success-strong on bg-success/${alpha * 100} over ${base}`,
      ).toBeGreaterThanOrEqual(MIN_CONTRAST);
    }
    // The old label reproduces the failure this slice fixes.
    expect(
      contrast(FROZEN_OK, FROZEN_OK, 0.15, "#f6f6f4"),
    ).toBeLessThan(MIN_CONTRAST);
  });

  it("never pairs a bare text-success with a persistent success wash", () => {
    const offenders: string[] = [];
    for (const file of sourceFiles()) {
      const lines = fs.readFileSync(file, "utf8").split("\n");
      lines.forEach((line, index) => {
        for (const match of line.matchAll(CLASS_STRINGS)) {
          const classes = match[2] ?? "";
          if (WASH_PAIR.test(classes) && BARE_LABEL.test(classes)) {
            offenders.push(
              `${path.relative(packagesRoot, file)}:${index + 1} ${classes}`,
            );
          }
        }
      });
    }
    expect(offenders, offenders.join("\n")).toEqual([]);
  });
});
