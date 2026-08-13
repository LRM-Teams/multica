// @vitest-environment node

import { readFileSync } from "node:fs";
import { join } from "node:path";
import { describe, expect, it } from "vitest";

const sources = ["node-card-shell.tsx", "generic-node-card.tsx"].map((file) => ({
  file,
  source: readFileSync(join(import.meta.dirname, file), "utf8"),
}));

describe("research node card long copy", () => {
  it.each(sources)("keeps unbroken titles and summaries inside $file", ({ source }) => {
    const wrapDeclarations = source.match(/\[overflow-wrap:anywhere\]/g) ?? [];
    expect(wrapDeclarations).toHaveLength(2);
    expect(source).toMatch(/(?:node|generic)-title[\s\S]*?line-clamp-2 \[overflow-wrap:anywhere\]/);
    expect(source).toMatch(/(?:node|generic)-summary[\s\S]*?line-clamp-2 \[overflow-wrap:anywhere\]/);
  });
});
