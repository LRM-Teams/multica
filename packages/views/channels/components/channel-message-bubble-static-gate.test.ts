import { readFileSync, readdirSync, statSync } from "node:fs";
import { join } from "node:path";

import { describe, expect, it } from "vitest";

/**
 * LRM-1360 — the desktop thread entry disappeared because LRM-1331 moved the
 * `(pointer:fine) and (min-width:640px)` gate into a TS constant and built the
 * Tailwind candidates with template interpolation:
 *
 *   const FINE_DESKTOP_MQ = "[@media(pointer:fine)_and_(min-width:640px)]";
 *   `${FINE_DESKTOP_MQ}:group-hover:opacity-100`
 *
 * Tailwind v4 extracts candidates by statically scanning source text, so no rule
 * was emitted for the interpolated variants. The action bar kept its base
 * `opacity-0 pointer-events-none`, i.e. the thread / copy / quote / edit keys
 * were invisible and unclickable on desktop. `className` assertions cannot see
 * this (the class string is still on the node), so the regression is guarded at
 * the source level: gate candidates must be literal.
 */

const COMPONENTS_DIR = __dirname;
const BUBBLE = join(COMPONENTS_DIR, "channel-message-bubble.tsx");
const GATE = "[@media(pointer:fine)_and_(min-width:640px)]";

/**
 * Every gate-dependent candidate the desktop hover affordance needs. The bar is
 * a pure hover overlay, so this list covers its visibility only — no geometry
 * reserve (`pr-[162px]`, the continuation `before:` float, the quote-card
 * `pr-[158px]`) exists to guard any more.
 */
const REQUIRED_LITERAL_CANDIDATES = [
  `${GATE}:flex`,
  `${GATE}:group-hover:pointer-events-auto`,
  `${GATE}:group-hover:opacity-100`,
  `${GATE}:group-focus-within:pointer-events-auto`,
  `${GATE}:group-focus-within:opacity-100`,
];

/**
 * `${SOMETHING}:utility` inside a class string — the shape Tailwind cannot
 * extract. Assertions in `*.test.tsx` may keep using it (they compare strings,
 * not CSS), so only production sources are scanned.
 */
const INTERPOLATED_CANDIDATE =
  /\$\{[A-Za-z_$][\w$]*\}:(?:group-|hover:|focus|before:|after:|flex\b|hidden\b|pr-|pl-|opacity-|translate)/;

function collectProductionSources(dir: string, out: string[] = []): string[] {
  for (const entry of readdirSync(dir)) {
    const full = join(dir, entry);
    if (statSync(full).isDirectory()) {
      if (entry === "__smoke__" || entry === "node_modules") continue;
      collectProductionSources(full, out);
      continue;
    }
    if (!/\.tsx?$/.test(entry)) continue;
    if (/\.(test|spec)\.tsx?$/.test(entry)) continue;
    out.push(full);
  }
  return out;
}

describe("LRM-1360 fine-desktop gate must be statically extractable", () => {
  it("keeps every desktop hover-bar candidate as a literal class", () => {
    const source = readFileSync(BUBBLE, "utf8");
    for (const candidate of REQUIRED_LITERAL_CANDIDATES) {
      expect(source, `${candidate} must appear literally`).toContain(candidate);
    }
  });

  it("never builds a gated candidate through template interpolation", () => {
    const offenders = collectProductionSources(COMPONENTS_DIR)
      .map((file) => ({ file, source: readFileSync(file, "utf8") }))
      .flatMap(({ file, source }) =>
        source
          .split("\n")
          .map((line, index) => ({ file, line: index + 1, text: line.trim() }))
          .filter(({ text }) => INTERPOLATED_CANDIDATE.test(text)),
      )
      .map(({ file, line, text }) => `${file.split("/").pop()}:${line} ${text}`);

    expect(offenders, "interpolated Tailwind candidates emit no CSS").toEqual([]);
  });
});
