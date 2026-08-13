import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";

const css = readFileSync(
  new URL("./research-d5-layout.css", import.meta.url),
  "utf8",
);

function tabletBlock(): string {
  const marker = "@media (min-width: 768px) and (max-width: 1199px)";
  const start = css.indexOf(marker);
  return start < 0 ? "" : css.slice(start);
}

describe("D5 tablet command bar", () => {
  it("moves the Goal surface to a full-width second row", () => {
    const tablet = tabletBlock();
    expect(tablet).toMatch(/\.d5-chrome-top\s*\{[^}]*flex-wrap:\s*wrap/s);
    expect(tablet).toMatch(
      /\.d5-goal-slot\s*\{[^}]*order:\s*3[^}]*flex:\s*1 1 100%/s,
    );
    expect(tablet).toMatch(
      /\.d5-brand\s*\{[^}]*flex:\s*1 1 auto[^}]*min-width:\s*0/s,
    );
  });
});
