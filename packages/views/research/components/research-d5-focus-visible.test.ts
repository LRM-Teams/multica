// @vitest-environment node

import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";

const css = readFileSync(
  new URL("./research-d5-layout.css", import.meta.url),
  "utf8",
);

describe("D5 custom controls focus visibility", () => {
  it.each(["d5-rail-close", "d5-rail-tab", "d5-lens-btn"])(
    "gives .%s a semantic focus-visible ring",
    (className) => {
      expect(css).toContain(`.${className}:focus-visible`);
    },
  );

  it("uses the theme ring token instead of a hard-coded colour", () => {
    const block = css.slice(
      css.indexOf(".d5-rail-close:focus-visible"),
      css.indexOf('.d5-workspace[data-d5-rail-open="false"]'),
    );
    expect(block).toContain("outline: 2px solid var(--ring)");
    expect(block).toContain("outline-offset: 2px");
    expect(block).not.toMatch(/#[0-9a-f]{3,8}/i);
  });
});
