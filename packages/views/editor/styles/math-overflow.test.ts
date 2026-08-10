import { readFileSync } from "node:fs";
import { join } from "node:path";
import { describe, expect, it } from "vitest";

const css = readFileSync(join(__dirname, "prose.css"), "utf8");

function ruleBody(selector: string) {
  const escaped = selector.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
  const match = new RegExp(`${escaped}\\s*\\{([^}]*)\\}`).exec(css);
  return match?.[1] ?? "";
}

describe("math formula overflow", () => {
  it("shows slash-inserted block formulas without local scrollbars", () => {
    expect(ruleBody(".rich-text-editor .math-node.block")).toContain("overflow: visible");
    expect(ruleBody(".rich-text-editor .math-node.block")).not.toContain("overflow-x: auto");
    expect(ruleBody(".rich-text-editor .math-node-preview")).toContain("overflow: visible");
    expect(ruleBody(".rich-text-editor .math-node-preview")).not.toContain("overflow-x: auto");
  });
});
