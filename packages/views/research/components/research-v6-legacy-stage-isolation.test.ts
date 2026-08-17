import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";

const source = readFileSync(
  new URL("./research-session-page.tsx", import.meta.url),
  "utf8",
);

describe("Director V6 legacy stage isolation", () => {
  it("does not derive fixed S1-S4 chat markers for Director runs", () => {
    expect(source).toMatch(
      /const startedStages = directorV6Enabled\s*\? \[\]\s*:\s*RESEARCH_STAGE_ORDER\.filter/,
    );
  });

  it("uses Ronaldo when a Director run has no legacy fleet member", () => {
    expect(source).toMatch(
      /fallbackName=\{[\s\S]*directorV6Enabled[\s\S]*director_ronaldo/,
    );
  });
});
