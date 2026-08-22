/**
 * LRM-1196 — [巡检][F] chip/inject dark parity + repo-level raw-hex guard.
 * Source scan only; no authenticated routes. Mutually exclusive with
 * research-f-state-a11y* (LRM-1192), LRM-1148, LRM-1170.
 */
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

/** Former chip selected glow — must never return. */
const FORBIDDEN_CHIP_HALO = /#dbeafe\b/i;

/** Arbitrary Tailwind ring/glow with embedded raw hex (LRM-1189 root cause). */
const FORBIDDEN_SHADOW_RAW_HEX =
  /shadow-\[[^\]]*#[0-9a-fA-F]{3,8}[^\]]*]/;

/** Frozen LRM-1175 / LRM-1189 template-blue dark triple. */
const CHIP_DARK_TRIPLE = [
  "dark:border-blue-400/45",
  "dark:bg-blue-400/[0.14]",
  "dark:text-blue-200",
] as const;

const here = path.dirname(fileURLToPath(import.meta.url));
const researchRoot = path.resolve(here, "..");

function readComponent(name: string) {
  return fs.readFileSync(path.join(here, name), "utf8");
}

function collectProductionSources(dir: string): string[] {
  const out: string[] = [];
  for (const entry of fs.readdirSync(dir, { withFileTypes: true })) {
    if (entry.name === "node_modules" || entry.name === "dist") continue;
    const full = path.join(dir, entry.name);
    if (entry.isDirectory()) {
      // Skip parallel smoke harness — may quote historical hex in comments.
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

describe("LRM-1196 research template chip/inject · dark parity + raw-hex guard", () => {
  const chipSrc = readComponent("research-template-chip-row.tsx");
  const injectSrc = readComponent("research-template-inject-tag.tsx");

  it("chip source is pixel-theme bevel: px-chip class, no blue tones, no raw hex", () => {
    // Pixel theme 2026-08-22: chips decoupled from the LRM-1175 blue pair —
    // state visuals live in research-home-visual.css keyed off aria-checked.
    expect(chipSrc).toContain("px-chip");
    expect(chipSrc).not.toMatch(/\b(bg|text|border)-blue-\d/);
    expect(chipSrc).not.toMatch(FORBIDDEN_CHIP_HALO);
    expect(chipSrc).not.toMatch(FORBIDDEN_SHADOW_RAW_HEX);
    expect(chipSrc).not.toContain("shadow-[");
  });

  it("inject tag industry tone keeps the same dark triple (pair contract)", () => {
    const tonesStart = injectSrc.indexOf("const TAG_TONES");
    const tonesEnd = injectSrc.indexOf("};", tonesStart);
    const toneBlock = injectSrc.slice(tonesStart, tonesEnd);
    const industryIdx = toneBlock.indexOf("industry:");
    expect(industryIdx).toBeGreaterThanOrEqual(0);
    const industrySlice = toneBlock.slice(industryIdx, industryIdx + 220);
    for (const token of CHIP_DARK_TRIPLE) {
      expect(industrySlice, token).toContain(token);
    }
    expect(injectSrc).not.toMatch(FORBIDDEN_CHIP_HALO);
    expect(injectSrc).not.toMatch(FORBIDDEN_SHADOW_RAW_HEX);
  });

  it("packages/views/research production sources ban #dbeafe and shadow-[…#hex]", () => {
    const files = collectProductionSources(researchRoot);
    expect(files.length).toBeGreaterThan(20);

    const hits: string[] = [];
    for (const file of files) {
      const src = fs.readFileSync(file, "utf8");
      const rel = path.relative(researchRoot, file);
      if (FORBIDDEN_CHIP_HALO.test(src)) {
        hits.push(`${rel}: #dbeafe`);
      }
      if (FORBIDDEN_SHADOW_RAW_HEX.test(src)) {
        hits.push(`${rel}: shadow-[…#hex]`);
      }
    }

    expect(hits, hits.join("\n") || "clean").toEqual([]);
  });
});
