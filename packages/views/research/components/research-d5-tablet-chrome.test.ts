// @vitest-environment node

import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";

const css = readFileSync(
  new URL("./research-d5-layout.css", import.meta.url),
  "utf8",
);

function tabletBlock(): string {
  const marker = "@media (min-width: 960px) and (max-width: 1199px)";
  const start = css.indexOf(marker);
  return start < 0 ? "" : css.slice(start);
}

describe("D5 tablet command bar", () => {
  it("keeps wide-tablet chrome on one row and collapses only the lens group", () => {
    const tablet = tabletBlock();
    expect(tablet).toMatch(
      /\.d5-goal-slot\s*\{[^}]*min-width:\s*260px/s,
    );
    expect(tablet).toMatch(
      /\.d5-lens-group\s*\{[^}]*display:\s*none/s,
    );
    expect(tablet).toMatch(
      /\.d5-lens-overflow-trigger\s*\{[^}]*display:\s*inline-flex/s,
    );
  });
});
