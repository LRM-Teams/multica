/**
 * LRM-1208 — Git topology branch lanes must resolve through `tokens.css`
 * semantic tokens, not inline hex.
 *
 * Why a token and not a `dark:` class: `colorForLane()` output lands on an SVG
 * `stroke` attribute (research-git-list.tsx path) and on inline
 * `style.borderColor` (lane dot). Neither position can carry a Tailwind
 * `dark:` variant, so the light-only palette rendered unchanged on the dark
 * card — lane 3 (`#1d4ed8`) measured 2.64:1, below WCAG 1.4.11's 3:1 floor for
 * a meaningful 2px graphic.
 *
 * Source scan + pure helper only; no authenticated routes, no DOM.
 * File scope is mutually exclusive with LRM-1204 (`research-rails-a11y`),
 * LRM-1196 (`research-template-chip-dark-hex`) and LRM-1151 (canvas Dock).
 */
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

import { GIT_BRANCH_COLORS, colorForLane } from "./git-topology";

const here = path.dirname(fileURLToPath(import.meta.url));
const researchRoot = path.resolve(here, "..");
const tokensPath = path.resolve(
  here,
  "../../../ui/styles/tokens.css",
);

const LANE_COUNT = 5;

/** Frozen light values — carried over verbatim from the pre-token palette. */
const FROZEN_LIGHT_LANES = [
  "#0f766e",
  "#c2410c",
  "#1d4ed8",
  "#7c3aed",
  "#b45309",
] as const;

/** WCAG 1.4.11 non-text floor is 3:1; this slice targets the text floor. */
const MIN_CONTRAST = 4.5;

/** Bare 6/8-digit hex color. 3–5 digit `#1952`-style issue refs are excluded. */
const RAW_HEX = /#[0-9a-fA-F]{6}(?:[0-9a-fA-F]{2})?\b/;

const tokensSrc = fs.readFileSync(tokensPath, "utf8");

/**
 * tokens.css declares light values under `:root, .light` and dark under
 * `.dark`; both blocks are flat and close with a column-0 `}`.
 */
function blockFor(selector: "light" | "dark"): string {
  const opener = selector === "light" ? ":root,\n.light {" : ".dark {";
  const start = tokensSrc.indexOf(opener);
  expect(start, `${selector} block present`).toBeGreaterThanOrEqual(0);
  const end = tokensSrc.indexOf("\n}", start);
  expect(end, `${selector} block terminated`).toBeGreaterThan(start);
  return tokensSrc.slice(start, end);
}

function declaredLanes(block: string): Map<string, string> {
  const out = new Map<string, string>();
  for (const m of block.matchAll(
    /--(research-lane-\d+)\s*:\s*([^;]+);/g,
  )) {
    out.set(m[1]!, m[2]!.trim());
  }
  return out;
}

function srgbToLinear(channel: number): number {
  const c = channel / 255;
  return c <= 0.04045 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4;
}

function relativeLuminance([r, g, b]: readonly [
  number,
  number,
  number,
]): number {
  return (
    0.2126 * srgbToLinear(r) +
    0.7152 * srgbToLinear(g) +
    0.0722 * srgbToLinear(b)
  );
}

function contrastRatio(a: number, b: number): number {
  const [hi, lo] = a >= b ? [a, b] : [b, a];
  return (hi + 0.05) / (lo + 0.05);
}

function hexToRgb(hex: string): readonly [number, number, number] {
  const h = hex.replace("#", "");
  return [
    Number.parseInt(h.slice(0, 2), 16),
    Number.parseInt(h.slice(2, 4), 16),
    Number.parseInt(h.slice(4, 6), 16),
  ] as const;
}

/** Minimal oklch() → sRGB, enough for the `oklch(L C H)` forms in tokens.css. */
function oklchToRgb(
  lightness: number,
  chroma: number,
  hueDeg: number,
): readonly [number, number, number] {
  const h = (hueDeg * Math.PI) / 180;
  const a = chroma * Math.cos(h);
  const bb = chroma * Math.sin(h);

  const l_ = lightness + 0.3963377774 * a + 0.2158037573 * bb;
  const m_ = lightness - 0.1055613458 * a - 0.0638541728 * bb;
  const s_ = lightness - 0.0894841775 * a - 1.291485548 * bb;

  const l = l_ ** 3;
  const m = m_ ** 3;
  const s = s_ ** 3;

  const linear = [
    4.0767416621 * l - 3.3077115913 * m + 0.2309699292 * s,
    -1.2684380046 * l + 2.6097574011 * m - 0.3413193965 * s,
    -0.0041960863 * l - 0.7034186147 * m + 1.707614701 * s,
  ];

  return linear.map((v) => {
    const clamped = Math.min(1, Math.max(0, v));
    const encoded =
      clamped <= 0.0031308
        ? 12.92 * clamped
        : 1.055 * clamped ** (1 / 2.4) - 0.055;
    return Math.round(encoded * 255);
  }) as unknown as readonly [number, number, number];
}

function parseColor(value: string): readonly [number, number, number] {
  const hex = value.match(/^#[0-9a-fA-F]{6}$/);
  if (hex) return hexToRgb(value);

  const oklch = value.match(
    /^oklch\(\s*([\d.]+)\s+([\d.]+)\s+([\d.]+)\s*\)$/,
  );
  expect(oklch, `parseable color: ${value}`).not.toBeNull();
  return oklchToRgb(
    Number(oklch![1]),
    Number(oklch![2]),
    Number(oklch![3]),
  );
}

function collectProductionSources(dir: string): string[] {
  const out: string[] = [];
  for (const entry of fs.readdirSync(dir, { withFileTypes: true })) {
    if (entry.name === "node_modules" || entry.name === "dist") continue;
    const full = path.join(dir, entry.name);
    if (entry.isDirectory()) {
      // Parallel smoke harness quotes historical PR refs in prose.
      if (entry.name === "__smoke__") continue;
      out.push(...collectProductionSources(full));
      continue;
    }
    if (!/\.(ts|tsx)$/.test(entry.name)) continue;
    if (/\.test\.(ts|tsx)$/.test(entry.name)) continue;
    out.push(full);
  }
  return out;
}

describe("LRM-1208 research Git lane colors · token indirection", () => {
  it("GIT_BRANCH_COLORS are token refs, never inline hex", () => {
    expect(GIT_BRANCH_COLORS).toHaveLength(LANE_COUNT);
    GIT_BRANCH_COLORS.forEach((value, index) => {
      expect(value).toBe(`var(--research-lane-${index + 1})`);
    });
    expect(GIT_BRANCH_COLORS.join(" ")).not.toMatch(RAW_HEX);
  });

  it("colorForLane keeps 1-based token naming and wraps every 5 lanes", () => {
    for (let lane = 0; lane < LANE_COUNT; lane += 1) {
      expect(colorForLane(lane)).toBe(`var(--research-lane-${lane + 1})`);
    }
    // Wrap-around contract preserved from the pre-token implementation.
    expect(colorForLane(LANE_COUNT)).toBe(colorForLane(0));
    expect(colorForLane(LANE_COUNT + 3)).toBe(colorForLane(3));
    expect(colorForLane(12)).toBe(colorForLane(2));
  });
});

describe("LRM-1208 tokens.css lane pairs", () => {
  const light = declaredLanes(blockFor("light"));
  const dark = declaredLanes(blockFor("dark"));

  it("light lanes carry the frozen pre-token hexes unchanged", () => {
    expect(light.size).toBe(LANE_COUNT);
    FROZEN_LIGHT_LANES.forEach((hex, index) => {
      expect(light.get(`research-lane-${index + 1}`)).toBe(hex);
    });
  });

  it("every :root lane token has a .dark counterpart (regression guard)", () => {
    const missing = [...light.keys()].filter((name) => !dark.has(name));
    expect(missing, missing.join(", ") || "complete").toEqual([]);
    // And no orphan dark-only lane.
    const orphan = [...dark.keys()].filter((name) => !light.has(name));
    expect(orphan, orphan.join(", ") || "clean").toEqual([]);
  });

  it("dark lanes clear 4.5:1 against dark --card and --background", () => {
    const darkBlock = blockFor("dark");
    const cardValue = darkBlock.match(/--card:\s*([^;]+);/)?.[1]?.trim();
    const bgValue = darkBlock
      .match(/--background:\s*([^;]+);/)?.[1]
      ?.trim();
    expect(cardValue, "dark --card declared").toBeTruthy();
    expect(bgValue, "dark --background declared").toBeTruthy();

    const cardLum = relativeLuminance(parseColor(cardValue!));
    const bgLum = relativeLuminance(parseColor(bgValue!));

    const failures: string[] = [];
    for (const [name, value] of dark) {
      const lum = relativeLuminance(parseColor(value));
      const vsCard = contrastRatio(lum, cardLum);
      const vsBg = contrastRatio(lum, bgLum);
      if (vsCard < MIN_CONTRAST || vsBg < MIN_CONTRAST) {
        failures.push(
          `${name}: card ${vsCard.toFixed(2)} / bg ${vsBg.toFixed(2)}`,
        );
      }
    }
    expect(failures, failures.join("\n") || "all lanes pass").toEqual([]);
  });

  it("light lanes still clear 4.5:1 against the light surface", () => {
    const lightBlock = blockFor("light");
    const surface = lightBlock.match(/--surface:\s*([^;]+);/)?.[1]?.trim();
    expect(surface, "light --surface declared").toBeTruthy();
    const surfaceLum = relativeLuminance(parseColor(surface!));

    const failures: string[] = [];
    for (const [name, value] of light) {
      const ratio = contrastRatio(
        relativeLuminance(parseColor(value)),
        surfaceLum,
      );
      if (ratio < MIN_CONTRAST) {
        failures.push(`${name}: ${ratio.toFixed(2)}`);
      }
    }
    expect(failures, failures.join("\n") || "all lanes pass").toEqual([]);
  });
});

describe("LRM-1208 research production sources ban bare hex colors", () => {
  it("no #rrggbb / #rrggbbaa literal outside tests and smoke harness", () => {
    const files = collectProductionSources(researchRoot);
    expect(files.length).toBeGreaterThan(20);

    const hits: string[] = [];
    for (const file of files) {
      const src = fs.readFileSync(file, "utf8");
      const match = src.match(RAW_HEX);
      if (match) {
        hits.push(`${path.relative(researchRoot, file)}: ${match[0]}`);
      }
    }
    expect(hits, hits.join("\n") || "clean").toEqual([]);
  });
});
