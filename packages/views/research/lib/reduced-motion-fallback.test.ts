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

/** Collect every `@media (prefers-reduced-motion: reduce)` body (LRM-1337
 *  and peers place fallbacks in dedicated blocks, not only the first). */
function allReducedMotionBodies(): string[] {
  const bodies: string[] = [];
  const needle = "@media (prefers-reduced-motion: reduce)";
  let from = 0;
  while (from < sharedBaseCss.length) {
    const start = sharedBaseCss.indexOf(needle, from);
    if (start < 0) break;
    const open = sharedBaseCss.indexOf("{", start);
    if (open < 0) break;
    let depth = 0;
    for (let index = open; index < sharedBaseCss.length; index += 1) {
      if (sharedBaseCss[index] === "{") depth += 1;
      if (sharedBaseCss[index] === "}") depth -= 1;
      if (depth === 0) {
        bodies.push(sharedBaseCss.slice(open + 1, index));
        from = index + 1;
        break;
      }
    }
  }
  return bodies;
}

describe("shared reduced-motion animation fallback (LRM-1165)", () => {
  it("disables Tailwind pulse and spin utilities only for reduced motion", () => {
    const reducedMotion = mediaBlock("prefers-reduced-motion: reduce");

    expect(reducedMotion).toMatch(
      /\.animate-pulse\s*,\s*\.animate-spin\s*\{\s*animation:\s*none\s*;/,
    );
  });
});

describe("chat-text-shimmer reduced-motion fallback (LRM-1337)", () => {
  it("keeps the non-reduce shimmer path byte-stable", () => {
    const start = sharedBaseCss.indexOf(".animate-chat-text-shimmer {");
    expect(start).toBeGreaterThanOrEqual(0);
    const open = sharedBaseCss.indexOf("{", start);
    let depth = 0;
    let end = open;
    for (let index = open; index < sharedBaseCss.length; index += 1) {
      if (sharedBaseCss[index] === "{") depth += 1;
      if (sharedBaseCss[index] === "}") depth -= 1;
      if (depth === 0) {
        end = index;
        break;
      }
    }
    const body = sharedBaseCss.slice(open + 1, end);
    expect(body).toMatch(/background-image:\s*linear-gradient\(/);
    expect(body).toMatch(/background-size:\s*200%\s+100%/);
    expect(body).toMatch(/background-clip:\s*text/);
    expect(body).toMatch(/animation:\s*chat-text-shimmer\s+2\.5s\s+linear\s+infinite/);
  });

  it("resets all four shimmer properties under prefers-reduced-motion", () => {
    const rule = allReducedMotionBodies()
      .map((body) => {
        const start = body.indexOf(".animate-chat-text-shimmer");
        if (start < 0) return null;
        const open = body.indexOf("{", start);
        if (open < 0) return null;
        let depth = 0;
        for (let index = open; index < body.length; index += 1) {
          if (body[index] === "{") depth += 1;
          if (body[index] === "}") depth -= 1;
          if (depth === 0) return body.slice(open + 1, index);
        }
        return null;
      })
      .find((r): r is string => r != null);

    expect(rule, "missing .animate-chat-text-shimmer reduce rule").toBeTruthy();
    expect(rule!).toMatch(/animation:\s*none\s*;/);
    expect(rule!).toMatch(/background-image:\s*none\s*;/);
    expect(rule!).toMatch(/color:\s*inherit\s*;/);
    expect(rule!).toMatch(/-webkit-text-fill-color:\s*currentColor\s*;/);
  });
});
