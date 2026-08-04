// @vitest-environment node
import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";

const sharedBaseCss = readFileSync("../ui/styles/base.css", "utf8");

function mediaBlock(query: string): string {
  const start = sharedBaseCss.indexOf(`@media (${query})`);
  const open = sharedBaseCss.indexOf("{", start);

  if (start < 0 || open < 0) {
    throw new Error(`Missing media query: ${query}`);
  }

  let depth = 0;
  for (let index = open; index < sharedBaseCss.length; index += 1) {
    if (sharedBaseCss[index] === "{") depth += 1;
    if (sharedBaseCss[index] === "}") depth -= 1;
    if (depth === 0) return sharedBaseCss.slice(open + 1, index);
  }

  throw new Error(`Unterminated media query: ${query}`);
}

describe("shared reduced-motion animation fallback (LRM-1165)", () => {
  it("disables Tailwind pulse and spin utilities only for reduced motion", () => {
    const reducedMotion = mediaBlock("prefers-reduced-motion: reduce");

    expect(reducedMotion).toMatch(
      /\.animate-pulse\s*,\s*\.animate-spin\s*\{\s*animation:\s*none\s*;/,
    );
  });
});
